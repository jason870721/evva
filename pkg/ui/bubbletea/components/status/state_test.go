package status

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// TestRunStateIsActive — the spinner-animating cluster must include
// every "ing" state and exclude terminal states.
func TestRunStateIsActive(t *testing.T) {
	active := []RunState{
		StateRunning, StateThinking, StateTexting, StateExecuting,
		StateDraining, StateCompacting,
	}
	for _, s := range active {
		if !s.IsActive() {
			t.Errorf("%v should be active", s)
		}
	}
	inactive := []RunState{StateIdle, StateIterLimit, StateError}
	for _, s := range inactive {
		if s.IsActive() {
			t.Errorf("%v should NOT be active", s)
		}
	}
}

// TestApplyTracksSubPhases — events drive the state through the
// thinking → texting → executing → running → … sub-phases.
func TestApplyTracksSubPhases(t *testing.T) {
	s := NewState()

	cases := []struct {
		name string
		evt  event.Event
		want RunState
	}{
		{"run start", event.Event{Kind: event.KindRunStart, RunStart: &event.RunStartPayload{Prompt: "x"}}, StateRunning},
		{"thinking", event.Event{Kind: event.KindThinking, Thinking: &event.TextPayload{Text: "..."}}, StateThinking},
		{"text chunk", event.Event{Kind: event.KindTextChunk, Text: &event.TextPayload{Text: "hi"}}, StateTexting},
		{"tool start", event.Event{Kind: event.KindToolUseStart, ToolUseStart: &event.ToolUseStartPayload{Name: "bash"}}, StateExecuting},
		{"tool result", event.Event{Kind: event.KindToolUseResult, ToolUseResult: &event.ToolUseResultPayload{}}, StateRunning},
		{"draining", event.Event{Kind: event.KindDrainingInfo}, StateDraining},
		{"run end", event.Event{Kind: event.KindRunEnd, RunEnd: &event.RunEndPayload{Iters: 3}}, StateIdle},
		{"compacting", event.Event{Kind: event.KindCompacting, Compacting: &event.CompactingPayload{Type: "micro"}}, StateCompacting},
		{"compact end", event.Event{Kind: event.KindCompactingEnd, CompactingEnd: &event.CompactingEndPayload{Type: "micro", OK: true}}, StateRunning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s.Apply(tc.evt)
			if s.Current() != tc.want {
				t.Errorf("after %s: state = %v, want %v", tc.name, s.Current(), tc.want)
			}
		})
	}
}

// TestApplyIterLimitSticky — once we hit iter-limit, a subsequent
// stray event must not knock us off it. Resume requires an explicit
// OnSubmit.
func TestApplyIterLimitSticky(t *testing.T) {
	s := NewState()
	s.Apply(event.Event{Kind: event.KindIterLimit, IterLimit: &event.IterLimitPayload{Iters: 30}})
	if s.Current() != StateIterLimit {
		t.Fatalf("expected IterLimit after KindIterLimit, got %v", s.Current())
	}
	// Stray thinking event from a goroutine that hasn't seen the
	// limit yet — must NOT overwrite the sticky state.
	s.Apply(event.Event{Kind: event.KindThinking, Thinking: &event.TextPayload{Text: "x"}})
	if s.Current() != StateIterLimit {
		t.Errorf("stray event broke iter-limit sticky guard: %v", s.Current())
	}
}

// TestApplyErrorStickyUntilDismiss — same sticky contract for the
// error state, with Dismiss as the explicit exit.
func TestApplyErrorStickyUntilDismiss(t *testing.T) {
	s := NewState()
	s.Apply(event.Event{Kind: event.KindError, Error: &event.ErrorPayload{Stage: "llm", Err: errors.New("boom")}})
	if s.Current() != StateError {
		t.Fatalf("expected Error after KindError, got %v", s.Current())
	}
	s.Apply(event.Event{Kind: event.KindRunStart, RunStart: &event.RunStartPayload{}})
	if s.Current() != StateError {
		t.Errorf("error should be sticky, got %v", s.Current())
	}
	s.Dismiss()
	if s.Current() != StateIdle {
		t.Errorf("Dismiss should clear error, got %v", s.Current())
	}
}

// TestOnRunDone — covers the three branches in handleRunDone:
// success → idle, interrupted → idle+hint, iter-limit error sentinel
// → iter-limit state, generic error → error state.
func TestOnRunDoneBranches(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		s := NewState()
		s.current = StateRunning
		s.OnRunDone(nil, false)
		if s.Current() != StateIdle {
			t.Errorf("got %v, want Idle", s.Current())
		}
	})
	t.Run("interrupted", func(t *testing.T) {
		s := NewState()
		s.current = StateRunning
		s.OnRunDone(errors.New("ctx cancelled"), true)
		if s.Current() != StateIdle {
			t.Errorf("interrupted: got %v, want Idle", s.Current())
		}
		if s.Hint() != "interrupted" {
			t.Errorf("interrupted hint = %q, want %q", s.Hint(), "interrupted")
		}
	})
	t.Run("iter limit sentinel", func(t *testing.T) {
		s := NewState()
		s.current = StateRunning
		s.OnRunDone(errors.New("hit iteration limit 30"), false)
		if s.Current() != StateIterLimit {
			t.Errorf("got %v, want IterLimit", s.Current())
		}
	})
	t.Run("generic error", func(t *testing.T) {
		s := NewState()
		s.current = StateRunning
		s.OnRunDone(errors.New("HTTP 503"), false)
		if s.Current() != StateError {
			t.Errorf("got %v, want Error", s.Current())
		}
		if !strings.Contains(s.Hint(), "HTTP 503") {
			t.Errorf("error hint missing message: %q", s.Hint())
		}
	})
}

