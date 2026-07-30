package web

import "github.com/johnny1110/evva/pkg/common"

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

// untrustedTag is the envelope this package speaks. MCP server mode frames
// inbound caller prompts with a different tag and a different meaning; the
// delimiter-defanging both rely on lives in common.Envelope so there is one
// implementation of the part that must not drift.
const untrustedTag = "untrusted-content"

// wrapUntrusted returns content framed in an <untrusted-content> envelope
// with its origin in the source attribute. Empty content returns "" — the
// caller skips the envelope rather than shipping an empty one.
func wrapUntrusted(source, content string) string {
	return common.Envelope(untrustedTag, "source", source, content)
}
