package memdir

import (
	"fmt"
	"strings"
)

const (
	// MaxIndexLines caps how many lines of MEMORY.md inject into the prompt.
	// Port of memdir.ts:MAX_ENTRYPOINT_LINES.
	MaxIndexLines = 200

	// MaxIndexBytes caps the injected index size (~125 chars/line × 200 lines).
	// It catches long-line indexes that slip past the line cap. Port of
	// memdir.ts:MAX_ENTRYPOINT_BYTES.
	MaxIndexBytes = 25_000
)

// ReadIndex reads <appHome>/memory/MEMORY.md and returns it ready for prompt
// injection: trimmed, truncated to the line + byte caps, with a named-cap
// warning appended when either cap fired. The second return is a short, bare
// truncation message for Snapshot.Warnings logging ("" when nothing was
// truncated). A missing or empty index returns ("", "").
//
// Go only READS the index — the model maintains MEMORY.md itself as part of its
// two-step save (PRD Task 2: a Go-side index writer would fight the model for
// ownership of the same file). Port of memdir.ts:truncateEntrypointContent.
func ReadIndex(appHome string) (body string, warning string) {
	path := MemoryIndexPath(appHome)
	if path == "" {
		return "", ""
	}
	raw, _ := readMemFile(path) // missing → ""; a read error here is non-fatal
	return truncateIndex(raw)
}

// truncateIndex applies the line-then-byte cap with a named-cap warning. Split
// out from ReadIndex so it is unit-testable without touching disk. It
// line-truncates first (a natural boundary), then byte-truncates at the last
// newline before the cap so a line is never cut mid-way.
func truncateIndex(raw string) (body string, warning string) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", ""
	}
	lines := strings.Split(trimmed, "\n")
	lineCount := len(lines)
	byteCount := len(trimmed)

	overLines := lineCount > MaxIndexLines
	// Measure the ORIGINAL byte count — long lines are exactly what the byte
	// cap targets, so a post-line-truncation size would understate the warning.
	overBytes := byteCount > MaxIndexBytes
	if !overLines && !overBytes {
		return trimmed, ""
	}

	truncated := trimmed
	if overLines {
		truncated = strings.Join(lines[:MaxIndexLines], "\n")
	}
	if len(truncated) > MaxIndexBytes {
		if cut := strings.LastIndexByte(truncated[:MaxIndexBytes], '\n'); cut > 0 {
			truncated = truncated[:cut]
		} else {
			truncated = truncated[:MaxIndexBytes]
		}
	}

	var reason string
	switch {
	case overBytes && !overLines:
		reason = fmt.Sprintf("%d bytes (limit %d) — index entries are too long", byteCount, MaxIndexBytes)
	case overLines && !overBytes:
		reason = fmt.Sprintf("%d lines (limit %d)", lineCount, MaxIndexLines)
	default:
		reason = fmt.Sprintf("%d lines and %d bytes", lineCount, byteCount)
	}
	warning = fmt.Sprintf(
		"%s is %s. Only part of it was loaded. Keep index entries to one line under ~200 chars; move detail into topic files.",
		MemoryIndexFile, reason)
	return truncated + "\n\n> WARNING: " + warning, warning
}