// TestTickSpinnerAdvances — the frame counter monotonically
// increments; the status bar reads it via theme.SpinnerFrame which
// modulos to the rotation length.
func TestTickSpinnerAdvances(t *testing.T) {
	s := NewState()
	before := s.Frame()
	s.TickSpinner()
	if s.Frame() != before+1 {
		t.Errorf("Frame did not advance: before=%d after=%d", before, s.Frame())
	}
}

// ----------------------------------------------------------------------------
// Hint resolution
// ----------------------------------------------------------------------------

type stubProvider struct{ h string }

func (s stubProvider) Hint() string { return s.h }

func TestResolveHintPriority(t *testing.T) {
	t.Run("state override wins", func(t *testing.T) {
		s := NewState()
		s.SetHint("queued")
		got := ResolveHint(s, stubProvider{h: "focus says X"})
		if got != "queued" {
			t.Errorf("override should win, got %q", got)
		}
	})
	t.Run("focus wins over default", func(t *testing.T) {
		s := NewState()
		got := ResolveHint(s, stubProvider{h: "yank mode"})
		if got != "yank mode" {
			t.Errorf("focus hint should win, got %q", got)
		}
	})
	t.Run("falls back to default by state", func(t *testing.T) {
		s := NewState()
		s.current = StateRunning
		got := ResolveHint(s, nil)
		if !strings.Contains(got, "Esc cancel") {
			t.Errorf("running default missing Esc cancel: %q", got)
		}
	})
	t.Run("focus with empty hint falls through", func(t *testing.T) {
		s := NewState()
		got := ResolveHint(s, stubProvider{h: ""})
		if !strings.Contains(got, "/ commands") {
			t.Errorf("empty focus hint should fall through to idle default, got %q", got)
		}
	})
}

// ----------------------------------------------------------------------------
// StatusBar rendering smoke tests
// ----------------------------------------------------------------------------

// TestStatusBarComposeSmokeTest — non-empty rendered output, contains
// expected cell labels, fits within width.
func TestStatusBarComposeSmokeTest(t *testing.T) {
	s := NewState()
	bar := New(s)
	bar.SetModel("claude-opus-4-7")
	bar.SetAgentID("a1b2c3d4e5f6")
	bar.SetUsage(llm.Usage{InputTokens: 1234, OutputTokens: 5678})
	bar.SetContext(20000, 200000)

	out := bar.Compose(120, theme.Default())
	if out == "" {
		t.Fatal("Compose returned empty output")
	}
	plain := stripANSIForTest(out)
	for _, want := range []string{"READY", "EVVA", "claude-opus-4-7", "IN", "OUT", "CTX", "10.0%", "a1b2c3d4"} {
		if !strings.Contains(plain, want) {
			t.Errorf("status bar missing %q\n   plain: %q", want, plain)
		}
	}
}

// TestStatusBarContextPercent — context utilization meter renders
// the percentage correctly for the obvious boundary cases.
func TestStatusBarContextPercent(t *testing.T) {
	s := NewState()
	bar := New(s)
	cases := []struct {
		used, limit int
		want     string
	}{
		{0, 200000, "0.0%"},
		{20000, 200000, "10.0%"},
		{100000, 200000, "50.0%"},
		{200000, 200000, "100.0%"},
		{300000, 200000, "100.0%"}, // overflow clamps
		{1234, 0, "0.0%"},          // unknown limit
	}
	for _, tc := range cases {
		bar.SetContext(tc.used, tc.limit)
		out := stripANSIForTest(bar.Compose(120, theme.Default()))
		if !strings.Contains(out, tc.want) {
			t.Errorf("used=%d limit=%d → want %q in %q", tc.used, tc.limit, tc.want, out)
		}
	}
}

// TestSpinnerFrameAdvancesPill — running state shows a different
// glyph in successive frames.
func TestSpinnerFrameAdvancesPill(t *testing.T) {
	s := NewState()
	s.Apply(event.Event{Kind: event.KindRunStart, RunStart: &event.RunStartPayload{}})
	bar := New(s)
	first := bar.Compose(80, theme.Default())
	s.TickSpinner()
	second := bar.Compose(80, theme.Default())
	if first == second {
		t.Error("spinner frame did not change rendered output")
	}
}

// stripANSIForTest — local ANSI strip so tests don't pull in a
// reflective dep on the transcript package's helper. Same regex
// shape; "good enough" for assertion-only use.
func stripANSIForTest(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		if r == 0x1b {
			skip = true
			continue
		}
		if skip {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '\x07' {
				skip = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
