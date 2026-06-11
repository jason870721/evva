// Package event defines the event stream the agent emits while running.
//
// The Event envelope is a discriminated union — every event has a Kind and
// exactly one non-nil typed payload field. This keeps consumer code
// type-safe (no interface{} assertions, no reflection) while still allowing
// one Sink to receive every kind of event the agent might emit.
//
// State-change events from backing stores (task list, subagent panel, future
// notes/todos/...) all flow through a single KindStoreUpdate so adding a
// new panel never requires a new event kind. The store's domain identifier
// (see internal/observable.Change.Domain) selects how the consumer renders
// the row.
//
// Sinks (see sink.go) are the consumer side. A TUI, a structured logger,
// and a JSON-over-websocket bridge can each implement Sink and subscribe
// independently of one another — the agent doesn't know about them.
package event

import (
	"encoding/json"
	"time"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// Kind tags every event. New kinds are added by extending this list and the
// matching payload field on Event.
type Kind string

const (
	// KindIdle marks the agent as inactive — no Run in flight. Useful for
	// status-bar widgets that want a "ready" indicator distinct from "running".
	KindIdle Kind = "idle"
	// KindRunStart fires once at the top of every Agent.Run invocation;
	// payload carries the user prompt that kicked off the run.
	KindRunStart Kind = "run_start"
	// KindRunResume fires when Agent.Continue resumes after an iter-limit
	// pause; payload carries the message index the resume picks up from.
	KindRunResume Kind = "run_resume"
	// KindRunEnd fires once per Run — terminal, win or lose; payload
	// carries the final iteration count and assistant text/thinking.
	KindRunEnd Kind = "run_end"
	// KindRunCancelled fires when context cancellation tore the run down
	// mid-flight (Ctrl-C, deadline, etc.). No payload.
	KindRunCancelled Kind = "run_cancelled"
	// KindIterLimit fires when the loop hits Agent.maxIters and pauses
	// without finishing. The caller may invoke Agent.Continue to resume.
	KindIterLimit Kind = "iter_limit"

	// KindTurnStart / KindTurnEnd bracket one iteration of the loop. Payload
	// carries the iteration index so subscribers can scope sub-events to a turn.
	KindTurnStart Kind = "turn_start"
	KindTurnEnd   Kind = "turn_end"

	// KindDrainingInfo signals the agent is folding deferred information
	// from subagents or background bash into the parent context. Cosmetic
	// — useful for a status bar "draining…" hint.
	KindDrainingInfo = "draining_info"
	// KindThinking carries the assistant's reasoning text as one full
	// block (buffered providers). Streaming providers emit KindThinkingChunk
	// deltas instead, then skip the final KindThinking to avoid duplication.
	KindThinking Kind = "thinking"
	// KindText carries the assistant's final text as one full block
	// (buffered providers). Streaming providers emit KindTextChunk deltas
	// instead, then skip the final KindText.
	KindText Kind = "text"

	// KindTextChunk and KindThinkingChunk are emitted by the streaming
	// path. Each carries an incremental delta in TextPayload.Text; the
	// UI accumulates consecutive chunks of the same kind into one logical
	// block. Reset on KindTurnEnd. Streaming agents emit chunks only —
	// the final KindText / KindThinking is skipped to avoid duplication.
	KindTextChunk     Kind = "text_chunk"
	KindThinkingChunk Kind = "thinking_chunk"

	// KindToolUseStart fires at tool-dispatch time, carrying the tool name
	// + raw JSON input. The matching KindToolUseResult follows when the
	// tool returns.
	KindToolUseStart Kind = "tool_use_start"
	// KindToolUseResult fires when a tool returns. Pairs with the prior
	// KindToolUseStart by ToolID.
	KindToolUseResult Kind = "tool_use_result"

	// KindApprovalNeeded is emitted when the permission gate decides a tool
	// call must be approved by the user. The TUI subscribes, opens an
	// approval overlay, and calls Broker.Respond with the user's decision.
	// The blocked tool goroutine sleeps in Broker.Request until the answer
	// arrives (or the context is cancelled).
	KindApprovalNeeded Kind = "approval_needed"

	// KindQuestionNeeded is emitted when the AskUserQuestion tool is invoked.
	// The TUI subscribes, opens a question overlay, and calls
	// Controller.RespondQuestion with the user's answers. The blocked tool
	// goroutine sleeps in question.Broker.Request until the answer arrives.
	KindQuestionNeeded Kind = "question_needed"

	// KindCompacting fires when the agent starts a session compaction
	// (micro or full). Pairs with KindCompactingEnd.
	KindCompacting Kind = "compacting"
	// KindCompactingEnd pairs with KindCompacting; OK reports success or
	// failure so the TUI can swap the spinner for the right final block.
	KindCompactingEnd Kind = "compacting_end"

	// KindError reports a Go-level failure that aborted the loop. Tool
	// errors surfaced via Result.IsError do NOT produce this event —
	// they flow through KindToolUseResult so the model can recover.
	KindError Kind = "error"

	// KindStoreUpdate carries every state change emitted by an
	// observable.Store registered on the agent's ToolState. The consumer
	// switches on StoreUpdatePayload.Domain to decide how to render.
	KindStoreUpdate Kind = "store_update"

	// KindUsage reports per-turn token usage plus the running session
	// total after the turn is folded in.
	KindUsage Kind = "usage"

	// KindModeChanged fires whenever the agent's permission mode changes
	// — Shift+Tab cycle, EnterPlanMode / ExitPlanMode tool calls, or a
	// SwitchProfile that resets the mode. Lets the TUI sync the status-
	// bar indicator without having to poll Agent.PermissionMode each
	// render. Emitted only by the root agent; subagent mode changes
	// stay internal.
	KindModeChanged Kind = "mode_changed"

	// KindBgResult fires when a background bash task transitions to a
	// terminal state (completed / failed / killed). Emitted whether the
	// agent loop is busy or idle — the TUI uses this to render the
	// "task-xxx completed." transcript line. Loop-side drain folds the
	// result into the conversation separately via KindDrainBackgroundTask.
	KindBgResult Kind = "bg_result"

	// KindMonitorEvent fires for every stdout line a running MonitorTool
	// streams plus the closing event when the monitor stops. The TUI
	// renders these inline in the transcript; loop-side drain folds the
	// queued events into the conversation via KindDrainMonitorEvents.
	KindMonitorEvent Kind = "monitor_event"

	// KindDrainBackgroundTask fires at the moment the agent loop folds
	// drained background-task results into the session as a synthetic
	// user message. Payload carries the task ids that were folded in.
	KindDrainBackgroundTask Kind = "drain_background_task"

	// KindDrainMonitorEvents fires at the moment the agent loop folds
	// queued monitor events into the session. Payload carries the line
	// count drained (events from multiple monitors are interleaved).
	KindDrainMonitorEvents Kind = "drain_monitor_events"

	// KindDrainInbox fires when the agent loop folds a message pulled from a
	// pluggable inbox Drainer (pkg/agent.WithInboxDrainer) into the session as
	// a synthetic user turn — the generalisation of the background-task drain
	// that lets a busy agent react to an incoming message mid-run. Payload
	// carries how many messages were folded on this boundary.
	KindDrainInbox Kind = "drain_inbox"
)

// Event is the envelope. Exactly one of the *Payload fields is non-nil per
// event, matched to Kind. Consumers should switch on Kind and read the
// corresponding field directly — type-safe access, no reflection.
//
// AgentID identifies the emitter. ParentID is empty for the root agent and
// equal to the root's AgentID for subagent events (the hierarchy is always
// exactly two layers — subagents cannot spawn subagents).
type Event struct {
	Kind     Kind
	AgentID  string
	ParentID string
	Time     time.Time

	RunStart       *RunStartPayload       `json:",omitempty"`
	RunResume      *RunResumePayload      `json:",omitempty"`
	RunEnd         *RunEndPayload         `json:",omitempty"`
	IterLimit      *IterLimitPayload      `json:",omitempty"`
	Turn           *TurnPayload           `json:",omitempty"`
	Thinking       *TextPayload           `json:",omitempty"`
	Text           *TextPayload           `json:",omitempty"`
	ToolUseStart   *ToolUseStartPayload   `json:",omitempty"`
	ToolUseResult  *ToolUseResultPayload  `json:",omitempty"`
	ApprovalNeeded *ApprovalNeededPayload `json:",omitempty"`
	QuestionNeeded *QuestionNeededPayload `json:",omitempty"`
	Error          *ErrorPayload          `json:",omitempty"`
	StoreUpdate    *StoreUpdatePayload    `json:",omitempty"`
	Usage          *UsagePayload          `json:",omitempty"`
	Compacting     *CompactingPayload     `json:",omitempty"`
	CompactingEnd  *CompactingEndPayload  `json:",omitempty"`
	ModeChanged    *ModeChangedPayload    `json:",omitempty"`

	BgResult            *BgResultPayload            `json:",omitempty"`
	MonitorEvent        *MonitorEventPayload        `json:",omitempty"`
	DrainBackgroundTask *DrainBackgroundTaskPayload `json:",omitempty"`
	DrainMonitorEvents  *DrainMonitorEventsPayload  `json:",omitempty"`
	DrainInbox          *DrainInboxPayload          `json:",omitempty"`
}

// ModeChangedPayload reports a permission-mode transition. PrevMode is the
// mode that was active before the change (empty on the very first
// initialization); Mode is the new mode. Both are the wire string form
// (permission.Mode is type-aliased to string for the same reason).
type ModeChangedPayload struct {
	// PrevMode is the mode that was active before the change. Empty on the
	// very first initialization.
	PrevMode string
	// Mode is the new mode the agent has transitioned into.
	Mode string
}

// --- payload types ---

// RunStartPayload carries the user prompt that kicked off a Run.
type RunStartPayload struct {
	// Prompt is the user message that opened this Run.
	Prompt string
}

// RunResumePayload carries the message index Agent.Continue resumed from
// after an iter-limit pause.
type RunResumePayload struct {
	// FromMessageIndex is the position in the session transcript where
	// Continue picked up.
	FromMessageIndex int
}

// RunEndPayload carries the final state of a completed Run.
type RunEndPayload struct {
	// Iters is the number of iterations the loop consumed before ending.
	Iters int
	// Content is the assistant's final text for the Run.
	Content string
	// Thinking is the assistant's reasoning text (if any).
	Thinking string
	// Usage is this run's token cost (RP-28): the session-usage delta from
	// loop entry (Run, or Continue after an iter-limit pause) to this event.
	// Cache fields carry the provider's prompt-cache accounting where
	// reported, zero otherwise. nil when the provider reported no usage at
	// all (e.g. a stub client) — absent, never fabricated.
	Usage *llm.Usage `json:",omitempty"`
}

// IterLimitPayload is emitted when the loop hits Agent.maxIters. The UI
// should prompt the user (e.g. "press Enter to keep going") and call
// Agent.Continue to resume; the loop is paused, not failed.
type IterLimitPayload struct {
	// Iters is the iteration count the loop hit before pausing — matches
	// RunEndPayload.Iters naming so callers can read iteration counts
	// from either payload without remembering two field names.
	Iters int
}

// TurnPayload carries the iteration index a TurnStart/TurnEnd event refers to.
type TurnPayload struct {
	// Iteration is the zero-based loop iteration index.
	Iteration int
}

// TextPayload carries an opaque text chunk — used for both Thinking and
// Text events. With streaming completions this becomes a stream of chunks;
// today it carries the full block.
type TextPayload struct {
	// Text is the assistant text content (a full block, or one streaming
	// delta when the event Kind is KindTextChunk / KindThinkingChunk).
	Text string
}

// ToolUseStartPayload reports a tool dispatch.
type ToolUseStartPayload struct {
	// Name is the tool's wire name (matches tools.ToolName).
	Name string
	// Input is the raw JSON the LLM passed to the tool — UIs typically
	// summarise one field rather than dumping the whole blob.
	Input json.RawMessage
	// ToolID correlates this dispatch with its eventual ToolUseResult.
	ToolID string
}

// ToolUseResultPayload reports the outcome of a single tool call.
//
// Metadata is an optional tool-specific structured payload (e.g. a
// *fs.FileDiff for write_file / edit_file). Carried opaquely through this
// layer; the UI type-asserts. Never sent to the LLM — Content alone is the
// model-facing summary.
type ToolUseResultPayload struct {
	// ToolID correlates this result with the prior ToolUseStart.
	ToolID string
	// Content is the LLM-facing text summary the tool produced.
	Content string
	// IsError is true when the tool itself returned an error result.
	// Distinct from a Go-level failure (which surfaces via KindError).
	IsError bool
	// Metadata is an optional tool-specific structured payload (e.g. a
	// *fs.FileDiff for write/edit). UIs type-assert to render rich views.
	Metadata any
	// ContentBlocks carries multimodal output (text + images). Empty for
	// text-only tool results.
	ContentBlocks []tools.ContentBlock
}

// ApprovalNeededPayload is the wire shape of a pending permission prompt.
// The TUI receives one of these per blocked tool call. Carries every piece
// of context the user needs to decide: the tool name, the raw input (UI
// summarises), the active mode, and the gate's reason for asking.
//
// RequestID is the Broker's correlation key — the TUI uses it when calling
// Broker.Respond. RiskHint is non-empty for Bash; other tools see "".
// PlanContent is non-empty only for ExitPlanMode — carries the markdown
// plan body so the approval overlay can render it inline.
type ApprovalNeededPayload struct {
	// RequestID is the Broker correlation key; the TUI passes it back to
	// Broker.Respond when forwarding the user's choice.
	RequestID string
	// ToolName is the wire name of the tool whose call is being gated.
	ToolName string
	// ToolInput is the raw JSON the LLM passed to the tool.
	ToolInput json.RawMessage
	// InputDescription is the model-supplied `description` field from
	// ToolInput; "" when the tool's input has no such field.
	InputDescription string
	// Mode is the permission mode active when the gate fired.
	Mode string
	// Reason is the gate's explanation for asking (e.g. "matches dangerous prefix").
	Reason string
	// RiskHint is non-empty for Bash (the classifier's risk label);
	// empty for other tools.
	RiskHint string
	// Matched is the rule fragment that triggered the prompt, if any.
	Matched string
	// PlanContent is non-empty only for ExitPlanMode — carries the
	// markdown plan body so the approval overlay can render it inline.
	PlanContent string
}

// QuestionNeededPayload is the wire shape of a pending question prompt.
// The TUI receives one of these when AskUserQuestion is invoked. RequestID
// is the question.Broker's correlation key used when calling
// Controller.RespondQuestion.
type QuestionNeededPayload struct {
	// RequestID is the question.Broker correlation key; the TUI passes
	// it back via Controller.RespondQuestion.
	RequestID string
	// AgentID is the agent that invoked AskUserQuestion (relevant when
	// subagent question routing lands).
	AgentID string
	// Questions are the items rendered in the overlay.
	Questions []QuestionItem
}

// QuestionItem mirrors question.Question for the event layer so event.go
// does not import internal/question (which would create a cycle through
// toolset → tools/ux → question → event).
type QuestionItem struct {
	// Question is the prompt body.
	Question string
	// Header is the short chip label (max 12 chars in the canonical UI).
	Header string
	// MultiSelect controls whether the user may pick more than one option.
	MultiSelect bool
	// Options are the offered choices.
	Options []QuestionOption
}

// QuestionOption mirrors question.Option for the same reason.
type QuestionOption struct {
	// Label is the choice text shown to the user.
	Label string
	// Description is the optional explanation rendered alongside.
	Description string
	// Preview is the optional code/diagram block shown in side-by-side mode.
	Preview string
}

// ErrorPayload reports a Go-level failure that aborted the loop. Tool errors
// surfaced as Result.IsError do NOT produce this event — they flow through
// ToolUseResult so the model can recover.
type ErrorPayload struct {
	// Stage tags where in the loop the failure occurred:
	// "llm" | "tool:<name>" | "loop".
	Stage string
	// Err is the underlying Go error. Use Message for the rendered
	// string form when you only need text (covers the nil case too).
	Err error
	// Message is err.Error() captured at emit time, or "" when Err is nil.
	// Convenience for consumers that don't want to nil-check + stringify
	// (UIs, JSON serialisers).
	Message string
}

// CompactingPayload reports the start of a session compaction. Type is
// "micro" or "full"; UsageRatio is the input/output token ratio that
// triggered the compaction.
type CompactingPayload struct {
	// Type is "micro" (elide old tool results) or "full" (summarise).
	Type string
	// UsageRatio is the input/budget ratio that triggered the compaction.
	UsageRatio float64
}

// CompactingEndPayload reports the outcome of a compaction the TUI was
// painting. OK=false marks the failure path so the transcript can swap
// the spinner block for a short error line instead of just removing it.
// BriefTokens carries the size of the full-compact brief so callers
// that already painted a percent can update the figure on completion.
type CompactingEndPayload struct {
	// Type matches the prior CompactingPayload.Type — "micro" or "full".
	Type string
	// OK reports success or failure; false means the TUI should swap
	// the spinner for an error line rather than removing it silently.
	OK bool
	// BriefTokens is the size of the full-compact brief; updated UIs use
	// this to replace the percent estimate they painted at start.
	BriefTokens int
	// Err is the failure reason when OK is false; empty otherwise.
	Err string
}

// StoreUpdatePayload is the bridge between observable.Change and the event
// stream. Domain names the emitting store ("task", "subagent", ...); Op is
// the verb ("created" / "updated" / "removed" / "phase" / "done" / "crushed");
// Payload is the store's domain-typed snapshot, switched on by Domain at
// the consumer.
type StoreUpdatePayload struct {
	// Domain identifies the emitting store ("task", "subagent", …).
	Domain string
	// Op is the verb: "created" / "updated" / "removed" / "phase" / "done" / "crushed".
	Op string
	// ID is the store-local identifier (task ID, subagent ID, …).
	ID string
	// Payload is the store's domain-typed snapshot; consumers type-assert
	// based on Domain.
	Payload any
	// Time is the emit timestamp.
	Time time.Time
}

// BgResultPayload reports one background bash task's terminal outcome.
// Emitted from the agent's signal pump regardless of loop idle/busy
// state — the conversation-side fold-in happens separately via
// KindDrainBackgroundTask. The TUI uses BgResultPayload to render the
// "task-xxx completed." transcript line and update the bg-tasks strip.
type BgResultPayload struct {
	// TaskID is the wire-stable id (e.g. "b4x9z1kp") the model received
	// from the original `bash run_in_background:true` call.
	TaskID string
	// Status is the terminal lifecycle state: completed / failed / killed.
	Status string
	// ExitCode is the process exit code (0 for completed; non-zero for
	// failed; -1 or os-defined for killed).
	ExitCode int
	// Output is the captured stdout+stderr, capped at the bg path's
	// output ceiling (~64 KiB).
	Output string
	// AgentID is the spawning agent — copied so subagent bubble-up can
	// label rows without inferring from event metadata.
	AgentID string
}

// MonitorEventPayload reports one streamed line from a running Monitor.
// Closing=true marks the final event when the underlying process exits
// or daemon_stop is called; the TUI strip flips the monitor chip to
// Stopped on the closing event.
//
// Deprecated: monitor lifecycle now flows through KindStoreUpdate on the
// daemon Observable. Kept for transcript renderer back-compat until that
// surface is rewritten.
type MonitorEventPayload struct {
	// MonitorID is the wire-stable id (e.g. "m4x9z1kp") the model
	// received from the original Monitor call.
	MonitorID string
	// Line is one stdout line from the monitored command (newline
	// stripped). Empty when Closing is true.
	Line string
	// Closing reports whether this is the last event for the monitor
	// (process exited or daemon_stop fired).
	Closing bool
	// AgentID is the spawning agent.
	AgentID string
}

// DrainBackgroundTaskPayload reports the batch of bg-task ids the agent
// loop folded into the session as a synthetic user message on the
// current iteration boundary. Used by debug telemetry / log inspectors;
// the TUI does not render this directly (the per-result KindBgResult
// already rendered the chip transition).
type DrainBackgroundTaskPayload struct {
	// TaskIDs are the ids drained on this iteration boundary, in
	// completion order.
	TaskIDs []string
}

// DrainMonitorEventsPayload reports the batch of monitor events the
// agent loop folded into the session as a synthetic user message.
// EventCount is the total number of streamed lines drained (events
// from multiple monitors are interleaved by arrival time).
type DrainMonitorEventsPayload struct {
	// EventCount is the total number of monitor events folded into the
	// session on this iteration boundary.
	EventCount int
	// MonitorIDs are the unique monitor ids the drained events came
	// from, in first-occurrence order.
	MonitorIDs []string
}
type DrainInboxPayload struct {
	// Count is the number of inbox messages folded into the session on
	// this iteration boundary (currently always 1 — one Drainer call per
	// boundary — but kept as a count for forward compatibility).
	Count int
}

// UsagePayload reports token usage for the LLM call that just completed.
// Turn is the just-completed call; Cumulative is the running session total
// after Turn has been folded in. The TUI typically shows both — Turn for
// the latest cost spike, Cumulative for the session budget.
type UsagePayload struct {
	// Turn is usage for the just-completed LLM call.
	Turn llm.Usage
	// Cumulative is the running session total after Turn is folded in.
	Cumulative llm.Usage
}

// Payload returns the payload pointer matching e.Kind, or nil when the
// event Kind has no associated payload (KindIdle, KindRunCancelled,
// KindTurnStart/End in some emitters, etc.). Consumers can switch on
// the returned type instead of remembering which of the 20+ pointer
// fields on Event corresponds to each Kind:
//
//	switch p := e.Payload().(type) {
//	case *event.TextPayload:
//	    render(p.Text)
//	case *event.ToolUseStartPayload:
//	    renderToolCall(p.Name, p.Input)
//	}
//
// The direct field access (e.Text, e.ToolUseStart, …) stays available
// for callers that already do a Kind switch — Payload is purely an
// ergonomics layer.
func (e Event) Payload() any {
	switch e.Kind {
	case KindRunStart:
		return e.RunStart
	case KindRunResume:
		return e.RunResume
	case KindRunEnd:
		return e.RunEnd
	case KindIterLimit:
		return e.IterLimit
	case KindTurnStart, KindTurnEnd:
		return e.Turn
	case KindThinking, KindThinkingChunk:
		return e.Thinking
	case KindText, KindTextChunk:
		return e.Text
	case KindToolUseStart:
		return e.ToolUseStart
	case KindToolUseResult:
		return e.ToolUseResult
	case KindApprovalNeeded:
		return e.ApprovalNeeded
	case KindQuestionNeeded:
		return e.QuestionNeeded
	case KindError:
		return e.Error
	case KindStoreUpdate:
		return e.StoreUpdate
	case KindUsage:
		return e.Usage
	case KindCompacting:
		return e.Compacting
	case KindCompactingEnd:
		return e.CompactingEnd
	case KindModeChanged:
		return e.ModeChanged
	case KindBgResult:
		return e.BgResult
	case KindMonitorEvent:
		return e.MonitorEvent
	case KindDrainBackgroundTask:
		return e.DrainBackgroundTask
	case KindDrainMonitorEvents:
		return e.DrainMonitorEvents
	}
	return nil
}
