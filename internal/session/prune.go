package session

import (
	"fmt"
	"strings"

	"github.com/johnny1110/evva/pkg/llm"
)

// Pruning is the cheapest rung of the context ladder: replace the body of
// a large, old, recoverable tool result with a one-line tombstone that
// tells the model exactly what was removed and how to get it back.
//
// The tombstone is a MODEL-FACING CONTRACT, not a log line. It is what
// makes pruning safe at temperature: the model is never left guessing
// whether content existed, and never has to infer the recovery action.
// A bare placeholder ("[elided]") fails both tests — it reads as "this
// was never important" and offers no way back.
//
// Three things are never pruned, in order of how badly pruning them
// would hurt:
//
//  1. Error results. Re-running a failed command may succeed the second
//     time and erase the evidence, so an error's text is the one tool
//     output that is genuinely NOT recoverable.
//  2. Pinned blocks. The operator said keep it.
//  3. Recent results — both a turn window and a count floor, so neither
//     a long quiet stretch nor one mega-turn of parallel calls can push
//     live working material out from under the model.

const (
	// tombstoneOpen prefixes every tombstone. Doubles as the detector:
	// a rebuilt ledger recognizes already-pruned blocks by this prefix
	// rather than by carrying a side-table across the rebuild.
	tombstoneOpen = "[pruned to save context:"

	// defaultPruneMinBytes is the size floor. Below this the tombstone
	// text is a meaningful fraction of the content it replaces, so the
	// swap costs clarity and buys nothing.
	defaultPruneMinBytes = 2048

	// defaultPruneKeepTurns protects the last N user turns outright.
	// Whatever the model is working on right now lives in this window.
	defaultPruneKeepTurns = 3

	// defaultPruneKeepResults is a floor on live tool results kept
	// verbatim regardless of turn, so a single turn that fans out 40
	// parallel calls still leaves the model something to stand on.
	defaultPruneKeepResults = 12
)

// PrunePolicy holds the tunables for one prune pass.
type PrunePolicy struct {
	// MinBytes is the size floor; results smaller are never pruned.
	MinBytes int
	// KeepRecentTurns protects blocks from the last N user turns.
	KeepRecentTurns int
	// KeepRecentResults protects the trailing N live tool results
	// regardless of which turn they belong to.
	KeepRecentResults int
}

// DefaultPrunePolicy returns the shipped defaults.
func DefaultPrunePolicy() PrunePolicy {
	return PrunePolicy{
		MinBytes:          defaultPruneMinBytes,
		KeepRecentTurns:   defaultPruneKeepTurns,
		KeepRecentResults: defaultPruneKeepResults,
	}
}

// normalized fills in zero fields with the defaults so a partially
// configured policy can't accidentally prune everything.
func (p PrunePolicy) normalized() PrunePolicy {
	d := DefaultPrunePolicy()
	if p.MinBytes <= 0 {
		p.MinBytes = d.MinBytes
	}
	if p.KeepRecentTurns <= 0 {
		p.KeepRecentTurns = d.KeepRecentTurns
	}
	if p.KeepRecentResults <= 0 {
		p.KeepRecentResults = d.KeepRecentResults
	}
	return p
}

// PrunePlan is the pure output of planning: which tool results to
// tombstone and with what text. Applying it is a separate, mechanical
// step so the decision can be tested without a session or an agent.
type PrunePlan struct {
	// Tombstones maps tool-result id → replacement content.
	Tombstones map[string]string
	// Bytes is the NET model-visible saving: content removed minus
	// tombstone text added.
	Bytes int
}

// Empty reports whether the plan would change nothing.
func (p PrunePlan) Empty() bool { return len(p.Tombstones) == 0 }

// Count is how many results the plan tombstones.
func (p PrunePlan) Count() int { return len(p.Tombstones) }

// IsTombstone reports whether content is a tombstone this package wrote.
// Used by BuildLedger to mark already-pruned blocks so a second pass
// neither re-plans them nor counts them as live recent results.
func IsTombstone(content string) bool {
	return strings.HasPrefix(content, tombstoneOpen)
}

// PlanPrune decides which blocks to tombstone under pol. Pure: same
// ledger and policy in, same plan out.
func PlanPrune(l Ledger, pol PrunePolicy) PrunePlan {
	pol = pol.normalized()
	plan := PrunePlan{Tombstones: make(map[string]string)}

	// The count floor walks backwards over live tool results. Already
	// tombstoned blocks don't consume the window — they are not content
	// the model can still stand on, so counting them would let the floor
	// silently erode as the session ages.
	protected := make(map[string]struct{}, pol.KeepRecentResults)
	for i := len(l.Blocks) - 1; i >= 0 && len(protected) < pol.KeepRecentResults; i-- {
		b := l.Blocks[i]
		if b.ToolID == "" || b.Pruned {
			continue
		}
		protected[b.ToolID] = struct{}{}
	}

	oldestProtectedTurn := l.Turns - pol.KeepRecentTurns

	for _, b := range l.Blocks {
		if !b.Recoverable() || b.Bytes < pol.MinBytes {
			continue
		}
		if b.Turn > oldestProtectedTurn {
			continue
		}
		if _, ok := protected[b.ToolID]; ok {
			continue
		}
		ts := Tombstone(b)
		saved := b.Bytes - len(ts)
		if saved <= 0 {
			continue
		}
		plan.Tombstones[b.ToolID] = ts
		plan.Bytes += saved
	}
	return plan
}

