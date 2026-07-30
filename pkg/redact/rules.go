package redact

import "regexp"

// rules.go is the credential-shape table. Each rule recognises one
// well-known secret format by its literal structure — a prefix plus a
// length, almost always — because format matching is the part that can be
// made near-zero-false-positive. The entropy fallback (entropy.go) is the
// deliberately narrower net for everything that has no fixed shape.
//
// Ordering matters: Rules are evaluated in slice order and the FIRST rule
// to claim a byte range wins it (see Redactor.scan). Specific formats must
// therefore precede general ones — a GitHub PAT is also a plausible
// generic assignment value, and it should be reported as the former.
//
// Every rule carries a literal prefilter, and that is not an optimisation
// detail — it is what makes the package usable. A pattern like
// `\bghp_[0-9A-Za-z]{36}\b` has no anchorable prefix, so RE2 walks all
// 100KB of a tool result at ~1.6ms a rule; eighteen of those is 40ms on
// every single tool call. Gating each rule on a `strings.Contains` for the
// literal its pattern cannot match without drops the clean-text path to a
// handful of SIMD scans. Measured in BenchmarkRedactLargeToolResult.
//
// Adding a rule is intended to be cheap and is the expected way this
// package improves: append an entry with its prefilter, add a positive and
// a negative case to the corpus in redact_test.go. The rule ID becomes
// operator-visible inside the placeholder, so it is a name a human reads
// at 3am, not a code.

// Rule is one credential shape.
type Rule struct {
	// ID is the rule's stable name. It appears inside every placeholder
	// this rule produces, so it is part of the operator-facing contract:
	// renaming one changes what shows up in transcripts and in the
	// per-rule disable list.
	ID string

	// Pattern matches the whole credential. When Group is 0 the entire
	// match is redacted; otherwise only that capture group is, which is how
	// assignment shapes keep their key visible ("AWS_SECRET=" stays,
	// the value goes).
	Pattern *regexp.Regexp

	// Group is the submatch index to redact, or 0 for the whole match.
	Group int

	// Why is a short human explanation, shown in the /redactions overlay.
	Why string

	// Needs lists literals that must ALL appear in the text for Pattern to
	// have any chance of matching. NeedsOne lists literals of which at
	// least one must appear. A rule failing either gate is skipped without
	// running its pattern. Getting these wrong makes the rule silently
	// stop firing, so each must be a substring the pattern genuinely
	// cannot match without — the tests assert every rule still catches its
	// positive case through the gate.
	Needs    []string
	NeedsOne []string

	// Fold runs both the prefilter and Pattern against an ASCII-lowercased
	// copy of the text. The copy is byte-for-byte length-preserving, so
	// match offsets stay valid against the original — which is what the
	// redactor slices. Patterns marked Fold are written in lowercase and
	// omit the (?i) flag: case-insensitive RE2 is markedly slower, and
	// this gets the same result for the ASCII these formats live in.
	Fold bool
}

