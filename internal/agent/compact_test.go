package agent

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/llm"
)

// stubLLM is a hand-wired llm.Client used to drive fullCompact's
// summarization call without standing up a real provider. Stream is
// unused by the compaction path; Complete returns whatever the test
// installed.
type stubLLM struct {
	complete func(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool) (llm.Response, error)
}

func (s *stubLLM) Name() string  { return "stub" }
func (s *stubLLM) Model() string { return "stub-model" }
func (s *stubLLM) Apply(...llm.Option) {}
func (s *stubLLM) Complete(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
	return s.complete(ctx, msgs, toolSet)
}
func (s *stubLLM) Stream(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool, sink llm.ChunkSink) (llm.Response, error) {
	return s.complete(ctx, msgs, toolSet)
}

// newTestAgent constructs a bare Agent for compaction tests. We bypass
// agent.New because the constructor wires an LLM via the factory, builds
// tool sets, and emits logs — none of which the compaction logic needs.
func newTestAgent(client llm.Client) *Agent {
	return &Agent{
		ID:      "test-agent",
		logger:  slog.Default(),
		session: session.New(),
		llm:     client,
		cfg:     config.Get(),
	}
}

// TestMicroCompactElidesOldToolResults verifies that micro-compact:
//   - leaves non-RoleTool messages untouched
//   - elides Content from every RoleTool message older than the recency
//     window while preserving ID + IsError
//   - leaves the most recent microCompactKeepRecent RoleTool messages intact
//   - flips s.IsMicroCompacted() to true so the next compact escalates
func TestMicroCompactElidesOldToolResults(t *testing.T) {
	a := newTestAgent(nil)

	// Build a session with 10 RoleTool messages interleaved with assistant
	// turns. Tool result content is identifiable so we can verify which
	// got elided.
	for i := 0; i < 10; i++ {
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: "u"})
		a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "a"})
		a.session.Append(llm.Message{
			Role: llm.RoleTool,
			ToolResults: []*llm.ToolResult{
				{ID: idForTurn(i), Content: contentForTurn(i), IsError: i%3 == 0},
			},
		})
	}

	a.microCompact(a.session)

	if !a.session.IsMicroCompacted() {
		t.Fatal("expected IsMicroCompacted true after microCompact")
	}

	msgs := a.session.GetMessages()
	// Walk and count surviving vs elided.
	var elided, kept int
	var foundElidedIDs, foundKeptIDs []string
	for _, m := range msgs {
		if m.Role != llm.RoleTool {
			continue
		}
		for _, tr := range m.ToolResults {
			if tr.Content == microCompactPlaceholder {
				elided++
				foundElidedIDs = append(foundElidedIDs, tr.ID)
			} else {
				kept++
				foundKeptIDs = append(foundKeptIDs, tr.ID)
				// Sanity: kept results must match their original Content.
				if want := contentForTurn(idxFromID(tr.ID)); tr.Content != want {
					t.Errorf("kept tool result %s: got %q, want %q", tr.ID, tr.Content, want)
				}
			}
		}
	}

	wantElided := 10 - microCompactKeepRecent // 2
	if elided != wantElided {
		t.Errorf("elided count: got %d, want %d", elided, wantElided)
	}
	if kept != microCompactKeepRecent {
		t.Errorf("kept count: got %d, want %d", kept, microCompactKeepRecent)
	}

	// Elided IDs should be the oldest two; kept IDs the newest eight.
	for _, id := range foundElidedIDs {
		if i := idxFromID(id); i >= wantElided {
			t.Errorf("turn %d should have been kept, but was elided", i)
		}
	}
	for _, id := range foundKeptIDs {
		if i := idxFromID(id); i < wantElided {
			t.Errorf("turn %d should have been elided, but was kept", i)
		}
	}
}

