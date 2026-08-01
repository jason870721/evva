package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// stubLLM is a hand-wired llm.Client used to drive fullCompact's
// summarization call without standing up a real provider. Stream is
// unused by the compaction path; Complete returns whatever the test
// installed.
type stubLLM struct {
	complete func(ctx context.Context, msgs []llm.Message, toolSet []tools.Tool) (llm.Response, error)
}

func (s *stubLLM) Name() string               { return "stub" }
func (s *stubLLM) Model() string              { return "stub-model" }
func (s *stubLLM) SupportsDeferLoading() bool { return false }
func (s *stubLLM) Apply(...llm.Option)        {}
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

// seedToolSession builds n turns of user → assistant(tool_use) →
// tool_result, each result padded to size bytes. The assistant message
// carries a real ToolCall so BuildLedger can resolve the tool name and
// file label — without it every block would be an unnamed CategoryTool
// and the tombstone text would lose its recovery instruction.
func seedToolSession(a *Agent, n, size int) []string {
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id := idForTurn(i)
		ids[i] = id
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: "u"})
		a.session.Append(llm.Message{
			Role: llm.RoleAssistant,
			ToolCalls: []*tools.Call{{
				ID:    id,
				Name:  "read",
				Input: json.RawMessage(`{"file_path":"/repo/f` + strconv.Itoa(i) + `.go"}`),
			}},
		})
		a.session.Append(llm.Message{
			Role:        llm.RoleTool,
			ToolResults: []*llm.ToolResult{{ID: id, Content: strings.Repeat("x", size)}},
		})
	}
	return ids
}

// resultByID finds a tool result in the live session.
func resultByID(a *Agent, id string) *llm.ToolResult {
	for _, m := range a.session.GetMessages() {
		for _, tr := range m.ToolResults {
			if tr != nil && tr.ID == id {
				return tr
			}
		}
	}
	return nil
}

// TestPruneTombstonesOldLargeResults verifies rung 1 tombstones results
// that are old AND large, and leaves the protected window verbatim.
//
// With the shipped defaults (keep 3 turns, keep 12 trailing results) a
// 20-turn session protects results 8..19 and prunes 0..7.
func TestPruneTombstonesOldLargeResults(t *testing.T) {
	a := newTestAgent(nil)
	ids := seedToolSession(a, 20, 4096)

	if !a.pruneContext(a.session) {
		t.Fatal("expected pruneContext to report work done")
	}

	for i, id := range ids {
		tr := resultByID(a, id)
		if tr == nil {
			t.Fatalf("result %s vanished", id)
		}
		pruned := session.IsTombstone(tr.Content)
		wantPruned := i < 8
		if pruned != wantPruned {
			t.Errorf("result %d (%s): pruned=%v, want %v — content %.60q", i, id, pruned, wantPruned, tr.Content)
		}
	}
}

// TestPruneNeverTouchesErrorResults locks down the one tool output that
// is genuinely unrecoverable: re-running a failed command may succeed the
// second time and erase the evidence, so an error's text must survive
// even when it is old, large, and otherwise a prime prune candidate.
func TestPruneNeverTouchesErrorResults(t *testing.T) {
	a := newTestAgent(nil)
	ids := seedToolSession(a, 20, 4096)

	failed := resultByID(a, ids[0])
	failed.IsError = true
	want := failed.Content

	a.pruneContext(a.session)

	got := resultByID(a, ids[0])
	if session.IsTombstone(got.Content) {
		t.Error("an error result was pruned; error text is not recoverable by re-running")
	}
	if got.Content != want {
		t.Error("error result content drifted")
	}
	if !got.IsError {
		t.Error("IsError flag lost")
	}
}

// TestPruneRespectsSizeFloor verifies small results are left alone —
// below the floor the tombstone costs more clarity than it buys bytes.
func TestPruneRespectsSizeFloor(t *testing.T) {
	a := newTestAgent(nil)
	ids := seedToolSession(a, 20, 64)

	if a.pruneContext(a.session) {
		t.Error("expected no prune work on a session of tiny results")
	}
	for _, id := range ids {
		if session.IsTombstone(resultByID(a, id).Content) {
			t.Errorf("result %s pruned despite being under the size floor", id)
		}
	}
}

