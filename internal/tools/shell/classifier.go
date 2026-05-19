package shell

import (
	"strings"
)

// Risk classifies a shell command's safety from the gate's perspective.
//
// The gate uses Risk to drive auto mode (RiskReadOnly → allow, RiskDangerous
// → ask with a hint, RiskMutate/RiskUnknown → ask without a hint). Default
// mode treats every risk level as "ask," so a misclassification can't bypass
// the user — at worst it shows a prompt that didn't need to appear.
//
// Conservative bias: RiskUnknown is the catch-all. Anything we can't
// confidently rate as ReadOnly stays Unknown, which forces ask.
type Risk int

const (
	RiskUnknown   Risk = iota // safe fallback: forces ask in auto mode
	RiskReadOnly              // read-only allowlist binary with safe arguments
	RiskMutate                // a non-allowlisted, non-dangerous command
	RiskDangerous             // matches a known code-execution prefix
)

// String returns a stable, human-readable name for the risk level.
func (r Risk) String() string {
	switch r {
	case RiskReadOnly:
		return "read-only"
	case RiskMutate:
		return "mutate"
	case RiskDangerous:
		return "dangerous"
	}
	return "unknown"
}

// Classification is the structured result of Classify.
//
// Matched is the rule entry that triggered the verdict — for ReadOnly it's
// the binary name; for Dangerous it's the prefix that matched. Surfaced in
// the approval UI so the user knows *why* a prompt is showing.
//
// IsCommonFS is an orthogonal flag (NOT a Risk level) set when the binary
// is one of {mkdir, touch, mv, cp, rmdir, ln, chmod, chown}. The gate
// uses it to auto-allow these in accept_edits mode while leaving them as
// regular RiskMutate calls in default mode (which still asks).
type Classification struct {
	Risk       Risk
	IsCommonFS bool
	Matched    string
	Reason     string
}

// dangerousPrefixes is the list of command prefixes that bypass shell safety
// because they execute arbitrary code. Ported from
// `ref/src/utils/permissions/dangerousPatterns.ts` — kept minimal to the
// CROSS_PLATFORM_CODE_EXEC set plus the most common shells and known
// network/exfil binaries. Order matters: longer prefixes are checked first
// (`npm run` before `npm`).
var dangerousPrefixes = []string{
	// Multi-word — must be checked before single-word npm/yarn/pnpm/bun.
	"npm run",
	"yarn run",
	"pnpm run",
	"bun run",
	"gh api",

	// Interpreters.
	"python",
	"python3",
	"python2",
	"node",
	"deno",
	"tsx",
	"ruby",
	"perl",
	"php",
	"lua",

	// Package runners (single word).
	"npx",
	"bunx",

	// Shells (re-entering one of these defeats every check above).
	"bash",
	"sh",
	"zsh",
	"fish",

	// Direct code execution.
	"eval",
	"exec",
	"xargs",
	"sudo",

	// Remote / network / exfil.
	"ssh",
	"curl",
	"wget",
	"gh",

	// Cloud writes / arbitrary remote effects.
	"kubectl",
	"aws",
	"gcloud",
	"gsutil",
}

// readOnlyBinaries are commands whose plain invocations don't mutate the
// filesystem or external state. The classifier accepts these unconditionally
// — flag-level validation (rejecting `-x`-style escape hatches) is a
// future-phase polish.
//
// Conservative selection: only binaries that have no common "write" flag
// surface area. `sed`/`awk` are special-cased below because their `-i`
// flag makes them mutating.
var readOnlyBinaries = map[string]bool{
	"ls":        true,
	"cat":       true,
	"head":      true,
	"tail":      true,
	"wc":        true,
	"sort":      true,
	"uniq":      true,
	"grep":      true,
	"egrep":     true,
	"fgrep":     true,
	"rg":        true,
	"find":      true,
	"fd":        true,
	"file":      true,
	"pwd":       true,
	"echo":      true,
	"printf":    true,
	"basename":  true,
	"dirname":   true,
	"which":     true,
	"type":      true,
	"true":      true,
	"false":     true,
	"date":      true,
	"uname":     true,
	"hostname":  true,
	"whoami":    true,
	"id":        true,
	"cut":       true,
	"tr":        true,
	"fold":      true,
	"fmt":       true,
	"expand":    true,
	"unexpand":  true,
	"od":        true,
	"hexdump":   true,
	"md5sum":    true,
	"sha1sum":   true,
	"sha256sum": true,
	"diff":      true,
	"comm":      true,
	"paste":     true,
	"join":      true,
	"tree":      true,
	"stat":      true,
	"realpath":  true,
	"readlink":  true,
	"env":       true, // bare `env` lists vars; `env FOO=bar cmd` reaches gitReadOnlyGitSubcommand etc.
	"jq":        true,
	"yq":        true,
	"go":        false, // `go` has too many write subcommands; explicitly excluded
}

