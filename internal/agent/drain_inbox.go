package agent

import (
	"context"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
)

// Drainer is a pluggable source of out-of-band messages folded into a running
// agent at each loop iteration boundary. It generalises the built-in
// background-task / monitor drains into a public seam: a host (e.g. a swarm
// supervisor) supplies a Drainer that checks an inbox so a *busy* agent reacts
// to an incoming message within its current run, instead of only between runs.
//
// Contract:
//   - Drain is called at most once per iteration boundary, on the loop
//     goroutine, with the run's context.
//   - It MUST be non-blocking: return ok=false immediately when there is
//     nothing to fold (an empty-inbox check should cost ~nothing per boundary).
//   - When ok is true, msg is appended verbatim as a synthetic user turn before
//     the next LLM call. An empty msg with ok=true folds nothing.
//   - A nil Drainer is a no-op: agents without one behave exactly as before.
type Drainer interface {
	Drain(ctx context.Context) (msg string, ok bool)
}

// WithInboxDrainer installs the inbox Drainer polled at every iteration
// boundary. Nil-safe: passing nil leaves the agent with no drainer (the
// default), so existing single-agent callers are unaffected.
func WithInboxDrainer(d Drainer) Option {
	return func(a *Agent) {
		a.inboxDrainer = d
	}
}

// drainInbox polls the configured Drainer once and folds any returned message
// into the session as a fresh user turn — the same between-turns safety as the
// wakeup / user-prompt / daemon-signal drains (the previous assistant turn's
// tool_calls are already answered, so the conversation stays well-formed).
func (a *Agent) drainInbox(ctx context.Context) {
	if a.inboxDrainer == nil {
		return
	}
	msg, ok := a.inboxDrainer.Drain(ctx)
	if !ok || msg == "" {
		return
	}
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: msg})
	a.emit(event.KindDrainInbox, func(e *event.Event) {
		e.DrainInbox = &event.DrainInboxPayload{Count: 1}
	})
	a.logger.Debug("inbox.drained")
}
