package lsp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

const (
	maxDefinitionResults   = 1
	maxDefinitionBytes     = 2 * 1024
	maxReferencesResults   = 50
	maxReferencesBytes     = 20 * 1024
	maxHoverBytes          = 5 * 1024
	maxSymbolResults       = 100
	maxSymbolBytes         = 30 * 1024
	maxWorkspaceResults    = 50
	maxWorkspaceBytes      = 20 * 1024
	maxImplementationResults = 20
	maxImplementationBytes   = 10 * 1024
	maxCallHierarchyResults  = 30
	maxCallHierarchyBytes    = 15 * 1024
	globalResultCapBytes     = 40 * 1024
)

// formatDefinition formats a textDocument/definition result.
// The result may be a single Location, a slice of Locations, or null.
func formatDefinition(raw json.RawMessage) string {
	// Check for null.
	if isEmpty(raw) {
		return "No definition found."
	}

	// Try as single Location.
	var loc protocol.Location
	if err := json.Unmarshal(raw, &loc); err == nil && loc.URI != "" {
		return fmt.Sprintf("Definition: %s", formatLocation(loc))
	}

	// Try as slice.
	var locs []protocol.Location
	if err := json.Unmarshal(raw, &locs); err == nil {
		if len(locs) == 0 {
			return "No definition found."
		}
		// Return the first (best) location.
		return fmt.Sprintf("Definition: %s", formatLocation(locs[0]))
	}

	return fmt.Sprintf("Definition: (unexpected format) %s", trunc(raw, maxDefinitionBytes))
}

// formatReferences formats a textDocument/references result.
func formatReferences(raw json.RawMessage) string {
	if isEmpty(raw) {
		return "No references found."
	}

	var locs []protocol.Location
	if err := json.Unmarshal(raw, &locs); err != nil {
		return fmt.Sprintf("References: (unexpected format) %s", trunc(raw, maxReferencesBytes))
	}
	if len(locs) == 0 {
		return "No references found."
	}

	var b strings.Builder
	total := len(locs)
	shown := total
	if shown > maxReferencesResults {
		shown = maxReferencesResults
	}

	fmt.Fprintf(&b, "References (%d results):\n", total)
	for i := 0; i < shown; i++ {
		fmt.Fprintf(&b, "  %s\n", formatLocation(locs[i]))
	}
	if total > shown {
		fmt.Fprintf(&b, "  ...and %d more locations\n", total-shown)
	}
	return truncString(b.String(), maxReferencesBytes)
}

// formatHover formats a textDocument/hover result.
func formatHover(raw json.RawMessage) string {
	if isEmpty(raw) {
		return "No hover information available."
	}

	var h protocol.Hover
	if err := json.Unmarshal(raw, &h); err != nil {
		return fmt.Sprintf("Hover: (unexpected format) %s", trunc(raw, maxHoverBytes))
	}

	content := formatMarkup(h.Contents)
	if content == "" {
		return "No hover information available."
	}
	return truncString(fmt.Sprintf("Hover:\n%s", content), maxHoverBytes)
}

// formatDocumentSymbols formats a textDocument/documentSymbol result.
// Servers may return DocumentSymbol (hierarchical) or SymbolInformation (flat).
func formatDocumentSymbols(raw json.RawMessage) string {
	if isEmpty(raw) {
		return "No symbols found."
	}

	// Try hierarchical DocumentSymbol first.
	var syms []protocol.DocumentSymbol
	if err := json.Unmarshal(raw, &syms); err == nil {
		return formatHierarchicalSymbols(syms)
	}

	// Fall back to flat SymbolInformation.
	var infos []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &infos); err == nil {
		if len(infos) == 0 {
			return "No symbols found."
		}
		var b strings.Builder
		total := len(infos)
		shown := total
		if shown > maxSymbolResults {
			shown = maxSymbolResults
		}
		fmt.Fprintf(&b, "Symbols (%d results):\n", total)
		for i := 0; i < shown; i++ {
			fmt.Fprintf(&b, "  %s\t%s @ %s\n",
				infos[i].Kind, infos[i].Name, formatLocation(infos[i].Location))
		}
		if total > shown {
			fmt.Fprintf(&b, "  ...and %d more symbols\n", total-shown)
		}
		return truncString(b.String(), maxSymbolBytes)
	}

	return fmt.Sprintf("Symbols: (unexpected format) %s", trunc(raw, maxSymbolBytes))
}