// BuiltinRules is the default table, ordered specific → general.
//
// Every pattern is anchored on a distinctive literal prefix wherever the
// vendor publishes one, because prefix+length is what makes a match a fact
// rather than a guess. Rules without a vendor prefix (the PEM block, the
// generic assignment) carry structural anchors instead.
func BuiltinRules() []Rule {
	return []Rule{
		// --- Cloud provider keys -------------------------------------
		{
			ID:       "aws-access-key",
			Pattern:  regexp.MustCompile(`\b(?:AKIA|ASIA|ABIA|ACCA)[0-9A-Z]{16}\b`),
			NeedsOne: []string{"AKIA", "ASIA", "ABIA", "ACCA"},
			Why:      "AWS access key ID",
		},
		{
			// AWS secret keys have no prefix — 40 chars of base64 alphabet
			// is not distinctive enough on its own, so this rule requires
			// the value to sit next to a naming hint. The bare-40-char case
			// is left to the entropy fallback.
			ID:      "aws-secret-key",
			Pattern: regexp.MustCompile(`aws_?(?:secret|session)_?\w*\s*[:=]\s*["']?([A-Za-z0-9/+=]{40})["']?`),
			Group:   1,
			Needs:   []string{"aws"},
			Fold:    true,
			Why:     "AWS secret access key",
		},
		{
			ID:      "gcp-api-key",
			Pattern: regexp.MustCompile(`\bAIza[0-9A-Za-z\-_]{35}\b`),
			Needs:   []string{"AIza"},
			Why:     "Google API key",
		},
		{
			ID:      "gcp-service-account",
			Pattern: regexp.MustCompile(`"private_key_id"\s*:\s*"([0-9a-f]{40})"`),
			Group:   1,
			Needs:   []string{"private_key_id"},
			Why:     "GCP service-account key ID",
		},

		// --- Source forges -------------------------------------------
		{
			// ghp_ user, gho_ oauth, ghu_/ghs_ app, ghr_ refresh.
			ID:       "github-token",
			Pattern:  regexp.MustCompile(`\bgh[pousr]_[0-9A-Za-z]{36,255}\b`),
			NeedsOne: []string{"ghp_", "gho_", "ghu_", "ghs_", "ghr_"},
			Why:      "GitHub access token",
		},
		{
			ID:      "github-fine-grained",
			Pattern: regexp.MustCompile(`\bgithub_pat_[0-9A-Za-z_]{22,255}\b`),
			Needs:   []string{"github_pat_"},
			Why:     "GitHub fine-grained PAT",
		},
		{
			ID:      "gitlab-token",
			Pattern: regexp.MustCompile(`\bglpat-[0-9A-Za-z\-_]{20,}\b`),
			Needs:   []string{"glpat-"},
			Why:     "GitLab personal access token",
		},

		// --- SaaS ----------------------------------------------------
		{
			ID:      "slack-token",
			Pattern: regexp.MustCompile(`\bxox[abposr]-[0-9A-Za-z\-]{10,}\b`),
			Needs:   []string{"xox"},
			Why:     "Slack token",
		},
		{
			ID:      "slack-webhook",
			Pattern: regexp.MustCompile(`https://hooks\.slack\.com/services/[0-9A-Za-z/+_-]{20,}`),
			Needs:   []string{"hooks.slack.com"},
			Why:     "Slack incoming-webhook URL",
		},
		{
			ID:       "stripe-key",
			Pattern:  regexp.MustCompile(`\b[rs]k_(?:live|test)_[0-9A-Za-z]{16,}\b`),
			NeedsOne: []string{"sk_live_", "sk_test_", "rk_live_", "rk_test_"},
			Why:      "Stripe API key",
		},
		{
			// Ahead of openai-key: "sk-ant-…" also satisfies that rule's
			// broader "sk-" prefix, and the operator should be told which
			// vendor's credential just leaked.
			ID:      "anthropic-key",
			Pattern: regexp.MustCompile(`\bsk-ant-[0-9A-Za-z\-_]{20,}\b`),
			Needs:   []string{"sk-ant-"},
			Why:     "Anthropic API key",
		},
		{
			ID:      "openai-key",
			Pattern: regexp.MustCompile(`\bsk-(?:proj-)?[0-9A-Za-z\-_]{20,}\b`),
			Needs:   []string{"sk-"},
			Why:     "OpenAI-style API key",
		},
		{
			ID:      "npm-token",
			Pattern: regexp.MustCompile(`\bnpm_[0-9A-Za-z]{36}\b`),
			Needs:   []string{"npm_"},
			Why:     "npm access token",
		},

		// --- Structural formats --------------------------------------
		{
			// The whole block, header to footer. Matching only the header
			// would leave the key material in the transcript, which is the
			// entire point of the rule.
			ID:      "private-key",
			Pattern: regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
			Needs:   []string{"PRIVATE KEY"},
			Why:     "PEM private key block",
		},
		{
			// Three base64url segments. The header segment is constrained
			// to start with "eyJ" (base64 of `{"`) so ordinary
			// dot-separated identifiers do not match.
			ID:      "jwt",
			Pattern: regexp.MustCompile(`\beyJ[0-9A-Za-z\-_]{10,}\.[0-9A-Za-z\-_]{10,}\.[0-9A-Za-z\-_]{10,}\b`),
			Needs:   []string{"eyJ"},
			Why:     "JSON Web Token",
		},
		{
			// Credentials embedded in a URL. Only the password is taken —
			// the host stays readable, which is usually what the model
			// actually needed from the line.
			ID:      "url-credentials",
			Pattern: regexp.MustCompile(`\b[a-z][a-z0-9+.\-]*://[^\s:/@]+:([^\s/@]+)@`),
			Group:   1,
			Needs:   []string{"://", "@"},
			Fold:    true,
			Why:     "password in a URL",
		},

		// --- Generic, last ------------------------------------------
		{
			// The catch-all for `.env` shapes: a key whose NAME declares it
			// a secret, assigned a non-trivial value. Keyed on the name
			// rather than the value, so it stays quiet on ordinary config.
			// Placed last so a value that also matches a vendor format is
			// reported under that vendor's rule instead.
			ID:      "secret-assignment",
			Pattern: regexp.MustCompile(`(?m)\b(?:[a-z0-9_]*(?:secret|password|passwd|token|apikey|api_key|access_key|private_key|credential)[a-z0-9_]*)\s*[:=]\s*["']?([^\s"'#,}\]]{8,})["']?`),
			Group:   1,
			NeedsOne: []string{
				"secret", "password", "passwd", "token",
				"apikey", "api_key", "access_key", "private_key", "credential",
			},
			Fold: true,
			Why:  "value of a secret-named variable",
		},
	}
}

// asciiLower returns s with A-Z mapped to a-z and every other byte left
// exactly as it was. Unlike strings.ToLower it is length-preserving by
// construction — no Unicode case folding can change the byte count — which
// is the property that lets Fold rules match against the copy and slice
// the original with the resulting offsets.
func asciiLower(s string) string {
	var buf []byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 'A' || c > 'Z' {
			continue
		}
		if buf == nil {
			buf = []byte(s)
		}
		buf[i] = c + ('a' - 'A')
	}
	if buf == nil {
		return s
	}
	return string(buf)
}
