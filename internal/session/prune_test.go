package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
)

// seedTurns builds n turns of read calls whose results are size bytes.
func seedTurns(n, size int) []llm.Message {
	var msgs []llm.Message
	for i := 0; i < n; i++ {
		msgs = append(msgs, toolTurn(
			"t"+strconv.Itoa(i),
			"read",
			`{"file_path":"/repo/f`+strconv.Itoa(i)+`.go"}`,
			strings.Repeat("x", size),
			false,
		)...)
	}
	return msgs
}

func TestPlanPruneIsPureAndDeterministic(t *testing.T) {
	l := BuildLedger(seedTurns(20, 4096), "", nil)
	pol := DefaultPrunePolicy()

	a := PlanPrune(l, pol)
	b := PlanPrune(l, pol)

	if a.Count() != b.Count() || a.Bytes != b.Bytes {
		t.Fatalf("PlanPrune is not deterministic: %d/%d vs %d/%d", a.Count(), a.Bytes, b.Count(), b.Bytes)
	}
	if a.Empty() {
		t.Fatal("expected a non-empty plan for 20 turns of 4KB results")
	}
	if a.Bytes <= 0 {
		t.Errorf("plan reports no saving: %d bytes", a.Bytes)
	}
}

func TestPlanPruneHonoursTheRecencyWindow(t *testing.T) {
	// A short keep-results floor so the turn window is the binding rule.
	pol := PrunePolicy{MinBytes: 100, KeepRecentTurns: 3, KeepRecentResults: 1}
	l := BuildLedger(seedTurns(10, 4096), "", nil)
	plan := PlanPrune(l, pol)

	for _, b := range l.Blocks {
		if b.ToolID == "" {
			continue
		}
		_, planned := plan.Tombstones[b.ToolID]
		// Turns 8, 9 and 10 sit inside the window and must survive.
		if b.Turn > 7 && planned {
			t.Errorf("turn %d is inside the 3-turn window but was planned for pruning", b.Turn)
		}
	}
}

func TestPlanPruneHonoursTheResultCountFloor(t *testing.T) {
	// No turn protection at all — the count floor is the only guard.
	pol := PrunePolicy{MinBytes: 100, KeepRecentTurns: 1, KeepRecentResults: 5}
	l := BuildLedger(seedTurns(20, 4096), "", nil)
	plan := PlanPrune(l, pol)

	live := 0
	for _, b := range l.Blocks {
		if b.ToolID == "" {
			continue
		}
		if _, planned := plan.Tombstones[b.ToolID]; !planned {
			live++
		}
	}
	if live < 5 {
		t.Errorf("only %d live results survive; the floor promised at least 5", live)
	}
}

func TestPlanPruneSkipsErrorsAndPins(t *testing.T) {
	msgs := seedTurns(20, 4096)
	// Mark the very first result as an error.
	for i := range msgs {
		if msgs[i].Role == llm.RoleTool && msgs[i].ToolResults[0].ID == "t0" {
			msgs[i].ToolResults[0].IsError = true
		}
	}
	l := BuildLedger(msgs, "", map[string]struct{}{"t1": {}})
	plan := PlanPrune(l, DefaultPrunePolicy())

	if _, ok := plan.Tombstones["t0"]; ok {
		t.Error("an error result was planned for pruning")
	}
	if _, ok := plan.Tombstones["t1"]; ok {
		t.Error("a pinned result was planned for pruning")
	}
	if _, ok := plan.Tombstones["t2"]; !ok {
		t.Error("precondition failed: t2 should be prunable")
	}
}

// A tombstone must never be larger than what it replaces, or the "prune"
// would grow the prompt.
func TestPlanPruneNeverGrowsTheContext(t *testing.T) {
	l := BuildLedger(seedTurns(20, 4096), "", nil)
	plan := PlanPrune(l, DefaultPrunePolicy())
	for id, ts := range plan.Tombstones {
		if len(ts) >= 4096 {
			t.Errorf("tombstone for %s is %d bytes, replacing 4096", id, len(ts))
		}
	}
}

func TestApplyPruneRewritesOnlyPlannedResults(t *testing.T) {
	msgs := seedTurns(20, 4096)
	l := BuildLedger(msgs, "", nil)
	plan := PlanPrune(l, DefaultPrunePolicy())

	out := ApplyPrune(msgs, plan)

	if len(out) != len(msgs) {
		t.Fatalf("message count changed: %d → %d", len(msgs), len(out))
	}
	for _, m := range out {
		for _, tr := range m.ToolResults {
			_, planned := plan.Tombstones[tr.ID]
			if planned != IsTombstone(tr.Content) {
				t.Errorf("result %s: planned=%v but tombstoned=%v", tr.ID, planned, IsTombstone(tr.Content))
			}
		}
	}
}

