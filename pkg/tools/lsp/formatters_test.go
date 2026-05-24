package lsp

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

func TestFormatDefinition(t *testing.T) {
	loc := protocol.Location{URI: "file:///project/main.go", Range: protocol.Range{
		Start: protocol.Position{Line: 9, Character: 5}, End: protocol.Position{Line: 9, Character: 22}}}
	raw, _ := json.Marshal(loc)

	result := formatDefinition(raw)
	if !strings.Contains(result, "file:///project/main.go") {
		t.Errorf("expected URI in result, got: %s", result)
	}
	if !strings.Contains(result, "10:6") {
		t.Errorf("expected 1-indexed position, got: %s", result)
	}
}

func TestFormatDefinitionNull(t *testing.T) {
	result := formatDefinition(json.RawMessage("null"))
	if !strings.Contains(result, "No definition") {
		t.Errorf("expected 'No definition', got: %s", result)
	}
}

func TestFormatReferences(t *testing.T) {
	locs := []protocol.Location{
		{URI: "file:///project/a.go", Range: protocol.Range{Start: protocol.Position{Line: 3, Character: 1}, End: protocol.Position{Line: 3, Character: 9}}},
		{URI: "file:///project/b.go", Range: protocol.Range{Start: protocol.Position{Line: 11, Character: 4}, End: protocol.Position{Line: 11, Character: 12}}},
	}
	raw, _ := json.Marshal(locs)

	result := formatReferences(raw)
	if !strings.Contains(result, "2 results") {
		t.Errorf("expected '2 results', got: %s", result)
	}
	if !strings.Contains(result, "file:///project/a.go") {
		t.Errorf("expected URI in result, got: %s", result)
	}
}

func TestFormatReferencesNull(t *testing.T) {
	result := formatReferences(json.RawMessage("null"))
	if !strings.Contains(result, "No references") {
		t.Errorf("expected 'No references', got: %s", result)
	}
}

func TestFormatHover(t *testing.T) {
	h := protocol.Hover{Contents: protocol.MarkupContent{Kind: "markdown", Value: "```go\nfunc hello() string\n```\n\nReturns a greeting."}}
	raw, _ := json.Marshal(h)

	result := formatHover(raw)
	if !strings.Contains(result, "func hello() string") {
		t.Errorf("expected hover content, got: %s", result)
	}
}

func TestFormatHoverNull(t *testing.T) {
	result := formatHover(json.RawMessage("null"))
	if !strings.Contains(result, "No hover") {
		t.Errorf("expected 'No hover', got: %s", result)
	}
}

func TestFormatDocumentSymbols(t *testing.T) {
	syms := []protocol.DocumentSymbol{
		{Name: "main", Kind: protocol.SKFunction,
			Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 10, Character: 1}},
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 4}}},
	}
	raw, _ := json.Marshal(syms)

	result := formatDocumentSymbols(raw)
	if !strings.Contains(result, "main") {
		t.Errorf("expected symbol 'main', got: %s", result)
	}
	if !strings.Contains(result, "Function") {
		t.Errorf("expected 'Function', got: %s", result)
	}
}

func TestFormatDocumentSymbolsNull(t *testing.T) {
	result := formatDocumentSymbols(json.RawMessage("null"))
	if !strings.Contains(result, "No symbols") {
		t.Errorf("expected 'No symbols', got: %s", result)
	}
}

func TestFormatWorkspaceSymbols(t *testing.T) {
	infos := []protocol.SymbolInformation{
		{Name: "ServeHTTP", Kind: protocol.SKFunction, Location: protocol.Location{URI: "file:///project/server.go",
			Range: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 5}}}},
		{Name: "Server", Kind: protocol.SKClass, Location: protocol.Location{URI: "file:///project/server.go",
			Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 1, Character: 5}}}},
	}
	raw, _ := json.Marshal(infos)

	result := formatWorkspaceSymbols(raw, "Server")
	if !strings.Contains(result, "ServeHTTP") {
		t.Errorf("expected ServeHTTP, got: %s", result)
	}
	if !strings.Contains(result, "results") {
		t.Errorf("expected result count, got: %s", result)
	}
}

