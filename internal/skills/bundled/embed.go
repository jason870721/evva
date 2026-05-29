package bundled

import (
	"embed"
	"fmt"
)

//go:embed content/*/SKILL.md
var contentFS embed.FS

// bundledNames is the canonical list of skill names this package owns.
// Iteration order here only affects Register order; the registry sorts by
// name for display, so visible order does not depend on this slice.
//
// To add a bundled skill:
//  1. Create content/<name>/SKILL.md (first line: `# <name> <description>`).
//  2. Append <name> here.
//  3. Add a test asserting it embeds and parses (see bundled_test.go).
var bundledNames = []string{
	"commit",
	"review",
	"security-review",
	"simplify",
	"setup-hooks",
	"setup-mcp",
}

// readBundled returns the raw SKILL.md content for a bundled skill. It
// returns an error (not a panic) on a missing file so a typo in bundledNames
// surfaces as a Register warning rather than a crash.
func readBundled(name string) (string, error) {
	path := "content/" + name + "/SKILL.md"
	b, err := contentFS.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("embed: %s: %w", path, err)
	}
	return string(b), nil
}
