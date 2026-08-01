package evalharness

import (
	"fmt"
	"io"
	"strings"
)

// WriteReport renders a run for a terminal or a CI log.
//
// Plain text, no color, no progress artifacts: this output's most important
// reader is a CI log someone opens days later trying to understand why a beta
// was blocked.
func WriteReport(w io.Writer, rep Report) {
	passed, failed, errored := rep.Counts()

	for _, res := range rep.Results {
		switch {
		case res.Err != nil:
			fmt.Fprintf(w, "ERROR %s\n    replay failed: %v\n", res.Fixture.Name, res.Err)
		case res.Diff.Passed():
			fmt.Fprintf(w, "PASS  %s — %s\n", res.Fixture.Name, res.Diff.Summary())
		default:
			fmt.Fprintf(w, "FAIL  %s — %s\n", res.Fixture.Name, res.Diff.Summary())
			fmt.Fprintln(w, res.Diff.Detail())
		}
		if res.Judge != nil {
			verdict := "judge: pass"
			if !res.Judge.Pass {
				verdict = "judge: FAIL (advisory)"
			}
			if res.Judge.Reason != "" {
				verdict += " — " + res.Judge.Reason
			}
			fmt.Fprintf(w, "    %s\n", verdict)
		}
	}

	fmt.Fprintf(w, "\n%d passed, %d failed", passed, failed)
	if errored > 0 {
		fmt.Fprintf(w, ", %d errored", errored)
	}
	fmt.Fprintln(w)

	if failed > 0 {
		fmt.Fprintln(w, "\nA divergence is not automatically a bug — it may be an intended behavior change.")
		fmt.Fprintln(w, "Re-baseline a fixture you meant to change with: evva eval capture --update <name>")
	}
	if judged := countJudged(rep); judged > 0 {
		fmt.Fprintf(w, "\nJudge scored %d fixture(s), advisory only — it does not affect the exit code.\n", judged)
	}
}

func countJudged(rep Report) int {
	n := 0
	for _, r := range rep.Results {
		if r.Judge != nil {
			n++
		}
	}
	return n
}

// ExitCode is the process status for a run: non-zero on any structural
// divergence or replay error, so `evva eval run` drops straight into CI or a
// release preflight with no glue.
func ExitCode(rep Report) int {
	if len(rep.Results) == 0 {
		return 1
	}
	if rep.Passed() {
		return 0
	}
	return 1
}

// FormatFixture renders a fixture for `evva eval list`.
func FormatFixture(f *Fixture) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-28s %2d turn(s), %2d call(s)", f.Name, len(f.UserTurns), len(f.Baseline))
	if f.JudgeEnabled() {
		b.WriteString("  [judge]")
	}
	if f.Description != "" {
		fmt.Fprintf(&b, "  — %s", f.Description)
	}
	return b.String()
}
