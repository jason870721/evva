package toolset

import (
	"strconv"
	"sync"
)

// SteerLevel is how urgently a mid-run user message wants to be delivered.
// It is the whole of steering v2's vocabulary: the two levels differ only
// in WHEN the message lands, never in what the model finally reads.
//
// A third level — abort the turn — deliberately has no constant here. It
// tears the run down instead of enqueuing anything, so it lives in the
// UI's cancel path (Esc), not in this queue.
type SteerLevel int

const (
	// SteerQueue is the polite level and the historical behaviour: the
	// message waits for the current iteration to finish on its own.
	SteerQueue SteerLevel = iota

	// SteerInterject cancels whatever phase is in flight — the LLM stream
	// or the running tool — so the message lands at once. The turn
	// survives; only the current phase is cut short.
	SteerInterject
)

// String renders the level for logs and the UI's queue panel.
func (l SteerLevel) String() string {
	if l == SteerInterject {
		return "interject"
	}
	return "queue"
}

// PendingPrompt is one queued message as the UI sees it before delivery.
// ID is stable for the entry's lifetime so a review panel can revoke a
// specific row without index arithmetic against a queue that another
// goroutine may be draining.
type PendingPrompt struct {
	ID     string
	Text   string
	Level  SteerLevel
	SeqNum int // arrival order; ties are impossible, so this totally orders the queue
}

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
// Steering v2 (STE-1) adds a level per entry. Draining is
// interjects-first, arrival order within each level: a message the user
// cancelled a four-minute bash for should not land behind three polite
// ones typed before it. Nothing else about the ordering moved — when no
// interject is present the drain is byte-identical to the pre-STE
// behaviour, which is what keeps wakeup prompts, alarms and plan-mode
// reminders in the positions they have always occupied.
//
// The queue is in-memory and per-agent. It is NOT durable: a crash
// between enqueue and drain loses the message, which is the correct
// trade — a prompt the model never saw is one the user can retype, and
// persisting it would resurrect stale instructions on the next resume.
//
// Subagents do not drain this queue; the user has no view into the
// subagent's loop, so enqueuing there would be invisible. Each agent
// has its own ToolState (and therefore its own queue), so subagent
// queues simply stay empty.
type UserPromptQueue struct {
	mu      sync.Mutex
	pending []PendingPrompt
	seq     int
}

// NewUserPromptQueue returns a fresh, empty queue.
func NewUserPromptQueue() *UserPromptQueue { return &UserPromptQueue{} }

// Enqueue appends a prompt at the polite level. Empty / whitespace-only
// prompts are silently dropped — they'd produce a useless empty RoleUser
// turn.
func (q *UserPromptQueue) Enqueue(prompt string) { q.EnqueueAt(SteerQueue, prompt) }

// EnqueueAt appends a prompt at the given level and returns its id (empty
// when the prompt was dropped as blank). The caller that asked for
// SteerInterject is responsible for actually cancelling the in-flight
// phase — this queue only records the intent, it cannot reach the loop.
func (q *UserPromptQueue) EnqueueAt(level SteerLevel, prompt string) string {
	if prompt == "" {
		return ""
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seq++
	e := PendingPrompt{
		ID:     "p" + strconv.Itoa(q.seq),
		Text:   prompt,
		Level:  level,
		SeqNum: q.seq,
	}
	q.pending = append(q.pending, e)
	return e.ID
}

// Drain returns every queued prompt in delivery order — interjects
// first, arrival order within each level — and clears the queue.
// Returns nil (not an empty slice) when nothing is queued so callers can
// short-circuit with a single nil-check.
func (q *UserPromptQueue) Drain() []PendingPrompt {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	// Two passes rather than a sort: the ordering is a partition by level
	// with arrival order preserved inside each part, which is exactly what
	// a stable two-pass walk gives — and it cannot be got wrong the way a
	// hand-written less-func can.
	out := make([]PendingPrompt, 0, len(q.pending))
	for _, e := range q.pending {
		if e.Level == SteerInterject {
			out = append(out, e)
		}
	}
	for _, e := range q.pending {
		if e.Level != SteerInterject {
			out = append(out, e)
		}
	}
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

// Pending returns a copy of the queue in delivery order, for a review
// panel. A copy rather than a view: the caller renders on another
// goroutine and the loop may drain underneath it.
func (q *UserPromptQueue) Pending() []PendingPrompt {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	out := make([]PendingPrompt, 0, len(q.pending))
	for _, e := range q.pending {
		if e.Level == SteerInterject {
			out = append(out, e)
		}
	}
	for _, e := range q.pending {
		if e.Level != SteerInterject {
			out = append(out, e)
		}
	}
	return out
}

// Revoke removes the queued prompt with the given id and reports whether
// it was still there. A false return is the normal outcome of a race with
// the drain, not an error: the user asked to unsend something the model
// had already been given, and the honest answer is "too late".
func (q *UserPromptQueue) Revoke(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, e := range q.pending {
		if e.ID == id {
			q.pending = append(q.pending[:i], q.pending[i+1:]...)
			return true
		}
	}
	return false
}
