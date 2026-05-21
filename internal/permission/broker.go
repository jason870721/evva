package permission

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
)

// Broker is the back-channel between the gate (blocked tool goroutine) and
// the TUI (user interaction). When Decide returns BehaviorAsk, the gate
// calls Request and parks on the reply channel; the TUI later calls
// Respond with the user's choice.
//
// Implementations are safe for concurrent Request/Respond/Cancel calls.
// A single Broker is shared by the root agent and all subagents — request
// IDs disambiguate.
type Broker interface {
	// Request blocks until the user responds OR ctx is cancelled.
	// Returns Deny if the context is cancelled (the caller surfaces the
	// ctx error to the LLM as a failed tool call).
	Request(ctx context.Context, req ApprovalRequest) (Decision, error)

	// Respond delivers the user's choice to the blocked Request. Idempotent
	// per id — a second Respond for the same id is a no-op (covers the
	// race where the user clicks while ctx cancellation is already in flight).
	Respond(id string, d Decision) error

	// Cancel discards a pending request without delivering a Decision.
	// Used internally by Request when ctx is cancelled.
	Cancel(id string) error
}

// ApprovalRequest is what the TUI needs to render the prompt.
//
// PlanContent is non-empty only for ExitPlanMode requests — the tool fills
// it with the markdown body it read from <workdir>/.evva/plans/current.md
// so the approval overlay can render the plan inline. Other tools leave
// it empty; the overlay's plan branch keys off the non-empty check.
type ApprovalRequest struct {
	ID          string // empty on input — Broker.Request assigns one
	AgentID     string // who's asking (root or subagent ID)
	ToolName    string
	ToolInput   []byte // raw JSON; UI summarises to ~200 chars
	Description string // human-readable tool description from the tool registry
	Mode        Mode
	Reason      string // from Decide() — why we're asking
	Hint        Hint   // pre-computed classification (Bash only today)
	PlanContent string // ExitPlanMode-only; markdown plan body
}

// NewBroker returns a Broker backed by an in-memory request map. There's
// no persistent state — a fresh Broker per process is correct.
func NewBroker() Broker {
	return &broker{
		pending: make(map[string]chan Decision),
	}
}

// OnRequest is the callback the broker invokes when a new request is
// created (after the ID is assigned). The TUI subscribes here to render
// the approval overlay. Set via Broker.(*broker).SetOnRequest before any
// Request fires.
type OnRequest func(req ApprovalRequest)

// broker is the default Broker implementation.
type broker struct {
	mu        sync.Mutex
	pending   map[string]chan Decision
	onRequest OnRequest
}

// SetOnRequest registers the callback invoked when a new approval is
// pending. The TUI calls this once at startup. Calling SetOnRequest twice
// replaces the previous callback (the broker doesn't fan out — there is
// one UI per process).
func (b *broker) SetOnRequest(fn OnRequest) {
	b.mu.Lock()
	b.onRequest = fn
	b.mu.Unlock()
}

// Request implements Broker.
func (b *broker) Request(ctx context.Context, req ApprovalRequest) (Decision, error) {
	id, err := newRequestID()
	if err != nil {
		return Decision{Behavior: BehaviorDeny, Reason: "broker: failed to assign id"}, err
	}
	req.ID = id

	ch := make(chan Decision, 1)
	b.mu.Lock()
	b.pending[id] = ch
	fn := b.onRequest
	b.mu.Unlock()

	if fn != nil {
		fn(req)
	}

	select {
	case d := <-ch:
		return d, nil
	case <-ctx.Done():
		// Best-effort cleanup; if Respond raced us the channel got the
		// answer and we discard it.
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return Decision{
			Behavior: BehaviorDeny,
			Reason:   "approval cancelled: " + ctx.Err().Error(),
		}, ctx.Err()
	}
}

// Respond implements Broker.
func (b *broker) Respond(id string, d Decision) error {
	b.mu.Lock()
	ch, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if !ok {
		return errors.New("permission: no pending request for id " + id)
	}
	ch <- d
	return nil
}

// Cancel implements Broker.
func (b *broker) Cancel(id string) error {
	b.mu.Lock()
	_, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if !ok {
		return errors.New("permission: no pending request for id " + id)
	}
	return nil
}

// SetOnRequest is exported on the concrete broker, not the interface, so
// callers building a Broker from NewBroker can register a callback without
// reflection.
func SetOnRequest(b Broker, fn OnRequest) {
	if br, ok := b.(*broker); ok {
		br.SetOnRequest(fn)
	}
}

// newRequestID returns a short hex token. 16 bytes (32 hex chars) is more
// than enough to avoid collisions across a single process's lifetime.
func newRequestID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
