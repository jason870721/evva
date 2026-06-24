package repomap

import (
	"sort"

	"github.com/johnny1110/evva/pkg/tools/lsp"
)

// kindRank orders symbol kinds by how load-bearing they are for understanding a
// package's shape: types first (they define the data model), then top-level
// functions, then methods/constructors, then the rest. Lower is higher
// priority. This is a deliberately cheap proxy for centrality — it beats
// alphabetical and costs nothing. Reference-count ranking (one find_references
// per top symbol) is a documented, time-boxed fast-follow, not v1 (PRD §5.3);
// keep this function the single swap point when that lands.
func kindRank(kind string) int {
	switch kind {
	case "Interface":
		return 0
	case "Struct", "Class":
		return 1
	case "Enum":
		return 2
	case "Function":
		return 3
	case "Constructor", "Method":
		return 4
	case "Constant":
		return 5
	default:
		return 6
	}
}

// interestingKinds are the symbol kinds worth putting in a high-level map.
// Locals, fields, parameters and the like surface as Variable/Field/etc and are
// dropped — they're noise at this altitude.
var interestingKinds = map[string]bool{
	"Interface": true, "Struct": true, "Class": true, "Enum": true,
	"Function": true, "Method": true, "Constructor": true, "Constant": true,
}

// keepInteresting returns a new slice with only the map-worthy symbol kinds.
func keepInteresting(syms []lsp.Symbol) []lsp.Symbol {
	out := make([]lsp.Symbol, 0, len(syms))
	for _, s := range syms {
		if interestingKinds[s.Kind] {
			out = append(out, s)
		}
	}
	return out
}

// rankSymbols sorts in place by (kind priority, then declaration line) so the
// budget spends on types and top-level funcs before lower-value members.
func rankSymbols(syms []lsp.Symbol) {
	sort.SliceStable(syms, func(i, j int) bool {
		ri, rj := kindRank(syms[i].Kind), kindRank(syms[j].Kind)
		if ri != rj {
			return ri < rj
		}
		return syms[i].Line < syms[j].Line
	})
}

// estimateTokens approximates a token count as bytes/4. evva ships no shared
// tokenizer (verified across pkg/ and internal/), and chars/4 is the standard
// rough proxy the PRD calls for (Task 2). The budget is an orientation aid, not
// an accounting boundary, so an approximation is fine.
func estimateTokens(s string) int { return len(s) / 4 }
