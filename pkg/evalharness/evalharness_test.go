package evalharness

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

func call(name string, args map[string]any) *tools.Call {
	raw, _ := json.Marshal(args)
	return &tools.Call{ID: name, Name: name, Input: raw}
}

func sum(name string, kv ...string) ToolCallSummary {
	s := ToolCallSummary{Name: name}
	if len(kv) > 0 {
		s.KeyArgs = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			s.KeyArgs[kv[i]] = kv[i+1]
		}
	}
	return s
}

// --- capture ---

func TestCaptureExtractsUserTurnsAndBaseline(t *testing.T) {
	state := session.SessionState{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "fix the bug in parser.go"},
		{Role: llm.RoleAssistant, ToolCalls: []*tools.Call{
			call("read", map[string]any{"file_path": "/abs/proj/parser.go"}),
			call("edit", map[string]any{"file_path": "/abs/proj/parser.go", "old_string": "x"}),
		}},
		{Role: llm.RoleUser, Content: "now run the tests"},
		{Role: llm.RoleAssistant, ToolCalls: []*tools.Call{
			call("bash", map[string]any{"command": "go test ./..."}),
		}},
	}}

	f, err := Capture(state, "fix-parser", "guards the read-before-edit habit")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(f.UserTurns) != 2 || f.UserTurns[0] != "fix the bug in parser.go" {
		t.Fatalf("UserTurns = %v", f.UserTurns)
	}
	if len(f.Baseline) != 3 {
		t.Fatalf("Baseline = %v", f.Baseline)
	}
	if got := f.Baseline[0].KeyArgs["file_path"]; got != "parser.go" {
		t.Errorf("paths must normalize to base names for portability, got %q", got)
	}
	if got := f.Baseline[2].KeyArgs["command"]; got != "go" {
		t.Errorf("command should reduce to its verb, got %q", got)
	}
}

// Runtime-injected user turns (subagent results, daemon lifecycles, hook
// output) must not be replayed — feeding the new run the old run's results
// would measure the recording, not the current configuration.
func TestCaptureSkipsSyntheticUserTurns(t *testing.T) {
	state := session.SessionState{Messages: []llm.Message{
		{Role: llm.RoleUser, Content: "do the thing"},
		{Role: llm.RoleUser, Content: "<system-reminder>subagent finished</system-reminder>"},
		{Role: llm.RoleUser, Content: "<external-request>from mcp</external-request>"},
		{Role: llm.RoleUser, Content: "  "},
		{Role: llm.RoleUser, Content: "and the other thing"},
	}}
	f, err := Capture(state, "x", "d")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	want := []string{"do the thing", "and the other thing"}
	if len(f.UserTurns) != len(want) {
		t.Fatalf("UserTurns = %v, want %v", f.UserTurns, want)
	}
	for i := range want {
		if f.UserTurns[i] != want[i] {
			t.Errorf("turn %d = %q, want %q", i, f.UserTurns[i], want[i])
		}
	}
}

func TestCaptureRefusesSessionWithNoUserTurns(t *testing.T) {
	state := session.SessionState{Messages: []llm.Message{
		{Role: llm.RoleAssistant, Content: "hello"},
	}}
	if _, err := Capture(state, "x", "d"); !errors.Is(err, ErrNoUserTurns) {
		t.Fatalf("want ErrNoUserTurns, got %v", err)
	}
}

func TestSummarizeNormalizesCommandVerb(t *testing.T) {
	cases := map[string]string{
		"go test ./...":            "go",
		"CGO_ENABLED=0 go build":   "go",
		"/usr/local/bin/npm run x": "npm",
		"rm -rf /tmp/x":            "rm",
	}
	for cmd, want := range cases {
		raw, _ := json.Marshal(map[string]any{"command": cmd})
		if got := Summarize("bash", raw).KeyArgs["command"]; got != want {
			t.Errorf("%q → %q, want %q", cmd, got, want)
		}
	}
}

// An unknown tool (an MCP tool, say) must still yield a usable projection
// rather than vanishing from the baseline.
func TestSummarizeUnknownToolUsesFallback(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"file_path": "/a/b/c.go", "extra": "ignored"})
	got := Summarize("mcp__server__do_thing", raw)
	if got.Name != "mcp__server__do_thing" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.KeyArgs["file_path"] != "c.go" {
		t.Errorf("fallback projection missed file_path: %v", got.KeyArgs)
	}
}

