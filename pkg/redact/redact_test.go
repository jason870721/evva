package redact

import (
	"strings"
	"sync"
	"testing"
)

// mustNew fails the test rather than returning an error, so table cases
// stay readable.
func mustNew(t *testing.T, opts Options) *Redactor {
	t.Helper()
	r, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

// TestBuiltinRulesCatchRealShapes is the true-positive corpus. Every rule
// in the table needs an entry here; a rule with no positive case is a rule
// nobody has proven works.
//
// FIXTURE HAZARD, learned the hard way: this file is a credential-shaped
// corpus in a public repo, and GitHub's push protection scans it exactly as
// it would scan a leak. The first push of this package was rejected for
// four "secrets" — Slack tokens and webhooks, Stripe keys — that were
// synthetic all along, including Stripe's own published documentation key.
//
// So fixtures must satisfy TWO opposing constraints: loose enough for our
// pattern, tight enough to fail the vendor's. The convention that squares
// them is an embedded NOTAREAL/EXAMPLE marker plus a length that sits
// deliberately outside the vendor's own (a Stripe key is 24+ chars; ours is
// 18, above our own {16,} floor and below theirs). AWS's AKIA…EXAMPLE is
// kept as-is because it is the canonical published non-secret and no
// scanner flags it.
//
// When adding a rule: assume any realistic-looking fixture will be caught,
// and shape it the same way.
func TestBuiltinRulesCatchRealShapes(t *testing.T) {
	cases := []struct {
		rule  string
		input string
		// secret is the substring that must disappear.
		secret string
	}{
		{"aws-access-key", "export AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE", "AKIAIOSFODNN7EXAMPLE"},
		{"aws-secret-key", `aws_secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`, "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"},
		{"gcp-api-key", "url?key=AIzaSyD-1234567890abcdefghijklmnopqrstu", "AIzaSyD-1234567890abcdefghijklmnopqrstu"},
		{"gcp-service-account", `{"private_key_id": "0123456789abcdef0123456789abcdef01234567"}`, "0123456789abcdef0123456789abcdef01234567"},
		{"github-token", "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A", "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"},
		{"github-fine-grained", "github_pat_11ABCDEFG0abcdefghijkl_MNOPQRSTUVWXYZ0123456789abcdefgh", "github_pat_11ABCDEFG0abcdefghijkl_MNOPQRSTUVWXYZ0123456789abcdefgh"},
		{"gitlab-token", "glpat-ABCdef123456789012345", "glpat-ABCdef123456789012345"},
		{"slack-token", "xoxb-NOTAREAL-EXAMPLE-000000000000", "xoxb-NOTAREAL-EXAMPLE-000000000000"},
		{"slack-webhook", "https://hooks.slack.com/services/NOTAREAL/EXAMPLE/000000000000000000", "NOTAREAL/EXAMPLE/000000000000000000"},
		{"stripe-key", "sk_live_NOTAREALEXAMPLE000", "sk_live_NOTAREALEXAMPLE000"},
		{"openai-key", "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789", "sk-proj-abcdefghijklmnopqrstuvwxyz0123456789"},
		{"anthropic-key", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz", "sk-ant-api03-abcdefghijklmnopqrstuvwxyz"},
		{"npm-token", "npm_abcdefghijklmnopqrstuvwxyz0123456789", "npm_abcdefghijklmnopqrstuvwxyz0123456789"},
		{"private-key", "-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----", "MIIEowIBAAKCAQEA"},
		{"jwt", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U", "eyJhbGciOiJIUzI1NiJ9"},
		{"url-credentials", "postgres://admin:hunter2supersecret@db.internal:5432/app", "hunter2supersecret"},
		{"secret-assignment", "DB_PASSWORD=correcthorsebattery", "correcthorsebattery"},
	}

	r := mustNew(t, Options{})
	for _, tc := range cases {
		t.Run(tc.rule, func(t *testing.T) {
			out := r.Redact(tc.input)
			if strings.Contains(out, tc.secret) {
				t.Fatalf("secret survived redaction:\n in: %s\nout: %s", tc.input, out)
			}
			if !strings.Contains(out, "[REDACTED:") {
				t.Fatalf("nothing was redacted: %s", out)
			}
		})
	}
}

// TestEveryBuiltinRuleHasCoverage guards the corpus above from drifting
// behind the table — adding a rule without a positive case fails here.
func TestEveryBuiltinRuleHasCoverage(t *testing.T) {
	r := mustNew(t, Options{})
	fired := map[string]bool{}
	// Re-run the corpus through a fresh redactor and collect rule ids.
	for _, tc := range []string{
		"AKIAIOSFODNN7EXAMPLE",
		`aws_secret_access_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`,
		"AIzaSyD-1234567890abcdefghijklmnopqrstu",
		`{"private_key_id": "0123456789abcdef0123456789abcdef01234567"}`,
		"ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A",
		"github_pat_11ABCDEFG0abcdefghijkl_MNOPQRSTUVWXYZ0123456789abcdefgh",
		"glpat-ABCdef123456789012345",
		"xoxb-NOTAREAL-EXAMPLE-000000000000",
		"https://hooks.slack.com/services/NOTAREAL/EXAMPLE/000000000000000000",
		"sk_live_NOTAREALEXAMPLE000",
		"sk-proj-abcdefghijklmnopqrstuvwxyz0123456789",
		"sk-ant-api03-abcdefghijklmnopqrstuvwxyz",
		"npm_abcdefghijklmnopqrstuvwxyz0123456789",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA\n-----END RSA PRIVATE KEY-----",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",
		"postgres://admin:hunter2supersecret@db.internal:5432/app",
		"DB_PASSWORD=correcthorsebattery",
	} {
		r.Redact(tc)
	}
	for _, f := range r.Findings() {
		fired[f.RuleID] = true
	}
	for _, rule := range BuiltinRules() {
		if !fired[rule.ID] {
			t.Errorf("rule %q has no positive case in the corpus", rule.ID)
		}
	}
}

// TestOrdinaryContentIsUntouched is the false-positive corpus, and it is
// the more important of the two: a redactor that mangles normal work gets
// switched off, and then it protects nothing.
func TestOrdinaryContentIsUntouched(t *testing.T) {
	corpus := []string{
		// Go source.
		"func main() {\n\tfmt.Println(\"hello, world\")\n}",
		`import "github.com/johnny1110/evva/pkg/llm"`,
		"var ErrNotFound = errors.New(\"not found\")",
		// The lockfile case the entropy floor exists to survive.
		`"integrity": "sha512-1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef"`,
		"commit 9f2c8ab4e5d6c7b8a9f0e1d2c3b4a5f6e7d8c9b0",
		`"uuid": "550e8400-e29b-41d4-a716-446655440000"`,
		// Ordinary config that merely looks structured.
		"host: db.internal\nport: 5432\ndatabase: production",
		"LOG_LEVEL=debug",
		"GOOS=linux GOARCH=arm64",
		`{"model": "claude-opus-5", "max_tokens": 8192}`,
		// Prose long enough to trip a naive length check.
		"The quick brown fox jumps over the lazy dog and keeps running for a while.",
		// A path and a URL with no credentials in them.
		"/usr/local/lib/go/src/net/http/transport.go:1234",
		"https://github.com/johnny1110/evva/blob/main/docs/architecture.md",
	}
	r := mustNew(t, Options{})
	for _, in := range corpus {
		if out := r.Redact(in); out != in {
			t.Errorf("false positive:\n in: %s\nout: %s", in, out)
		}
	}
}

func TestPlaceholderIsStableWithinASession(t *testing.T) {
	// The property that lets the model still reason about "the same key is
	// in both files" without ever seeing it.
	r := mustNew(t, Options{})
	a := r.Redact("first file: ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A")
	b := r.Redact("second file: ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A")

	pa, pb := extractPlaceholder(t, a), extractPlaceholder(t, b)
	if pa != pb {
		t.Errorf("same secret produced different placeholders: %s vs %s", pa, pb)
	}
	if r.Unique() != 1 {
		t.Errorf("Unique = %d, want 1", r.Unique())
	}
	if r.Total() != 2 {
		t.Errorf("Total = %d, want 2", r.Total())
	}
}

func TestDistinctSecretsGetDistinctPlaceholders(t *testing.T) {
	r := mustNew(t, Options{})
	a := extractPlaceholder(t, r.Redact("ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"))
	b := extractPlaceholder(t, r.Redact("ghp_999C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"))
	if a == b {
		t.Errorf("two different keys collapsed to one placeholder %s — the model would infer they are the same secret", a)
	}
}

func TestFindingsCarryTheOriginalForTheOperator(t *testing.T) {
	r := mustNew(t, Options{})
	r.Redact("token: ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A")
	fs := r.Findings()
	if len(fs) != 1 {
		t.Fatalf("got %d findings, want 1", len(fs))
	}
	f := fs[0]
	if f.Value != "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A" {
		t.Errorf("Value = %q, want the raw secret", f.Value)
	}
	if f.RuleID != "github-token" || f.Why == "" {
		t.Errorf("finding should name the rule and explain it: %+v", f)
	}
}

func TestSpecificRuleWinsOverGenericAssignment(t *testing.T) {
	// The value matches both github-token and secret-assignment. Table
	// order decides, and the operator should see the specific diagnosis.
	r := mustNew(t, Options{})
	r.Redact("GITHUB_TOKEN=ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A")
	fs := r.Findings()
	if len(fs) != 1 {
		t.Fatalf("got %d findings, want 1 (overlapping matches must merge): %+v", len(fs), fs)
	}
	if fs[0].RuleID != "github-token" {
		t.Errorf("RuleID = %q, want github-token to win over secret-assignment", fs[0].RuleID)
	}
}

func TestAssignmentRuleKeepsTheKeyVisible(t *testing.T) {
	// Masking "DB_PASSWORD" itself would tell the model less than it needs:
	// it should know a password exists and is unavailable, not that some
	// unnamed thing was removed.
	r := mustNew(t, Options{})
	out := r.Redact("DB_PASSWORD=correcthorsebattery")
	if !strings.HasPrefix(out, "DB_PASSWORD=") {
		t.Errorf("the variable name should survive: %s", out)
	}
}

func TestURLCredentialsKeepTheHost(t *testing.T) {
	r := mustNew(t, Options{})
	out := r.Redact("postgres://admin:hunter2supersecret@db.internal:5432/app")
	if !strings.Contains(out, "db.internal:5432/app") || !strings.Contains(out, "admin") {
		t.Errorf("host and user should survive so the line stays useful: %s", out)
	}
	if strings.Contains(out, "hunter2supersecret") {
		t.Errorf("password survived: %s", out)
	}
}

func TestMultipleSecretsInOneBlob(t *testing.T) {
	env := strings.Join([]string{
		"# production",
		"AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
		"GITHUB_TOKEN=ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A",
		"LOG_LEVEL=info",
		"STRIPE_KEY=sk_live_NOTAREALEXAMPLE000",
	}, "\n")
	r := mustNew(t, Options{})
	out := r.Redact(env)

	for _, secret := range []string{"AKIAIOSFODNN7EXAMPLE", "ghp_016C7869", "sk_live_NOTAREALEXAMPLE000"} {
		if strings.Contains(out, secret) {
			t.Errorf("%s survived:\n%s", secret, out)
		}
	}
	if !strings.Contains(out, "LOG_LEVEL=info") {
		t.Errorf("non-secret line was damaged:\n%s", out)
	}
	if !strings.Contains(out, "# production") {
		t.Errorf("comment was damaged:\n%s", out)
	}
	if n := r.Unique(); n != 3 {
		t.Errorf("Unique = %d, want 3: %+v", n, r.Findings())
	}
}

func TestAllowlistExemptsAValue(t *testing.T) {
	// The fixture-file case: a repo full of documented example keys.
	r := mustNew(t, Options{Allow: []string{`^AKIAIOSFODNN7EXAMPLE$`}})
	in := "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE"
	if out := r.Redact(in); out != in {
		t.Errorf("allowlisted value was redacted: %s", out)
	}
}

func TestDisableTurnsOffOneRule(t *testing.T) {
	r := mustNew(t, Options{Disable: []string{"github-token"}})
	out := r.Redact("ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A")
	if strings.Contains(out, "[REDACTED:github-token") {
		t.Errorf("disabled rule still fired: %s", out)
	}
}

func TestDisableRejectsAnUnknownRule(t *testing.T) {
	// A typo in config must fail loudly at startup — silently ignoring it
	// leaves the operator believing a rule is off when it is on, or worse,
	// believing redaction is configured when the whole block was dropped.
	_, err := New(Options{Disable: []string{"githubb-token"}})
	if err == nil {
		t.Fatal("want an error for an unknown rule id")
	}
	if !strings.Contains(err.Error(), "github-token") {
		t.Errorf("the error should list what IS available, got: %v", err)
	}
}

func TestBadAllowPatternIsRejected(t *testing.T) {
	if _, err := New(Options{Allow: []string{"("}}); err == nil {
		t.Fatal("want an error for an uncompilable allow pattern")
	}
}

func TestNilRedactorIsANoOp(t *testing.T) {
	// "Redaction off" is represented by a nil *Redactor, so every method
	// must tolerate it rather than forcing a branch at each call site.
	var r *Redactor
	if got := r.Redact("ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A"); got != "ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A" {
		t.Errorf("nil redactor modified text: %q", got)
	}
	if r.Findings() != nil || r.Total() != 0 || r.Unique() != 0 {
		t.Error("nil redactor should report nothing")
	}
}

func TestRedactIsConcurrencySafe(t *testing.T) {
	// evva dispatches tool calls in parallel goroutines; they share one
	// Redactor. Run with -race.
	r := mustNew(t, Options{})
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if i%2 == 0 {
				r.Redact("ghp_016C7869F0BE4A1E9C2F3D5A7B8E0D2C4F6A")
			} else {
				r.Redact("AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE")
			}
			_ = r.Findings()
		})
	}
	wg.Wait()
	if r.Total() != 20 {
		t.Errorf("Total = %d, want 20", r.Total())
	}
	if r.Unique() != 2 {
		t.Errorf("Unique = %d, want 2", r.Unique())
	}
}

