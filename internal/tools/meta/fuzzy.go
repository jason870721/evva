package meta

import "strings"

// Tag-based fuzzy match for TOOL_SEARCH. Tags are short, curated keywords
// (see toolset.toolTags) — the LLM may type a near-miss like "noteboook" or
// "jpyter" and we still want to surface the right tool. Names and
// descriptions stay on plain substring match; only tags get fuzzy treatment
// because tags are the field we deliberately picked for discovery.

// fuzzyTagScore returns the additive score for tok against tags. Per-tag we
// take the best of these tiers (lowercase compare; tok must already be lowered):
//
//   - tok == tag                             -> +4 (exact)
//   - strings.Contains(tag, tok)             -> +2 (substring; prior behavior)
//   - len(tok)>=4 && levenshtein(tok,tag)<=1 -> +2 (single typo)
//   - len(tok)>=5 && subsequence(tok,tag)    -> +1 (chars-in-order)
//   - len(tok)>=5 && levenshtein(tok,tag)<=2 -> +1 (two-edit typo)
//
// Length floors exist so short tokens ("go", "ls") don't fuzzy-match
// unrelated tags by accident. Multiple matching tags accumulate, so a query
// that touches several tags (e.g. "notebook jupyter" on NOTEBOOK_EDIT)
// outranks one that touches a single tag.
func fuzzyTagScore(tags []string, tok string) int {
	if tok == "" {
		return 0
	}
	s := 0
	for _, tag := range tags {
		t := strings.ToLower(tag)
		switch {
		case t == tok:
			s += 4
		case strings.Contains(t, tok):
			s += 2
		case len(tok) >= 4 && levenshtein(tok, t) <= 1:
			s += 2
		case len(tok) >= 5 && (isSubsequence(tok, t) || levenshtein(tok, t) <= 2):
			s += 1
		}
	}
	return s
}

// fuzzyTagHit is the binary version used by required "+keyword" filtering.
// Mirrors fuzzyTagScore's tiers — any tier counts as a hit.
func fuzzyTagHit(tags []string, tok string) bool {
	if tok == "" {
		return false
	}
	for _, tag := range tags {
		t := strings.ToLower(tag)
		if t == tok || strings.Contains(t, tok) {
			return true
		}
		if len(tok) >= 4 && levenshtein(tok, t) <= 1 {
			return true
		}
		if len(tok) >= 5 && (isSubsequence(tok, t) || levenshtein(tok, t) <= 2) {
			return true
		}
	}
	return false
}

// isSubsequence reports whether every byte of needle appears in haystack in
// the same order (gaps allowed). Both args must already be lowercase.
func isSubsequence(needle, haystack string) bool {
	if needle == "" {
		return true
	}
	i := 0
	for j := 0; j < len(haystack) && i < len(needle); j++ {
		if needle[i] == haystack[j] {
			i++
		}
	}
	return i == len(needle)
}

// levenshtein is the classic edit-distance (insert, delete, substitute, cost
// 1 each). Single-row DP, O(len(a)*len(b)) time, O(len(b)) space. Both args
// must already be lowercase. Tags are short enough (≤16 chars) that the
// allocation cost is negligible.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	cur := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		cur[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[lb]
}