// --- diff ---

func TestDiffIdenticalPasses(t *testing.T) {
	base := []ToolCallSummary{sum("read", "file_path", "a.go"), sum("edit", "file_path", "a.go")}
	res := Diff(base, base)
	if !res.Passed() {
		t.Fatalf("identical sequences should pass: %s", res.Detail())
	}
}

// Order is a decision: tests-then-edit is different behavior from
// edit-then-tests even though the multiset matches.
func TestDiffCatchesReordering(t *testing.T) {
	base := []ToolCallSummary{sum("edit", "file_path", "a.go"), sum("bash", "command", "go")}
	actual := []ToolCallSummary{sum("bash", "command", "go"), sum("edit", "file_path", "a.go")}
	res := Diff(base, actual)
	if res.Passed() {
		t.Fatal("a reordered sequence must not pass")
	}
}

func TestDiffCatchesWrongFile(t *testing.T) {
	base := []ToolCallSummary{sum("edit", "file_path", "parser.go")}
	actual := []ToolCallSummary{sum("edit", "file_path", "lexer.go")}
	res := Diff(base, actual)
	if res.Passed() {
		t.Fatal("editing a different file must be a divergence")
	}
	if res.Divergences[0].Kind != KindChanged {
		t.Errorf("kind = %v, want changed", res.Divergences[0].Kind)
	}
	if !strings.Contains(res.Divergences[0].String(), "lexer.go") {
		t.Errorf("report should name the actual call: %s", res.Divergences[0])
	}
}

// The regression the PRD's own motivating example describes: a prompt edit
// that removes "always run tests before finishing" shows up as a missing call.
func TestDiffCatchesDroppedCall(t *testing.T) {
	base := []ToolCallSummary{
		sum("read", "file_path", "a.go"),
		sum("edit", "file_path", "a.go"),
		sum("bash", "command", "go"),
	}
	actual := []ToolCallSummary{
		sum("read", "file_path", "a.go"),
		sum("edit", "file_path", "a.go"),
	}
	res := Diff(base, actual)
	if res.Passed() {
		t.Fatal("a dropped verification step must be a divergence")
	}
	if len(res.Divergences) != 1 || res.Divergences[0].Kind != KindMissing {
		t.Fatalf("want exactly one missing divergence, got %v", res.Divergences)
	}
}

// A single inserted call should read as one divergence, not as every
// subsequent call being wrong — a report nobody can read is a report nobody
// acts on.
func TestDiffResynchronizesAfterInsertion(t *testing.T) {
	base := []ToolCallSummary{
		sum("read", "file_path", "a.go"),
		sum("edit", "file_path", "a.go"),
		sum("bash", "command", "go"),
	}
	actual := []ToolCallSummary{
		sum("read", "file_path", "a.go"),
		sum("grep", "pattern", "TODO"),
		sum("edit", "file_path", "a.go"),
		sum("bash", "command", "go"),
	}
	res := Diff(base, actual)
	if res.Passed() {
		t.Fatal("an extra call is a divergence")
	}
	if len(res.Divergences) != 1 {
		t.Fatalf("want 1 divergence after resync, got %d: %s", len(res.Divergences), res.Detail())
	}
	if res.Divergences[0].Kind != KindExtra {
		t.Errorf("kind = %v, want extra", res.Divergences[0].Kind)
	}
}

// A new optional argument appearing in a tool schema is not a behavior
// regression, and must not fail every fixture that touches the tool.
func TestDiffIgnoresNewArgsAbsentFromBaseline(t *testing.T) {
	base := []ToolCallSummary{sum("read", "file_path", "a.go")}
	actual := []ToolCallSummary{sum("read", "file_path", "a.go", "limit", "50")}
	if !Diff(base, actual).Passed() {
		t.Error("an added argument the baseline never recorded is not a regression")
	}
}

func TestDiffEmptyBaselineWithCallsIsDivergence(t *testing.T) {
	res := Diff(nil, []ToolCallSummary{sum("bash", "command", "rm")})
	if res.Passed() {
		t.Fatal("calls appearing where the baseline had none must diverge")
	}
	if res.Divergences[0].Kind != KindExtra {
		t.Errorf("kind = %v, want extra", res.Divergences[0].Kind)
	}
}

// --- fixture io ---

