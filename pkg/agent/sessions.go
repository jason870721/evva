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
