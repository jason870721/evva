// Package redact masks credential-shaped strings before they leave the
// process for an LLM provider.
//
// evva sends tool results verbatim to whichever of five providers a session
// is configured for, and persists them in session snapshots on disk. A
// `bash: cat .env`, a `read` of a production config, a `printenv` dump —
// all of it used to travel intact. This package is the layer that says what
// may leave.
//
// The design has one load-bearing property: **placeholders are stable**.
// The same secret always masks to the same token within a session, so the
// model can still reason about "this key appears in both files" — it just
// never learns the value. A redactor that emitted [REDACTED] uniformly
// would destroy that inference; one that emitted a fresh id per occurrence
// would invent a distinction that isn't there.
//
// This reduces exposure, it does not eliminate it. Format rules cover the
// credentials that publish a shape, and the entropy fallback covers a
// narrow slice of the rest (see entropy.go for why it is deliberately
// narrow). A secret that looks like ordinary prose gets through. The honest
// framing — the one the user guide uses — is "a seatbelt, not a vault".
//
// Scope note: this operates on tool results, the unattended path. Content
// the operator types or pastes is theirs and is left alone; see
// Redactor.Redact.
package redact

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Finding is one masked value: what was caught, what replaced it, and the
// original. The original lives only here, in memory, for the lifetime of
// the session — it is never persisted and never re-enters the LLM path.
type Finding struct {
	// Placeholder is the exact text substituted into the transcript.
	Placeholder string
	// RuleID names the rule that claimed it ("github-token").
	RuleID string
	// Why is that rule's human explanation.
	Why string
	// Value is the raw secret. Handle accordingly.
	Value string
	// Count is how many times this value has been masked this session.
	Count int
}

// Options configures a Redactor.
type Options struct {
	// Rules overrides the built-in table. Nil means BuiltinRules().
	Rules []Rule

	// Disable lists rule IDs to switch off, for the operator whose repo
	// trips one rule constantly.
	Disable []string

	// Allow lists regular expressions matched against each candidate
	// value; a value matching any of them is left alone. This is the
	// escape hatch for the fixture file full of fake keys.
	Allow []string

	// NoEntropy switches off the entropy fallback, leaving only the
	// format rules. The format rules are the high-confidence half.
	NoEntropy bool
}

// Redactor masks secrets and remembers what it masked.
//
// A nil *Redactor is a valid no-op: every method tolerates it, so callers
// can hold one field and let "redaction off" be represented by nil rather
// than by a branch at every call site.
//
// Safe for concurrent use — evva dispatches tool calls in parallel
// goroutines and they all funnel through one Redactor.
type Redactor struct {
	rules []Rule
	gates []gate // parallel to rules; literals pre-lowercased
	allow []*regexp.Regexp
	noEnt bool

	mu    sync.RWMutex
	byKey map[string]*Finding // placeholder → finding
	order []string            // placeholders in first-seen order
	total int                 // every masking, including repeats
}

// New builds a Redactor. It returns an error only for an unparseable
// Allow pattern or an unknown Disable id — both are operator config
// mistakes worth reporting at startup rather than silently ignoring.
func New(opts Options) (*Redactor, error) {
	rules := opts.Rules
	if rules == nil {
		rules = BuiltinRules()
	}

	if len(opts.Disable) > 0 {
		known := make(map[string]bool, len(rules))
		for _, r := range rules {
			known[r.ID] = true
		}
		var unknown []string
		for _, id := range opts.Disable {
			if !known[id] && id != "high-entropy" {
				unknown = append(unknown, id)
			}
		}
		if len(unknown) > 0 {
			return nil, fmt.Errorf("unknown redaction rule(s) %s; known rules: %s",
				strings.Join(unknown, ", "), strings.Join(RuleIDs(rules), ", "))
		}
		off := make(map[string]bool, len(opts.Disable))
		for _, id := range opts.Disable {
			off[id] = true
		}
		kept := rules[:0:0]
		for _, r := range rules {
			if !off[r.ID] {
				kept = append(kept, r)
			}
		}
		rules = kept
		if off["high-entropy"] {
			opts.NoEntropy = true
		}
	}

	r := &Redactor{rules: rules, noEnt: opts.NoEntropy, byKey: map[string]*Finding{}}
	for _, rule := range rules {
		r.gates = append(r.gates, newGate(rule))
	}
	for _, pat := range opts.Allow {
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("redaction_allow %q: %w", pat, err)
		}
		r.allow = append(r.allow, re)
	}
	return r, nil
}

// RuleIDs lists the ids of a rule table, plus the entropy fallback's
// pseudo-id, for error messages and docs.
func RuleIDs(rules []Rule) []string {
	out := make([]string, 0, len(rules)+1)
	for _, r := range rules {
		out = append(out, r.ID)
	}
	return append(out, "high-entropy")
}