func TestFixtureRoundTrip(t *testing.T) {
	f := &Fixture{
		Version:         FixtureVersion,
		Name:            "demo",
		Description:     "d",
		ExpectedOutcome: "should refuse",
		UserTurns:       []string{"a", "b"},
		Baseline:        []ToolCallSummary{sum("read", "file_path", "x.go")},
	}
	path := filepath.Join(t.TempDir(), "demo.json")
	if err := f.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	back, err := LoadFixture(path)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if back.Name != "demo" || len(back.UserTurns) != 2 || len(back.Baseline) != 1 {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if !back.JudgeEnabled() {
		t.Error("ExpectedOutcome should enable judging")
	}
	if back.Baseline[0].KeyArgs["file_path"] != "x.go" {
		t.Errorf("key args lost: %v", back.Baseline[0].KeyArgs)
	}
}

func TestLoadFixtureRejectsUnknownVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f.json")
	f := &Fixture{Version: 999, Name: "f", UserTurns: []string{"x"}}
	if err := f.Save(path); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixture(path); err == nil {
		t.Fatal("want an error for an unknown fixture version")
	}
}

// A gate that scores zero fixtures always passes, which is the most dangerous
// possible output.
func TestLoadDirRefusesEmptyDirectory(t *testing.T) {
	_, err := LoadDir(t.TempDir())
	if err == nil {
		t.Fatal("an empty fixture dir must be an error, not a silent pass")
	}
	if !strings.Contains(err.Error(), "always passes") {
		t.Errorf("error should explain why, got: %v", err)
	}
}

func TestLoadDirSortsByName(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"zeta", "alpha", "mid"} {
		f := &Fixture{Version: FixtureVersion, Name: n, UserTurns: []string{"x"}}
		if err := f.Save(filepath.Join(dir, n+".json")); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "mid", "zeta"}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("position %d = %s, want %s", i, got[i].Name, w)
		}
	}
}

// --- run ---

type stubRunner struct {
	calls []ToolCallSummary
	final string
	err   error
	turns [][]string
}

func (s *stubRunner) Run(_ context.Context, turns []string) (RunTrace, error) {
	s.turns = append(s.turns, turns)
	if s.err != nil {
		return RunTrace{}, s.err
	}
	return RunTrace{ToolCalls: s.calls, FinalText: s.final}, nil
}