// TestPruneIsRepeatableAndDoesNotSpendTheLadder is the regression test for
// audit finding 4: the old micro-compact flipped a one-shot bool, so the
// free rung could run exactly once per session and everything after it
// escalated to a full summarization. Pruning must stay available.
func TestPruneIsRepeatableAndDoesNotSpendTheLadder(t *testing.T) {
	a := newTestAgent(nil)
	seedToolSession(a, 20, 4096)

	a.pruneContext(a.session)
	if a.session.IsSpanCompacted() {
		t.Fatal("prune must not mark the span rung spent — it is free and re-runnable")
	}
	first := append([]llm.Message(nil), a.session.GetMessages()...)

	// A second pass over unchanged history finds nothing new: tombstones
	// are recognized on rebuild, so they are neither re-planned nor
	// counted as live recent results.
	if a.pruneContext(a.session) {
		t.Error("second prune pass reported work on unchanged history")
	}
	second := a.session.GetMessages()
	if len(first) != len(second) {
		t.Fatalf("message count drifted: %d → %d", len(first), len(second))
	}
	for i := range first {
		for j := range first[i].ToolResults {
			if first[i].ToolResults[j].Content != second[i].ToolResults[j].Content {
				t.Errorf("msg %d result %d content drifted across repeated prune", i, j)
			}
		}
	}

	// New material after the first pass IS prunable — that is the whole
	// point of the rung being repeatable.
	for i := 20; i < 30; i++ {
		id := "tc2-" + strconv.Itoa(i)
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: "u"})
		a.session.Append(llm.Message{
			Role:      llm.RoleAssistant,
			ToolCalls: []*tools.Call{{ID: id, Name: "bash", Input: json.RawMessage(`{"command":"go test ./..."}`)}},
		})
		a.session.Append(llm.Message{
			Role:        llm.RoleTool,
			ToolResults: []*llm.ToolResult{{ID: id, Content: strings.Repeat("y", 4096)}},
		})
	}
	if !a.pruneContext(a.session) {
		t.Error("prune found no work after the session grew — the rung is not repeatable")
	}
}

// TestPruneSkipsPinnedBlocks verifies the operator's explicit keep wins
// over every prune rule.
func TestPruneSkipsPinnedBlocks(t *testing.T) {
	a := newTestAgent(nil)
	ids := seedToolSession(a, 20, 4096)
	a.session.Pin(ids[0])

	a.pruneContext(a.session)

	if session.IsTombstone(resultByID(a, ids[0]).Content) {
		t.Error("a pinned result was pruned")
	}
	if !session.IsTombstone(resultByID(a, ids[1]).Content) {
		t.Error("precondition failed: the unpinned neighbour should have been pruned")
	}
}

// TestPruneTombstoneCarriesRecovery is audit finding 6: a bare
// "[elided]" placeholder tells the model neither what was lost nor how to
// get it back. The tombstone is a contract and must state both.
func TestPruneTombstoneCarriesRecovery(t *testing.T) {
	a := newTestAgent(nil)
	ids := seedToolSession(a, 20, 4096)
	a.pruneContext(a.session)

	ts := resultByID(a, ids[0]).Content
	for _, want := range []string{"read", "f0.go", "4.0KB", "turn 1", "read the file again"} {
		if !strings.Contains(ts, want) {
			t.Errorf("tombstone missing %q: %s", want, ts)
		}
	}
}

// TestPruneEmptySession is a no-op smoke test.
func TestPruneEmptySession(t *testing.T) {
	a := newTestAgent(nil)
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: "ok"})

	if a.pruneContext(a.session) {
		t.Error("expected no prune work on a session with no tool results")
	}
	if got := len(a.session.GetMessages()); got != 2 {
		t.Errorf("message count changed: got %d, want 2", got)
	}
	if a.session.IsSpanCompacted() {
		t.Error("a no-op prune must not spend the span rung")
	}
}

