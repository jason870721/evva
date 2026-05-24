// Package hooks implements evva's lifecycle extension system.
//
// Hooks are user-authored shell commands or HTTP webhooks that fire at six
// well-defined moments in the agent loop: SessionStart, UserPromptSubmit,
// PreToolUse, PostToolUse, Stop, Notification. The package is pure
// (no logging, no filesystem) except for loader.go (settings.json I/O) and
// runner.go / http.go (subprocess + HTTP I/O at fire time).
//
// Hooks compose with permissions. PreToolUse runs BEFORE the permission gate
// and may return a permissionDecision (allow/deny/ask) that overrides the
// gate, or an updatedInput that mutates the tool's args before the gate sees
// them. PostToolUse is non-blocking — it can only append additionalContext
// to the tool's result for the LLM's next turn.
package hooks

// Event identifies one of the six hook fire points.
type Event string

const (
	EventSessionStart     Event = "SessionStart"
	EventUserPromptSubmit Event = "UserPromptSubmit"
	EventPreToolUse       Event = "PreToolUse"
	EventPostToolUse      Event = "PostToolUse"
	EventStop             Event = "Stop"
	EventNotification     Event = "Notification"
)

// Valid reports whether e is a known event name.
func (e Event) Valid() bool {
	switch e {
	case EventSessionStart, EventUserPromptSubmit, EventPreToolUse,
		EventPostToolUse, EventStop, EventNotification:
		return true
	}
	return false
}

// CommandType discriminates the two hook execution backends. v1 supports
// shell subprocess and HTTP webhook.
type CommandType string

const (
	TypeCommand CommandType = "command"
	TypeHTTP    CommandType = "http"
)

// Command is one hook entry — either a shell command or an HTTP webhook
// invocation. Fields are normalized at load time so the dispatcher can
// treat both kinds uniformly until it dispatches.
type Command struct {
	Type    CommandType
	Command string            // for TypeCommand: the shell command
	URL     string            // for TypeHTTP: the endpoint
	Method  string            // for TypeHTTP: HTTP method (default POST)
	Headers map[string]string // for TypeHTTP
	Timeout int               // seconds, 0 = use event default
	Async   bool              // fire-and-forget when true (default true for http)
}

// Config groups a matcher pattern with the hooks that fire when the
// matcher matches. Matcher is empty for events that don't carry a tool
// name (SessionStart, Stop, Notification) — those run unconditionally.
type Config struct {
	Matcher string // tool-name glob; empty = match-all
	Hooks   []Command
}

// Decision is the parsed shape of a hook command's stdout JSON. Fields
// are optional; an empty Decision means "no opinion, pass through."
//
// The dispatcher interprets these per event:
//   - PreToolUse: PermissionDecision and HookSpecificOutput.UpdatedInput
//     influence the gate. Decision="block" or Continue=false blocks the tool.
//   - PostToolUse: HookSpecificOutput.AdditionalContext is appended to the
//     tool result. Block/Continue ignored.
//   - UserPromptSubmit: AdditionalContext appended to prompt; Decision="block"
//     or Continue=false drops the prompt.
//   - Stop: Decision="block" or Continue=false re-enters the loop once.
//   - SessionStart: AdditionalContext and HookSpecificOutput.InitialUserMessage
//     prepended to the first prompt.
//   - Notification: stdout ignored.
type Decision struct {
	Continue           *bool                  // pointer so we distinguish unset
	Decision           string                 // "" | "approve" | "block"
	Reason             string
	SystemMessage      string
	HookSpecificOutput map[string]any         // raw — fields pulled per event
}

// PreToolUseDecision is the resolved verdict from PreToolUse hooks, as
// consumed by the agent loop. Built by the dispatcher after running all
// matched hooks sequentially.
type PreToolUseDecision struct {
	PermissionDecision string // "" | "allow" | "deny" | "ask"
	Reason             string
	UpdatedInput       []byte // raw JSON of the new tool input, nil if unchanged
	AdditionalContext  string
	Blocked            bool
	BlockReason        string
}