// commonFSBinaries are filesystem-mutating commands at a level a developer
// using `accept_edits` has typically already opted into. Strictly write-y
// (rm) is intentionally excluded — accept_edits should auto-apply the
// edits the agent is proposing, not nuke things on its own.
var commonFSBinaries = map[string]bool{
	"mkdir": true,
	"touch": true,
	"mv":    true,
	"cp":    true,
	"rmdir": true,
	"ln":    true,
	"chmod": true,
	"chown": true,
}

// gitReadOnlySubcommands is the allowlist of git operations that don't
// mutate the repo or remote. Matches the read-only set from
// `ref/src/utils/shell/readOnlyCommandValidation.ts` (GIT_READ_ONLY_COMMANDS).
var gitReadOnlySubcommands = map[string]bool{
	"status":       true,
	"log":          true,
	"diff":         true,
	"show":         true,
	"blame":        true,
	"branch":       true, // bare `git branch` lists; -d/-D handled in flag check (not implemented v1)
	"tag":          true, // bare lists; deferring -d check to a future tightening
	"remote":       true, // -v / show; deferring add/remove
	"reflog":       true,
	"rev-parse":    true,
	"rev-list":     true,
	"ls-files":     true,
	"ls-tree":      true,
	"ls-remote":    true,
	"cat-file":     true,
	"check-ignore": true,
	"describe":     true,
	"shortlog":     true,
	"whatchanged":  true,
	"grep":         true,
	"help":         true,
	"version":      true,
	"config":       true, // `--get` is read-only; broader use is mutating, deferred
	"name-rev":     true,
	"merge-base":   true,
	"symbolic-ref": true,
}

// Classify inspects a shell command string and returns a structured Risk
// assessment. The classifier:
//
//  1. Splits on safe operators (`|`, `&&`) and recurses on each segment.
//     The combined Risk is the max — a chain is only ReadOnly if every
//     segment is ReadOnly, and is Dangerous if any segment is Dangerous.
//  2. Blocks unsafe operators (`;`, `||`, `>`, `>>`, `<`) — they collapse
//     the command to RiskUnknown so the gate asks.
//  3. Strips leading `VAR=value` env assignments (POSIX prefix; not the
//     same as `env VAR=value cmd`).
//  4. Checks the first non-env token against the dangerous-prefix list.
//  5. Checks the first token against the read-only allowlist.
//
// Empty input is RiskUnknown — defensive.
func Classify(command string) Classification {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return Classification{Risk: RiskUnknown, Reason: "empty command"}
	}

	// Dangerous prefix wins over operator confusion: `eval $(...)` should
	// still surface as Dangerous so the approval UI shows the right hint.
	if d, ok := matchDangerousPrefix(stripEnvAssignments(cmd)); ok {
		return Classification{
			Risk:    RiskDangerous,
			Matched: d,
			Reason:  "matches dangerous prefix",
		}
	}

	if hasUnsafeOperator(cmd) {
		return Classification{
			Risk:   RiskUnknown,
			Reason: "command contains a redirect or unsupported operator",
		}
	}

	segments := splitOnSafeOperators(cmd)
	if len(segments) > 1 {
		combined := classifyChain(segments)
		return combined
	}

	return classifyOne(segments[0])
}

// classifyChain merges per-segment results. Dangerous wins; otherwise
// ReadOnly only if every segment is ReadOnly; otherwise the highest
// non-dangerous risk among segments.
func classifyChain(segments []string) Classification {
	allReadOnly := true
	var any Classification
	for _, s := range segments {
		one := classifyOne(s)
		if one.Risk == RiskDangerous {
			return one
		}
		if one.Risk != RiskReadOnly {
			allReadOnly = false
		}
		// Track the most informative (non-empty) classification for the
		// reason text.
		if any.Risk == RiskUnknown {
			any = one
		}
	}
	if allReadOnly {
		return Classification{Risk: RiskReadOnly, Reason: "all chained commands read-only"}
	}
	if any.Risk == RiskMutate {
		return any
	}
	return Classification{Risk: RiskUnknown, Reason: "chained command has uncertain segments"}
}

