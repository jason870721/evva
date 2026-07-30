package redact

import (
	"math"
	"strings"
)

// entropy.go is the net for credentials with no publishable shape — the
// rotated database password, the internal service token, the bearer blob
// some vendor invented last week. Format rules (rules.go) cannot enumerate
// those, so this fires on *randomness* instead.
//
// Randomness alone is a terrible signal: a git SHA, a UUID, a lockfile
// integrity hash and a base64 test fixture are all high-entropy and all
// completely uninteresting. Three constraints stack to keep the false
// positive rate near zero, and every one of them is load-bearing:
//
//  1. Context — the value must sit in an assignment or inside quotes.
//     Free-floating tokens in prose or code are ignored outright.
//  2. Alphabet width — the threshold below sits ABOVE what a hex string
//     can reach. This is deliberate and is what buys immunity to the
//     lockfile/SHA/UUID case that would otherwise dominate the noise.
//  3. Composition — digits plus letters, no whitespace, not a path.
//
// The cost of this design is that hex-encoded secrets slip past. That is
// the right trade: they are usually caught by name via secret-assignment,
// and a redactor the operator turns off because it mangles their lockfiles
// protects nothing at all.

const (
	// minEntropyLen is the shortest run worth judging. Below this, Shannon
	// entropy over a single short string is dominated by sampling noise.
	minEntropyLen = 20

	// maxEntropyLen bounds the scan. Anything longer is a blob, not a
	// credential; PEM material is already handled structurally.
	maxEntropyLen = 512

	// entropyFloor is bits per character, and the number is chosen to sit
	// between two real distributions rather than picked for roundness:
	//
	//	hex / UUID / git SHA      ≤ 4.0   (16 symbols — a hard ceiling)
	//	English prose             ~4.0-4.3
	//	random base64 / alnum     ~5.5-5.9
	//
	// 4.5 is the gap. Raising it starts missing real secrets; lowering it
	// walks straight into every lockfile in the repo.
	entropyFloor = 4.5
)

// isTokenByte reports whether c can appear inside a candidate run. The set
// is the union of the base64, base64url and hex alphabets — everything a
// generated credential is realistically encoded in.
func isTokenByte(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	case c == '+', c == '/', c == '=', c == '_', c == '-':
		return true
	}
	return false
}

// shannon returns the entropy of s in bits per character.
func shannon(s string) float64 {
	if s == "" {
		return 0
	}
	var freq [256]int
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range freq {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// looksRandom reports whether s has the composition of generated material
// rather than of something a human typed. Applied after the length and
// context gates, it rejects the residue that clears the entropy floor for
// the wrong reason — mostly long lowercase identifiers and path-like
// strings.
func looksRandom(s string) bool {
	if len(s) < minEntropyLen || len(s) > maxEntropyLen {
		return false
	}
	var hasDigit, hasUpper, hasLower bool
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			hasDigit = true
		case c >= 'A' && c <= 'Z':
			hasUpper = true
		case c >= 'a' && c <= 'z':
			hasLower = true
		}
	}
	// Digits plus at least one letter case. A pure-alpha string of any
	// length is a sentence or an identifier; a pure-digit one is a number.
	if !hasDigit || (!hasUpper && !hasLower) {
		return false
	}
	// Dotted or slashed runs are versions, paths and package coordinates.
	// Real credentials in this alphabet do not carry separators.
	if strings.Count(s, "/") > 1 {
		return false
	}
	// A trailing "==" is base64 padding and stays in scope; interior "="
	// means this is a query string or a compound value, not one secret.
	if i := strings.Index(s, "="); i >= 0 && i < len(s)-2 {
		return false
	}
	return shannon(s) >= entropyFloor
}

// entropySpans returns the byte ranges the fallback claims, in ascending
// order. The caller merges them against rule matches, which take
// precedence.
//
// Hand-rolled rather than regex-driven: the equivalent pattern
// (a bounded {20,512} run behind an assignment or quote) compiles to an
// NFA that costs ~6ms on a 100KB tool result, which is most of the budget
// for the whole package. This is one linear pass with an arithmetic
// rejection at each step.
func entropySpans(text string) []span {
	var out []span
	for i := 0; i < len(text); {
		if !isTokenByte(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && isTokenByte(text[j]) {
			j++
		}
		// A run is a candidate only if it is positioned like a value:
		// immediately after a quote, or after an assignment operator
		// (with optional spaces). Anything free-floating is ignored,
		// which is what keeps prose and code out of scope.
		if j-i >= minEntropyLen && j-i <= maxEntropyLen && precededByAssignment(text, i) && looksRandom(text[i:j]) {
			out = append(out, span{start: i, end: j, rule: "high-entropy"})
		}
		i = j
	}
	return out
}

// precededByAssignment reports whether the run starting at i sits on the
// value side of an assignment or inside quotes.
func precededByAssignment(text string, i int) bool {
	k := i - 1
	// Skip an opening quote, then any horizontal whitespace, then expect
	// the operator. A bare quoted string (no operator) also qualifies —
	// that is the JSON-value case.
	quoted := false
	if k >= 0 && (text[k] == '"' || text[k] == '\'') {
		quoted = true
		k--
	}
	for k >= 0 && (text[k] == ' ' || text[k] == '\t') {
		k--
	}
	if k < 0 {
		return false
	}
	if text[k] == '=' || text[k] == ':' {
		return true
	}
	return quoted
}
