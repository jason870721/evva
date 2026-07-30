package redact

import "strings"

// prefilter.go answers one question as cheaply as possible: which of the
// eighteen rules could possibly match this text?
//
// The naive answer — a strings.Contains per rule literal — is already a
// huge win over running every pattern (40ms → 0.9ms on 100KB), but it
// still walks the text once per literal. Around thirty literals × 100KB is
// 3MB of scanning, and at that point the cost is memory bandwidth, not
// cleverness: the measured 630µs is close to what thirty passes must cost.
//
// So: one pass instead of thirty. The pass records which adjacent byte
// pairs occur anywhere in the text, ASCII-case-folded, into a small bit
// set. A literal whose bigrams are not all present cannot appear in the
// text, so its rule is skipped without a scan. Survivors — normally none —
// still get an exact strings.Contains, so the filter can only ever save
// work, never change an outcome.
//
// Folding means the set is checked with lowercased literals. That is
// conservative in the safe direction: a folded bigram missing from the set
// guarantees the case-sensitive literal is absent too.

const (
	// bigramBits is the size of the bit set. 4096 bits (512 bytes) fits in
	// L1 and keeps the false-positive rate low enough that ordinary text
	// still rejects nearly every rule. A precise 65536-bit set would be
	// exact but costs 8KB of cache per call for no measurable gain.
	bigramBits = 4096
	bigramMask = bigramBits - 1
)

type bigramFilter [bigramBits / 8]byte

// buildBigramFilter records every case-folded adjacent byte pair in s.
func buildBigramFilter(s string) *bigramFilter {
	var f bigramFilter
	if len(s) < 2 {
		return &f
	}
	prev := foldByte(s[0])
	for i := 1; i < len(s); i++ {
		cur := foldByte(s[i])
		h := (uint32(prev)<<8 | uint32(cur)) & bigramMask
		f[h>>3] |= 1 << (h & 7)
		prev = cur
	}
	return &f
}

// mayContain reports whether lit (already lowercased) could appear in the
// filtered text. False is definitive; true means "run the real check".
func (f *bigramFilter) mayContain(lit string) bool {
	if len(lit) < 2 {
		return true // nothing to filter on; let Contains decide
	}
	prev := lit[0]
	for i := 1; i < len(lit); i++ {
		cur := lit[i]
		h := (uint32(prev)<<8 | uint32(cur)) & bigramMask
		if f[h>>3]&(1<<(h&7)) == 0 {
			return false
		}
		prev = cur
	}
	return true
}

func foldByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

// gate is a rule's prefilter, with its literals pre-lowercased at
// construction so the hot path allocates nothing.
type gate struct {
	needs    []string // all must be present
	needsOne []string // at least one must be present
	// The same literals lowercased, for the bigram check.
	needsLower    []string
	needsOneLower []string
}

func newGate(r Rule) gate {
	g := gate{needs: r.Needs, needsOne: r.NeedsOne}
	for _, l := range r.Needs {
		g.needsLower = append(g.needsLower, strings.ToLower(l))
	}
	for _, l := range r.NeedsOne {
		g.needsOneLower = append(g.needsOneLower, strings.ToLower(l))
	}
	return g
}

// mayPass is the cheap half: a bit-set lookup per literal, no scanning.
// f is built over the ORIGINAL text; being case-folded it serves fold and
// non-fold rules alike. A false here is definitive, and it is the answer
// for essentially every rule on essentially every tool result — which is
// the whole point, since it is reached before any haystack is prepared.
func (g gate) mayPass(f *bigramFilter) bool {
	for _, lit := range g.needsLower {
		if !f.mayContain(lit) {
			return false
		}
	}
	if len(g.needsOneLower) == 0 {
		return true
	}
	for _, lit := range g.needsOneLower {
		if f.mayContain(lit) {
			return true
		}
	}
	return false
}

// confirms is the exact half, run only on the few rules mayPass let
// through. Case-sensitive, against the haystack the rule will actually be
// matched over.
func (g gate) confirms(hay string) bool {
	for _, lit := range g.needs {
		if !strings.Contains(hay, lit) {
			return false
		}
	}
	if len(g.needsOne) == 0 {
		return true
	}
	for _, lit := range g.needsOne {
		if strings.Contains(hay, lit) {
			return true
		}
	}
	return false
}
