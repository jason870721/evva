// Package repomap composes a compact, ranked, token-bounded overview of a
// codebase's symbols from the LSP layer (pkg/tools/lsp), with a glob fallback
// when no language server is available. The map is injected into the Main
// agent's system prompt at session start (opt-in, gated on EnableRepoMap) so
// the model starts with a model of the repo's shape instead of re-deriving it
// every cold start. The repo_map tool (this package) lets the model zoom into a
// subtree on demand.
//
// The dependency arrow is one-way: repomap → lsp. The neutral lsp.Symbol type
// (not protocol wire types) is the only LSP surface this package touches.
package repomap

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/johnny1110/evva/pkg/tools/lsp"
)

// Source is the narrow seam onto the LSP layer the builder needs — *lsp.Manager
// satisfies it. Declared here (not as the whole Manager) so the builder couples
// to three methods and stays trivially mockable in tests.
type Source interface {
	Servers() []string
	WorkspaceSymbols(ctx context.Context, query string) ([]lsp.Symbol, error)
	DocumentSymbols(ctx context.Context, path string) ([]lsp.Symbol, error)
}

// errNoServer / errNoSymbols are internal sentinels: Build returns them so the
// caller (the agent) degrades to BuildFallback rather than injecting an empty
// or misleading map.
var (
	errNoServer  = errors.New("repomap: no LSP server configured")
	errNoSymbols = errors.New("repomap: no symbols returned")
)

// enrichFileCap bounds how many files Build queries for signatures so a huge
// repo doesn't fan out into thousands of document_symbol round-trips. The ctx
// deadline is the other guard (A9).
const enrichFileCap = 60

// Build composes the session-open repo map: a workspace/symbol sweep grouped by
// package directory, ranked, best-effort signature-enriched, and rendered
// within the token budget. Returns an error (errNoServer / errNoSymbols / a
// transport error) when it can't produce a useful map — the caller falls back.
//
// ctx carries the time-box: a cold language-server index returns partial or no
// results, so signature enrichment stops on ctx cancellation and the render
// gets an "(indexing — partial)" note rather than blocking session start.
func Build(ctx context.Context, src Source, root string, budget int) (string, error) {
	if src == nil || len(src.Servers()) == 0 {
		return "", errNoServer
	}
	syms, err := src.WorkspaceSymbols(ctx, "")
	if err != nil {
		return "", err
	}
	syms = keepInteresting(filterUnderRoot(syms, root))
	if len(syms) == 0 {
		return "", errNoSymbols
	}
	groups := groupByPackage(syms, root)
	enrichSignatures(ctx, src, groups)

	header := fmt.Sprintf(
		"Repo map — %d symbols across %d packages, from the language server. "+
			"A ranked orientation aid, not exhaustive; call `repo_map` to zoom into a path.",
		len(syms), len(groups))
	if ctx.Err() != nil {
		header += " (indexing — partial)"
	}
	return "# Repo map\n\n" + header + "\n\n" + renderGroups(groups, budget, true), nil
}

