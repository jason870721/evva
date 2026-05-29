package memdir

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// MemoryHeader is the lightweight view of one memory file — enough to build the
// recall manifest and surface freshness without reading the full body. Port of
// memoryScan.ts:MemoryHeader.
type MemoryHeader struct {
	// Filename is the path RELATIVE to the memory dir (e.g. "feedback/foo.md").
	// This same string is the key everywhere: in the manifest, in the recall
	// selector's valid-filename set, and in the alreadySurfaced de-dup — it is
	// the real hallucinated-path safety net (PRD Task 1 note).
	Filename    string
	Path        string     // absolute path on disk
	ModTime     time.Time  // file mtime; drives newest-first ordering + freshness
	Description string     // frontmatter `description`; "" when absent
	Type        MemoryType // frontmatter `type`; "" when absent/unknown
}

const (
	// MaxMemoryFiles caps how many headers a scan returns (newest-first). Port
	// of memoryScan.ts:MAX_MEMORY_FILES.
	MaxMemoryFiles = 200

	// FrontmatterMaxLines bounds how many leading lines a scan reads per file
	// when looking for frontmatter. Port of memoryScan.ts:FRONTMATTER_MAX_LINES.
	FrontmatterMaxLines = 30

	// frontmatterScanBytes is the byte ceiling backing FrontmatterMaxLines —
	// ~4 KiB comfortably covers 30 lines without slurping a large body. Mirrors
	// the io.LimitReader discipline in readMemFile.
	frontmatterScanBytes = 4 * 1024
)

// ScanMemoryFiles walks dir recursively for *.md files (excluding MEMORY.md),
// reads each one's frontmatter header, and returns the headers sorted
// newest-first, capped at MaxMemoryFiles.
//
// Never errors: a missing dir, an unreadable file, or malformed frontmatter is
// skipped — mirroring memoryScan.ts (Promise.allSettled + catch → []). A
// missing dir or a dir with no .md files returns an empty slice.
func ScanMemoryFiles(dir string) []MemoryHeader {
	if dir == "" {
		return nil
	}
	var headers []MemoryHeader
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entry — skip, never fail the whole walk
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".md") || d.Name() == MemoryIndexFile {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		fm := readHeaderFrontmatter(path)
		typ, _ := ParseMemoryType(fm["type"])
		headers = append(headers, MemoryHeader{
			Filename:    filepath.ToSlash(rel),
			Path:        path,
			ModTime:     info.ModTime(),
			Description: fm["description"],
			Type:        typ,
		})
		return nil
	})

	sort.SliceStable(headers, func(i, j int) bool {
		return headers[i].ModTime.After(headers[j].ModTime)
	})
	if len(headers) > MaxMemoryFiles {
		headers = headers[:MaxMemoryFiles]
	}
	return headers
}

// readHeaderFrontmatter reads the first FrontmatterMaxLines of a file (bounded
// by frontmatterScanBytes) and parses its frontmatter. Returns an empty map on
// any read error — the caller treats that as "untyped, no description."
func readHeaderFrontmatter(path string) map[string]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, frontmatterScanBytes))
	if err != nil {
		return nil
	}
	lines := strings.SplitN(string(buf), "\n", FrontmatterMaxLines+1)
	if len(lines) > FrontmatterMaxLines {
		lines = lines[:FrontmatterMaxLines]
	}
	fm, _ := ParseFrontmatter(strings.Join(lines, "\n"))
	return fm
}

// FormatManifest renders the header list as one line per file:
//
//   - [type] filename (RFC3339): description
//
// The `[type] ` tag and `: description` suffix are omitted when absent
// (formatMemoryManifest parity, memoryScan.ts:84). Timestamps are UTC RFC3339.
func FormatManifest(hs []MemoryHeader) string {
	var b strings.Builder
	for i, h := range hs {
		if i > 0 {
			b.WriteByte('\n')
		}
		tag := ""
		if h.Type != "" {
			tag = "[" + string(h.Type) + "] "
		}
		ts := h.ModTime.UTC().Format(time.RFC3339)
		if h.Description != "" {
			fmt.Fprintf(&b, "- %s%s (%s): %s", tag, h.Filename, ts, h.Description)
		} else {
			fmt.Fprintf(&b, "- %s%s (%s)", tag, h.Filename, ts)
		}
	}
	return b.String()
}
