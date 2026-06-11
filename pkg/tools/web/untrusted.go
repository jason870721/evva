package web

import (
	"regexp"
	"strings"
)

// untrusted.go frames external web content as data-not-instructions (RP-21).
// Anything fetched from the open web enters the conversation wrapped in an
// <untrusted-content source="…"> envelope, so the model — taught the protocol
// once in its system prompt — can tell "the outside world said this" from
// "someone is talking to me". The wrapper is the framework half of the
// prompt-injection defence; the prompt line is the other half. Deliberately
// NOT <system-reminder>: that tag means "the system is speaking to you",
// while this one means "nobody is speaking to you — this is material".
//
// Scope is deliberately web-only: http_request typically targets the
// operator's own services, where blanket-untrusted framing would muddy
// trusted API signals (RP-21 §2.3).

// nestedUntrustedTag matches any embedded opening or closing form of the
// envelope tag, case-insensitively. A malicious page could include a literal
// "</untrusted-content>" to escape the envelope and forge trusted text after
// it — defanging the angle bracket makes the fake delimiter inert while
// keeping the page text readable.
var nestedUntrustedTag = regexp.MustCompile(`(?i)<(/?)untrusted-content`)

// attrEscaper neutralises characters that could terminate or restructure the
// source attribute. URLs from url.URL.String() are already percent-encoded,
// so this is belt-and-suspenders for hand-built sources.
var attrEscaper = strings.NewReplacer(`"`, "%22", "<", "%3C", ">", "%3E", "\n", "", "\r", "")

// wrapUntrusted returns content framed in an <untrusted-content> envelope
// with its origin in the source attribute. Empty content returns "" — the
// caller skips the envelope rather than shipping an empty one.
func wrapUntrusted(source, content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(content) + len(source) + 64)
	b.WriteString(`<untrusted-content source="`)
	b.WriteString(attrEscaper.Replace(source))
	b.WriteString("\">\n")
	b.WriteString(nestedUntrustedTag.ReplaceAllString(content, "&lt;${1}untrusted-content"))
	b.WriteString("\n</untrusted-content>")
	return b.String()
}