// span is a claimed byte range in the text being scanned.
type span struct {
	start, end int
	rule       string
	why        string
}

// Redact returns text with every recognised credential replaced by a
// stable placeholder, recording what it masked.
//
// It is deliberately NOT applied to operator-authored input. Someone
// pasting a key into the prompt is making a decision with their eyes open —
// masking it would break "help me rotate this" and would offer only the
// appearance of protection, since they can always paste it again.
func (r *Redactor) Redact(text string) string {
	if r == nil || text == "" {
		return text
	}
	spans := r.scan(text)
	if len(spans) == 0 {
		return text
	}

	var b strings.Builder
	b.Grow(len(text))
	prev := 0
	for _, s := range spans {
		b.WriteString(text[prev:s.start])
		b.WriteString(r.record(text[s.start:s.end], s.rule, s.why))
		prev = s.end
	}
	b.WriteString(text[prev:])
	return b.String()
}

// scan collects the byte ranges to mask, resolving overlaps by rule
// precedence: rules are tried in table order and the first to claim a
// range keeps it, so a value matching both a vendor format and the generic
// assignment rule is reported as the vendor's.
func (r *Redactor) scan(text string) []span {
	var claimed []span

	overlaps := func(start, end int) bool {
		for _, c := range claimed {
			if start < c.end && c.start < end {
				return true
			}
		}
		return false
	}

	// One bigram pass over the text gates every rule; see prefilter.go.
	// The lowercased copy is built at most once, and only once some Fold
	// rule has actually survived that gate. Offsets into it are valid
	// against text, since asciiLower is length-preserving.
	filter := buildBigramFilter(text)
	var lowered string
	var haveLowered bool
	subject := func(fold bool) string {
		if !fold {
			return text
		}
		if !haveLowered {
			lowered, haveLowered = asciiLower(text), true
		}
		return lowered
	}

	for i, rule := range r.rules {
		// Bit-set gate first, so a rule that cannot match never causes
		// the lowercased copy to be built.
		if !r.gates[i].mayPass(filter) {
			continue
		}
		hay := subject(rule.Fold)
		if !r.gates[i].confirms(hay) {
			continue
		}
		for _, m := range rule.Pattern.FindAllStringSubmatchIndex(hay, -1) {
			start, end := m[0], m[1]
			if rule.Group > 0 {
				g := 2 * rule.Group
				if g+1 >= len(m) || m[g] < 0 {
					continue
				}
				start, end = m[g], m[g+1]
			}
			if start >= end || overlaps(start, end) || r.allowed(text[start:end]) {
				continue
			}
			claimed = append(claimed, span{start: start, end: end, rule: rule.ID, why: rule.Why})
		}
	}

	if !r.noEnt {
		for _, s := range entropySpans(text) {
			if overlaps(s.start, s.end) || r.allowed(text[s.start:s.end]) {
				continue
			}
			s.why = "high-entropy value in an assignment or quoted string"
			claimed = append(claimed, s)
		}
	}

	sort.Slice(claimed, func(i, j int) bool { return claimed[i].start < claimed[j].start })
	return claimed
}

// allowed reports whether value is exempted by the operator's allowlist.
func (r *Redactor) allowed(value string) bool {
	for _, re := range r.allow {
		if re.MatchString(value) {
			return true
		}
	}
	return false
}

// record returns the placeholder for value, registering it on first sight.
// The same value always yields the same placeholder within a session,
// which is what lets the model reason about co-reference.
func (r *Redactor) record(value, ruleID, why string) string {
	key := placeholder(ruleID, value)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total++
	if f, ok := r.byKey[key]; ok {
		f.Count++
		return key
	}
	r.byKey[key] = &Finding{Placeholder: key, RuleID: ruleID, Why: why, Value: value, Count: 1}
	r.order = append(r.order, key)
	return key
}

// placeholder renders the substitution token. The fingerprint is a
// non-reversible 16-bit digest — enough to distinguish the two keys in a
// transcript, far too little to reconstruct either.
func placeholder(ruleID, value string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(value))
	return fmt.Sprintf("[REDACTED:%s:%04x]", ruleID, h.Sum32()&0xffff)
}

// Findings returns what has been masked this session, in first-seen order.
// The returned Findings carry raw secret values: they exist for the
// operator's own eyes in the /redactions overlay and must never be written
// to the session, a log, or a snapshot.
func (r *Redactor) Findings() []Finding {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Finding, 0, len(r.order))
	for _, k := range r.order {
		out = append(out, *r.byKey[k])
	}
	return out
}

// Total is the number of values masked this session, counting repeats —
// the figure the status line shows.
func (r *Redactor) Total() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.total
}

// Unique is the number of distinct secrets masked this session.
func (r *Redactor) Unique() int {
	if r == nil {
		return 0
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.order)
}
