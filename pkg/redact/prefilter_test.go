package redact

import (
	"strings"
	"testing"
)

// TestPrefilterNeverHidesAMatch is the safety property the whole
// optimisation rests on: the gate may only skip work, never change an
// outcome. A wrong literal in a rule's Needs/NeedsOne would make that rule
// silently stop firing — the worst possible failure for this package,
// because everything still looks like it works.
//
// For every rule, this runs its pattern directly (ungated) over a corpus
// of real secrets and asserts the gate agrees wherever the pattern matched.
func TestPrefilterNeverHidesAMatch(t *testing.T) {
	samples := []string{
		"export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		`aws_secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`,
		"AIzaSyD-1234567890abcdefghijklmnopqrstu",
		`{"private_key_id": "0123456789abcdef0123456789abcdef01234567"}`,
		"ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A",
		"gho_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A",
		"github_pat_11ABCDEFG0abcdefghijkl_MNOPQRSTUVWXYZ0123456789abcdefgh",
		"glpat-ABCdef123456789012345",
		"xoxp-NOTAREAL-EXAMPLE-000000000000",
		"https://hooks.slack.com/services/NOTAREAL/EXAMPLE/000000000000000000",
		"rk_test_NOTAREALEXAMPLE000",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
		"sk-abcdefghijklmnopqrstuvwxyz0123456789",
		"npm_abcdefghijklmnopqrstuvwxyz0123456789",
		"-----BEGIN EC PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END EC PRIVATE KEY-----",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"redis://user:s3cr3tp4ssw0rd@cache.internal:6379",
		"API_TOKEN=abcd1234efgh5678",
		"MyPassword: hunter2hunter2",
		// Mixed blob — the realistic case.
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\nGITHUB_TOKEN=ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A\n",
	}

	for _, rule := range BuiltinRules() {
		g := newGate(rule)
		for _, s := range samples {
			hay := s
			if rule.Fold {
				hay = asciiLower(s)
			}
			if !rule.Pattern.MatchString(hay) {
				continue // nothing to hide
			}
			if !g.mayPass(buildBigramFilter(s)) {
				t.Errorf("rule %q: bigram gate rejected text its pattern matches:\n%s", rule.ID, s)
			}
			if !g.confirms(hay) {
				t.Errorf("rule %q: literal gate rejected text its pattern matches:\n%s", rule.ID, s)
			}
		}
	}
}

func TestBigramFilterRejectsAbsentLiterals(t *testing.T) {
	f := buildBigramFilter("the quick brown fox jumps over the lazy dog")
	// "ghp_" cannot occur: no "gh" bigram, no "hp", no "p_".
	if f.mayContain("ghp_") {
		t.Error("filter admitted a literal with no bigrams in the text")
	}
	// Present as a real substring.
	if !f.mayContain("quick") {
		t.Error("filter rejected a literal that IS present")
	}
}

func TestBigramFilterIsCaseFolded(t *testing.T) {
	// The filter is built over the original text but checked with
	// lowercased literals, so uppercase source must still be admitted.
	f := buildBigramFilter("AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE")
	if !f.mayContain("akia") {
		t.Error("case-folded lookup missed an uppercase occurrence")
	}
}

func TestBigramFilterHandlesShortInput(t *testing.T) {
	// Degenerate inputs must not panic or produce a filter that rejects
	// everything a Contains would have found.
	for _, s := range []string{"", "a", "ab"} {
		f := buildBigramFilter(s)
		if !f.mayContain("x") {
			t.Errorf("single-char literal should always pass (input %q)", s)
		}
	}
}

func TestAsciiLowerPreservesLength(t *testing.T) {
	// The offsets of every Fold rule's match depend on this. Unicode
	// case folding does NOT preserve length ("İ" lowercases to two runes),
	// which is exactly why strings.ToLower is not used here.
	for _, s := range []string{
		"AWS_SECRET=abc",
		"İstanbul TOKEN=x",
		"日本語 PASSWORD=y",
		"ΣΊΣΥΦΟΣ",
		"",
	} {
		got := asciiLower(s)
		if len(got) != len(s) {
			t.Errorf("asciiLower(%q) changed length %d → %d", s, len(s), len(got))
		}
		if strings.ContainsAny(got, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") {
			t.Errorf("asciiLower(%q) left ASCII uppercase: %q", s, got)
		}
	}
}

func TestAsciiLowerDoesNotCopyWhenUnneeded(t *testing.T) {
	s := "already lowercase, 123"
	if got := asciiLower(s); got != s {
		t.Errorf("got %q", got)
	}
}

// BenchmarkRedactCleanText is the path that actually matters: almost every
// tool result contains no secret at all, and pays only the bigram pass and
// the entropy scan.
func BenchmarkRedactCleanText(b *testing.B) {
	text := strings.Repeat("if err != nil { return fmt.Errorf(\"read config: %w\", err) }\n", 400)
	r, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		r.Redact(text)
	}
}

// BenchmarkRedactDirtyText measures a real .env dump — every rule that
// fires costs a full pattern walk, and this is the price of a hit.
func BenchmarkRedactDirtyText(b *testing.B) {
	text := strings.Repeat(
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE\n"+
			"GITHUB_TOKEN=ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A\n"+
			"DATABASE_URL=postgres://app:s3cr3tp4ssw0rd@db.internal:5432/prod\n", 100)
	r, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		r.Redact(text)
	}
}