// formatHierarchicalSymbols recursively formats DocumentSymbols.
func formatHierarchicalSymbols(syms []protocol.DocumentSymbol) string {
	if len(syms) == 0 {
		return "No symbols found."
	}

	ptrs := make([]*protocol.DocumentSymbol, len(syms))
	for i := range syms {
		ptrs[i] = &syms[i]
	}

	var b strings.Builder
	count := 0
	formatSymTree(&b, ptrs, 0, &count)
	if count == 0 {
		return "No symbols found."
	}

	return truncString(fmt.Sprintf("Symbols:\n%s", b.String()), maxSymbolBytes)
}

func formatSymTree(b *strings.Builder, syms []*protocol.DocumentSymbol, depth int, count *int) {
	for _, s := range syms {
		if *count >= maxSymbolResults {
			return
		}
		indent := strings.Repeat("  ", depth)
		rng := fmt.Sprintf("%d:%d-%d:%d",
			s.Range.Start.Line+1, s.Range.Start.Character+1,
			s.Range.End.Line+1, s.Range.End.Character+1)
		fmt.Fprintf(b, "%s%s\t%s @ %s\n", indent, s.Kind, s.Name, rng)
		if s.Detail != "" {
			fmt.Fprintf(b, "%s  %s\n", indent, s.Detail)
		}
		*count++
		formatSymTree(b, s.Children, depth+1, count)
	}
}

// ── workspace symbol ──────────────────────────────────────────────────

// formatWorkspaceSymbols formats a workspace/symbol result.
func formatWorkspaceSymbols(raw json.RawMessage, query string) string {
	if isEmpty(raw) {
		return fmt.Sprintf("No symbols found for %q.", query)
	}

	var infos []protocol.SymbolInformation
	if err := json.Unmarshal(raw, &infos); err != nil {
		return fmt.Sprintf("Workspace symbols: (unexpected format) %s", trunc(raw, maxWorkspaceBytes))
	}
	if len(infos) == 0 {
		return fmt.Sprintf("No symbols found for %q.", query)
	}

	// Sort by kind priority: Function > Class > Method > Variable > others.
	sortSymbolsByKind(infos)

	var b strings.Builder
	total := len(infos)
	shown := total
	if shown > maxWorkspaceResults {
		shown = maxWorkspaceResults
	}

	fmt.Fprintf(&b, "Workspace symbols matching %q (%d results):\n", query, total)
	for i := 0; i < shown; i++ {
		fmt.Fprintf(&b, "  %s\t%s — %s\n",
			infos[i].Kind, infos[i].Name, formatLocation(infos[i].Location))
	}
	if total > shown {
		fmt.Fprintf(&b, "  ...and %d more symbols\n", total-shown)
	}
	return truncString(b.String(), maxWorkspaceBytes)
}

// sortSymbolsByKind sorts SymbolInformation by kind priority.
func sortSymbolsByKind(infos []protocol.SymbolInformation) {
	priority := map[protocol.SymbolKind]int{
		protocol.SKFunction:  1,
		protocol.SKClass:     2,
		protocol.SKMethod:    3,
		protocol.SKVariable:  4,
		protocol.SKConstant:  5,
		protocol.SKInterface: 6,
		protocol.SKStruct:    7,
	}
	for i := 0; i < len(infos); i++ {
		for j := i + 1; j < len(infos); j++ {
			pi := priority[infos[i].Kind]
			pj := priority[infos[j].Kind]
			if pi == 0 {
				pi = 99
			}
			if pj == 0 {
				pj = 99
			}
			if pj < pi || (pj == pi && infos[j].Name < infos[i].Name) {
				infos[i], infos[j] = infos[j], infos[i]
			}
		}
	}
}

// ── implementation ─────────────────────────────────────────────────────

// formatImplementation formats a textDocument/implementation result.
func formatImplementation(raw json.RawMessage) string {
	if isEmpty(raw) {
		return "No implementations found."
	}

	var locs []protocol.Location
	if err := json.Unmarshal(raw, &locs); err != nil {
		return fmt.Sprintf("Implementations: (unexpected format) %s", trunc(raw, maxImplementationBytes))
	}
	if len(locs) == 0 {
		return "No implementations found."
	}

	var b strings.Builder
	total := len(locs)
	shown := total
	if shown > maxImplementationResults {
		shown = maxImplementationResults
	}

	fmt.Fprintf(&b, "Implementations (%d results):\n", total)
	for i := 0; i < shown; i++ {
		fmt.Fprintf(&b, "  %s\n", formatLocation(locs[i]))
	}
	if total > shown {
		fmt.Fprintf(&b, "  ...and %d more locations\n", total-shown)
	}
	return truncString(b.String(), maxImplementationBytes)
}

