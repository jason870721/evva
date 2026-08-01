// Package evalharness regression-tests agent *behavior* the way `go test`
// regression-tests code.
//
// # The gap it closes
//
// evva's release workflow gates on `go test ./...` — code correctness. It has
// no equivalent for the thing that changes on almost every release: system
// prompt wording, tool descriptions, model defaults. "Ship it and watch" is
// the only feedback loop for those, and it has already missed real defects.
//
// The harness records a real session as a *fixture*, then replays its user
// turns against the current configuration and reports whether the agent's
// decisions changed. Two tiers:
//
//   - Structural diff (cheap, deterministic, the hard gate): did the sequence
//     of tool calls change?
//   - LLM judge (opt-in, advisory): does the outcome still satisfy a short
//     human-written expectation?
//
// # What it deliberately does not measure
//
// Not byte-for-byte reproduction. LLM non-determinism is a fact, not a bug,
// so the unit of comparison is the model's *decisions* — which tools it
// reached for, in what order — never exact text. And not absolute capability:
// this scores evva against its own prior behavior, not against a benchmark.
//
// See docs/roadmap/PRD/agent-eval-harness.md.
package evalharness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FixtureVersion is the on-disk schema version. Bumped only on a breaking
// change to the fixture shape; the loader refuses a version it does not know
// rather than silently misreading one.
const FixtureVersion = 1

// ToolCallSummary is one tool invocation reduced to what a regression should
// actually be sensitive to: which tool, and the identity of what it acted on.
//
// The reduction is the point. Comparing whole argument blobs would make every
// fixture flap when the model rephrases a grep pattern or reorders an edit;
// comparing nothing but the name would miss a model that started editing the
// wrong file. KeyArgs is the middle: a small identity projection per tool
// family (see keyArgsFor).
type ToolCallSummary struct {
	Name    string            `json:"name"`
	KeyArgs map[string]string `json:"key_args,omitempty"`
}

// String renders a summary for divergence reports.
func (s ToolCallSummary) String() string {
	if len(s.KeyArgs) == 0 {
		return s.Name
	}
	keys := make([]string, 0, len(s.KeyArgs))
	for k := range s.KeyArgs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+s.KeyArgs[k])
	}
	return fmt.Sprintf("%s(%s)", s.Name, strings.Join(parts, " "))
}

// Equal reports whether two summaries describe the same decision. Same tool,
// and every key arg the BASELINE recorded must match. Args present only in the
// replay are ignored: a new optional parameter appearing in a tool's schema is
// not a behavior regression, and treating it as one would make every fixture
// fail the first time a tool grows a field.
func (s ToolCallSummary) Equal(other ToolCallSummary) bool {
	if s.Name != other.Name {
		return false
	}
	for k, want := range s.KeyArgs {
		if other.KeyArgs[k] != want {
			return false
		}
	}
	return true
}

// Fixture is one recorded scenario: the user turns to replay, the tool-call
// sequence the recorded run produced, and enough metadata for a human to know
// what it is for.
//
// Note what is NOT here: the session envelope. A session.Snapshot carries
// absolute machine paths (Workdir, WorkdirSlug), which would make a committed
// fixture non-portable to any other checkout — and the replay truncates to
// user turns anyway, so the rest of the transcript is data the harness
// discards. FromSnapshot derives a fixture from a real session; the on-disk
// shape is deliberately just the replayable part.
type Fixture struct {
	Version int `json:"version"`
	// Name identifies the fixture in reports; defaults to the file's base name.
	Name string `json:"name"`
	// Description is human-authored: what behavior this exercises, and why it
	// is worth guarding.
	Description string `json:"description"`
	// ExpectedOutcome is optional prose. Non-empty enables judge scoring — the
	// escape hatch for fixtures where exact tool-call shape is not the point
	// (e.g. "does the persona still refuse this").
	ExpectedOutcome string `json:"expected_outcome,omitempty"`
	// RecordedWith notes the provider/model the baseline came from. Purely
	// informational — a fixture is replayable against any provider, and often
	// deliberately is.
	RecordedWith string `json:"recorded_with,omitempty"`
	// UserTurns are the user-authored prompts, in order. Replaying these is
	// the whole experiment: same input, current configuration, what changes?
	UserTurns []string `json:"user_turns"`
	// Baseline is the tool-call sequence the recorded run produced.
	Baseline []ToolCallSummary `json:"baseline"`
}

// Validate reports why a fixture is unusable, if it is.
func (f *Fixture) Validate() error {
	if f.Version != FixtureVersion {
		return fmt.Errorf("fixture %q: unsupported version %d (this build reads version %d)", f.Name, f.Version, FixtureVersion)
	}
	if len(f.UserTurns) == 0 {
		return fmt.Errorf("fixture %q: no user turns to replay", f.Name)
	}
	for _, c := range f.Baseline {
		if c.Name == "" {
			return fmt.Errorf("fixture %q: baseline contains an unnamed tool call", f.Name)
		}
	}
	return nil
}

// JudgeEnabled reports whether this fixture carries an expectation a judge can
// score.
func (f *Fixture) JudgeEnabled() bool { return strings.TrimSpace(f.ExpectedOutcome) != "" }

// Save writes the fixture as indented JSON. Indented on purpose: fixtures are
// committed, reviewed in pull requests, and hand-edited when a behavior change
// is intentional.
func (f *Fixture) Save(path string) error {
	if f.Version == 0 {
		f.Version = FixtureVersion
	}
	if f.Name == "" {
		f.Name = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal fixture: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create fixture dir: %w", err)
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

// LoadFixture reads one fixture file.
func LoadFixture(path string) (*Fixture, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fixture: %w", err)
	}
	var f Fixture
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse fixture %s: %w", filepath.Base(path), err)
	}
	if f.Name == "" {
		f.Name = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// LoadDir reads every *.json fixture in dir, sorted by name so reports are
// stable run to run. An empty directory is an error, not an empty pass: a
// green run over zero fixtures is the most dangerous possible output for a
// regression gate.
func LoadDir(dir string) ([]*Fixture, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read fixture dir: %w", err)
	}
	var out []*Fixture
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		f, err := LoadFixture(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no fixtures in %s — a gate that scores zero fixtures always passes, which is worse than failing", dir)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ErrNoUserTurns is returned when a session holds nothing replayable.
var ErrNoUserTurns = errors.New("evalharness: session has no user turns")