// ApplyPrune must not mutate its input: the caller may still be holding
// the pre-prune history for a snapshot write.
func TestApplyPruneDoesNotMutateInput(t *testing.T) {
	msgs := seedTurns(20, 4096)
	plan := PlanPrune(BuildLedger(msgs, "", nil), DefaultPrunePolicy())

	var before string
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.ToolResults[0].ID == "t0" {
			before = m.ToolResults[0].Content
		}
	}
	ApplyPrune(msgs, plan)
	for _, m := range msgs {
		if m.Role == llm.RoleTool && m.ToolResults[0].ID == "t0" && m.ToolResults[0].Content != before {
			t.Error("ApplyPrune mutated the input slice's tool result")
		}
	}
}

// Applying a plan twice must converge: the second ledger sees tombstones
// and plans nothing further. Without this the ladder would loop forever
// re-pruning the same blocks.
func TestPruneConverges(t *testing.T) {
	msgs := seedTurns(20, 4096)
	once := ApplyPrune(msgs, PlanPrune(BuildLedger(msgs, "", nil), DefaultPrunePolicy()))
	second := PlanPrune(BuildLedger(once, "", nil), DefaultPrunePolicy())
	if !second.Empty() {
		t.Errorf("prune did not converge: second pass planned %d more tombstones", second.Count())
	}
}

func TestPolicyNormalizationRejectsZeroes(t *testing.T) {
	// A YAML block that only sets one field must not become a policy that
	// prunes everything.
	l := BuildLedger(seedTurns(20, 4096), "", nil)
	plan := PlanPrune(l, PrunePolicy{MinBytes: 100})
	if plan.Count() >= 20 {
		t.Errorf("a partially-specified policy pruned %d of 20 results; defaults should still protect the tail", plan.Count())
	}
}

func TestTombstoneStatesSizeTurnAndRecovery(t *testing.T) {
	ts := Tombstone(Block{ToolName: "bash", Label: "go", Turn: 7, Bytes: 8_600})
	for _, want := range []string{"bash go", "turn 7", "8.4KB", "re-run the command"} {
		if !strings.Contains(ts, want) {
			t.Errorf("tombstone missing %q: %s", want, ts)
		}
	}
	if !IsTombstone(ts) {
		t.Error("Tombstone output is not recognized by IsTombstone")
	}
}

func TestRenderPinnedQuotesPinnedContent(t *testing.T) {
	msgs := seedTurns(3, 64)
	l := BuildLedger(msgs, "", map[string]struct{}{"t1": {}})

	got := RenderPinned(msgs, l)
	if !strings.Contains(got, "PINNED CONTEXT") {
		t.Error("pin block is missing its header")
	}
	if !strings.Contains(got, "f1.go") {
		t.Error("pin block does not name the pinned block")
	}
	if strings.Contains(got, "f0.go") || strings.Contains(got, "f2.go") {
		t.Error("pin block leaked unpinned neighbours")
	}
}

func TestRenderPinnedIsEmptyWithoutPins(t *testing.T) {
	msgs := seedTurns(3, 64)
	if got := RenderPinned(msgs, BuildLedger(msgs, "", nil)); got != "" {
		t.Errorf("expected empty pin block, got %q", got)
	}
}

func TestPinSetRoundTripsThroughSnapshot(t *testing.T) {
	s := New()
	s.Append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	s.Pin("keep-me")
	s.Pin("keep-me-too")

	restored := FromSnapshot(s.ToSnapshot())
	if !restored.IsPinned("keep-me") || !restored.IsPinned("keep-me-too") {
		t.Error("pins did not survive the snapshot round trip")
	}
	if restored.IsPinned("never-pinned") {
		t.Error("restored session invented a pin")
	}
}

// An older snapshot has no pins key at all; it must decode to "nothing
// pinned" rather than to an error or a nil-map panic.
func TestSnapshotWithoutPinsDecodesCleanly(t *testing.T) {
	var st SessionState
	if err := json.Unmarshal([]byte(`{"messages":[],"usage":{},"micro_compacted":true}`), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	s := FromSnapshot(st)
	if len(s.Pins()) != 0 {
		t.Errorf("expected no pins, got %v", s.Pins())
	}
	if !s.IsSpanCompacted() {
		t.Error("the legacy micro_compacted key should still rehydrate the span rung's state")
	}
	s.Pin("x") // must not panic on the nil map
	if !s.IsPinned("x") {
		t.Error("pin on a rehydrated session did not take")
	}
}

func TestTogglePin(t *testing.T) {
	s := New()
	if !s.TogglePin("a") {
		t.Error("first toggle should pin")
	}
	if s.TogglePin("a") {
		t.Error("second toggle should unpin")
	}
	if s.TogglePin("") {
		t.Error("empty id should never pin")
	}
}