// Tombstone renders the model-facing replacement text for one block.
func Tombstone(b Block) string {
	subject := b.ToolName
	if subject == "" {
		subject = "tool"
	}
	if b.Label != "" {
		subject += " " + b.Label
	}
	return fmt.Sprintf("%s %s result from turn %d, %s — %s]",
		tombstoneOpen, subject, b.Turn, humanBytes(b.Bytes), recoveryHint(b))
}

// recoveryHint is the imperative half of the tombstone: the exact action
// that brings the content back. Phrased as an instruction because that
// is how the model will read it.
func recoveryHint(b Block) string {
	switch b.ToolName {
	case "read":
		if b.Label != "" {
			return "read the file again if you still need it"
		}
		return "read it again if you still need it"
	case "bash":
		return "re-run the command if you still need the output"
	case "grep", "glob", "tree":
		return "re-run the search if you still need the matches"
	case "":
		return "re-run the tool call if you still need the result"
	default:
		return "call " + b.ToolName + " again if you still need the result"
	}
}

// humanBytes formats a size the way the tombstone's reader — the model
// weighing whether recovery is worth it — needs to see it.
func humanBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// ApplyPrune rewrites msgs with the plan's tombstones in place. The
// returned slice is fresh; msgs is not mutated, because the caller may
// still be holding it for a snapshot write.
//
// ToolResult.ID and IsError survive so tool_use/tool_result pairing stays
// well-formed. ContentBlocks are dropped along with Content — for an
// image result the base64 payload IS the weight, and keeping it would
// make the tombstone a lie.
func ApplyPrune(msgs []llm.Message, plan PrunePlan) []llm.Message {
	if plan.Empty() {
		return msgs
	}
	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		if m.Role != llm.RoleTool {
			out[i] = m
			continue
		}
		hit := false
		for _, tr := range m.ToolResults {
			if tr == nil {
				continue
			}
			if _, ok := plan.Tombstones[tr.ID]; ok {
				hit = true
				break
			}
		}
		if !hit {
			out[i] = m
			continue
		}
		results := make([]*llm.ToolResult, len(m.ToolResults))
		for j, tr := range m.ToolResults {
			if tr == nil {
				continue
			}
			ts, ok := plan.Tombstones[tr.ID]
			if !ok {
				results[j] = tr
				continue
			}
			results[j] = &llm.ToolResult{ID: tr.ID, Content: ts, IsError: tr.IsError}
		}
		out[i] = llm.Message{Role: llm.RoleTool, ToolResults: results}
	}
	return out
}

// pinnedHeader introduces the re-injected pin block after a compaction
// rewrite. Addressed to the model, since it is the only reader.
const pinnedHeader = "[PINNED CONTEXT — the operator pinned these blocks; they survive compaction verbatim. " +
	"Treat them as current.]"

// RenderPinned returns the pinned blocks' content as one text payload,
// or "" when nothing is pinned.
//
// Pins are re-injected as USER text rather than as tool results: a
// RoleTool message is only well-formed when a matching assistant
// tool_use precedes it, and compaction has just deleted those. Quoting
// the content sidesteps the pairing rule entirely, at the cost of the
// model seeing it as narration rather than as a tool result — which is
// the correct reading after the surrounding conversation is gone.
func RenderPinned(msgs []llm.Message, l Ledger) string {
	if len(l.Blocks) == 0 {
		return ""
	}
	byID := make(map[string]*llm.ToolResult)
	for _, m := range msgs {
		if m.Role != llm.RoleTool {
			continue
		}
		for _, tr := range m.ToolResults {
			if tr != nil {
				byID[tr.ID] = tr
			}
		}
	}

	var b strings.Builder
	for _, blk := range l.Blocks {
		if !blk.Pinned || blk.ToolID == "" {
			continue
		}
		tr, ok := byID[blk.ToolID]
		if !ok {
			continue
		}
		content := tr.Content
		if content == "" && len(tr.ContentBlocks) > 0 {
			content = llm.RenderContentBlocksAsText(tr.ContentBlocks)
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		if b.Len() == 0 {
			b.WriteString(pinnedHeader)
			b.WriteString("\n")
		}
		label := blk.ToolName
		if label == "" {
			label = "tool"
		}
		if blk.Label != "" {
			label += ": " + blk.Label
		}
		fmt.Fprintf(&b, "\n── %s (turn %d) ──\n%s\n", label, blk.Turn, content)
	}
	return b.String()
}
