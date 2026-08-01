package session

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// The block ledger is the accounting layer the context engine plans
// against: one annotated entry per model-visible chunk of the prompt.
//
// It is DERIVED, never threaded. BuildLedger recomputes it from the
// message list on every call, because Session.Messages is replaced
// wholesale by compaction (MicroCompact / FullCompact) and by /rewind —
// an incrementally-maintained ledger would desync from history on the
// first of those and stay wrong forever. Rebuilding costs one pass over
// a slice we already hold in memory.
//
// The only ledger state that genuinely persists is the pin set, and it
// is keyed on tool-result IDs rather than message indices for exactly
// the same reason: indices move, IDs don't.

// Category classifies a block by the kind of context weight it carries.
// The /context overlay groups by these, and the prune rung applies its
// size floor per category.
type Category string

const (
	// CategorySystem is the system prompt. It lives on the LLM client
	// (llm.WithSystem) rather than in Session.Messages, so it enters the
	// ledger only when the caller supplies it — but it is real prompt
	// weight on every single request and a breakdown that omitted it
	// would understate the largest fixed cost in the session.
	CategorySystem Category = "system"
	// CategoryUser covers operator prompts and the synthetic user
	// messages the drains inject (wakeups, daemon signals, diagnostics).
	CategoryUser Category = "user"
	// CategoryAssistant covers model text, thinking traces, and the
	// serialized arguments of the tool calls it requested.
	CategoryAssistant Category = "assistant"
	// CategoryFile is a tool result from a file-reading tool. Split out
	// from CategoryTool because it is both the heaviest category in
	// practice and the one with the cheapest recovery — re-reading a
	// file is one call, re-running a test suite is minutes.
	CategoryFile Category = "file"
	// CategoryTool is every other tool result.
	CategoryTool Category = "tool"
)

// fileReadTools are the tools whose results a tombstone can tell the
// model to recover by simply reading the file again. Only `read` today —
// it handles notebooks and PDFs too, so there is no separate reader to
// list here.
var fileReadTools = map[string]bool{
	string(tools.READ_FILE): true,
}

// Block is one accounted unit of context weight.
type Block struct {
	// Index is the position in Session.Messages this block came from, or
	// -1 for the system prompt (which is not in the message list).
	// Several blocks can share an Index: one RoleTool message carries a
	// result per parallel tool call.
	Index int
	// Category classifies the weight for the /context breakdown.
	Category Category
	// Bytes is the model-visible size of the block's content. For image
	// results this counts the base64 payload, which is what actually
	// crosses the wire — not the rendered "[Image: …]" placeholder.
	Bytes int
	// Turn is the 1-based user-turn ordinal this block belongs to.
	// Incremented at each RoleUser message.
	Turn int
	// ToolID is the provider's tool-call id. Non-empty only for tool
	// results, where it doubles as the pin key and the prune-plan key.
	ToolID string
	// ToolName is the tool that produced this result ("read", "bash", …).
	ToolName string
	// Label is the block's human-facing subject: a file path for
	// read-family results, the leading command verb for bash, the
	// pattern for grep/glob. Empty when the tool has no obvious subject.
	Label string
	// Pinned blocks are exempt from every rung of the ladder and are
	// re-injected verbatim when compaction rewrites history.
	Pinned bool
	// Pruned is true when this block's content has already been replaced
	// by a tombstone — the prune rung uses it to avoid re-planning work
	// it has already done.
	Pruned bool
	// IsError marks a failed tool result. Never pruned: an error is the
	// one result whose content cannot be recovered by re-running, since
	// re-running may well succeed the second time and erase the evidence.
	IsError bool
}

// Recoverable reports whether this block is a candidate for pruning at
// all — i.e. whether losing its content is a recoverable event.
func (b Block) Recoverable() bool {
	return (b.Category == CategoryFile || b.Category == CategoryTool) &&
		!b.Pinned && !b.Pruned && !b.IsError
}

// Ledger is the full accounting of one session's context weight.
type Ledger struct {
	Blocks []Block
	// Turns is the number of user turns seen, i.e. the ordinal of the
	// most recent one. The prune rung's recency window is measured
	// against it.
	Turns int
}

// Bytes returns the ledger's total model-visible size.
func (l Ledger) Bytes() int {
	n := 0
	for _, b := range l.Blocks {
		n += b.Bytes
	}
	return n
}

// ByCategory returns per-category byte totals. Categories with no blocks
// are absent rather than zero-valued.
func (l Ledger) ByCategory() map[Category]int {
	out := make(map[Category]int, 5)
	for _, b := range l.Blocks {
		out[b.Category] += b.Bytes
	}
	return out
}

