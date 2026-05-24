package hooks

import "encoding/json"

// BasePayload is the common envelope shared by every hook event. Field
// names use snake_case so the JSON shipped to hook commands matches
// Claude Code's settings-file consumers verbatim.
type BasePayload struct {
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path,omitempty"`
	Cwd            string `json:"cwd"`
	PermissionMode string `json:"permission_mode,omitempty"`
	AgentID        string `json:"agent_id,omitempty"`
	AgentType      string `json:"agent_type,omitempty"`
	HookEventName  string `json:"hook_event_name"`
}

// SessionStartPayload fires when an agent first runs. Source is "startup"
// (initial Run) — "resume" / "clear" / "compact" reserved for later phases.
type SessionStartPayload struct {
	BasePayload
	Source string `json:"source"`
	Model  string `json:"model,omitempty"`
}

// UserPromptSubmitPayload fires once per user prompt before it's appended
// to the session.
type UserPromptSubmitPayload struct {
	BasePayload
	Prompt string `json:"prompt"`
}

// PreToolUsePayload fires before the permission gate runs. ToolInput is
// the raw JSON the LLM emitted; a hook can return an updatedInput to
// mutate it before the tool sees it.
type PreToolUsePayload struct {
	BasePayload
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	ToolUseID string          `json:"tool_use_id"`
}

// PostToolUsePayload fires after the tool returns. ToolResponse is the
// tool's serialized result content; IsError mirrors result.IsError.
type PostToolUsePayload struct {
	BasePayload
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse string          `json:"tool_response"`
	IsError      bool            `json:"is_error"`
	ToolUseID    string          `json:"tool_use_id"`
}

// StopPayload fires when the main agent reaches a terminal turn (no more
// tool calls). LastAssistantMessage carries the model's last reply so
// hooks can summarize / log. StopHookActive is true on a re-entry pass
// after a previous Stop hook blocked — used to prevent infinite loops.
type StopPayload struct {
	BasePayload
	StopHookActive      bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message,omitempty"`
}

// NotificationPayload fires for out-of-band events: iteration limit,
// internal errors, approval-needed. NType is a short tag so hooks can
// route on it (e.g. only Slack-ping on approval_needed).
type NotificationPayload struct {
	BasePayload
	Message string `json:"message"`
	Title   string `json:"title,omitempty"`
	NType   string `json:"notification_type,omitempty"`
}
