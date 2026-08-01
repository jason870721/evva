package evalharness

import (
	"fmt"
	"strings"
)

// DivergenceKind classifies how a replay departed from its baseline.
type DivergenceKind string

const (
	// KindChanged: the call at this position is a different decision.
	KindChanged DivergenceKind = "changed"
	// KindMissing: the baseline made a call the replay did not.
	KindMissing DivergenceKind = "missing"
	// KindExtra: the replay made a call the baseline did not.
	KindExtra DivergenceKind = "extra"
)

// Divergence is one point of departure between a baseline and a replay.
type Divergence struct {
	Kind     DivergenceKind
	Index    int    // position in the tool-call sequence
	Expected string // baseline call, rendered ("" for KindExtra)
	Actual   string // replay call, rendered ("" for KindMissing)
}

func (d Divergence) String() string {
	switch d.Kind {
	case KindMissing:
		return fmt.Sprintf("call %d: expected %s, but the run made no such call", d.Index+1, d.Expected)
	case KindExtra:
		return fmt.Sprintf("call %d: unexpected %s (baseline had nothing here)", d.Index+1, d.Actual)
	default:
		return fmt.Sprintf("call %d: expected %s, got %s", d.Index+1, d.Expected, d.Actual)
	}
}

// DiffResult is the structural comparison of one replay against its baseline.
type DiffResult struct {
	Divergences []Divergence
	BaselineLen int
	ActualLen   int
}

// Passed reports whether the replay matched structurally.
func (r DiffResult) Passed() bool { return len(r.Divergences) == 0 }

// Summary renders a one-line verdict.
func (r DiffResult) Summary() string {
	if r.Passed() {
		return fmt.Sprintf("%d tool call(s), matching baseline", r.ActualLen)
	}
	return fmt.Sprintf("%d divergence(s) (baseline %d calls, run %d calls)",
		len(r.Divergences), r.BaselineLen, r.ActualLen)
}

// Detail renders the full divergence list, one per line.
func (r DiffResult) Detail() string {
	if r.Passed() {
		return ""
	}
	lines := make([]string, 0, len(r.Divergences))
	for _, d := range r.Divergences {
		lines = append(lines, "    "+d.String())
	}
	return strings.Join(lines, "\n")
}

// Diff compares a replay's tool-call sequence against a fixture's baseline.
//
// Positional, not set-based, and deliberately so: *order* is a decision. An
// agent that runs the tests before editing rather than after has changed its
// behavior even though the multiset of calls is identical, and that is exactly
// the class of regression a prompt edit causes.
//
// The comparison stops reporting after the sequences realign structurally
// (see below) — a single early insertion should read as one divergence, not
// as every subsequent call being "wrong".
func Diff(baseline, actual []ToolCallSummary) DiffResult {
	res := DiffResult{BaselineLen: len(baseline), ActualLen: len(actual)}

	i, j := 0, 0
	for i < len(baseline) && j < len(actual) {
		if baseline[i].Equal(actual[j]) {
			i, j = i+1, j+1
			continue
		}
		// Try to resynchronize: if the actual run inserted a call, the
		// baseline's current entry shows up shortly after; if it skipped one,
		// the reverse. Reporting the shift once and realigning keeps a report
		// legible, which is what makes it act on.
		if skip := findMatch(baseline[i], actual[j:]); skip > 0 {
			for k := range skip {
				res.Divergences = append(res.Divergences, Divergence{
					Kind: KindExtra, Index: j + k, Actual: actual[j+k].String(),
				})
			}
			j += skip
			continue
		}
		if skip := findMatch(actual[j], baseline[i:]); skip > 0 {
			for k := range skip {
				res.Divergences = append(res.Divergences, Divergence{
					Kind: KindMissing, Index: i + k, Expected: baseline[i+k].String(),
				})
			}
			i += skip
			continue
		}
		res.Divergences = append(res.Divergences, Divergence{
			Kind: KindChanged, Index: i, Expected: baseline[i].String(), Actual: actual[j].String(),
		})
		i, j = i+1, j+1
	}
	for ; i < len(baseline); i++ {
		res.Divergences = append(res.Divergences, Divergence{
			Kind: KindMissing, Index: i, Expected: baseline[i].String(),
		})
	}
	for ; j < len(actual); j++ {
		res.Divergences = append(res.Divergences, Divergence{
			Kind: KindExtra, Index: j, Actual: actual[j].String(),
		})
	}
	return res
}

// findMatch returns how far into seq the first match for want sits, or 0 when
// it is absent (or already at the head). The window is bounded: beyond a few
// calls, "it reappears later" stops meaning the sequences are the same shape
// and starts meaning the run genuinely diverged.
func findMatch(want ToolCallSummary, seq []ToolCallSummary) int {
	const window = 4
	limit := min(len(seq), window)
	for k := 1; k < limit; k++ {
		if want.Equal(seq[k]) {
			return k
		}
	}
	return 0
}
