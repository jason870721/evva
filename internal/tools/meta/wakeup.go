package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/tools"
)

// WakeupQueue is the side-channel between WakeupTool and the agent loop.
//
// The tool sleeps for the requested delay, then Enqueue's its prompt; the
// loop calls Drain at the top of each iteration and appends every drained
// entry as a fresh RoleUser message before the next LLM call. Same
// pattern as drainAsyncSubagents — the model sees the prompt as if the
// user just typed it.
type WakeupQueue struct {
	mu      sync.Mutex
	pending []string
}

// NewWakeupQueue returns a fresh, empty queue.
func NewWakeupQueue() *WakeupQueue { return &WakeupQueue{} }

// Enqueue appends a prompt to be delivered on the next loop iteration.
// Empty prompts are silently dropped — the tool validates non-empty at
// the public boundary, so a zero-arg push here would be a bug.
func (q *WakeupQueue) Enqueue(prompt string) {
	if prompt == "" {
		return
	}
	q.mu.Lock()
	q.pending = append(q.pending, prompt)
	q.mu.Unlock()
}

// Drain returns every queued prompt and clears the queue. Returns nil
// (not an empty slice) when nothing is queued so callers can short-circuit
// with a single nil-check.
func (q *WakeupQueue) Drain() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 {
		return nil
	}
	out := q.pending
	q.pending = nil
	return out
}

// WakeupTool implements SCHEDULE_WAKEUP. Execute blocks for delaySeconds
// (cancellable via ctx) and then enqueues prompt for delivery as a fresh
// user message on the next loop iteration.
//
// The tool exists so the model can self-pace polling loops — e.g.
// "spawn async subagents, wakeup in 60s, then check on them". When the
// wakeup fires the queued prompt re-enters the conversation as if the
// user just typed it, giving the model a clean re-entry point.
type WakeupTool struct {
	queue *WakeupQueue
}

// NewWakeup constructs a WakeupTool bound to the given queue. The queue
// must be the same instance the agent loop drains from — typically
// obtained via ToolState.WakeupQueue().
func NewWakeup(queue *WakeupQueue) *WakeupTool {
	return &WakeupTool{queue: queue}
}

func (t *WakeupTool) Name() string { return string(tools.SCHEDULE_WAKEUP) }

func (t *WakeupTool) Description() string {
	return "Schedule when to resume work — sleep for delaySeconds, then re-enter the conversation with the supplied prompt as a fresh user message.\n\n" +
		"Behavior: this tool BLOCKS for delaySeconds (cancellable via interrupt). On wake, `prompt` is appended to the conversation as a RoleUser message; the next assistant turn sees it as if the user just sent it. Omit the call to end a self-paced loop.\n\n" +
		"Typical use: spawn async subagents, then call schedule_wakeup to pause until they likely complete; the next turn drains their results and continues with your queued prompt.\n\n" +
		"delaySeconds clamps to [1, 3600]. Picking the right delay: Anthropic prompt cache has a 5-minute TTL — sleeping past 300s pays a cache miss. Stay <270s when actively polling, commit to 1200s+ when waiting longer is fine. Don't pick exactly 300s."
}

func (t *WakeupTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["delaySeconds","reason","prompt"],
		"properties":{
			"delaySeconds":{"type":"number","minimum":1,"maximum":3600,"description":"Seconds to sleep before re-entering the conversation. Clamped to [1, 3600]."},
			"prompt":{"type":"string","description":"The prompt to inject as a fresh user message on wake. Pass the same /loop input verbatim each turn for repeating tasks."},
			"reason":{"type":"string","description":"One short sentence explaining the chosen delay. Shown back to the user in telemetry."}
		}
	}`)
}

type wakeupInput struct {
	DelaySeconds float64 `json:"delaySeconds"`
	Prompt       string  `json:"prompt"`
	Reason       string  `json:"reason"`
}

// wakeupMinSeconds / wakeupMaxSeconds cap delaySeconds. The lower bound
// is 1s (not 60s as the model-facing description suggests) so local
// testing isn't painful; the description still steers the model toward
// >=60s for cache-friendly pacing.
const (
	wakeupMinSeconds = 1.0
	wakeupMaxSeconds = 3600.0
)

func (t *WakeupTool) Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in wakeupInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("schedule_wakeup: decode: %v", err)}, nil
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return tools.Result{IsError: true, Content: "schedule_wakeup: prompt is required"}, nil
	}
	if strings.TrimSpace(in.Reason) == "" {
		return tools.Result{IsError: true, Content: "schedule_wakeup: reason is required"}, nil
	}
	if t.queue == nil {
		return tools.Result{IsError: true, Content: "schedule_wakeup: no wakeup queue configured"}, nil
	}

	seconds := in.DelaySeconds
	switch {
	case seconds < wakeupMinSeconds:
		seconds = wakeupMinSeconds
	case seconds > wakeupMaxSeconds:
		seconds = wakeupMaxSeconds
	}
	logger.Debug("wakeup.dispatch", "delay_s", seconds, "reason", in.Reason)

	dur := time.Duration(seconds * float64(time.Second))
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		// Interrupted mid-sleep — surface to the model but do NOT enqueue
		// the prompt. The conversation is unwinding; injecting a fresh
		// user message on a cancelled run would just confuse the next Run.
		now := time.Now()
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf("schedule_wakeup: cancelled during sleep — %v, currentTime: %s", ctx.Err(), common.Stamp(now)),
		}, nil
	case <-timer.C:
	}

	t.queue.Enqueue(in.Prompt)
	now := time.Now()
	return tools.Result{
		Content: fmt.Sprintf("woke up after %gs — reason: %s, currentTime: %s. Wakeup prompt queued; next turn will see it as a fresh user message.", seconds, in.Reason, common.Stamp(now)),
	}, nil
}