// TestMicroCompactPreservesIsErrorOnElidedResults verifies IsError survives
// elision — the model must still see which old tool calls failed.
func TestMicroCompactPreservesIsErrorOnElidedResults(t *testing.T) {
	a := newTestAgent(nil)
	for i := 0; i < microCompactKeepRecent+2; i++ {
		a.session.Append(llm.Message{
			Role: llm.RoleTool,
			ToolResults: []*llm.ToolResult{
				{ID: idForTurn(i), Content: contentForTurn(i), IsError: i == 0},
			},
		})
	}

	a.microCompact(a.session)

	msgs := a.session.GetMessages()
	// Turn 0 was the oldest → elided, and was IsError=true.
	first := msgs[0].ToolResults[0]
	if first.Content != microCompactPlaceholder {
		t.Errorf("oldest result content: got %q, want elided placeholder", first.Content)
	}
	if !first.IsError {
		t.Errorf("oldest result IsError: got false, want true (must survive elision)")
	}
	if first.ID != idForTurn(0) {
		t.Errorf("oldest result ID: got %q, want %q (must survive elision)", first.ID, idForTurn(0))
	}
}

// TestMicroCompactSkipsWhenTooFewResults verifies micro-compact is a
// no-op (but still records the level transition) when there aren't yet
// more than the recency-window's worth of RoleTool messages.
func TestMicroCompactSkipsWhenTooFewResults(t *testing.T) {
	a := newTestAgent(nil)
	for i := 0; i < 3; i++ {
		a.session.Append(llm.Message{
			Role: llm.RoleTool,
			ToolResults: []*llm.ToolResult{
				{ID: idForTurn(i), Content: contentForTurn(i)},
			},
		})
	}
	a.microCompact(a.session)

	if !a.session.IsMicroCompacted() {
		t.Error("expected IsMicroCompacted true even on no-op")
	}
	for _, m := range a.session.GetMessages() {
		for _, tr := range m.ToolResults {
			if tr.Content == microCompactPlaceholder {
				t.Errorf("did not expect any elision in a <=keepRecent session, got %q", tr.Content)
			}
		}
	}
}

// TestFullCompactReplacesMessagesWithBrief drives fullCompact through a
// stub LLM that returns a canned brief. Verifies:
//   - Messages collapses to a single RoleUser entry carrying the brief
//   - the brief text from the LLM survives
//   - "Proceed with the Next Step" instruction is appended
//   - session.IsMicroCompacted() resets to false (the next compact
//     starts the cycle over)
//   - the summarization call's usage is folded into session.Usage
func TestFullCompactReplacesMessagesWithBrief(t *testing.T) {
	const cannedBrief = "## Original Task\nBuild it\n\n## Done So Far\n- step 1\n\n## Current Target\nstep 2\n\n## Next Step\nDo step 2.\n\n## Key Context\n- foo/bar.go"

	var capturedRequest []llm.Message
	stub := &stubLLM{
		complete: func(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
			capturedRequest = msgs
			if toolSet != nil {
				t.Errorf("summarization passed tools (want nil), got %d", len(toolSet))
			}
			return llm.Response{
				Content: cannedBrief,
				Usage:   llm.Usage{InputTokens: 100, OutputTokens: 50},
			}, nil
		},
	}
	a := newTestAgent(stub)

	// Pre-populate so the prompt has something to flatten.
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "build it"})
	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	// Mark micro already done so the compact() escalation path matches the
	// real-world preconditions, even though we call fullCompact directly.
	a.session.MicroCompact(a.session.GetMessages())

	a.fullCompact(context.Background(), a.session)

	if len(capturedRequest) != 1 {
		t.Fatalf("summarizer messages: got %d, want 1", len(capturedRequest))
	}
	if capturedRequest[0].Role != llm.RoleUser {
		t.Errorf("summarizer role: got %q, want user", capturedRequest[0].Role)
	}
	if !strings.Contains(capturedRequest[0].Content, "CONVERSATION TO SUMMARIZE") {
		t.Error("summarizer prompt missing instruction front-matter")
	}
	if !strings.Contains(capturedRequest[0].Content, "USER: build it") {
		t.Error("summarizer prompt missing flattened user turn")
	}

	msgs := a.session.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("post-compact messages: got %d, want 1 (just the brief)", len(msgs))
	}
	if msgs[0].Role != llm.RoleUser {
		t.Errorf("brief role: got %q, want user", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, cannedBrief) {
		t.Error("brief content not wrapped into the new message")
	}
	if !strings.Contains(msgs[0].Content, "Proceed with the Next Step") {
		t.Error("brief missing proceed instruction")
	}

	if a.session.IsMicroCompacted() {
		t.Error("IsMicroCompacted should reset to false after full compact")
	}
	// After full compact, cumulative Usage is reset to reflect the
	// post-compact context: input = brief output tokens (the new
	// prompt-payload size), output = 0 (no assistant turn yet). The
	// summarizer's own cost is preserved in the structured log, not on
	// the live struct — see compact.full's pre_compact_in / out fields.
	if got, want := a.session.Usage.InputTokens, 50; got != want {
		t.Errorf("session input tokens: got %d, want %d (post-compact = brief output tokens)", got, want)
	}
	if got, want := a.session.Usage.OutputTokens, 0; got != want {
		t.Errorf("session output tokens: got %d, want %d (fresh context after compact)", got, want)
	}
}

