package agent

import (
	"os"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/session"
)

// ResetWorkdirSessions deletes every persisted session snapshot for a workdir —
// the whole <appHome>/sessions/<workdir-slug>/ directory that the agent loop
// writes to and ResumeSession reads from. After it, a freshly built agent for
// that workdir finds nothing to resume: it starts with empty context.
//
// This is the public seam a host uses to wipe a project's conversation history
// without reaching into the agent's internal session store — e.g. a swarm
// "reset" that clears every member's transcript. Best-effort and idempotent: a
// missing directory (no prior sessions) is not an error; an empty appHome or
// workdir is a no-op.
func ResetWorkdirSessions(appHome, workdir string) error {
	slug := memdir.ProjectKey(workdir)
	if appHome == "" || slug == "" {
		return nil
	}
	return os.RemoveAll(session.SessionsDir(appHome, slug))
}

// ResetPersonaSessions deletes every persisted snapshot that belongs to one
// persona under a workdir — the per-member complement of
// ResetWorkdirSessions. A swarm member's transcripts are keyed by persona ==
// member name in the shared <appHome>/sessions/<workdir-slug>/ directory, so
// a member-scoped clear must filter by snapshot Profile instead of removing
// the whole directory (which would wipe its teammates too).
//
// Best-effort and idempotent like its workdir-wide sibling: missing
// directory, empty inputs, or zero matches are not errors. Corrupt files are
// skipped (they don't parse, so their Profile is unknowable).
func ResetPersonaSessions(appHome, workdir, persona string) error {
	slug := memdir.ProjectKey(workdir)
	if appHome == "" || slug == "" || persona == "" {
		return nil
	}
	entries, _, err := session.List(appHome, slug)
	if err != nil {
		return err
	}
	var firstErr error
	for _, e := range entries {
		if e.Snapshot.Profile != persona {
			continue
		}
		if err := session.Delete(appHome, slug, e.Snapshot.SessionID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