// TestFullCompactReplacesMessagesWithBrief drives fullCompact through a
// stub LLM that returns a canned brief. Verifies:
//   - Messages collapses to a single RoleUser entry carrying the brief
//   - the brief text from the LLM survives
//   - "Proceed with the Next Step" instruction is appended
//   - session.IsSpanCompacted() resets to false (the next compact
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
	a.session.SpanCompact(a.session.GetMessages())

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

	if a.session.IsSpanCompacted() {
		t.Error("IsSpanCompacted should reset to false after full compact")
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
	a.session.SpanCompact(a.session.GetMessages())

	before := a.session.GetMessages()
	a.fullCompact(context.Background(), a.session)
	after := a.session.GetMessages()

	if len(before) != len(after) {
		t.Errorf("messages mutated on LLM error: before=%d after=%d", len(before), len(after))
	}
	// IsSpanCompacted must NOT flip back via FullCompact, since FullCompact
	// was never called — session.microCompacted should still be true.
	if !a.session.IsSpanCompacted() {
		t.Error("IsSpanCompacted should remain true on summarization failure")
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
	a.session.SpanCompact(a.session.GetMessages())

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
	// Seeded with prunable material so a spurious trigger would be
	// unmistakable rather than merely a no-op that looks like a pass.
	ids := seedToolSession(a, 20, 4096)
	a.session.AddUsage(llm.Usage{InputTokens: 450_000, OutputTokens: 100_000})
	a.session.RecordTurn(llm.Usage{InputTokens: 5_000}) // tiny current prompt
	a.compact(context.Background(), a.session)

	if session.IsTombstone(resultByID(a, ids[0]).Content) {
		t.Error("compact triggered on tiny last-turn (cumulative was big — bug repro). want no-op")
	}

	// Case 2: cumulative is small, last-turn is huge → SHOULD compact.
	// The ladder's first rung is free, so this stays safe with a stub that
	// fails the test if the LLM is called at all.
	a2 := newTestAgent(&knownModelStub{stubLLM: stub, model: "claude-sonnet-4-6"})
	ids2 := seedToolSession(a2, 20, 4096)
	a2.session.AddUsage(llm.Usage{InputTokens: 1_000}) // tiny cumulative
	a2.session.RecordTurn(llm.Usage{InputTokens: 450_000})
	a2.compact(context.Background(), a2.session)

	if !session.IsTombstone(resultByID(a2, ids2[0]).Content) {
		t.Error("compact failed to trigger on huge last-turn (cumulative was small). LastTurnInputTokens not read?")
	}
	if a2.session.IsSpanCompacted() {
		t.Error("the ladder skipped its free rung and escalated straight to span compaction")
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
	a.session.SpanCompact(a.session.GetMessages())

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

// TestPrunePreservesToolID locks down that ToolResult.ID survives
// tombstoning. The model uses the ID to match tool_use ↔ tool_result
// blocks; losing it produces an invalid request that providers 400 on.
func TestPrunePreservesToolID(t *testing.T) {
	a := newTestAgent(nil)
	ids := seedToolSession(a, 20, 4096)

	a.pruneContext(a.session)

	for _, id := range ids {
		if resultByID(a, id) == nil {
			t.Errorf("result %s lost its ID across pruning", id)
		}
	}
}

// TestSpanCompactFoldsOldestSpan drives rung 2: the oldest span collapses
// into a brief while the recent tail survives verbatim.
func TestSpanCompactFoldsOldestSpan(t *testing.T) {
	client := &stubLLM{complete: func(context.Context, []llm.Message, []tools.Tool) (llm.Response, error) {
		return llm.Response{Content: "## Original Task\nfix the parser"}, nil
	}}
	a := newTestAgent(client)
	seedToolSession(a, 20, 512)
	before := len(a.session.GetMessages())

	a.spanCompact(context.Background(), a.session)

	msgs := a.session.GetMessages()
	if len(msgs) >= before {
		t.Fatalf("span compaction did not shrink history: %d → %d", before, len(msgs))
	}
	if msgs[0].Role != llm.RoleUser {
		t.Fatalf("first message after span compaction: got role %q, want user", msgs[0].Role)
	}
	if !strings.Contains(msgs[0].Content, "EARLIER CONTEXT FOLDED") {
		t.Error("brief is missing its framing header")
	}
	if !strings.Contains(msgs[0].Content, "fix the parser") {
		t.Error("summarizer output did not survive into the brief")
	}
	if !a.session.IsSpanCompacted() {
		t.Error("span compaction must mark its rung spent so the next escalation goes full")
	}
}

// TestSpanCompactLeavesSessionIntactOnFailure verifies a transport error
// neither rewrites history nor spends the rung — otherwise one flaky call
// would push the session straight to full compaction.
func TestSpanCompactLeavesSessionIntactOnFailure(t *testing.T) {
	client := &stubLLM{complete: func(context.Context, []llm.Message, []tools.Tool) (llm.Response, error) {
		return llm.Response{}, errors.New("transport boom")
	}}
	a := newTestAgent(client)
	seedToolSession(a, 20, 512)
	before := len(a.session.GetMessages())

	a.spanCompact(context.Background(), a.session)

	if got := len(a.session.GetMessages()); got != before {
		t.Errorf("history changed on a failed span compaction: %d → %d", before, got)
	}
	if a.session.IsSpanCompacted() {
		t.Error("a failed span compaction must not spend the rung")
	}
}

// TestSpanBoundaryNeverLandsOnToolMessage is the well-formedness
// invariant: cutting in front of a RoleTool message would orphan it from
// the assistant tool_use that requested it, and every provider rejects
// that.
func TestSpanBoundaryNeverLandsOnToolMessage(t *testing.T) {
	a := newTestAgent(nil)
	seedToolSession(a, 20, 128)
	msgs := a.session.GetMessages()

	for _, frac := range []float64{0.1, 0.25, 0.5, 0.75, 0.9} {
		end := spanBoundary(msgs, frac)
		if end == 0 {
			continue
		}
		if msgs[end].Role == llm.RoleTool {
			t.Errorf("frac %.2f: boundary %d lands on a tool message", frac, end)
		}
	}
}

// TestSpanBoundaryDeclinesShortTranscripts verifies the rung reports "no
// safe boundary" rather than folding a session that has nothing old.
func TestSpanBoundaryDeclinesShortTranscripts(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "a"},
		{Role: llm.RoleAssistant, Content: "b"},
	}
	if got := spanBoundary(msgs, 0.5); got != 0 {
		t.Errorf("spanBoundary on a 2-message session: got %d, want 0", got)
	}
}

// TestFullCompactReinjectsPins verifies a pin survives the rung that
// discards everything else.
func TestFullCompactReinjectsPins(t *testing.T) {
	client := &stubLLM{complete: func(context.Context, []llm.Message, []tools.Tool) (llm.Response, error) {
		return llm.Response{Content: "brief body"}, nil
	}}
	a := newTestAgent(client)
	ids := seedToolSession(a, 6, 256)
	pinned := resultByID(a, ids[0])
	pinned.Content = "PINNED-PAYLOAD-MARKER"
	a.session.Pin(ids[0])

	a.fullCompact(context.Background(), a.session)

	msgs := a.session.GetMessages()
	if len(msgs) != 1 {
		t.Fatalf("full compaction should collapse to one message, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "PINNED-PAYLOAD-MARKER") {
		t.Error("pinned content did not survive full compaction")
	}
	if !strings.Contains(msgs[0].Content, "brief body") {
		t.Error("the brief itself is missing")
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

func idForTurn(i int) string      { return "tc-" + string(rune('a'+i)) }
func contentForTurn(i int) string { return "result-" + string(rune('a'+i)) }
func idxFromID(id string) int {
	if len(id) != 4 {
		return -1
	}
	return int(id[3] - 'a')
}
