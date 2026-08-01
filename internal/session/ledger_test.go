package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// toolTurn builds one user → assistant(tool_use) → tool_result triple.
func toolTurn(id, name, input, result string, isErr bool) []llm.Message {
	return []llm.Message{
		{Role: llm.RoleUser, Content: "u"},
		{Role: llm.RoleAssistant, ToolCalls: []*tools.Call{{ID: id, Name: name, Input: json.RawMessage(input)}}},
		{Role: llm.RoleTool, ToolResults: []*llm.ToolResult{{ID: id, Content: result, IsError: isErr}}},
	}
}

func TestBuildLedgerCountsBytesAndCategories(t *testing.T) {
	var msgs []llm.Message
	msgs = append(msgs, toolTurn("a", "read", `{"file_path":"/repo/loop.go"}`, strings.Repeat("x", 100), false)...)
	msgs = append(msgs, toolTurn("b", "bash", `{"command":"go test ./..."}`, strings.Repeat("y", 50), false)...)

	l := BuildLedger(msgs, "SYSTEM", nil)

	if l.Turns != 2 {
		t.Errorf("Turns: got %d, want 2", l.Turns)
	}
	cats := l.ByCategory()
	if cats[CategorySystem] != len("SYSTEM") {
		t.Errorf("system bytes: got %d, want %d", cats[CategorySystem], len("SYSTEM"))
	}
	if cats[CategoryFile] != 100 {
		t.Errorf("file bytes: got %d, want 100 (the read result)", cats[CategoryFile])
	}
	if cats[CategoryTool] != 50 {
		t.Errorf("tool bytes: got %d, want 50 (the bash result)", cats[CategoryTool])
	}
}

// The category split is the whole reason /context is useful — a read is
// cheap to recover, a test run is not.
func TestBuildLedgerLabelsAndNamesBlocks(t *testing.T) {
	msgs := toolTurn("a", "read", `{"file_path":"/repo/pkg/agent/loop.go"}`, "body", false)
	l := BuildLedger(msgs, "", nil)

	var found bool
	for _, b := range l.Blocks {
		if b.ToolID != "a" {
			continue
		}
		found = true
		if b.ToolName != "read" {
			t.Errorf("ToolName: got %q, want read", b.ToolName)
		}
		if b.Label != "loop.go" {
			t.Errorf("Label: got %q, want loop.go (base name, so a fixture survives a checkout move)", b.Label)
		}
		if b.Category != CategoryFile {
			t.Errorf("Category: got %q, want file", b.Category)
		}
		if b.Turn != 1 {
			t.Errorf("Turn: got %d, want 1", b.Turn)
		}
	}
	if !found {
		t.Fatal("no block for tool id a")
	}
}

// Image payloads are the heaviest thing a tool result can carry, and
// RenderContentBlocksAsText renders them as a short placeholder — so
// counting the rendered text instead of the base64 would understate the
// block by orders of magnitude.
func TestBuildLedgerCountsImagePayloadNotItsPlaceholder(t *testing.T) {
	b64 := strings.Repeat("A", 4000)
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "look"},
		{Role: llm.RoleAssistant, ToolCalls: []*tools.Call{{ID: "img", Name: "read", Input: json.RawMessage(`{"file_path":"/s.png"}`)}}},
		{Role: llm.RoleTool, ToolResults: []*llm.ToolResult{{
			ID: "img",
			ContentBlocks: []tools.ContentBlock{{
				Type:  tools.ContentBlockImage,
				Image: &tools.ImageBlock{MIMEType: "image/png", Base64Data: b64, OriginalSize: 3000},
			}},
		}}},
	}
	l := BuildLedger(msgs, "", nil)
	if got := l.ByCategory()[CategoryFile]; got != len(b64) {
		t.Errorf("image block bytes: got %d, want %d", got, len(b64))
	}
}

