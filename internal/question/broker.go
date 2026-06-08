// Package question provides the back-channel between the AskUserQuestion tool
// (blocked agent goroutine) and the TUI (user interaction). The design mirrors
// internal/permission/broker.go exactly: one Broker per process, shared by all
// agents; the TUI registers an OnRequest callback at startup and calls Respond
// when the user has answered all questions.
package question

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
)

// Option is one item the user can select.
type Option struct {
	Label       string
	Description string
	Preview     string // optional; only meaningful for single-select questions
}

// Question is one question in a Request.
type Question struct {
	Question    string
	Header      string // short chip label, max 12 chars
	MultiSelect bool
	Options     []Option
}

// Annotation carries extra metadata about the user's answer — the preview
// content they were shown, or free-text notes they added.
type Annotation struct {
	Notes   string
	Preview string
}

// Request is what the TUI needs to render the question overlay.
// ID is empty on input; Broker.Request assigns one.
type Request struct {
	ID        string
	AgentID   string
	Questions []Question
}

// Response is the payload delivered back to the blocked tool goroutine once
// the user has finished answering.
//
// Answers maps question text → the chosen option labels. Single-select carries a
// one-element slice; multi-select carries one entry per chosen option; "Other"
// free-text carries the user-typed string as the sole element. This is the
// canonical multi-value shape — the public ui.QuestionResponse keeps a string
// map (comma-joined) for back-compat plus an additive MultiAnswers that maps
// onto this.
//
// Annotations is keyed by question text. It is always non-nil but entries are
// only present when the user's selection had an associated preview or notes.
type Response struct {
	Answers     map[string][]string
	Annotations map[string]Annotation
}

// Broker is the back-channel between the AskUserQuestion tool and the TUI.
// A single Broker is shared by the root agent and all subagents.
type Broker interface {
	// Request blocks until the user responds or ctx is cancelled.
	// Returns an error response when ctx is cancelled.
	Request(ctx context.Context, req Request) (Response, error)

	// Respond delivers the user's answers to the blocked Request.
	// Idempotent per ID — a second call for the same ID is a no-op.
	Respond(id string, r Response) error
}

// OnRequest is the callback the broker invokes when a new request is created.
// The TUI registers here to render the question overlay.
type OnRequest func(req Request)

// broker is the default Broker implementation.
type broker struct {
	mu        sync.Mutex
	pending   map[string]chan Response
	onRequest OnRequest
}

// NewBroker returns a Broker backed by an in-memory request map.
func NewBroker() Broker {
	return &broker{
		pending: make(map[string]chan Response),
	}
}

// SetOnRequest registers the callback invoked when a new question request is
// pending. The TUI calls this once at startup. Calling it twice replaces the
// previous callback.
func (b *broker) setOnRequest(fn OnRequest) {
	b.mu.Lock()
	b.onRequest = fn
	b.mu.Unlock()
}

// Request implements Broker.
func (b *broker) Request(ctx context.Context, req Request) (Response, error) {
	id, err := newRequestID()
	if err != nil {
		return Response{}, err
	}
	req.ID = id

	ch := make(chan Response, 1)
	b.mu.Lock()
	b.pending[id] = ch
	fn := b.onRequest
	b.mu.Unlock()

	if fn != nil {
		fn(req)
	}

	select {
	case r := <-ch:
		return r, nil
	case <-ctx.Done():
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		return Response{}, ctx.Err()
	}
}

// Respond implements Broker.
func (b *broker) Respond(id string, r Response) error {
	b.mu.Lock()
	ch, ok := b.pending[id]
	if ok {
		delete(b.pending, id)
	}
	b.mu.Unlock()
	if !ok {
		return errors.New("question: no pending request for id " + id)
	}
	ch <- r
	return nil
}

// SetOnRequest is exported as a free function (not on the interface) so callers
// building a Broker from NewBroker can register a callback without reflection.
func SetOnRequest(b Broker, fn OnRequest) {
	if br, ok := b.(*broker); ok {
		br.setOnRequest(fn)
	}
}

func newRequestID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf[:]), nil
}
