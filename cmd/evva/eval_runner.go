package main

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/evalharness"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
)

// agentRunner replays a fixture through a real evva agent.
//
// It builds the agent through agent.New — the production construction path —
// rather than assembling a bare struct. That is the whole point: this harness
// exists to regression-test the system prompt and tool set, and those are
// exactly what agent.New assembles and a hand-built Agent skips. Replaying
// against a shortcut construction would measure a configuration nothing in
// production uses.
//
// A fresh agent per fixture keeps fixtures independent: leaked conversation
// state between them would make results depend on file ordering.
type agentRunner struct {
	cfg agent.Config
}

// collector turns the agent's event stream into a tool-call sequence.
//
// Reading events rather than the final transcript captures calls in dispatch
// order including ones whose results were later compacted away — a long run
// that micro-compacts would otherwise lose its early decisions, which are
// often the interesting ones.
type collector struct {
	mu    sync.Mutex
	calls []evalharness.ToolCallSummary
	text  strings.Builder
}

func (c *collector) Emit(e event.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch e.Kind {
	case event.KindToolUseStart:
		if p := e.ToolUseStart; p != nil {
			c.calls = append(c.calls, evalharness.Summarize(p.Name, p.Input))
		}
	case event.KindText:
		if p := e.Text; p != nil {
			c.text.WriteString(p.Text)
		}
	}
}

func (c *collector) snapshot() ([]evalharness.ToolCallSummary, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]evalharness.ToolCallSummary, len(c.calls))
	copy(out, c.calls)
	return out, c.text.String()
}

// Run implements evalharness.Runner.
func (r *agentRunner) Run(ctx context.Context, turns []string) (evalharness.RunTrace, error) {
	col := &collector{}
	a, err := agent.New(r.cfg, agent.WithSink(col), agent.WithHeadlessBypass())
	if err != nil {
		return evalharness.RunTrace{}, fmt.Errorf("build agent: %w", err)
	}

	var last string
	for i, turn := range turns {
		out, rerr := a.Run(ctx, turn)
		if rerr != nil {
			return evalharness.RunTrace{}, fmt.Errorf("turn %d/%d: %w", i+1, len(turns), rerr)
		}
		last = out
	}

	calls, streamed := col.snapshot()
	final := last
	if strings.TrimSpace(final) == "" {
		final = streamed
	}
	return evalharness.RunTrace{
		ToolCalls:  calls,
		FinalText:  final,
		Transcript: []llm.Message{{Role: llm.RoleAssistant, Content: final}},
	}, nil
}
