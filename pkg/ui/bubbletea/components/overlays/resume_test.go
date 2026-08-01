package overlays

import (
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/ui"
)

// resumeCtrl is a ui.Controller stub answering only the session surface the
// picker uses. Embedding the interface leaves everything else nil-panicking,
// which is the point: a test that reaches further should fail loudly.
type resumeCtrl struct {
	ui.Controller
	rows    []ui.SessionInfo
	all     []ui.SessionInfo
	pinned  map[string]bool
	deleted []string
	delErr  error
}

func (c *resumeCtrl) ListSessions() ([]ui.SessionInfo, []string)    { return c.rows, nil }
func (c *resumeCtrl) ListAllSessions() ([]ui.SessionInfo, []string) { return c.all, nil }
func (c *resumeCtrl) PinSession(id string, pinned bool) error {
	if c.pinned == nil {
		c.pinned = map[string]bool{}
	}
	c.pinned[id] = pinned
	for i := range c.rows {
		if c.rows[i].ID == id {
			c.rows[i].Pinned = pinned
		}
	}
	return nil
}
func (c *resumeCtrl) DeleteSession(id string) error {
	if c.delErr != nil {
		return c.delErr
	}
	c.deleted = append(c.deleted, id)
	kept := c.rows[:0:0]
	for _, r := range c.rows {
		if r.ID != id {
			kept = append(kept, r)
		}
	}
	c.rows = kept
	return nil
}

func key(s string) tea.KeyMsg {
	if len(s) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
	}
	switch s {
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func sess(id, parent string, updated int64) ui.SessionInfo {
	return ui.SessionInfo{ID: id, ParentID: parent, Label: id, UpdatedAt: updated}
}

// A fork renders directly under the session it branched from, indented,
// regardless of where its timestamp would otherwise place it.
func TestArrangeForkTreeNestsChildren(t *testing.T) {
	rows := arrangeForkTree([]ui.SessionInfo{
		sess("root-new", "", 500),
		sess("child", "root-old", 400),
		sess("root-old", "", 300),
		sess("grandchild", "child", 200),
	})

	var order []string
	depth := map[string]int{}
	for _, r := range rows {
		order = append(order, r.info.ID)
		depth[r.info.ID] = r.depth
	}
	want := []string{"root-new", "root-old", "child", "grandchild"}
	for i := range want {
		if i >= len(order) || order[i] != want[i] {
			t.Fatalf("tree order: got %v, want %v", order, want)
		}
	}
	if depth["root-new"] != 0 || depth["root-old"] != 0 {
		t.Errorf("roots should be at depth 0: %v", depth)
	}
	if depth["child"] != 1 || depth["grandchild"] != 2 {
		t.Errorf("forks should nest: %v", depth)
	}
}

// A fork whose parent is not in the current view is still shown — hiding
// it would make the list lie about what exists on disk.
func TestArrangeForkTreeShowsOrphansAsRoots(t *testing.T) {
	rows := arrangeForkTree([]ui.SessionInfo{sess("orphan", "long-gone", 100)})
	if len(rows) != 1 {
		t.Fatalf("orphan dropped: %+v", rows)
	}
	if rows[0].depth != 0 {
		t.Errorf("an orphan renders as a root; got depth %d", rows[0].depth)
	}
}

func TestResumePinTogglesSelectedRow(t *testing.T) {
	c := &resumeCtrl{rows: []ui.SessionInfo{sess("a", "", 200), sess("b", "", 100)}}
	r := NewResume(c)

	r.Update(key("down")) // move to "b"
	r.Update(key("p"))
	if !c.pinned["b"] {
		t.Fatalf("expected b pinned; got %v", c.pinned)
	}
	r.Update(key("p"))
	if c.pinned["b"] {
		t.Errorf("a second press should unpin; got %v", c.pinned)
	}
}

// Deletion is unrecoverable and 'd' sits next to the cursor keys, so the
// first press only arms it.
func TestResumeDeleteRequiresConfirmation(t *testing.T) {
	c := &resumeCtrl{rows: []ui.SessionInfo{sess("a", "", 200), sess("b", "", 100)}}
	r := NewResume(c)

	r.Update(key("d"))
	if len(c.deleted) != 0 {
		t.Fatal("the first 'd' must not delete anything")
	}
	r.Update(key("d"))
	if len(c.deleted) != 1 || c.deleted[0] != "a" {
		t.Fatalf("the second 'd' should delete the selected row; got %v", c.deleted)
	}
	if len(r.rows) != 1 {
		t.Errorf("the list should reload after a delete; got %d rows", len(r.rows))
	}
}

// Moving away must disarm: otherwise a 'd', a cursor move, and a second
// 'd' would delete a row the operator never confirmed.
func TestResumeDeleteConfirmationIsCancelledByAnyOtherKey(t *testing.T) {
	c := &resumeCtrl{rows: []ui.SessionInfo{sess("a", "", 200), sess("b", "", 100)}}
	r := NewResume(c)

	r.Update(key("d"))
	r.Update(key("down"))
	r.Update(key("d"))
	if len(c.deleted) != 0 {
		t.Fatalf("a cursor move between the two presses must disarm; deleted %v", c.deleted)
	}
	r.Update(key("d"))
	if len(c.deleted) != 1 || c.deleted[0] != "b" {
		t.Errorf("confirming on the new row should delete that row; got %v", c.deleted)
	}
}

func TestResumeAllTogglesScope(t *testing.T) {
	c := &resumeCtrl{
		rows: []ui.SessionInfo{sess("here", "", 200)},
		all:  []ui.SessionInfo{sess("here", "", 200), sess("elsewhere", "", 100)},
	}
	r := NewResume(c)
	if len(r.rows) != 1 {
		t.Fatalf("the picker opens scoped to this workdir; got %d rows", len(r.rows))
	}
	r.Update(key("a"))
	if len(r.rows) != 2 {
		t.Fatalf("[a] should widen to every workdir; got %d rows", len(r.rows))
	}
	r.Update(key("a"))
	if len(r.rows) != 1 {
		t.Errorf("[a] should toggle back; got %d rows", len(r.rows))
	}
}

func TestResumeSurfacesDeleteErrors(t *testing.T) {
	c := &resumeCtrl{rows: []ui.SessionInfo{sess("a", "", 200)}, delErr: errors.New("cannot delete the session you are in")}
	r := NewResume(c)
	r.Update(key("d"))
	r.Update(key("d"))
	if r.errMsg == "" {
		t.Error("a refused delete must be shown, not swallowed")
	}
}

func TestResumeEmptyListIsNotAnError(t *testing.T) {
	r := NewResume(&resumeCtrl{})
	if r == nil {
		t.Fatal("the picker should open even with nothing to show")
	}
	// Keys on an empty list must not panic.
	r.Update(key("d"))
	r.Update(key("p"))
	r.Update(key("down"))
	if done, _ := r.Update(key("enter")); !done {
		t.Error("Enter on an empty list should close the overlay")
	}
}