func TestBuildLedgerMarksPinnedAndPruned(t *testing.T) {
	msgs := toolTurn("a", "read", `{"file_path":"/f.go"}`, "body", false)
	msgs = append(msgs, toolTurn("b", "read", `{"file_path":"/g.go"}`, Tombstone(Block{ToolName: "read", Label: "g.go", Turn: 1, Bytes: 9999}), false)...)

	l := BuildLedger(msgs, "", map[string]struct{}{"a": {}})

	for _, b := range l.Blocks {
		switch b.ToolID {
		case "a":
			if !b.Pinned {
				t.Error("block a should be pinned")
			}
			if b.Recoverable() {
				t.Error("a pinned block must not be a prune candidate")
			}
		case "b":
			if !b.Pruned {
				t.Error("block b carries a tombstone and should be marked pruned")
			}
			if b.Recoverable() {
				t.Error("an already-pruned block must not be a prune candidate")
			}
		}
	}
}

func TestHeaviestIsDescendingAndCapped(t *testing.T) {
	var msgs []llm.Message
	for i, size := range []int{10, 500, 50, 900, 200} {
		msgs = append(msgs, toolTurn("t"+strconv.Itoa(i), "bash", `{"command":"ls"}`, strings.Repeat("x", size), false)...)
	}
	got := BuildLedger(msgs, "", nil).Heaviest(3)
	if len(got) != 3 {
		t.Fatalf("Heaviest(3): got %d blocks", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].Bytes > got[i-1].Bytes {
			t.Errorf("Heaviest is not descending at %d: %d > %d", i, got[i].Bytes, got[i-1].Bytes)
		}
	}
	if got[0].Bytes != 900 {
		t.Errorf("largest block: got %d, want 900", got[0].Bytes)
	}
}

// The /context overlay builds its ledger on the UI goroutine while the
// agent loop is appending on its own. Run under -race this is the test
// that justifies Session.msgMu: without it, a slice-header tear here is
// a TUI panic rather than a stale byte count.
func TestLedgerIsSafeWhileTheLoopAppends(t *testing.T) {
	s := New()
	done := make(chan struct{})

	go func() {
		defer close(done)
		for i := 0; i < 150; i++ {
			s.Append(llm.Message{Role: llm.RoleUser, Content: strings.Repeat("x", 64)})
			s.Append(llm.Message{
				Role:        llm.RoleTool,
				ToolResults: []*llm.ToolResult{{ID: "t" + strconv.Itoa(i), Content: "body"}},
			})
		}
	}()

	for i := 0; i < 150; i++ {
		l := s.Ledger("SYSTEM")
		_ = l.Bytes()
		_ = l.ByCategory()
		_ = l.Heaviest(5)
		s.TogglePin("t" + strconv.Itoa(i%50))
	}
	<-done

	// Sanity: the concurrent writer's work is all there afterwards.
	if got := len(s.CopyMessages()); got != 300 {
		t.Errorf("expected 300 messages after the writer finished, got %d", got)
	}
}

// firstWord skipping VAR=value keeps `FOO=1 go test` labelled "go"
// rather than "FOO=1", which is what an operator scanning /context reads.
func TestCallLabelSkipsEnvAssignments(t *testing.T) {
	if got := callLabel("bash", json.RawMessage(`{"command":"CGO_ENABLED=0 go test ./..."}`)); got != "go" {
		t.Errorf("bash label: got %q, want go", got)
	}
}

// An unknown tool should still get a usable label rather than falling
// back to a bare name — new tools land without touching the ledger.
func TestCallLabelFallsBackForUnknownTools(t *testing.T) {
	if got := callLabel("some_new_tool", json.RawMessage(`{"query":"widgets"}`)); got != "widgets" {
		t.Errorf("fallback label: got %q, want widgets", got)
	}
	if got := callLabel("some_new_tool", json.RawMessage(`not json`)); got != "" {
		t.Errorf("malformed input should yield an empty label, got %q", got)
	}
}
