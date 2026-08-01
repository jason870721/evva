package evalharness

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/llm"
)

// identityArgs names, per tool, the arguments that identify *what a call acted
// on* — as opposed to how it was phrased.
//
// This is the definition §4.3 of the PRD left open, and getting the width
// right is the whole art of the structural tier. Too wide (compare every
// argument) and a fixture fails when the model rewords a grep pattern or
// reorders an edit's replacement text — noise that trains people to ignore
// the gate. Too narrow (compare only tool names) and a model that started
// editing the wrong file scores clean. The middle is: which file, which
// command, which URL.
//
// A tool with no entry falls back to a small set of near-universal identity
// keys, so a newly added tool is imperfectly covered rather than invisible.
var identityArgs = map[string][]string{
	"read":            {"file_path"},
	"write":           {"file_path"},
	"edit":            {"file_path"},
	"multi_edit":      {"file_path"},
	"notebook_edit":   {"notebook_path"},
	"bash":            {"command"},
	"grep":            {"pattern", "path"},
	"glob":            {"pattern", "path"},
	"tree":            {"path"},
	"web_fetch":       {"url"},
	"web_search":      {"query"},
	"agent":           {"kind", "isolation"},
	"todo_write":      {},
	"enter_worktree":  {"name"},
	"exit_worktree":   {"action", "branch"},
	"json_query":      {"file_path", "query"},
	"lsp_diagnostics": {"file_path"},
}

// fallbackIdentityArgs is used for tools with no explicit projection —
// including MCP tools, whose names are not known at build time.
var fallbackIdentityArgs = []string{"file_path", "path", "command", "url", "query", "pattern", "name"}

// Capture derives a fixture from a live session snapshot: the user turns to
// replay, and the tool-call sequence that session produced as the baseline.
//
// Replaying recorded *assistant* turns would test nothing — they are static
// data. The useful question is "given the same user turns, what does the
// current configuration decide?", so only user turns are carried forward.
func Capture(state session.SessionState, name, description string) (*Fixture, error) {
	f := &Fixture{
		Version:     FixtureVersion,
		Name:        name,
		Description: description,
		UserTurns:   userTurns(state.Messages),
		Baseline:    Baseline(state.Messages),
	}
	if len(f.UserTurns) == 0 {
		return nil, ErrNoUserTurns
	}
	return f, nil
}

// FromSnapshot derives a fixture from a stored session, carrying the
// provider/model that recorded it for the record.
func FromSnapshot(snap *session.Snapshot, name, description string) (*Fixture, error) {
	f, err := Capture(snap.Session, name, description)
	if err != nil {
		return nil, err
	}
	if snap.Provider != "" {
		f.RecordedWith = snap.Provider
		if snap.Model != "" {
			f.RecordedWith += "/" + snap.Model
		}
	}
	if f.Name == "" {
		f.Name = snap.SessionID
	}
	return f, nil
}

// userTurns extracts the user-authored prompts in order.
//
// System-injected user-role messages are skipped: evva delivers subagent
// results, daemon lifecycles and hook output as synthetic user turns wrapped
// in <system-reminder>. Replaying those would feed the new run stale results
// from the old one — the harness would be measuring the recorded session's
// output, not the current configuration's behavior.
func userTurns(msgs []llm.Message) []string {
	var out []string
	for _, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		text := strings.TrimSpace(m.Content)
		if text == "" || isSynthetic(text) {
			continue
		}
		out = append(out, text)
	}
	return out
}

// isSynthetic reports whether a user-role message was injected by the runtime
// rather than typed by a person.
func isSynthetic(text string) bool {
	return strings.HasPrefix(text, "<system-reminder>") ||
		strings.HasPrefix(text, "<external-request>") ||
		strings.HasPrefix(text, "<command-name>")
}

// Baseline reduces a transcript's tool calls to the comparable sequence.
func Baseline(msgs []llm.Message) []ToolCallSummary {
	var out []ToolCallSummary
	for _, m := range msgs {
		for _, c := range m.ToolCalls {
			if c == nil {
				continue
			}
			out = append(out, Summarize(c.Name, c.Input))
		}
	}
	return out
}

// Summarize reduces one call to its identity projection.
func Summarize(name string, input json.RawMessage) ToolCallSummary {
	s := ToolCallSummary{Name: name}
	keys, ok := identityArgs[name]
	if !ok {
		keys = fallbackIdentityArgs
	}
	if len(keys) == 0 || len(input) == 0 {
		return s
	}
	var args map[string]any
	if err := json.Unmarshal(input, &args); err != nil {
		return s
	}
	for _, k := range keys {
		v, present := args[k]
		if !present {
			continue
		}
		str, ok := v.(string)
		if !ok {
			continue
		}
		str = strings.TrimSpace(str)
		if str == "" {
			continue
		}
		if s.KeyArgs == nil {
			s.KeyArgs = map[string]string{}
		}
		s.KeyArgs[k] = normalizeArg(k, str)
	}
	return s
}

// normalizeArg makes a recorded argument comparable across checkouts.
//
// Path-like arguments are reduced to their base name. A session recorded in
// /Users/alice/proj and replayed in /home/bob/proj must still match — and
// absent this, every fixture would be bound to the machine that produced it,
// which is exactly the portability trap that also kept the session envelope
// out of the fixture format.
func normalizeArg(key, val string) string {
	switch key {
	case "file_path", "notebook_path", "path":
		if filepath.IsAbs(val) || strings.ContainsAny(val, `/\`) {
			return filepath.Base(filepath.FromSlash(val))
		}
		return val
	case "command":
		// Commands are compared by their leading verb, not the whole line:
		// `go test ./...` and `go test ./pkg/...` are the same decision, while
		// `go test` versus `rm -rf` emphatically is not.
		return commandVerb(val)
	default:
		return val
	}
}

// commandVerb extracts the program a shell command runs, skipping leading
// environment assignments (FOO=bar cmd) so `CGO_ENABLED=0 go build` and
// `go build` compare equal.
func commandVerb(cmd string) string {
	fields := strings.Fields(cmd)
	for _, f := range fields {
		if strings.Contains(f, "=") && !strings.HasPrefix(f, "-") {
			continue
		}
		return filepath.Base(filepath.FromSlash(f))
	}
	if len(fields) > 0 {
		return fields[0]
	}
	return ""
}