// BuildPath renders the map for a single subtree — the repo_map zoom tool. It
// finds the files under relPath via the workspace index, then reads each file's
// document-symbol outline. detail "full" includes members (methods/fields) with
// signatures; "overview" keeps only top-level declarations.
func BuildPath(ctx context.Context, src Source, root, relPath, detail string, budget int) (string, error) {
	if src == nil || len(src.Servers()) == 0 {
		return "", errNoServer
	}
	target := filepath.Clean(filepath.Join(root, relPath))
	sweep, err := src.WorkspaceSymbols(ctx, "")
	if err != nil {
		return "", err
	}
	files := distinctFilesUnder(sweep, target)
	if len(files) == 0 {
		return "", errNoSymbols
	}
	full := detail == "full"

	var b strings.Builder
	fmt.Fprintf(&b, "# Repo map — %s (%s)\n\n", relPath, detailLabel(full))
	used := b.Len()
	shownFiles := 0
	for _, f := range files {
		if ctx.Err() != nil {
			break
		}
		ds, derr := src.DocumentSymbols(ctx, f)
		if derr != nil || len(ds) == 0 {
			continue
		}
		if !full {
			ds = topLevelOnly(ds)
		}
		ds = keepInteresting(ds)
		rankSymbols(ds)
		if len(ds) == 0 {
			continue
		}
		head := "### " + packageRel(f, root) + "\n"
		if estimateTokens(head)+used/4 > budget && shownFiles > 0 {
			fmt.Fprintf(&b, "… +%d more files under %s\n", len(files)-shownFiles, relPath)
			break
		}
		b.WriteString(head)
		used += len(head)
		shown := 0
		for _, s := range ds {
			line := formatMember(s)
			if (used+len(line))/4 > budget && shown > 0 {
				break
			}
			b.WriteString(line)
			used += len(line)
			shown++
		}
		if shown < len(ds) {
			more := fmt.Sprintf("  … +%d more\n", len(ds)-shown)
			b.WriteString(more)
			used += len(more)
		}
		b.WriteByte('\n')
		used++
		shownFiles++
	}
	if ctx.Err() != nil {
		b.WriteString("(indexing — partial)\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// pkgGroup is a package directory and its ranked symbols.
type pkgGroup struct {
	dir  string
	syms []lsp.Symbol
}

// groupByPackage buckets symbols by package directory (relative to root), ranks
// each bucket, and orders buckets by descending symbol count (tie-break
// alphabetical) so the budget surfaces the meatiest packages first and the
// ordering stays deterministic for prompt-cache stability.
func groupByPackage(syms []lsp.Symbol, root string) []pkgGroup {
	byDir := map[string][]lsp.Symbol{}
	for _, s := range syms {
		dir := packageRel(s.File, root)
		byDir[dir] = append(byDir[dir], s)
	}
	groups := make([]pkgGroup, 0, len(byDir))
	for dir, g := range byDir {
		rankSymbols(g)
		groups = append(groups, pkgGroup{dir: dir, syms: g})
	}
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].syms) != len(groups[j].syms) {
			return len(groups[i].syms) > len(groups[j].syms)
		}
		return groups[i].dir < groups[j].dir
	})
	return groups
}