func TestRunScoresStructurally(t *testing.T) {
	base := []ToolCallSummary{sum("read", "file_path", "a.go")}
	f := &Fixture{Version: FixtureVersion, Name: "f", UserTurns: []string{"go"}, Baseline: base}

	rep, err := Run(context.Background(), []*Fixture{f}, &stubRunner{calls: base}, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Passed() {
		t.Errorf("matching run should pass: %s", rep.Results[0].Diff.Detail())
	}
	if ExitCode(rep) != 0 {
		t.Error("a passing report should exit 0")
	}
}

func TestRunFailsOnDivergence(t *testing.T) {
	f := &Fixture{
		Version: FixtureVersion, Name: "f", UserTurns: []string{"go"},
		Baseline: []ToolCallSummary{sum("read", "file_path", "a.go")},
	}
	rep, _ := Run(context.Background(), []*Fixture{f}, &stubRunner{calls: []ToolCallSummary{sum("write", "file_path", "a.go")}}, Options{})
	if rep.Passed() {
		t.Fatal("a divergent run must fail")
	}
	if ExitCode(rep) == 0 {
		t.Error("a failing report must exit non-zero")
	}
}

// A replay that could not run has produced no evidence either way; reporting
// it as a regression would be a lie.
func TestRunSeparatesErrorsFromFailures(t *testing.T) {
	f := &Fixture{Version: FixtureVersion, Name: "f", UserTurns: []string{"go"}}
	rep, _ := Run(context.Background(), []*Fixture{f}, &stubRunner{err: errors.New("provider down")}, Options{})
	passed, failed, errored := rep.Counts()
	if errored != 1 || failed != 0 || passed != 0 {
		t.Errorf("counts = passed %d failed %d errored %d, want 0/0/1", passed, failed, errored)
	}
	if rep.Passed() {
		t.Error("an errored fixture must not report as passing")
	}
}

type stubJudge struct {
	verdict JudgeVerdict
	err     error
	called  int
}

func (s *stubJudge) Judge(context.Context, string, RunTrace) (JudgeVerdict, error) {
	s.called++
	return s.verdict, s.err
}

// Judge mode is advisory: a judge failure must not flip the hard gate.
func TestJudgeFailureDoesNotAffectExitCode(t *testing.T) {
	base := []ToolCallSummary{sum("read", "file_path", "a.go")}
	f := &Fixture{
		Version: FixtureVersion, Name: "f", UserTurns: []string{"go"},
		Baseline: base, ExpectedOutcome: "should refuse",
	}
	j := &stubJudge{verdict: JudgeVerdict{Pass: false, Reason: "did not refuse"}}
	rep, _ := Run(context.Background(), []*Fixture{f}, &stubRunner{calls: base}, Options{Judge: j})

	if j.called != 1 {
		t.Errorf("judge called %d times, want 1", j.called)
	}
	if !rep.Passed() {
		t.Error("a judge failure must not fail the structural gate")
	}
	if ExitCode(rep) != 0 {
		t.Error("exit code must ignore the judge")
	}
	if rep.Results[0].Judge == nil || rep.Results[0].Judge.Pass {
		t.Error("the judge verdict should still be recorded and visible")
	}
}

func TestJudgeSkippedWithoutExpectation(t *testing.T) {
	base := []ToolCallSummary{sum("read", "file_path", "a.go")}
	f := &Fixture{Version: FixtureVersion, Name: "f", UserTurns: []string{"go"}, Baseline: base}
	j := &stubJudge{verdict: JudgeVerdict{Pass: true}}
	rep, _ := Run(context.Background(), []*Fixture{f}, &stubRunner{calls: base}, Options{Judge: j})
	if j.called != 0 {
		t.Error("a fixture with no ExpectedOutcome must not be judged")
	}
	if rep.Results[0].Judge != nil {
		t.Error("no verdict should be recorded")
	}
}

// An unreachable judge must not break a run whose hard gate is structural.
func TestJudgeErrorDegradesToAdvisoryPass(t *testing.T) {
	base := []ToolCallSummary{sum("read", "file_path", "a.go")}
	f := &Fixture{
		Version: FixtureVersion, Name: "f", UserTurns: []string{"go"},
		Baseline: base, ExpectedOutcome: "x",
	}
	j := &stubJudge{err: errors.New("429 rate limited")}
	rep, _ := Run(context.Background(), []*Fixture{f}, &stubRunner{calls: base}, Options{Judge: j})
	if !rep.Passed() {
		t.Error("a judge outage must not fail the run")
	}
	v := rep.Results[0].Judge
	if v == nil || !v.Pass || !strings.Contains(v.Reason, "judge unavailable") {
		t.Errorf("verdict should record the outage, got %+v", v)
	}
}

func TestRunPassesUserTurnsThrough(t *testing.T) {
	f := &Fixture{Version: FixtureVersion, Name: "f", UserTurns: []string{"first", "second"}}
	r := &stubRunner{}
	if _, err := Run(context.Background(), []*Fixture{f}, r, Options{}); err != nil {
		t.Fatal(err)
	}
	if len(r.turns) != 1 || len(r.turns[0]) != 2 || r.turns[0][1] != "second" {
		t.Errorf("runner received %v", r.turns)
	}
}

func TestRunHonorsCancellation(t *testing.T) {
	f := &Fixture{Version: FixtureVersion, Name: "f", UserTurns: []string{"go"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Run(ctx, []*Fixture{f}, &stubRunner{}, Options{}); err == nil {
		t.Fatal("a cancelled context should stop the run")
	}
}

// --- judge parsing ---

func TestParseVerdictAcceptsFencedJSON(t *testing.T) {
	for _, raw := range []string{
		`{"pass": true, "reason": "ok"}`,
		"```json\n{\"pass\": true, \"reason\": \"ok\"}\n```",
		"Here you go:\n```\n{\"pass\": true, \"reason\": \"ok\"}\n```",
	} {
		v, err := parseVerdict(raw)
		if err != nil {
			t.Errorf("parseVerdict(%q): %v", raw, err)
			continue
		}
		if !v.Pass || v.Reason != "ok" {
			t.Errorf("parseVerdict(%q) = %+v", raw, v)
		}
	}
}

func TestParseVerdictRejectsGarbage(t *testing.T) {
	if _, err := parseVerdict("I think it was fine, honestly"); err == nil {
		t.Fatal("want an error for a non-JSON verdict")
	}
}
