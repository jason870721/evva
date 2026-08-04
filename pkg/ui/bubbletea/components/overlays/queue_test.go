package overlays

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// queueCtrl is a Controller stub exposing only what /queue reads. Embedding
// the interface keeps it compiling as the surface grows; the panel touching
// any other method would nil-panic, which is the assertion we want.
type queueCtrl struct {
	ui.Controller
	rows    []ui.PendingPrompt
	revoked []string
	// allow controls whether a revoke "wins" the race with the drain.
	allow bool
}

func (c *queueCtrl) PendingPrompts() []ui.PendingPrompt { return c.rows }

func (c *queueCtrl) RevokePendingPrompt(id string) bool {
	c.revoked = append(c.revoked, id)
	if !c.allow {
		return false
	}
	for i, r := range c.rows {
		if r.ID == id {
			c.rows = append(c.rows[:i], c.rows[i+1:]...)
			return true
		}
	}
	return false
}

func TestQueueNilControllerReturnsNil(t *testing.T) {
	if NewQueue(nil) != nil {
		t.Error("NewQueue(nil) should return nil so the App can hint instead")
	}
}

// TestQueueRendersLevelsAndOrder: the panel must show what the model will
// actually receive, in the order it will receive it.
func TestQueueRendersLevelsAndOrder(t *testing.T) {
	c := &queueCtrl{rows: []ui.PendingPrompt{
		{ID: "p2", Text: "stop that", Level: "interject"},
		{ID: "p1", Text: "also check the tests", Level: "queue"},
	}}
	q := NewQueue(c)
	out := q.View(80, theme.Default())
	if !strings.Contains(out, "stop that") || !strings.Contains(out, "also check the tests") {
		t.Fatalf("view missing rows:\n%s", out)
	}
	if strings.Index(out, "stop that") > strings.Index(out, "also check the tests") {
		t.Error("interject row must render above the queued row")
	}
	if !strings.Contains(out, "interject") || !strings.Contains(out, "queued") {
		t.Errorf("view should label both levels:\n%s", out)
	}
}

// TestQueueEmptyStateExplainsTheKeys — the panel doubles as where the
// Ctrl+G gesture is discoverable.
func TestQueueEmptyStateExplainsTheKeys(t *testing.T) {
	q := NewQueue(&queueCtrl{})
	out := q.View(80, theme.Default())
	if !strings.Contains(out, "Nothing is queued") {
		t.Errorf("missing empty state:\n%s", out)
	}
	if !strings.Contains(out, "Ctrl+G") {
		t.Errorf("empty state should teach the interject key:\n%s", out)
	}
}

func TestQueueRevokeRemovesRow(t *testing.T) {
	c := &queueCtrl{allow: true, rows: []ui.PendingPrompt{
		{ID: "p1", Text: "one", Level: "queue"},
		{ID: "p2", Text: "two", Level: "queue"},
	}}
	q := NewQueue(c)
	if closed, _ := q.Update(key("d")); closed {
		t.Fatal("revoke should not close the panel")
	}
	if len(c.revoked) != 1 || c.revoked[0] != "p1" {
		t.Fatalf("revoked = %v, want [p1]", c.revoked)
	}
	out := q.View(80, theme.Default())
	if strings.Contains(out, "one") {
		t.Errorf("revoked row still rendered:\n%s", out)
	}
	if !strings.Contains(out, "revoked") {
		t.Errorf("missing confirmation notice:\n%s", out)
	}
}

// TestQueueRevokeTooLateSaysSo is the honesty case: the loop drained the
// message between render and keypress, and pretending otherwise would leave
// the operator believing they unsent something the model has read.
func TestQueueRevokeTooLateSaysSo(t *testing.T) {
	c := &queueCtrl{allow: false, rows: []ui.PendingPrompt{{ID: "p1", Text: "one", Level: "queue"}}}
	q := NewQueue(c)
	q.Update(key("d"))
	out := q.View(80, theme.Default())
	if !strings.Contains(out, "too late") {
		t.Errorf("a failed revoke must say so:\n%s", out)
	}
}

// TestQueueCursorStaysInBounds: the loop drains underneath the panel, so
// the cursor must survive the list shrinking to nothing.
func TestQueueCursorStaysInBounds(t *testing.T) {
	c := &queueCtrl{allow: true, rows: []ui.PendingPrompt{{ID: "p1", Text: "one", Level: "queue"}}}
	q := NewQueue(c)
	q.Update(key("j"))
	q.Update(key("d"))
	if q.cursor != 0 {
		t.Errorf("cursor = %d after emptying the list, want 0", q.cursor)
	}
	// A revoke against an empty list must be a no-op, not an index panic.
	if closed, _ := q.Update(key("d")); closed {
		t.Error("no-op revoke closed the panel")
	}
}

func TestQueueEscCloses(t *testing.T) {
	q := NewQueue(&queueCtrl{})
	closed, _ := q.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !closed {
		t.Error("Esc should close /queue")
	}
}

// TestQueueOneLineFlattensPastes keeps a pasted stack trace from pushing
// the panel off screen.
func TestQueueOneLineFlattensPastes(t *testing.T) {
	got := oneLine("first line\nsecond line\n\n  third", 100)
	if strings.Contains(got, "\n") {
		t.Errorf("oneLine kept a newline: %q", got)
	}
	if got != "first line second line third" {
		t.Errorf("oneLine = %q", got)
	}
	if long := oneLine(strings.Repeat("x", 200), 30); len([]rune(long)) != 30 {
		t.Errorf("oneLine width = %d, want 30", len([]rune(long)))
	}
}
