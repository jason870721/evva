package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/johnny1110/evva/internal/tools"
)

// DeferredLookupFn is the late-binding shape NewToolSearch accepts; pass a
// method value bound to whatever owns the lookup (typically toolset.ToolState).
type DeferredLookupFn func() DeferredLookup

// ToolSearchTool exposes deferred-tool metadata to the model.
//
// It does NOT build or "load" the tool — the only side effect of calling
// TOOL_SEARCH is returning a JSON block of <function> entries the model
// can read to learn each tool's name, description, and input schema. The
// actual build happens later, when the model invokes the tool for real
// and the agent's loop calls Agent.ResolveTool.
type ToolSearchTool struct {
	lookup DeferredLookupFn
}

// NewToolSearch constructs a ToolSearchTool that reads its lookup at
// Execute time. lookup may be nil (yields a clear runtime error); it may
// also return nil (same outcome).
func NewToolSearch(lookup DeferredLookupFn) *ToolSearchTool {
	return &ToolSearchTool{lookup: lookup}
}

func (t *ToolSearchTool) Name() string { return string(tools.TOOL_SEARCH) }

func (t *ToolSearchTool) Description() string {
	return "Fetch the full schema definitions for deferred tools so they can be called.\n\n" +
		"Deferred tools are advertised by name in the system prompt; their schemas are loaded on demand.\n" +
		"Query forms:\n" +
		"- \"select:Foo,Bar\" — fetch these exact tool names.\n" +
		"- \"notebook jupyter\" — fuzzy match over tags (exact/substring/typo/subsequence) plus substring on name and description.\n" +
		"- \"+slack send\" — require the +-prefixed term (fuzzy-on-tags or substring elsewhere); rank the rest.\n\n" +
		"Returns a <functions> block with one <function>{...}</function> entry per matched tool. " +
		"After TOOL_SEARCH the model may call the tool directly; the agent builds it on first use."
}

