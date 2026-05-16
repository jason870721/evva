package toolset

import "sync"

// UserPromptQueue is the bridge that lets the UI hand the agent a fresh
// user message WITHOUT starting a new Run. The agent loop drains the
// queue at the top of every iteration and folds each entry into the
// session as a RoleUser message — same pattern as drainAsyncSubagents /
// drainWakeupPrompts.
//
// Why a side-channel and not just another Run: while a Run is in flight
// the previous assistant turn's tool_calls may not yet be answered. A
// second Run that appended RoleUser there would orphan the tool_calls
// and every provider would 400 (the bug we fixed earlier in this
// branch). The queue defers the append to a safe point — between
// iterations, after the previous turn's RoleTool message has landed —
// so the conversation stays well-formed.
//
// Subagents do not drain this queue; the user has no view into the
// subagent's loop, so enqueuing there would be invisible. Each agent
// has its own ToolState (and therefore its own queue), so subagent
// queues simply stay empty.
type UserPromptQueue struct {
	mu      sync.Mutex
	pending []string
}

// NewUserPromptQueue returns a fresh, empty queue.
func NewUserPromptQueue() *UserPromptQueue { return &UserPromptQueue{} }

// Enqueue appends a prompt to be delivered on the next loop iteration.
// Empty / whitespace-only prompts are silently dropped — they'd produce
// a useless empty RoleUser turn.
func (q *UserPromptQueue) Enqueue(prompt string) {
	if prompt == "" {
		return
	}
	q.mu.Lock()
	q.pending = append(q.pending, prompt)
	q.mu.Unlock()
}

// Drain returns every queued prompt and clears the queue. Returns nil
// (not an empty slice) when nothing is queued so callers can
// short-circuit with a single nil-check.
func (q *UserPromptQueue) Drain() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	out := q.pending
	q.pending = nil
	return out
}

// Len reports the number of pending prompts without draining. UIs use
// this to badge a "+N queued" indicator on the status bar without
// consuming the queue.
func (q *UserPromptQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}
