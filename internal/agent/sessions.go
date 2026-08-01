// Session lifecycle beyond load-and-list: fork, title, pin, delete.
//
// The pieces the /resume picker and the `evva resume|sessions|export`
// subcommands need in order to treat sessions as things the operator
// curates, not just a directory that accumulates.
package agent

import (
	"fmt"
	"time"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/common"
)

// resolveSession finds a session by id, trying this agent's own workdir
// first and falling back to a machine-wide scan.
//
// The fast path is the overwhelmingly common one — the operator is
// resuming something from the project they are standing in — and the scan
// only pays for itself when they are not. Returns the workdir slug the
// session lives under, which every mutating call needs to name the file.
func (a *Agent) resolveSession(id string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("agent: empty session id")
	}
	if a.cfg == nil || a.cfg.AppHome == "" {
		return "", fmt.Errorf("agent: no app home configured")
	}
	if slug := a.sessionSlug(); slug != "" {
		if _, err := session.Load(a.cfg.AppHome, slug, id); err == nil {
			return slug, nil
		}
	}
	all, _, err := session.ListAll(a.cfg.AppHome)
	if err != nil {
		return "", err
	}
	for _, h := range all {
		if h.SessionID == id {
			return h.WorkdirSlug, nil
		}
	}
	return "", fmt.Errorf("agent: no session %q on this machine", id)
}

// ForkSession branches the live session. Implements ui.Controller.
//
// Order matters: the parent is persisted BEFORE the id changes, so the
// branch point is durable even if evva dies in the next instant. After
// the swap the agent is the child — same conversation in memory, new
// identity on disk — and the parent's file is never written again by this
// process.
//
// The child's checkpoint namespace is empty because it is keyed by session
// id. That is the whole of the PRD's "a fork's rewind cannot cross the
// fork point": not a rule the code enforces, a consequence of where
// checkpoints live.
func (a *Agent) ForkSession() (string, error) {
	if a.IsSubagent() {
		return "", fmt.Errorf("agent: only the root agent can fork its session")
	}
	if a.running.Load() {
		return "", ErrRunInProgress
	}
	if a.cfg == nil || a.cfg.AppHome == "" || a.workdir == "" {
		return "", fmt.Errorf("agent: cannot fork without cfg + workdir")
	}

	a.persistSession()

	parentID := a.ID
	atLen := len(a.session.GetMessages())
	now := time.Now().UTC()

	parentMeta := a.sessionMeta
	parentMeta.SessionID = parentID
	child := session.ForkMeta(parentMeta, common.GenUUID(), atLen, now)

	a.ID = child.SessionID
	a.sessionMeta = child
	a.sessionCreatedAt = now
	a.checkpoints.SetSession(a.ID)
	// A fresh, empty board: workflow tasks are keyed by session and the
	// child owns none of the parent's in-flight work.
	a.rescopeWorkflow(false)
	a.persistSession()

	a.logger.Info("agent: session forked", "parent", parentID, "child", a.ID, "messages", atLen)
	return a.ID, nil
}

// SetSessionTitle names a persisted session. Implements ui.Controller.
//
// Naming the live session writes the on-disk file AND the in-memory
// envelope; without the second half the next auto-save would restore the
// old title within seconds, which reads as the command silently failing.
func (a *Agent) SetSessionTitle(id, title string) error {
	if id == "" {
		id = a.ID
	}
	slug, err := a.resolveSession(id)
	if err != nil {
		return err
	}
	if err := session.SetFlags(a.cfg.AppHome, slug, id, &title, nil); err != nil {
		return err
	}
	if id == a.ID {
		a.sessionMeta.Title = title
	}
	return nil
}

// PinSession marks a session exempt from retention. Implements
// ui.Controller. Mirrors SetSessionTitle's live-session write-through.
func (a *Agent) PinSession(id string, pinned bool) error {
	if id == "" {
		id = a.ID
	}
	slug, err := a.resolveSession(id)
	if err != nil {
		return err
	}
	if err := session.SetFlags(a.cfg.AppHome, slug, id, nil, &pinned); err != nil {
		return err
	}
	if id == a.ID {
		a.sessionMeta.Pinned = pinned
	}
	return nil
}

// DeleteSession removes a persisted session. Implements ui.Controller.
//
// Refuses the live session: its file is recreated by the next iteration
// boundary, so "delete" would be a flicker rather than a deletion. /clear
// is the command for letting go of the current conversation.
func (a *Agent) DeleteSession(id string) error {
	if id == "" {
		return fmt.Errorf("agent: empty session id")
	}
	if id == a.ID {
		return fmt.Errorf("agent: cannot delete the session you are in — /clear starts a new one and leaves this in /resume")
	}
	slug, err := a.resolveSession(id)
	if err != nil {
		return err
	}
	return session.Delete(a.cfg.AppHome, slug, id)
}
