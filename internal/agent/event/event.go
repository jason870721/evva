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

	"github.com/johnny1110/evva/internal/llm"
)

// Kind tags every event. New kinds are added by extending this list and the
// matching payload field on Event.
type Kind string

const (
	KindRunStart     Kind = "run_start"
	KindRunResume    Kind = "run_resume"
	KindRunEnd       Kind = "run_end"
	KindRunCancelled Kind = "run_cancelled"
	KindIterLimit    Kind = "iter_limit" // paused — caller may Continue

	KindTurnStart Kind = "turn_start"
	KindTurnEnd   Kind = "turn_end"

	KindDrainingInfo      = "draining_info" // agent is draining info from subagent or bg bash
	KindThinking     Kind = "thinking"      // assistant reasoning text
	KindText         Kind = "text"          // assistant final text

	KindToolUseStart  Kind = "tool_use_start"
	KindToolUseResult Kind = "tool_use_result"

	KindCompacting Kind = "compacting"

	KindError Kind = "error"

	// KindStoreUpdate carries every state change emitted by an
	// observable.Store registered on the agent's ToolState. The consumer
	// switches on StoreUpdatePayload.Domain to decide how to render.
	KindStoreUpdate Kind = "store_update"

	KindUsage Kind = "usage" // per-turn token usage report
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

	RunStart      *RunStartPayload      `json:",omitempty"`
	RunResume     *RunResumePayload     `json:",omitempty"`
	RunEnd        *RunEndPayload        `json:",omitempty"`
	IterLimit     *IterLimitPayload     `json:",omitempty"`
	Turn          *TurnPayload          `json:",omitempty"`
	Thinking      *TextPayload          `json:",omitempty"`
	Text          *TextPayload          `json:",omitempty"`
	ToolUseStart  *ToolUseStartPayload  `json:",omitempty"`
	ToolUseResult *ToolUseResultPayload `json:",omitempty"`
	Error         *ErrorPayload         `json:",omitempty"`
	StoreUpdate   *StoreUpdatePayload   `json:",omitempty"`
	Usage         *UsagePayload         `json:",omitempty"`
	Compacting    *CompactingPayload    `json:",omitempty"`
}

// --- payload types ---

type RunStartPayload struct {
	Prompt string
}

type RunResumePayload struct {
	FromMessageIndex int
}

type RunEndPayload struct {
	Iters    int
	Content  string
	Thinking string
}

// IterLimitPayload is emitted when the loop hits Agent.maxIters. The UI
// should prompt the user (e.g. "press Enter to keep going") and call
// Agent.Continue to resume; the loop is paused, not failed.
type IterLimitPayload struct {
	Reached int
}

type TurnPayload struct {
	Iteration int
}

// TextPayload carries an opaque text chunk — used for both Thinking and
// Text events. With streaming completions this becomes a stream of chunks;
// today it carries the full block.
type TextPayload struct {
	Text string
}

type ToolUseStartPayload struct {
	Name   string
	Input  json.RawMessage
	ToolID string
}

// ToolUseResultPayload reports the outcome of a single tool call.
//
// Metadata is an optional tool-specific structured payload (e.g. a
// *fs.FileDiff for write_file / edit_file). Carried opaquely through this
// layer; the UI type-asserts. Never sent to the LLM — Content alone is the
// model-facing summary.
type ToolUseResultPayload struct {
	ToolID   string
	Content  string
	IsError  bool
	Metadata any
}

// ErrorPayload reports a Go-level failure that aborted the loop. Tool errors
// surfaced as Result.IsError do NOT produce this event — they flow through
// ToolUseResult so the model can recover.
type ErrorPayload struct {
	Stage string // "llm" | "tool:<name>" | "loop"
	Err   error
}

type CompactingPayload struct {
	Type       string
	UsageRatio float64
}

// StoreUpdatePayload is the bridge between observable.Change and the event
// stream. Domain names the emitting store ("task", "subagent", ...); Op is
// the verb ("created" / "updated" / "removed" / "phase" / "done" / "crushed");
// Payload is the store's domain-typed snapshot, switched on by Domain at
// the consumer.
type StoreUpdatePayload struct {
	Domain  string
	Op      string
	ID      string
	Payload any
	Time    time.Time
}

// UsagePayload reports token usage for the LLM call that just completed.
// Turn is the just-completed call; Cumulative is the running session total
// after Turn has been folded in. The TUI typically shows both — Turn for
// the latest cost spike, Cumulative for the session budget.
type UsagePayload struct {
	Turn       llm.Usage
	Cumulative llm.Usage
}
