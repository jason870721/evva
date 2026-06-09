package kits_test

import (
	"fmt"
	"sort"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/kits"
)

// ExampleGeneralPurposeKit shows the canonical "coding agent" tool
// composition. Pass the returned slices into agent.ProfileOptions
// instead of hand-assembling fs/shell/todo/util names by hand.
func ExampleGeneralPurposeKit() {
	active, deferred := kits.GeneralPurposeKit()

	// Slice membership is the contract; the order is stable but doesn't
	// matter to callers — for the Output assertion below we sort + join.
	activeStr := stringify(active)
	deferredStr := stringify(deferred)

	fmt.Println("active:", activeStr)
	fmt.Println("deferred:", deferredStr)
	// Output:
	// active: bash,calc,edit,glob,grep,json_query,read,todo_write,tool_search,tree,write
	// deferred: http_request,web_fetch,web_search
}

// ExampleReadOnlyKit shows the audit/explore variant — no bash, no
// edit/write. Useful for agents that should investigate but never
// mutate the filesystem.
func ExampleReadOnlyKit() {
	got := kits.ReadOnlyKit()
	fmt.Println("read-only:", stringify(got))
	// Output:
	// read-only: glob,grep,json_query,read,tree,web_fetch,web_search
}

// stringify returns the tool names as a sorted comma-joined string.
// Sorting is the trick that lets `// Output:` work against any
// underlying order — kit functions are free to evolve their internal
// composition order without breaking these examples.
func stringify(names []tools.ToolName) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = string(n)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
