package common

import (
	"fmt"
	"time"
)

// StampLayout is the canonical wall-clock layout for any time string handed to
// a model. The explicit UTC offset is the point: a model reads a bare
// "2006-01-02 15:04:05" as UTC, so in any non-UTC deployment every zone-less
// timestamp it sees is misread by the local offset (observed in the field as a
// phantom "clock skew" bug report from a swarm agent in a UTC+8 container).
const StampLayout = "2006-01-02 15:04:05 -07:00"

// Stamp renders t as local wall-clock time with its explicit UTC offset,
// e.g. "2026-06-10 20:25:00 +08:00".
func Stamp(t time.Time) string { return t.Local().Format(StampLayout) }

// StampWithUTC renders t local-with-offset plus its UTC twin, e.g.
// "2026-06-10 20:25:00 +08:00 (= 2026-06-10 12:25 UTC)". Echoing both back
// after parsing a model-supplied time makes a timezone mix-up visible at a
// glance — the cheapest defense against an alarm armed 8 hours off intent.
func StampWithUTC(t time.Time) string {
	return fmt.Sprintf("%s (= %s UTC)", Stamp(t), t.UTC().Format("2006-01-02 15:04"))
}

// ZoneLabel names the process-local timezone, e.g. "HKT (UTC+08:00)". Built
// from Zone() rather than time.Local.String(), which degrades to "Local" when
// TZ is unset. Stable for the lifetime of a run (absent a DST flip), so it is
// safe to embed in prompt-cache-sensitive system prompts.
func ZoneLabel() string {
	name, off := time.Now().Zone()
	sign := "+"
	if off < 0 {
		sign = "-"
		off = -off
	}
	return fmt.Sprintf("%s (UTC%s%02d:%02d)", name, sign, off/3600, (off%3600)/60)
}