// Heaviest returns the n largest blocks, descending. Ties keep ledger
// order, so the /context overlay is stable between renders.
func (l Ledger) Heaviest(n int) []Block {
	out := make([]Block, len(l.Blocks))
	copy(out, l.Blocks)
	// Insertion sort by descending Bytes: n is small, the slice is
	// nearly always under a few hundred entries, and this keeps the
	// tie order stable without pulling in sort.SliceStable's closure.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Bytes > out[j-1].Bytes; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	if n > 0 && len(out) > n {
		out = out[:n]
	}
	return out
}

// BuildLedger annotates msgs into a ledger. systemPrompt may be empty
// (subagents and tests); pins may be nil.
//
// Tool names and labels come from the ToolCalls on the preceding
// assistant message — a RoleTool message carries results keyed only by
// id, so the correlation has to be built while walking forward.
func BuildLedger(msgs []llm.Message, systemPrompt string, pins map[string]struct{}) Ledger {
	l := Ledger{Blocks: make([]Block, 0, len(msgs)+1)}

	if systemPrompt != "" {
		l.Blocks = append(l.Blocks, Block{
			Index:    -1,
			Category: CategorySystem,
			Bytes:    len(systemPrompt),
			Label:    "system prompt",
		})
	}

	names := make(map[string]string)
	labels := make(map[string]string)
	turn := 0

	for i, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			turn++
			l.Blocks = append(l.Blocks, Block{
				Index:    i,
				Category: CategoryUser,
				Bytes:    len(m.Content),
				Turn:     turn,
			})
		case llm.RoleAssistant:
			n := len(m.Content) + len(m.Thinking)
			for _, tc := range m.ToolCalls {
				if tc == nil {
					continue
				}
				n += len(tc.Input)
				names[tc.ID] = tc.Name
				labels[tc.ID] = callLabel(tc.Name, tc.Input)
			}
			l.Blocks = append(l.Blocks, Block{
				Index:    i,
				Category: CategoryAssistant,
				Bytes:    n,
				Turn:     turn,
			})
		case llm.RoleTool:
			for _, tr := range m.ToolResults {
				if tr == nil {
					continue
				}
				name := names[tr.ID]
				cat := CategoryTool
				if fileReadTools[name] {
					cat = CategoryFile
				}
				_, pinned := pins[tr.ID]
				l.Blocks = append(l.Blocks, Block{
					Index:    i,
					Category: cat,
					Bytes:    resultBytes(tr),
					Turn:     turn,
					ToolID:   tr.ID,
					ToolName: name,
					Label:    labels[tr.ID],
					Pinned:   pinned,
					Pruned:   IsTombstone(tr.Content),
					IsError:  tr.IsError,
				})
			}
		case llm.RoleSystem:
			l.Blocks = append(l.Blocks, Block{
				Index:    i,
				Category: CategorySystem,
				Bytes:    len(m.Content),
				Turn:     turn,
			})
		}
	}

	l.Turns = turn
	return l
}

// resultBytes is a tool result's true on-wire weight. Content is the
// common case; ContentBlocks carry multimodal payloads where the base64
// image data — not its rendered placeholder — is what the provider bills.
func resultBytes(tr *llm.ToolResult) int {
	n := len(tr.Content)
	for _, cb := range tr.ContentBlocks {
		switch cb.Type {
		case tools.ContentBlockText:
			n += len(cb.Text)
		case tools.ContentBlockImage:
			if cb.Image != nil {
				n += len(cb.Image.Base64Data)
			}
		}
	}
	return n
}

// callLabel extracts the human-facing subject from a tool call's raw
// arguments. Best-effort by design: an unrecognized tool or malformed
// JSON yields an empty label, and every consumer falls back to the tool
// name. Kept in sync with the arguments the tombstone recovery text
// references.
func callLabel(name string, input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var args map[string]any
	if err := json.Unmarshal(input, &args); err != nil {
		return ""
	}
	str := func(k string) string {
		s, _ := args[k].(string)
		return s
	}
	switch name {
	case string(tools.READ_FILE), string(tools.EDIT_FILE), string(tools.WRITE_FILE), string(tools.NOTEBOOK_EDIT):
		if p := str("file_path"); p != "" {
			return filepath.Base(p)
		}
	case string(tools.BASH):
		return firstWord(str("command"))
	case string(tools.GREP), string(tools.GLOB):
		if p := str("pattern"); p != "" {
			return p
		}
	}
	// Unknown tool: try the conventional argument names in order, so a
	// new tool gets a usable label without touching this function.
	for _, k := range []string{"file_path", "path", "pattern", "query", "url", "name", "command"} {
		if v := str(k); v != "" {
			return firstWord(v)
		}
	}
	return ""
}

// firstWord returns the leading token of s, truncated so a pathological
// argument can't blow out an overlay row. Leading VAR=value assignments
// are skipped so `FOO=1 go test` labels as "go".
func firstWord(s string) string {
	for _, f := range strings.Fields(s) {
		if strings.Contains(f, "=") && !strings.ContainsAny(f, "/\\") {
			continue
		}
		if len(f) > 40 {
			return f[:40]
		}
		return f
	}
	return ""
}
