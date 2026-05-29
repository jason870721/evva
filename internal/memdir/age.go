package memdir

import (
	"fmt"
	"time"
)

// AgeDays returns whole days elapsed since mtime, floored: 0 for today, 1 for
// yesterday, 2+ for older. A future mtime (clock skew) clamps to 0. Port of
// memoryAge.ts:memoryAgeDays.
func AgeDays(mtime time.Time) int {
	d := int(time.Since(mtime).Hours() / 24)
	if d < 0 {
		return 0
	}
	return d
}

// Age renders a human-readable age string ("today" / "yesterday" / "N days
// ago"). Models reason about staleness from "47 days ago" far better than from
// a raw timestamp. Port of memoryAge.ts:memoryAge.
func Age(mtime time.Time) string {
	switch d := AgeDays(mtime); d {
	case 0:
		return "today"
	case 1:
		return "yesterday"
	default:
		return fmt.Sprintf("%d days ago", d)
	}
}

// FreshnessText is the plain-text staleness caveat for memories older than one
// day; "" for today/yesterday (a caveat there is just noise). The wording is
// copied verbatim from memoryAge.ts:memoryFreshnessText — it was tuned against
// real incidents where a stale file:line citation made a wrong claim sound more
// authoritative, not less.
//
// Use this when the consumer adds its own wrapping (the recall path wraps the
// whole batch in one <system-reminder>, so it embeds FreshnessText, not the
// self-wrapping FreshnessNote).
func FreshnessText(mtime time.Time) string {
	d := AgeDays(mtime)
	if d <= 1 {
		return ""
	}
	return fmt.Sprintf(
		"This memory is %d days old. "+
			"Memories are point-in-time observations, not live state — "+
			"claims about code behavior or file:line citations may be outdated. "+
			"Verify against current code before asserting as fact.", d)
}

// FreshnessNote wraps FreshnessText in <system-reminder> tags with a trailing
// newline, for callers that don't add their own wrapper. "" for memories ≤1 day
// old. Port of memoryAge.ts:memoryFreshnessNote.
func FreshnessNote(mtime time.Time) string {
	text := FreshnessText(mtime)
	if text == "" {
		return ""
	}
	return "<system-reminder>" + text + "</system-reminder>\n"
}
