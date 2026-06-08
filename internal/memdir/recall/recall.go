// Package recall finds the memory files relevant to a user query via a cheap
// LLM side-query, so only the few memories that matter get pulled into a turn
// (the rest stay on disk, surfaced on demand).
//
// It lives apart from the base internal/memdir package because it needs
// llm.Client — internal/memdir is stdlib-only by charter. Downstream SDK hosts
// inherit the agent's recall behavior without importing this package.
package recall

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// SystemPrompt is the selection instruction for the side-query model. Ported
// from ref/src/memdir/findRelevantMemories.ts:18 — it encodes hard-won
// discipline ("be selective", and the subtle "don't re-surface usage docs for
// tools already in use, DO surface gotchas about them"). The closing JSON
// instruction replaces ref's provider-side output_format schema, since not every
// evva provider supports JSON-schema-constrained output (PRD Task 3).
const SystemPrompt = `You are selecting memories that will be useful to evva as it processes a user's query. You will be given the user's query and a list of available memory files with their filenames and descriptions.

Return a list of filenames for the memories that will clearly be useful to evva as it processes the user's query (up to 5). Only include memories that you are certain will be helpful based on their name and description.
- If you are unsure if a memory will be useful in processing the user's query, then do not include it in your list. Be selective and discerning.
- If there are no memories in the list that would clearly be useful, feel free to return an empty list.
- If a list of recently-used tools is provided, do not select memories that are usage reference or API documentation for those tools (evva is already exercising them). DO still select memories containing warnings, gotchas, or known issues about those tools — active use is exactly when those matter.

Respond with ONLY a JSON object of the form {"selected_memories": ["filename1.md", "filename2.md"]} and nothing else.`

// maxSelected bounds how many memories one turn surfaces (ref's "up to 5").
const maxSelected = 5

// recallMaxTokens caps the side-query output — a short filename list needs few.
const recallMaxTokens = 256

// FindRelevant scans the memory dir, asks the side-query model to select the
// memories whose name/description are clearly useful for query (≤5), and
// returns their headers newest-first. MEMORY.md is never a candidate (the
// scanner excludes it; it already rides the static prompt). alreadySurfaced —
// keyed on the dir-relative Filename — drops memories the caller already put in
// context this session (prior recall reminders + files the model read
// directly), so the 5-slot budget spends on genuinely fresh files.
//
// client must already be configured for the recall model; model is carried for
// parity with ref / observability — the client binds the actual model, so this
// function does not (and cannot) switch it. Never errors out of band: an empty
// dir, a model failure, a context cancel, or a parse failure returns nil, so a
// recall hiccup degrades to "no extra memories this turn"
// (findRelevantMemories.ts catch parity).
func FindRelevant(
	ctx context.Context,
	client llm.Client,
	model constant.Model,
	query string,
	dir string,
	recentTools []string,
	alreadySurfaced map[string]bool,
) []memdir.MemoryHeader {
	if client == nil || dir == "" {
		return nil
	}
	headers := scanFresh(dir, alreadySurfaced)
	if len(headers) == 0 {
		return nil
	}

	valid := make(map[string]bool, len(headers))
	for _, h := range headers {
		valid[h.Filename] = true
	}

	// selected ⊆ valid is the real safety net — the model can hallucinate
	// filenames, so anything not in the scanned set is discarded here.
	selected := selectRelevant(ctx, client, query, headers, recentTools)
	keep := make(map[string]bool, len(selected))
	for _, name := range selected {
		if valid[name] {
			keep[name] = true
		}
	}
	if len(keep) == 0 {
		return nil
	}

	var out []memdir.MemoryHeader
	for _, h := range headers { // headers are already newest-first + unique
		if keep[h.Filename] {
			out = append(out, h)
			if len(out) >= maxSelected {
				break
			}
		}
	}
	return out
}

// scanFresh scans dir and drops any header whose Filename is in alreadySurfaced.
func scanFresh(dir string, alreadySurfaced map[string]bool) []memdir.MemoryHeader {
	all := memdir.ScanMemoryFiles(dir)
	if len(alreadySurfaced) == 0 {
		return all
	}
	out := all[:0] // reuse backing array; all is local
	for _, h := range all {
		if alreadySurfaced[h.Filename] {
			continue
		}
		out = append(out, h)
	}
	return out
}

// selectRelevant runs the one side-query completion and returns the model's
// raw selection (unfiltered). Any failure returns nil — the caller treats that
// as "no extra memories this turn."
func selectRelevant(ctx context.Context, client llm.Client, query string, headers []memdir.MemoryHeader, recentTools []string) []string {
	var sb strings.Builder
	sb.WriteString("Query: ")
	sb.WriteString(query)
	sb.WriteString("\n\nAvailable memories:\n")
	sb.WriteString(memdir.FormatManifest(headers))
	if len(recentTools) > 0 {
		// Active tool use is exactly when surfacing that tool's usage docs is
		// noise (the conversation already has working usage). The system prompt
		// tells the model to skip docs but keep gotchas for these.
		sb.WriteString("\n\nRecently used tools: ")
		sb.WriteString(strings.Join(recentTools, ", "))
	}

	// The client is dedicated to recall, so pinning the selection system prompt
	// + token cap on it is safe (it never carries the main loop's prompt).
	client.Apply(llm.WithSystem(SystemPrompt), llm.WithMaxTokens(recallMaxTokens))
	resp, err := client.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: sb.String()}}, nil)
	if err != nil {
		return nil
	}
	return parseSelected(resp.Content)
}

// parseSelected extracts the selected_memories array, tolerating providers that
// wrap the JSON in prose or a ```json fence (not every provider honors a strict
// output format). Returns nil on any parse failure.
func parseSelected(content string) []string {
	var out struct {
		SelectedMemories []string `json:"selected_memories"`
	}
	raw := strings.TrimSpace(content)
	if json.Unmarshal([]byte(raw), &out) == nil {
		return out.SelectedMemories
	}
	if i := strings.IndexByte(raw, '{'); i >= 0 {
		if j := strings.LastIndexByte(raw, '}'); j > i {
			if json.Unmarshal([]byte(raw[i:j+1]), &out) == nil {
				return out.SelectedMemories
			}
		}
	}
	return nil
}