func (t *ToolSearchTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["query"],
		"properties":{
			"query":{"type":"string","description":"Query: \"select:<name>[,<name>...]\" for exact names, or whitespace-separated keywords (prefix with \"+\" to require the term)."},
			"max_results":{"type":"integer","minimum":1,"default":5,"description":"Cap the number of returned schemas. Default 5."}
		}
	}`)
}

type toolSearchInput struct {
	Query      string `json:"query"`
	MaxResults int    `json:"max_results"`
}

func (t *ToolSearchTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in toolSearchInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("tool_search: decode: %v", err)}, nil
	}
	if strings.TrimSpace(in.Query) == "" {
		return tools.Result{IsError: true, Content: "tool_search: query is required"}, nil
	}
	if t.lookup == nil {
		return tools.Result{IsError: true, Content: "tool_search: no deferred-lookup configured"}, nil
	}
	lookup := t.lookup()
	if lookup == nil {
		return tools.Result{IsError: true, Content: "tool_search: no deferred-lookup configured (root agent only)"}, nil
	}
	max := in.MaxResults
	if max <= 0 {
		max = 5
	}

	descriptors, err := allDescriptors(lookup)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("tool_search: enumerate: %v", err)}, nil
	}
	if len(descriptors) == 0 {
		return tools.Result{Content: "(no deferred tools in this profile)"}, nil
	}

	matches := search(in.Query, max, descriptors)
	if len(matches) == 0 {
		return tools.Result{Content: fmt.Sprintf("(no matches for %q)", in.Query)}, nil
	}
	return tools.Result{Content: formatFunctions(matches)}, nil
}

// allDescriptors fetches every deferred descriptor through the lookup and
// returns them sorted by name for stable output. Per-tool errors are
// surfaced as a single combined error — the model will rarely care about
// per-name failures, and aborting the whole search is friendlier than
// silently dropping one name.
func allDescriptors(lookup DeferredLookup) ([]tools.Descriptor, error) {
	names := lookup.DeferredNames()
	out := make([]tools.Descriptor, 0, len(names))
	var firstErr error
	for _, n := range names {
		d, err := lookup.Describe(n)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if firstErr != nil && len(out) == 0 {
		return nil, firstErr
	}
	return out, nil
}

// search ranks descriptors against query under the three documented forms.
// Returns the top max matches (or fewer if not enough matched).
func search(query string, max int, all []tools.Descriptor) []tools.Descriptor {
	q := strings.ToLower(strings.TrimSpace(query))

	// 1. select: form — exact name lookup, preserves the user's order.
	if rest, ok := strings.CutPrefix(q, "select:"); ok {
		return selectByName(rest, all)
	}

	// 2. Tokenize. "+keyword" tokens are required filters; the rest contribute to score.
	var required, optional []string
	for _, tok := range strings.FieldsFunc(q, func(r rune) bool { return r == ' ' || r == ',' || r == '\t' }) {
		if tok == "" {
			continue
		}
		if strings.HasPrefix(tok, "+") {
			required = append(required, tok[1:])
		} else {
			optional = append(optional, tok)
		}
	}
	if len(required) == 0 && len(optional) == 0 {
		return nil
	}

	type scored struct {
		d     tools.Descriptor
		score int
	}
	var ranked []scored
	for _, d := range all {
		// All required tokens must appear somewhere.
		ok := true
		for _, r := range required {
			if !hits(d, r) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}

		s := 0
		for _, tok := range required {
			s += scoreTok(d, tok)
		}
		for _, tok := range optional {
			s += scoreTok(d, tok)
		}
		// If the query was nothing but required tokens we already filtered
		// to matches; keep them even if optional bonus is 0.
		if s == 0 && len(optional) > 0 {
			continue
		}
		ranked = append(ranked, scored{d: d, score: s})
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].d.Name < ranked[j].d.Name
	})
	if len(ranked) > max {
		ranked = ranked[:max]
	}
	out := make([]tools.Descriptor, len(ranked))
	for i, s := range ranked {
		out[i] = s.d
	}
	return out
}

// selectByName implements "select:a,b,c" — exact (case-insensitive) name
// lookup; unknown names are silently dropped (the result list is the
// authoritative signal).
func selectByName(list string, all []tools.Descriptor) []tools.Descriptor {
	wanted := strings.Split(list, ",")
	out := make([]tools.Descriptor, 0, len(wanted))
	for _, w := range wanted {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		for _, d := range all {
			if strings.EqualFold(d.Name, w) {
				out = append(out, d)
				break
			}
		}
	}
	return out
}

// scoreTok grants weighted credit for a token. Tags use the layered fuzzy
// match (exact +4, substring +2, single-typo +2, subsequence/two-typo +1 —
// see fuzzyTagScore); name and description fall back to substring (+1 each)
// since they're long-form text where fuzzy matching would over-match.
func scoreTok(d tools.Descriptor, tok string) int {
	s := fuzzyTagScore(d.Tags, tok)
	if strings.Contains(strings.ToLower(d.Name), tok) {
		s++
	}
	if strings.Contains(strings.ToLower(d.Description), tok) {
		s++
	}
	return s
}

// hits is the binary version of scoreTok — does this token appear at all?
// Tags use fuzzy match (so required +tokens tolerate typos); name and
// description use plain substring.
func hits(d tools.Descriptor, tok string) bool {
	if strings.Contains(strings.ToLower(d.Name), tok) {
		return true
	}
	if strings.Contains(strings.ToLower(d.Description), tok) {
		return true
	}
	return fuzzyTagHit(d.Tags, tok)
}

// funcEntry mirrors the Claude Code <function> JSON shape so models that
// know that format can parse the output directly.
type funcEntry struct {
	Description string          `json:"description"`
	Name        string          `json:"name"`
	Parameters  json.RawMessage `json:"parameters"`
}

// formatFunctions renders the matched descriptors as a <functions> block.
// Each match becomes one <function>{...}</function> line; the surrounding
// envelope matches the convention models trained on Claude Code know.
func formatFunctions(matches []tools.Descriptor) string {
	var b strings.Builder
	b.WriteString("<functions>\n")
	for _, d := range matches {
		entry := funcEntry{
			Description: d.Description,
			Name:        d.Name,
			Parameters:  d.Schema,
		}
		raw, err := json.Marshal(entry)
		if err != nil {
			fmt.Fprintf(&b, "<function>{\"name\":%q,\"error\":%q}</function>\n", d.Name, err.Error())
			continue
		}
		fmt.Fprintf(&b, "<function>%s</function>\n", raw)
	}
	b.WriteString("</functions>")
	return b.String()
}