// renderGroups greedily fills the budget group by group, truncating at symbol
// boundaries with a "… +N more" marker per group (and a final summary marker
// when whole groups don't fit). Never cuts mid-symbol (A4).
func renderGroups(groups []pkgGroup, budget int, withLoc bool) string {
	var b strings.Builder
	used := 0
	for gi, g := range groups {
		head := "### " + g.dir + "\n"
		// Need room for the header plus at least one symbol line; otherwise
		// summarize everything still pending and stop.
		if (used+len(head))/4 > budget && gi > 0 {
			pending, pkgs := 0, 0
			for _, gg := range groups[gi:] {
				pending += len(gg.syms)
				pkgs++
			}
			fmt.Fprintf(&b, "… +%d more symbols across %d more packages\n", pending, pkgs)
			break
		}
		b.WriteString(head)
		used += len(head)
		shown := 0
		for _, s := range g.syms {
			line := formatSym(s, withLoc)
			if (used+len(line))/4 > budget && shown > 0 {
				break
			}
			b.WriteString(line)
			used += len(line)
			shown++
		}
		if shown < len(g.syms) {
			more := fmt.Sprintf("  … +%d more in %s\n", len(g.syms)-shown, g.dir)
			b.WriteString(more)
			used += len(more)
		}
		b.WriteByte('\n')
		used++
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// formatSym renders one symbol for the package-grouped overview: kind, name,
// signature when known, and the basename:line (a package spans files, so the
// file is useful here).
func formatSym(s lsp.Symbol, withLoc bool) string {
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(s.Kind)
	b.WriteByte(' ')
	b.WriteString(s.Name)
	if s.Detail != "" {
		b.WriteByte(' ')
		b.WriteString(s.Detail)
	}
	if withLoc && s.File != "" {
		fmt.Fprintf(&b, "  %s", filepath.Base(s.File))
		if s.Line > 0 {
			fmt.Fprintf(&b, ":%d", s.Line)
		}
	}
	b.WriteByte('\n')
	return b.String()
}

// formatMember renders one symbol for the per-file zoom view: indented under
// its container, with signature. No filename (the file is the group header).
func formatMember(s lsp.Symbol) string {
	indent := "  "
	if s.Container != "" {
		indent = "    "
	}
	line := indent + s.Kind + " " + s.Name
	if s.Container != "" {
		line = indent + s.Kind + " " + s.Container + "." + s.Name
	}
	if s.Detail != "" {
		line += " " + s.Detail
	}
	if s.Line > 0 {
		line += fmt.Sprintf("  :%d", s.Line)
	}
	return line + "\n"
}

// enrichSignatures fills Detail (signatures) on grouped symbols by reading the
// document-symbol outline of the files involved, in group/rank order so the
// most important files get enriched first. Bounded by enrichFileCap and the ctx
// deadline; best-effort, so on cancellation the remaining symbols simply render
// with name+kind (A9 graceful degradation).
func enrichSignatures(ctx context.Context, src Source, groups []pkgGroup) {
	seen := map[string]bool{}
	var files []string
	for _, g := range groups {
		for _, s := range g.syms {
			if s.File != "" && !seen[s.File] {
				seen[s.File] = true
				files = append(files, s.File)
			}
		}
	}
	details := map[string]map[string]string{} // file -> symbol name -> signature
	for i, f := range files {
		if i >= enrichFileCap || ctx.Err() != nil {
			break
		}
		ds, err := src.DocumentSymbols(ctx, f)
		if err != nil {
			continue
		}
		m := map[string]string{}
		for _, d := range ds {
			if d.Detail != "" && d.Container == "" {
				m[d.Name] = d.Detail
			}
		}
		details[f] = m
	}
	for gi := range groups {
		for si := range groups[gi].syms {
			s := &groups[gi].syms[si]
			if m, ok := details[s.File]; ok {
				if det, ok := m[s.Name]; ok {
					s.Detail = det
				}
			}
		}
	}
}

// filterUnderRoot drops symbols whose file lives outside the repo root (stdlib,
// module cache, generated deps) — a workspace sweep with an empty query pulls
// those in on some servers.
func filterUnderRoot(syms []lsp.Symbol, root string) []lsp.Symbol {
	if root == "" {
		return syms
	}
	out := make([]lsp.Symbol, 0, len(syms))
	for _, s := range syms {
		if s.File == "" {
			continue
		}
		rel, err := filepath.Rel(root, s.File)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		out = append(out, s)
	}
	return out
}

// distinctFilesUnder returns the unique, sorted files from a sweep that live
// under dir (an absolute path), for the zoom.
func distinctFilesUnder(syms []lsp.Symbol, dir string) []string {
	seen := map[string]bool{}
	var files []string
	for _, s := range syms {
		if s.File == "" || seen[s.File] {
			continue
		}
		rel, err := filepath.Rel(dir, s.File)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		seen[s.File] = true
		files = append(files, s.File)
	}
	sort.Strings(files)
	return files
}

// topLevelOnly keeps symbols with no container (the file's top-level decls).
func topLevelOnly(syms []lsp.Symbol) []lsp.Symbol {
	out := make([]lsp.Symbol, 0, len(syms))
	for _, s := range syms {
		if s.Container == "" {
			out = append(out, s)
		}
	}
	return out
}

// packageRel renders a file's package directory relative to root, falling back
// to the raw path when it can't be made relative.
func packageRel(file, root string) string {
	if file == "" {
		return "."
	}
	rel := file
	if root != "" {
		if r, err := filepath.Rel(root, file); err == nil && !strings.HasPrefix(r, "..") {
			rel = r
		}
	}
	dir := filepath.Dir(rel)
	if dir == "" || dir == "." {
		return "."
	}
	return filepath.ToSlash(dir)
}

func detailLabel(full bool) string {
	if full {
		return "full"
	}
	return "overview"
}
