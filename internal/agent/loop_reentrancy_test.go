package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/toolset"
)

// TestRun_RejectsReentrantCall locks in the fix for the
// schedule_wakeup-while-user-types-another-prompt corruption bug. A
// concurrent Run must return ErrRunInProgress instead of appending a
// second user message and racing on session.Messages.
func TestRun_RejectsReentrantCall(t *testing.T) {
	// gate blocks inside Complete so the first Run sits inside the loop
	// long enough for the test to fire a second concurrent Run.
	gate := make(chan struct{})
	stub := &stubLLM{
		complete: func(ctx context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
			select {
			case <-gate:
				return llm.Response{}, nil // terminal: no tool_calls -> loop ends
			case <-ctx.Done():
				return llm.Response{}, ctx.Err()
			}
		},
	}
	a := newTestAgent(stub)
	a.toolState = toolset.NewToolState() // drainAsyncSubagents needs a non-nil state
	a.maxIters = 5

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = a.Run(context.Background(), "first")
	}()

	// Spin until the first Run has actually entered the running flag.
	deadline := 0
	for !a.running.Load() && deadline < 1_000_000 {
		deadline++
	}
	if !a.running.Load() {
		close(gate)
		wg.Wait()
		t.Fatal("first Run never acquired the running flag")
	}

	// Second concurrent Run must bounce immediately. The rejection path
	// must not touch session (would race with the first Run's Append).
	_, err := a.Run(context.Background(), "second")
	if !errors.Is(err, ErrRunInProgress) {
		t.Fatalf("expected ErrRunInProgress, got %v", err)
	}

	// Release the first Run and wait for it to exit. Only inspect
	// session after the owner has fully unwound — Session has no
	// internal lock, so the goroutine join is our synchronization point.
	close(gate)
	wg.Wait()

	// The first Run appended one RoleUser ("first") and the loop tail
	// appended one RoleAssistant (empty terminal response). The blocked
	// second Run must NOT have appended its "second" prompt — count
	// RoleUser messages and verify the content.
	msgs := a.session.GetMessages()
	var userMsgs []llm.Message
	for _, m := range msgs {
		if m.Role == llm.RoleUser {
			userMsgs = append(userMsgs, m)
		}
	}
	if len(userMsgs) != 1 {
		t.Fatalf("expected 1 RoleUser message, got %d (re-entrant Run leaked a prompt)", len(userMsgs))
	}
	if userMsgs[0].Content != "first" {
		t.Fatalf("expected user message content \"first\", got %q", userMsgs[0].Content)
	}

	// After the first Run finishes, the flag should be cleared and a
	// follow-up Run should succeed (no permanent lockout). Replace the
	// stub's complete with a non-blocking version so this Run can exit.
	if a.running.Load() {
		t.Fatal("running flag should clear after Run returns")
	}
	stub.complete = func(ctx context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
		return llm.Response{}, nil
	}
	if _, err := a.Run(context.Background(), "third"); err != nil {
		t.Fatalf("follow-up Run after release: %v", err)
	}
}

// TestContinue_RejectsReentrantCall mirrors the Run test for Continue —
// same invariant, same failure mode if violated.
func TestContinue_RejectsReentrantCall(t *testing.T) {
	gate := make(chan struct{})
	stub := &stubLLM{
		complete: func(ctx context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
			select {
			case <-gate:
				return llm.Response{}, nil
			case <-ctx.Done():
				return llm.Response{}, ctx.Err()
			}
		},
	}
	a := newTestAgent(stub)
	a.toolState = toolset.NewToolState() // drainAsyncSubagents needs a non-nil state
	a.maxIters = 5

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = a.Continue(context.Background())
	}()

	deadline := 0
	for !a.running.Load() && deadline < 1_000_000 {
		deadline++
	}
	if !a.running.Load() {
		close(gate)
		wg.Wait()
		t.Fatal("first Continue never acquired the running flag")
	}

	if _, err := a.Continue(context.Background()); !errors.Is(err, ErrRunInProgress) {
		t.Fatalf("expected ErrRunInProgress from concurrent Continue, got %v", err)
	}

	close(gate)
	wg.Wait()
}
