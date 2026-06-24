package repomap

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// codeExts are the source extensions the fallback scans. Kept small and
// common; the fallback is a coarse orientation aid, not a parser.
var codeExts = map[string]bool{
	".go": true, ".py": true, ".js": true, ".jsx": true, ".ts": true, ".tsx": true,
	".rs": true, ".java": true, ".rb": true, ".c": true, ".h": true, ".cc": true,
	".cpp": true, ".cs": true, ".kt": true, ".swift": true, ".php": true, ".scala": true,
}

// skipDirs are directories the walk never descends into — caches, deps, VCS,
// build output. Keeps the fallback fast and the outline about the user's code.
var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	"target": true, ".venv": true, "venv": true, "__pycache__": true, ".idea": true,
	".vscode": true, "testdata": true, ".evva": true,
}

// declPatterns capture top-level declaration names across a few common
// languages (capture group 1 = name). False positives are acceptable in a
// coarse outline; the goal is a useful sketch, not precision.
var declPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^func\s+(?:\([^)]*\)\s+)?([A-Z]\w*)`), // Go exported func/method
	regexp.MustCompile(`^type\s+([A-Z]\w*)`),                  // Go exported type
	regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:abstract\s+)?(?:async\s+)?(?:function|class|interface|enum)\s+([A-Za-z_$][\w$]*)`), // JS/TS
	regexp.MustCompile(`^(?:async\s+)?(?:class|def)\s+([A-Za-z_]\w*)`),                                                                      // Python
	regexp.MustCompile(`^pub\s+(?:fn|struct|enum|trait|mod)\s+([A-Za-z_]\w*)`),                                                              // Rust
}

const (
	fallbackMaxFiles    = 4000 // cap the walk on very large trees
	fallbackMaxScanByte = 256 * 1024
)

// BuildFallback derives a coarse outline with no language server: it walks the
// tree, groups source files by directory, and greps each for top-level
// declarations. Used when no LSP server is configured for the repo's languages
// (A5). Always prefixes a note so the model knows the map is heuristic, not
// semantic. Respects ctx for the time-box.
func BuildFallback(ctx context.Context, root string, budget int) (string, error) {
	if root == "" {
		root = "."
	}
	byDir := map[string][]string{}
	files := 0
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries rather than aborting
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || skipDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if files >= fallbackMaxFiles {
			return filepath.SkipDir
		}
		if !codeExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		files++
		dir := packageRel(path, root)
		for _, name := range grepDecls(path) {
			byDir[dir] = append(byDir[dir], name)
		}
		return nil
	})
	if walkErr != nil && ctx.Err() == nil {
		return "", walkErr
	}
	if len(byDir) == 0 {
		return "", errNoSymbols
	}

	dirs := make([]string, 0, len(byDir))
	for d := range byDir {
		dirs = append(dirs, d)
	}
	// Richest directories first (descending decl count, alpha tie-break) so the
	// budget surfaces the meatiest packages; deterministic for cache stability.
	sort.Slice(dirs, func(i, j int) bool {
		if len(byDir[dirs[i]]) != len(byDir[dirs[j]]) {
			return len(byDir[dirs[i]]) > len(byDir[dirs[j]])
		}
		return dirs[i] < dirs[j]
	})

	var b strings.Builder
	b.WriteString("# Repo map\n\n")
	b.WriteString("No language server is configured for this repo — this is a coarse, " +
		"grep-derived outline (top-level declarations by directory), not a semantic map. " +
		"Configure an LSP server for a ranked, signature-level map.\n\n")
	used := b.Len()
	for di, dir := range dirs {
		names := dedupeSorted(byDir[dir])
		head := "### " + dir + "\n"
		if (used+len(head))/4 > budget && di > 0 {
			fmt.Fprintf(&b, "… +%d more directories\n", len(dirs)-di)
			break
		}
		b.WriteString(head)
		used += len(head)
		shown := 0
		for _, n := range names {
			line := "  " + n + "\n"
			if (used+len(line))/4 > budget && shown > 0 {
				break
			}
			b.WriteString(line)
			used += len(line)
			shown++
		}
		if shown < len(names) {
			more := fmt.Sprintf("  … +%d more\n", len(names)-shown)
			b.WriteString(more)
			used += len(more)
		}
		b.WriteByte('\n')
		used++
	}
	if ctx.Err() != nil {
		b.WriteString("(scan incomplete — partial)\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// grepDecls scans one file for top-level declaration names. Bounded by
// fallbackMaxScanByte; only lines starting in column 0 are considered (a cheap
// proxy for "top-level").
func grepDecls(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var names []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	read := 0
	for sc.Scan() {
		line := sc.Text()
		read += len(line) + 1
		if read > fallbackMaxScanByte {
			break
		}
		if len(line) == 0 || line[0] == ' ' || line[0] == '\t' {
			continue // not a top-level declaration
		}
		for _, re := range declPatterns {
			if m := re.FindStringSubmatch(line); m != nil {
				names = append(names, m[1])
				break
			}
		}
	}
	// A scan error (an over-long line, an IO hiccup) just yields a partial
	// outline for this file — acceptable for a coarse, best-effort heuristic.
	_ = sc.Err()
	return names
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
