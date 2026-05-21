package agent

import (
	"time"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/session"
)

// persistSession snapshots the current session state to disk under
//
//	<APP_HOME>/sessions/<workdir-slug>/<a.ID>.json
//
// Called at iteration boundaries (after the assistant turn lands, after
// tool results land, after a full compact) so /resume always has a
// recent checkpoint. Best-effort: any I/O failure is logged at warn
// level but never aborts the loop — losing one save is recoverable;
// crashing the agent because of it is not.
//
// Skipped for subagents (their transcripts are ephemeral by design;
// only the root agent's session is persisted) and when no config is
// installed (test harnesses that wire agents without cmd/evva).
func (a *Agent) persistSession() {
	if a.IsSubagent() {
		return
	}
	if a.cfg == nil || a.cfg.AppHome == "" {
		return
	}
	if a.workdir == "" {
		return
	}
	slug := memdir.ProjectKey(a.workdir)
	if slug == "" {
		return
	}
	now := time.Now().UTC()
	snap := &session.Snapshot{
		Version:         session.SnapshotVersion,
		SessionID:       a.ID,
		Workdir:         a.workdir,
		WorkdirSlug:     slug,
		Profile:         a.activePersona,
		Provider:        a.profile.LLMProvider.Name,
		Model:           string(a.profile.LLMModel),
		CreatedAt:       a.sessionCreatedAt,
		UpdatedAt:       now,
		FirstUserPrompt: session.FirstUserPromptPreview(a.session.GetMessages()),
		Session:         a.session.ToSnapshot(),
	}
	if snap.CreatedAt.IsZero() {
		snap.CreatedAt = now
		a.sessionCreatedAt = now
	}
	if err := session.Save(a.cfg.AppHome, snap); err != nil {
		a.logger.Warn("session.persist", "err", err, "id", a.ID, "slug", slug)
		return
	}
	a.logger.Debug("session.persist.ok", "id", a.ID, "messages", len(snap.Session.Messages))
}