// TestFullCompactLeavesSessionAloneOnLLMError verifies a failed summarization
// is non-fatal — Messages stays as it was, the user can retry on the next
// iteration.
func TestFullCompactLeavesSessionAloneOnLLMError(t *testing.T) {
	stub := &stubLLM{
		complete: func(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
			return llm.Response{}, errors.New("boom")
		},
	}
	a := newTestAgent(stub)
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "build it"})
	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})
	a.session.MicroCompact(a.session.GetMessages())

	before := a.session.GetMessages()
	a.fullCompact(context.Background(), a.session)
	after := a.session.GetMessages()

	if len(before) != len(after) {
		t.Errorf("messages mutated on LLM error: before=%d after=%d", len(before), len(after))
	}
	// IsMicroCompacted must NOT flip back via FullCompact, since FullCompact
	// was never called — session.microCompacted should still be true.
	if !a.session.IsMicroCompacted() {
		t.Error("IsMicroCompacted should remain true on summarization failure")
	}
}

// TestFullCompactLeavesSessionAloneOnEmptyBrief: an empty Content reply
// (defensive — providers sometimes return whitespace-only blocks) should
// be treated identically to an error.
func TestFullCompactLeavesSessionAloneOnEmptyBrief(t *testing.T) {
	stub := &stubLLM{
		complete: func(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
			return llm.Response{Content: "   \n  "}, nil
		},
	}
	a := newTestAgent(stub)
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "build it"})
	a.session.MicroCompact(a.session.GetMessages())

	beforeLen := len(a.session.GetMessages())
	a.fullCompact(context.Background(), a.session)
	if got := len(a.session.GetMessages()); got != beforeLen {
		t.Errorf("messages mutated on empty brief: got len %d, want %d", got, beforeLen)
	}
}