// ── call hierarchy ─────────────────────────────────────────────────────

// formatCallHierarchy formats a textDocument/prepareCallHierarchy result.
func formatCallHierarchy(raw json.RawMessage) string {
	if isEmpty(raw) {
		return "No call hierarchy items found."
	}

	var items []protocol.CallHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Sprintf("Call hierarchy: (unexpected format) %s", trunc(raw, maxCallHierarchyBytes))
	}
	if len(items) == 0 {
		return "No call hierarchy items found."
	}

	var b strings.Builder
	shown := len(items)
	if shown > maxCallHierarchyResults {
		shown = maxCallHierarchyResults
	}

	fmt.Fprintf(&b, "Call hierarchy (%d items):\n", shown)
	for i := 0; i < shown; i++ {
		fmt.Fprintf(&b, "  %s\t%s — %s\n",
			items[i].Kind, items[i].Name, formatLocation(protocol.Location{
				URI:   items[i].URI,
				Range: items[i].SelectionRange,
			}))
	}
	if len(items) > shown {
		fmt.Fprintf(&b, "  ...and %d more items\n", len(items)-shown)
	}
	return truncString(b.String(), maxCallHierarchyBytes)
}

// formatIncomingCalls formats a callHierarchy/incomingCalls result.
func formatIncomingCalls(raw json.RawMessage) string {
	if isEmpty(raw) {
		return "No incoming calls."
	}

	var calls []protocol.CallHierarchyIncomingCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return fmt.Sprintf("Incoming calls: (unexpected format) %s", trunc(raw, maxCallHierarchyBytes))
	}
	if len(calls) == 0 {
		return "No incoming calls."
	}

	var b strings.Builder
	shown := len(calls)
	if shown > maxCallHierarchyResults {
		shown = maxCallHierarchyResults
	}

	fmt.Fprintf(&b, "Incoming calls (%d):\n", shown)
	for i := 0; i < shown; i++ {
		c := calls[i]
		for _, r := range c.FromRanges {
			fmt.Fprintf(&b, "  from %s %s @ %s:%d:%d\n",
				c.From.Kind, c.From.Name, c.From.URI,
				r.Start.Line+1, r.Start.Character+1)
		}
	}
	if len(calls) > shown {
		fmt.Fprintf(&b, "  ...and %d more callers\n", len(calls)-shown)
	}
	return truncString(b.String(), maxCallHierarchyBytes)
}

// formatOutgoingCalls formats a callHierarchy/outgoingCalls result.
func formatOutgoingCalls(raw json.RawMessage) string {
	if isEmpty(raw) {
		return "No outgoing calls."
	}

	var calls []protocol.CallHierarchyOutgoingCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return fmt.Sprintf("Outgoing calls: (unexpected format) %s", trunc(raw, maxCallHierarchyBytes))
	}
	if len(calls) == 0 {
		return "No outgoing calls."
	}

	var b strings.Builder
	shown := len(calls)
	if shown > maxCallHierarchyResults {
		shown = maxCallHierarchyResults
	}

	fmt.Fprintf(&b, "Outgoing calls (%d):\n", shown)
	for i := 0; i < shown; i++ {
		c := calls[i]
		for _, r := range c.FromRanges {
			fmt.Fprintf(&b, "  to %s %s @ %s:%d:%d\n",
				c.To.Kind, c.To.Name, c.To.URI,
				r.Start.Line+1, r.Start.Character+1)
		}
	}
	if len(calls) > shown {
		fmt.Fprintf(&b, "  ...and %d more callees\n", len(calls)-shown)
	}
	return truncString(b.String(), maxCallHierarchyBytes)
}

// ── helpers ────────────────────────────────────────────────────────────

func formatLocation(loc protocol.Location) string {
	rng := fmt.Sprintf("%d:%d",
		loc.Range.Start.Line+1, loc.Range.Start.Character+1)
	return fmt.Sprintf("%s:%s", loc.URI, rng)
}

func formatMarkup(mc protocol.MarkupContent) string {
	if mc.Value == "" {
		return ""
	}
	return mc.Value
}

func isEmpty(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	s := strings.TrimSpace(string(raw))
	return s == "" || s == "null" || s == "[]"
}

func trunc(raw json.RawMessage, maxBytes int) string {
	return truncString(string(raw), maxBytes)
}

func truncString(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	// Try to break at a newline.
	cut := s[:maxBytes]
	if idx := strings.LastIndexByte(cut, '\n'); idx > maxBytes/2 {
		cut = cut[:idx]
	}
	return cut + fmt.Sprintf("\n... [truncated at %d bytes]", maxBytes)
}
