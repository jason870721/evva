package agent

import (
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/ui"
)

// ContextReport maps the internal block ledger onto the public
// ui.ContextReport DTO.
//
// The translation exists so pkg/ui stays implementable from outside the
// module — session.Ledger is an internal type, and a downstream UI must
// be able to satisfy ui.Controller without reaching into internal/. The
// publicOnlyController compile gate in pkg/ui enforces exactly that.
func (a *Agent) ContextReport(topN int) ui.ContextReport {
	// The system prompt is real weight on every request but lives on the
	// LLM client, not in Messages — pass it in explicitly or the overlay
	// understates the largest fixed cost in the session.
	ledger := a.session.Ledger(a.systemPromptForLedger())

	cats := make(map[string]int, 5)
	for cat, n := range ledger.ByCategory() {
		cats[string(cat)] = n
	}

	heaviest := ledger.Heaviest(topN)
	blocks := make([]ui.ContextBlock, 0, len(heaviest))
	for _, b := range heaviest {
		blocks = append(blocks, ui.ContextBlock{
			ToolID:   b.ToolID,
			Category: string(b.Category),
			ToolName: b.ToolName,
			Label:    b.Label,
			Bytes:    b.Bytes,
			Turn:     b.Turn,
			Pinned:   b.Pinned,
			Pruned:   b.Pruned,
			IsError:  b.IsError,
		})
	}

	return ui.ContextReport{
		Blocks:      blocks,
		Categories:  cats,
		TotalBytes:  ledger.Bytes(),
		Turns:       ledger.Turns,
		UsedTokens:  a.session.LastTurnInputTokens(),
		LimitTokens: constant.MODEL_CONTEXT_SIZE[constant.Model(a.llm.Model())],
	}
}

// TogglePinnedBlock flips a pin from the UI goroutine.
func (a *Agent) TogglePinnedBlock(toolID string) bool {
	pinned := a.session.TogglePin(toolID)
	a.logger.Info("context.pin", "tool_id", toolID, "pinned", pinned)
	return pinned
}

// systemPromptForLedger reads the active persona's prompt. Profile is a
// value type, so a zero profile yields an empty prompt and the overlay
// simply omits the system row rather than reporting a wrong one.
func (a *Agent) systemPromptForLedger() string {
	return a.profile.SystemPrompt
}

// compile-time proof the ledger's category strings and the DTO's
// documented values do not drift apart.
var _ = [...]string{
	string(session.CategorySystem),
	string(session.CategoryUser),
	string(session.CategoryAssistant),
	string(session.CategoryFile),
	string(session.CategoryTool),
}