// TestCompactRatioUsesLastTurnInputTokens proves the cumulative-usage bug
// is fixed:
//   - A session whose CUMULATIVE Usage has crossed the threshold but
//     whose LAST turn's InputTokens is small should NOT trigger compact.
//   - The companion case — last turn's InputTokens above threshold —
//     SHOULD trigger compact, even when cumulative is low.
//
// Together these prove the ratio reads from LastTurnInputTokens, not
// from Usage.Total().
func TestCompactRatioUsesLastTurnInputTokens(t *testing.T) {
	// Sonnet's context is 500k (constant.MODEL_CONTEXT_SIZE). Threshold
	// defaults to 0.8, so the cutoff is 400k tokens.
	stub := &stubLLM{
		complete: func(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
			t.Fatal("compact should not have called LLM for this scenario")
			return llm.Response{}, nil
		},
	}
	a := newTestAgent(stub)
	// Override the model so MODEL_CONTEXT_SIZE returns the real Sonnet
	// context budget. (Stub model returns "stub-model" which has size 0
	// and would early-out via the unknown-model guard.)
	a.llm = &stubLLM{
		complete: stub.complete,
	}
	a.llm.(*stubLLM).complete = stub.complete

	// We need maxContextSize to be > 0. Use a stub that lies about its
	// model name.
	a.llm = &knownModelStub{stubLLM: stub, model: "claude-sonnet-4-6"}

	// Case 1: cumulative is huge, last-turn is tiny → must NOT compact.
	a.session.AddUsage(llm.Usage{InputTokens: 450_000, OutputTokens: 100_000})
	a.session.RecordTurn(llm.Usage{InputTokens: 5_000}) // tiny current prompt
	a.compact(context.Background(), a.session)

	if a.session.IsMicroCompacted() {
		t.Error("compact triggered on tiny last-turn (cumulative was big — bug repro). want no-op")
	}

	// Case 2: cumulative is small, last-turn is huge → SHOULD compact.
	// Reset and re-arm. micro compact has no LLM call, so it's safe with
	// the failing stub.
	a2 := newTestAgent(&knownModelStub{stubLLM: stub, model: "claude-sonnet-4-6"})
	a2.session.AddUsage(llm.Usage{InputTokens: 1_000}) // tiny cumulative
	a2.session.RecordTurn(llm.Usage{InputTokens: 450_000})
	// Need at least one tool message so microCompact does something
	// observable (otherwise it short-circuits as "too few results").
	a2.session.Append(llm.Message{Role: llm.RoleTool, ToolResults: []*llm.ToolResult{{ID: "x", Content: "y"}}})
	a2.compact(context.Background(), a2.session)

	if !a2.session.IsMicroCompacted() {
		t.Error("compact failed to trigger on huge last-turn (cumulative was small). LastTurnInputTokens not read?")
	}
}

// TestFullCompactResetsLastTurnInputTokens guards the second half of
// the fix: after full-compact replaces Messages with a brief, the
// session reshapes to reflect the post-compact context:
//   - LastTurnInputTokens jumps to the brief size so the bar / threshold
//     check immediately read the realistic new prompt size (no spurious
//     re-fire on the next compact() call).
//   - Cumulative Usage resets to {InputTokens: briefTokens, OutputTokens: 0}
//     so the HUD reads as "fresh context after compact" — the user
//     visually confirms the bar drop. Pre-compact totals are not preserved
//     on the live struct (they go to the structured log instead).
func TestFullCompactResetsLastTurnInputTokens(t *testing.T) {
	stub := &stubLLM{
		complete: func(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool) (llm.Response, error) {
			return llm.Response{
				Content: "## Original Task\nX\n## Done So Far\n-\n## Current Target\nY\n## Next Step\nZ\n## Key Context\n-",
				Usage:   llm.Usage{InputTokens: 400_000, OutputTokens: 800}, // big summarizer prompt
			}, nil
		},
	}
	a := newTestAgent(&knownModelStub{stubLLM: stub, model: "claude-sonnet-4-6"})

	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "build it"})
	// Simulate: a turn happened with a huge prompt → ratio crossed.
	a.session.RecordTurn(llm.Usage{InputTokens: 450_000})
	a.session.MicroCompact(a.session.GetMessages())

	if got := a.session.LastTurnInputTokens(); got != 450_000 {
		t.Fatalf("precondition: LastTurnInputTokens got %d, want 450000", got)
	}

	a.fullCompact(context.Background(), a.session)

	const briefTokens = 800
	if got := a.session.LastTurnInputTokens(); got != briefTokens {
		t.Errorf("after fullCompact: LastTurnInputTokens got %d, want %d (post-compact estimate from brief size)", got, briefTokens)
	}
	if got := a.session.Usage.InputTokens; got != briefTokens {
		t.Errorf("after fullCompact: cumulative input got %d, want %d (fresh context after compact)", got, briefTokens)
	}
	if got := a.session.Usage.OutputTokens; got != 0 {
		t.Errorf("after fullCompact: cumulative output got %d, want 0 (fresh context after compact)", got)
	}
}

