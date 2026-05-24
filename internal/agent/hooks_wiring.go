package agent

import "github.com/johnny1110/evva/pkg/hooks"

// hookBaseFactory builds a fresh hooks.BasePayload each time a hook fires.
// Live fields (PermissionMode) can change mid-session, so the dispatcher
// rebuilds the base every fire instead of caching it.
func (a *Agent) hookBaseFactory() hooks.BasePayload {
	return hooks.BasePayload{
		SessionID:      a.ID,
		TranscriptPath: "", // not yet implemented; BasePayload is omitempty
		Cwd:            a.Workdir(),
		PermissionMode: string(a.PermissionMode()),
		AgentID:        a.ID,
		AgentType:      a.profile.Type.String(),
	}
}