func TestEmptyAndCleanInputAreCheap(t *testing.T) {
	r := mustNew(t, Options{})
	if got := r.Redact(""); got != "" {
		t.Errorf("empty input returned %q", got)
	}
	clean := "nothing to see here"
	if got := r.Redact(clean); got != clean {
		t.Errorf("clean input was rewritten: %q", got)
	}
	if r.Total() != 0 {
		t.Errorf("clean input recorded %d findings", r.Total())
	}
}

// BenchmarkRedactLargeToolResult pins the PRD's < 1ms-on-100KB budget.
func BenchmarkRedactLargeToolResult(b *testing.B) {
	var sb strings.Builder
	for sb.Len() < 100*1024 {
		sb.WriteString("func handler(w http.ResponseWriter, r *http.Request) error {\n")
		sb.WriteString("\tif err := svc.Do(r.Context()); err != nil { return err }\n")
		sb.WriteString("\treturn json.NewEncoder(w).Encode(map[string]any{\"ok\": true})\n}\n")
	}
	text := sb.String()
	r, err := New(Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for b.Loop() {
		r.Redact(text)
	}
}

// extractPlaceholder pulls the first [REDACTED:...] token out of s.
func extractPlaceholder(t *testing.T, s string) string {
	t.Helper()
	i := strings.Index(s, "[REDACTED:")
	if i < 0 {
		t.Fatalf("no placeholder in %q", s)
	}
	j := strings.Index(s[i:], "]")
	if j < 0 {
		t.Fatalf("unterminated placeholder in %q", s)
	}
	return s[i : i+j+1]
}