// TestMicroCompactEmptySession is a no-op smoke test — verifies micro
// compact on a session with zero RoleTool messages doesn't panic, leaves
// Messages untouched, but still flips IsMicroCompacted so the next
// compact escalates.
func TestMicroCompactEmptySession(t *testing.T) {
	a := newTestAgent(nil)
	// Pre-populate with non-tool messages only — micro should leave them.
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})

	before := a.session.GetMessages()

	a.microCompact(a.session)

	after := a.session.GetMessages()
	if len(before) != len(after) {
		t.Fatalf("micro-compact mutated message count on a no-tool session: before=%d after=%d", len(before), len(after))
	}
	for i := range before {
		if before[i].Content != after[i].Content {
			t.Errorf("message %d content drifted: before=%q after=%q", i, before[i].Content, after[i].Content)
		}
	}
	if !a.session.IsMicroCompacted() {
		t.Error("expected IsMicroCompacted true even on a no-op session — escalation must still happen next time")
	}
}

// TestMicroCompactPreservesToolID locks down that ToolResult.ID survives
// elision. The model uses the ID to match tool_use ↔ tool_result blocks;
// losing it produces an invalid request that providers 400 on.
func TestMicroCompactPreservesToolID(t *testing.T) {
	a := newTestAgent(nil)
	for i := 0; i < microCompactKeepRecent+3; i++ {
		a.session.Append(llm.Message{
			Role: llm.RoleTool,
			ToolResults: []*llm.ToolResult{
				{ID: idForTurn(i), Content: contentForTurn(i)},
			},
		})
	}

	a.microCompact(a.session)

	for i, m := range a.session.GetMessages() {
		if m.Role != llm.RoleTool {
			continue
		}
		for j, tr := range m.ToolResults {
			wantID := idForTurn(i)
			if tr.ID != wantID {
				t.Errorf("msg %d result %d: ID drifted, got %q want %q (must survive elision)",
					i, j, tr.ID, wantID)
			}
		}
	}
}

// TestMicroCompactIsStableUnderRepeatedCalls verifies calling
// microCompact twice in a row doesn't double-elide or otherwise corrupt
// already-placeholder results.
func TestMicroCompactIsStableUnderRepeatedCalls(t *testing.T) {
	a := newTestAgent(nil)
	for i := 0; i < microCompactKeepRecent+2; i++ {
		a.session.Append(llm.Message{
			Role: llm.RoleTool,
			ToolResults: []*llm.ToolResult{
				{ID: idForTurn(i), Content: contentForTurn(i)},
			},
		})
	}

	a.microCompact(a.session)
	firstPass := a.session.GetMessages()
	a.microCompact(a.session)
	secondPass := a.session.GetMessages()

	if len(firstPass) != len(secondPass) {
		t.Fatalf("message count drifted: first=%d second=%d", len(firstPass), len(secondPass))
	}
	for i := range firstPass {
		if len(firstPass[i].ToolResults) != len(secondPass[i].ToolResults) {
			t.Fatalf("msg %d tool result count drifted", i)
		}
		for j := range firstPass[i].ToolResults {
			if firstPass[i].ToolResults[j].Content != secondPass[i].ToolResults[j].Content {
				t.Errorf("msg %d result %d content drifted across repeat micro-compact", i, j)
			}
		}
	}
}

// knownModelStub wraps stubLLM and reports a real Anthropic model name
// so MODEL_CONTEXT_SIZE returns a non-zero budget. Without this the
// ratio test would hit the unknown-model guard and silently no-op.
type knownModelStub struct {
	*stubLLM
	model string
}

func (k *knownModelStub) Model() string { return k.model }

// --- helpers --------------------------------------------------------------

func idForTurn(i int) string     { return "tc-" + string(rune('a'+i)) }
func contentForTurn(i int) string { return "result-" + string(rune('a'+i)) }
func idxFromID(id string) int {
	if len(id) != 4 {
		return -1
	}
	return int(id[3] - 'a')
}
