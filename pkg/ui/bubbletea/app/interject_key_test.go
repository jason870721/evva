package app

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/components/input"
)

// interjectKey is the KeyMsg the App sees for Ctrl+G.
var interjectKey = tea.KeyMsg{Type: tea.KeyCtrlG}

// steerCtrl records which delivery path the App chose. Embedding the
// interface means any method the App reaches for that we have not stubbed
// nil-panics — which is the assertion, not an accident.
type steerCtrl struct {
	ui.Controller
	interjected  []string
	queued       []string
	pending      []ui.PendingPrompt
	interjectErr error
}

func (c *steerCtrl) InterjectUserPrompt(p string) error {
	c.interjected = append(c.interjected, p)
	return c.interjectErr
}
func (c *steerCtrl) EnqueueUserPrompt(p string)         { c.queued = append(c.queued, p) }
func (c *steerCtrl) PendingPrompts() []ui.PendingPrompt { return c.pending }
func (c *steerCtrl) LastTurnInputTokens() int           { return 0 }
func (c *steerCtrl) Model() string                      { return "claude-opus-5" }

// runningApp returns an App wired to ctrl with a live runCancel, i.e. in the
// state where the queue-vs-interject choice actually exists.
func runningApp(t *testing.T, ctrl ui.Controller) *App {
	t.Helper()
	a := New(t.TempDir())
	a.controller = ctrl
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	a.runCancel = cancel
	return a
}

// TestCtrlGInterjectsWhileRunning is STE-4's core routing claim: the same
// text reaches the agent by a different path depending on which key sent it.
func TestCtrlGInterjectsWhileRunning(t *testing.T) {
	c := &steerCtrl{}
	a := runningApp(t, c)
	a.input.SetValue("stop, tests are in ./scripts")

	a.handleKey(interjectKey)

	if len(c.interjected) != 1 || c.interjected[0] != "stop, tests are in ./scripts" {
		t.Fatalf("interjected = %v, want the composed text", c.interjected)
	}
	if len(c.queued) != 0 {
		t.Errorf("Ctrl+G must not also queue: %v", c.queued)
	}
	if a.input.Value() != "" {
		t.Errorf("composer should clear after an interject, got %q", a.input.Value())
	}
}

// TestEnterQueuesWhileRunning is the other half of the same claim — the
// polite path must stay exactly as it was.
func TestEnterQueuesWhileRunning(t *testing.T) {
	c := &steerCtrl{}
	a := runningApp(t, c)
	a.input.SetValue("also check the tests")

	a.handleSubmit(input.SubmitMsg{ForAgent: "also check the tests", ForView: "also check the tests"})

	if len(c.queued) != 1 || c.queued[0] != "also check the tests" {
		t.Fatalf("queued = %v", c.queued)
	}
	if len(c.interjected) != 0 {
		t.Errorf("Enter must not interject: %v", c.interjected)
	}
}

// TestCtrlGIdleFallsBackToSubmit: with nothing running there is nothing to
// interrupt, so the key must behave as an ordinary send rather than
// reporting an error the user cannot act on.
func TestCtrlGIdleFallsBackToSubmit(t *testing.T) {
	c := &steerCtrl{}
	a := New(t.TempDir())
	a.controller = c
	a.input.SetValue("hello")

	_, cmd := a.handleKey(interjectKey)

	if len(c.interjected) != 0 {
		t.Errorf("idle Ctrl+G should not interject: %v", c.interjected)
	}
	if cmd == nil {
		t.Fatal("idle Ctrl+G should return a submit command")
	}
	if _, ok := cmd().(input.SubmitMsg); !ok {
		t.Errorf("idle Ctrl+G returned %T, want a SubmitMsg", cmd())
	}
}

// TestCtrlGEmptyComposerIsANoop — the key is only meaningful with something
// to say; pressing it bare must not cancel the run for nothing.
func TestCtrlGEmptyComposerIsANoop(t *testing.T) {
	c := &steerCtrl{}
	a := runningApp(t, c)

	a.handleKey(interjectKey)

	if len(c.interjected)+len(c.queued) != 0 {
		t.Error("empty Ctrl+G delivered something")
	}
}

// TestCtrlGFallsBackToQueueWhenTheRunEnded covers the race between the
// keystroke and the loop finishing: the text was typed on purpose, so it
// must not evaporate just because it arrived a millisecond late.
func TestCtrlGFallsBackToQueueWhenTheRunEnded(t *testing.T) {
	c := &steerCtrl{interjectErr: errNoRun{}}
	a := runningApp(t, c)
	a.input.SetValue("late steer")

	a.handleKey(interjectKey)

	if len(c.queued) != 1 || c.queued[0] != "late steer" {
		t.Fatalf("queued = %v, want the message to survive as a queued prompt", c.queued)
	}
}

// TestRefreshQueuedCountsInterjects wires the status badge to the queue.
func TestRefreshQueuedCountsInterjects(t *testing.T) {
	c := &steerCtrl{pending: []ui.PendingPrompt{
		{ID: "p1", Level: "interject"},
		{ID: "p2", Level: "queue"},
		{ID: "p3", Level: "queue"},
	}}
	a := New(t.TempDir())
	a.controller = c
	a.refreshQueued()
	if a.status == nil {
		t.Fatal("status bar missing")
	}
	// Render at a generous width so the cell cannot be dropped for space.
	out := a.status.Compose(200, a.theme)
	if !strings.Contains(out, "3!") {
		t.Errorf("status bar should badge 3 pending with an interject:\n%q", out)
	}
}

type errNoRun struct{}

func (errNoRun) Error() string { return "agent: no run in flight to interject" }
