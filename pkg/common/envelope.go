package common

import (
	"regexp"
	"strings"
	"sync"
)

// envelope.go holds the delimiter-defanging primitive behind every
// "this text came from outside — frame it, don't trust it" wrapper in evva.
//
// It was extracted from pkg/tools/web's RP-21 <untrusted-content> envelope
// when MCP server mode needed the same hardening for a different tag
// (<external-request>, an inbound MCP caller's prompt). The two callers mean
// different things by their envelopes and keep their own semantics; what
// they share is the part that must not drift — a hostile payload embedding
// a literal closing delimiter to escape the wrapper and forge trusted text
// after it.

// tagPattern returns the regexp matching any embedded opening or closing
// form of tag, case-insensitively. Compiled patterns are cached: the tag set
// is tiny and fixed at compile time, but a per-call MustCompile on a hot
// tool-result path would be wasteful.
var (
	tagCacheMu sync.RWMutex
	tagCache   = map[string]*regexp.Regexp{}
)

func tagPattern(tag string) *regexp.Regexp {
	tagCacheMu.RLock()
	re, ok := tagCache[tag]
	tagCacheMu.RUnlock()
	if ok {
		return re
	}
	re = regexp.MustCompile(`(?i)<(/?)` + regexp.QuoteMeta(tag))
	tagCacheMu.Lock()
	tagCache[tag] = re
	tagCacheMu.Unlock()
	return re
}

// attrEscaper neutralises characters that could terminate or restructure an
// attribute value. Callers generally pass already-encoded values (a URL from
// url.URL.String(), a validated client name), so this is belt-and-suspenders
// for hand-built sources.
var attrEscaper = strings.NewReplacer(`"`, "%22", "<", "%3C", ">", "%3E", "\n", "", "\r", "")

// DefangTag makes every embedded opening or closing form of tag inert by
// escaping its angle bracket, leaving the surrounding text readable. This is
// what stops content from closing its own envelope early.
func DefangTag(tag, content string) string {
	return tagPattern(tag).ReplaceAllString(content, "&lt;${1}"+tag)
}

// Envelope frames content in a <tag attr="value"> … </tag> wrapper, with the
// attribute value escaped and any embedded copy of tag defanged. Empty (or
// whitespace-only) content returns "" — the caller skips the envelope rather
// than shipping an empty one.
//
// attr may be empty, in which case the tag carries no attribute.
func Envelope(tag, attr, value, content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(content) + len(value) + len(tag)*2 + 32)
	b.WriteString("<")
	b.WriteString(tag)
	if attr != "" {
		b.WriteString(" ")
		b.WriteString(attr)
		b.WriteString(`="`)
		b.WriteString(attrEscaper.Replace(value))
		b.WriteString(`"`)
	}
	b.WriteString(">\n")
	b.WriteString(DefangTag(tag, content))
	b.WriteString("\n</")
	b.WriteString(tag)
	b.WriteString(">")
	return b.String()
}