func classifyOne(segment string) Classification {
	s := strings.TrimSpace(segment)
	if s == "" {
		return Classification{Risk: RiskUnknown, Reason: "empty segment"}
	}

	tokens := strings.Fields(stripEnvAssignments(s))
	if len(tokens) == 0 {
		return Classification{Risk: RiskUnknown, Reason: "no command after env assignments"}
	}

	bin := tokens[0]
	rest := tokens[1:]

	if d, ok := matchDangerousPrefix(s); ok {
		return Classification{
			Risk:    RiskDangerous,
			Matched: d,
			Reason:  "matches dangerous prefix",
		}
	}

	if bin == "git" {
		if len(rest) == 0 {
			// bare `git` prints usage — safe.
			return Classification{Risk: RiskReadOnly, Matched: "git", Reason: "git (usage)"}
		}
		if gitReadOnlySubcommands[rest[0]] {
			return Classification{
				Risk:    RiskReadOnly,
				Matched: "git " + rest[0],
				Reason:  "git read-only subcommand",
			}
		}
		// Mutating git subcommand (push, commit, add, rm, checkout, ...).
		return Classification{Risk: RiskMutate, Matched: bin, Reason: "git mutating subcommand"}
	}

	if bin == "sed" {
		// `sed -i` writes in place; without -i it's a stream editor.
		for _, a := range rest {
			if a == "-i" || strings.HasPrefix(a, "-i") {
				return Classification{Risk: RiskMutate, Matched: "sed -i", Reason: "sed -i mutates files"}
			}
		}
		return Classification{Risk: RiskReadOnly, Matched: "sed", Reason: "sed (stream-mode)"}
	}

	if bin == "awk" {
		// awk is read-only by default; `awk -i inplace` mutates.
		for i, a := range rest {
			if a == "-i" && i+1 < len(rest) && rest[i+1] == "inplace" {
				return Classification{Risk: RiskMutate, Matched: "awk -i inplace", Reason: "awk inplace mutates files"}
			}
		}
		return Classification{Risk: RiskReadOnly, Matched: "awk", Reason: "awk"}
	}

	if readOnlyBinaries[bin] {
		return Classification{Risk: RiskReadOnly, Matched: bin, Reason: bin + " is read-only"}
	}

	if commonFSBinaries[bin] {
		return Classification{
			Risk:       RiskMutate,
			IsCommonFS: true,
			Matched:    bin,
			Reason:     bin + " is a common fs command",
		}
	}

	return Classification{Risk: RiskMutate, Matched: bin, Reason: "no allowlist match"}
}

// hasUnsafeOperator reports whether the command contains an operator that
// the v1 classifier can't safely reason about. These force RiskUnknown so
// the gate asks. The check is conservative: a `>` inside single quotes
// would still be flagged, but a false-positive ask is harmless.
func hasUnsafeOperator(cmd string) bool {
	unsafeChars := []string{";", "||", ">", "<", "`", "$("}
	for _, op := range unsafeChars {
		if strings.Contains(cmd, op) {
			return true
		}
	}
	return false
}

// splitOnSafeOperators splits a command on `|` and `&&`. These are
// composition operators that don't introduce new attack surface beyond
// what classifyOne already evaluates per segment.
func splitOnSafeOperators(cmd string) []string {
	// Replace && with | then split — both are treated identically downstream.
	cmd = strings.ReplaceAll(cmd, "&&", "|")
	parts := strings.Split(cmd, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// stripEnvAssignments removes leading POSIX env-prefix tokens
// (`FOO=bar BAZ=qux cmd`) so the binary name is the first remaining token.
func stripEnvAssignments(s string) string {
	tokens := strings.Fields(s)
	for len(tokens) > 0 && isEnvAssignment(tokens[0]) {
		tokens = tokens[1:]
	}
	return strings.Join(tokens, " ")
}

func isEnvAssignment(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := tok[i]
		isAlpha := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
		isDigit := c >= '0' && c <= '9'
		if !isAlpha && !isDigit && c != '_' {
			return false
		}
	}
	return true
}

// matchDangerousPrefix returns the dangerous-prefix entry that matches s
// (longest first). A match is "<entry>" or "<entry> ..." — bare-word
// `python` matches `python`, `python3` matches `python3` (because both
// are explicit entries), but `python_script.sh` does NOT match `python`.
func matchDangerousPrefix(s string) (string, bool) {
	for _, p := range dangerousPrefixes {
		if s == p || strings.HasPrefix(s, p+" ") {
			return p, true
		}
	}
	return "", false
}
