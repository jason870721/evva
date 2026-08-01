package evalharness

import (
	"context"
	"fmt"

	"github.com/johnny1110/evva/pkg/llm"
)

// RunTrace is what one replay produced: the decisions the agent made, and the
// text it finished with.
type RunTrace struct {
	// ToolCalls is the sequence the run produced, in order — the structural
	// tier's entire input.
	ToolCalls []ToolCallSummary
	// FinalText is the agent's last assistant message, handed to the judge
	// when one is enabled.
	FinalText string
	// Transcript is the full message list, for judges that need more context
	// than the final answer.
	Transcript []llm.Message
}

// Runner drives one fixture's user turns through an agent and reports what it
// did.
//
// This is an interface rather than a concrete driver for two reasons. It keeps
// the scoring half of this package — fixtures, structural diff, reporting —
// free of any dependency on the agent loop, which is what lets a swarm-side
// harness import the scorer without dragging the solo runtime behind it. And
// it makes everything here testable without a live provider or an API key,
// which a package whose only entry point constructs a real Agent would not be.
//
// The production implementation lives at the CLI, where wiring a real agent is
// already the job.
type Runner interface {
	// Run executes the turns in order against one agent session and returns
	// what it did. Implementations must go through the real agent construction
	// path — the point of a replay is to exercise the CURRENT system prompt
	// and tool set, so anything that bypasses their assembly measures a
	// configuration nothing in production uses.
	Run(ctx context.Context, turns []string) (RunTrace, error)
}

// Judge scores a run against a fixture's human-written expectation.
type Judge interface {
	Judge(ctx context.Context, expectation string, trace RunTrace) (JudgeVerdict, error)
}

// JudgeVerdict is one judge call's answer.
type JudgeVerdict struct {
	Pass   bool
	Reason string
}

// Result is one fixture's full score.
type Result struct {
	Fixture *Fixture
	Diff    DiffResult
	// Judge is nil unless a judge ran for this fixture.
	Judge *JudgeVerdict
	// Err is set when the replay itself failed (provider error, cancelled
	// run). Distinct from a divergence: a fixture that could not be replayed
	// has produced no evidence either way, and reporting it as a regression
	// would be a lie.
	Err error
}

// Passed reports the hard-gate verdict: the structural tier only.
//
// Judge results deliberately do not affect it. Judge mode is advisory until it
// has enough runs to characterize its false-positive rate — a probabilistic
// scorer wired straight into a release gate produces exactly the flaky
// failures that teach people to bypass gates.
func (r Result) Passed() bool { return r.Err == nil && r.Diff.Passed() }

// Report is a whole run's results.
type Report struct {
	Results []Result
}

// Passed reports whether every fixture passed the structural gate.
func (r Report) Passed() bool {
	for _, res := range r.Results {
		if !res.Passed() {
			return false
		}
	}
	return true
}

// Counts tallies the run.
func (r Report) Counts() (passed, failed, errored int) {
	for _, res := range r.Results {
		switch {
		case res.Err != nil:
			errored++
		case res.Diff.Passed():
			passed++
		default:
			failed++
		}
	}
	return
}

// Options tunes a run.
type Options struct {
	// Judge, when non-nil, scores fixtures carrying an ExpectedOutcome.
	Judge Judge
	// Progress, when non-nil, is called before each fixture so a long run
	// shows movement — every replay is real LLM traffic and can take a while.
	Progress func(name string, index, total int)
}

// Run replays every fixture and scores it.
//
// Fixtures run sequentially rather than concurrently, deliberately: replays
// are real, billable LLM calls, and a parallel fan-out over a fixture set is
// an easy way to hit provider rate limits and turn a regression run into a
// cascade of transport errors that look like regressions.
func Run(ctx context.Context, fixtures []*Fixture, runner Runner, opt Options) (Report, error) {
	if runner == nil {
		return Report{}, fmt.Errorf("evalharness: no runner")
	}
	rep := Report{Results: make([]Result, 0, len(fixtures))}
	for i, f := range fixtures {
		if err := ctx.Err(); err != nil {
			return rep, err
		}
		if opt.Progress != nil {
			opt.Progress(f.Name, i, len(fixtures))
		}
		rep.Results = append(rep.Results, runOne(ctx, f, runner, opt))
	}
	return rep, nil
}

func runOne(ctx context.Context, f *Fixture, runner Runner, opt Options) Result {
	res := Result{Fixture: f}
	trace, err := runner.Run(ctx, f.UserTurns)
	if err != nil {
		res.Err = err
		return res
	}
	res.Diff = Diff(f.Baseline, trace.ToolCalls)

	if opt.Judge != nil && f.JudgeEnabled() {
		v, jerr := opt.Judge.Judge(ctx, f.ExpectedOutcome, trace)
		if jerr != nil {
			// A judge that could not be reached must not fail the fixture —
			// the hard gate is structural, and letting an advisory tier break
			// the run would make the gate less trustworthy, not more.
			v = JudgeVerdict{Pass: true, Reason: "judge unavailable: " + jerr.Error()}
		}
		res.Judge = &v
	}
	return res
}