func TestFormatWorkspaceSymbolsNull(t *testing.T) {
	result := formatWorkspaceSymbols(json.RawMessage("null"), "q")
	if !strings.Contains(result, "No symbols found") {
		t.Errorf("expected 'No symbols found', got: %s", result)
	}
}

func TestFormatImplementation(t *testing.T) {
	locs := []protocol.Location{
		{URI: "file:///project/impl.go", Range: protocol.Range{
			Start: protocol.Position{Line: 10, Character: 0}, End: protocol.Position{Line: 10, Character: 5}}},
	}
	raw, _ := json.Marshal(locs)

	result := formatImplementation(raw)
	if !strings.Contains(result, "file:///project/impl.go") {
		t.Errorf("expected implementation location, got: %s", result)
	}
}

func TestFormatImplementationNull(t *testing.T) {
	result := formatImplementation(json.RawMessage("null"))
	if !strings.Contains(result, "No implementations") {
		t.Errorf("expected 'No implementations', got: %s", result)
	}
}

func TestFormatCallHierarchy(t *testing.T) {
	items := []protocol.CallHierarchyItem{
		{Name: "main", Kind: protocol.SKFunction, URI: "file:///project/main.go",
			SelectionRange: protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 4}}},
	}
	raw, _ := json.Marshal(items)

	result := formatCallHierarchy(raw)
	if !strings.Contains(result, "main") {
		t.Errorf("expected 'main', got: %s", result)
	}
}

func TestFormatCallHierarchyNull(t *testing.T) {
	result := formatCallHierarchy(json.RawMessage("null"))
	if !strings.Contains(result, "No call hierarchy") {
		t.Errorf("expected 'No call hierarchy', got: %s", result)
	}
}

func TestFormatIncomingCalls(t *testing.T) {
	calls := []protocol.CallHierarchyIncomingCall{
		{From: protocol.CallHierarchyItem{Name: "caller", Kind: protocol.SKFunction, URI: "file:///project/caller.go"},
			FromRanges: []protocol.Range{{Start: protocol.Position{Line: 1, Character: 2}, End: protocol.Position{Line: 1, Character: 8}}}},
	}
	raw, _ := json.Marshal(calls)

	result := formatIncomingCalls(raw)
	if !strings.Contains(result, "caller") {
		t.Errorf("expected 'caller', got: %s", result)
	}
}

func TestFormatIncomingCallsNull(t *testing.T) {
	result := formatIncomingCalls(json.RawMessage("null"))
	if !strings.Contains(result, "No incoming calls") {
		t.Errorf("expected 'No incoming calls', got: %s", result)
	}
}

func TestFormatOutgoingCalls(t *testing.T) {
	calls := []protocol.CallHierarchyOutgoingCall{
		{To: protocol.CallHierarchyItem{Name: "callee", Kind: protocol.SKFunction, URI: "file:///project/callee.go"},
			FromRanges: []protocol.Range{{Start: protocol.Position{Line: 1, Character: 2}, End: protocol.Position{Line: 1, Character: 8}}}},
	}
	raw, _ := json.Marshal(calls)

	result := formatOutgoingCalls(raw)
	if !strings.Contains(result, "callee") {
		t.Errorf("expected 'callee', got: %s", result)
	}
}

func TestFormatOutgoingCallsNull(t *testing.T) {
	result := formatOutgoingCalls(json.RawMessage("null"))
	if !strings.Contains(result, "No outgoing calls") {
		t.Errorf("expected 'No outgoing calls', got: %s", result)
	}
}

func TestTruncation(t *testing.T) {
	long := strings.Repeat("x", 100)
	result := truncString(long, 50)
	if !strings.Contains(result, "truncated") {
		t.Errorf("expected truncation marker, got: %s", result)
	}
	if len(result) > 100 {
		t.Errorf("truncated result still large: %d bytes", len(result))
	}
}
