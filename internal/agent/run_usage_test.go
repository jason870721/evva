package agent

import (
	"context"
	"testing"

	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// lastRunEnd returns the most recent run_end event's payload, or nil.
func (s *capturingSink) lastRunEnd() *event.RunEndPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].Kind == event.KindRunEnd {
			return s.events[i].RunEnd
		}
	}
	return nil
}

// TestRunEndCarriesPerRunUsage (RP-28 Part A): the run_end event reports the
// run's OWN token cost — the session-usage delta since runLoop entered — not
// the cumulative session counter. A second run on the same (grown) session
// must report only its own spend; that delta is what lets an event-log day
// reconstruct a member's per-run series.
func TestRunEndCarriesPerRunUsage(t *testing.T) {
	turn := llm.Usage{InputTokens: 1000, OutputTokens: 50, CacheReadTokens: 800}
	stub := &stubLLM{
		complete: func(_ context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
			return llm.Response{Content: "done", Usage: turn}, nil // terminal turn
		},
	}
	a := newTestAgent(stub)
	a.toolState = toolset.NewToolState()
	a.maxIters.Store(2)
	captured := newCapturingSink()
	a.sink = captured

	if _, err := a.Run(context.Background(), "first"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	re := captured.lastRunEnd()
	if re == nil || re.Usage == nil {
		t.Fatalf("run_end missing per-run usage: %+v", re)
	}
	if *re.Usage != turn {
		t.Errorf("first run usage = %+v, want %+v", *re.Usage, turn)
	}

	// Second run: session.Usage is now cumulative (2 turns' worth after this
	// run), but the event must carry ONLY this run's delta.
	turn2 := llm.Usage{InputTokens: 1500, OutputTokens: 70, CacheReadTokens: 1400}
	turn = turn2
	if _, err := a.Run(context.Background(), "second"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	re = captured.lastRunEnd()
	if re == nil || re.Usage == nil {
		t.Fatalf("second run_end missing usage: %+v", re)
	}
	if *re.Usage != turn2 {
		t.Errorf("second run usage = %+v, want only the second run's %+v", *re.Usage, turn2)
	}
	if got := a.session.Usage.InputTokens; got != 2500 {
		t.Errorf("cumulative session input = %d, want 2500 (per-run reporting must not break cumulation)", got)
	}
}

// TestRunEndUsageAbsentWhenUnreported (RP-28 acceptance): a provider that
// reports no usage (every field zero) yields a run_end with Usage == nil —
// absent, never fabricated — so downstream consumers can tell "free" from
// "unmetered".
func TestRunEndUsageAbsentWhenUnreported(t *testing.T) {
	stub := &stubLLM{
		complete: func(_ context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
			return llm.Response{Content: "done"}, nil // zero Usage
		},
	}
	a := newTestAgent(stub)
	a.toolState = toolset.NewToolState()
	a.maxIters.Store(2)
	captured := newCapturingSink()
	a.sink = captured

	if _, err := a.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	re := captured.lastRunEnd()
	if re == nil {
		t.Fatal("no run_end event")
	}
	if re.Usage != nil {
		t.Errorf("Usage = %+v, want nil when the provider reported nothing", *re.Usage)
	}
}
