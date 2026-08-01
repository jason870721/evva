package evalharness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/pkg/llm"
)

// judgePrompt frames the scoring task.
//
// Two things in it are load-bearing. It asks about the *expectation* rather
// than about quality, because a judge invited to have opinions about quality
// will find something to dislike in any transcript and the tier becomes noise.
// And it instructs a pass on ambiguity: this is a regression detector, so a
// false alarm costs a human investigation of a non-problem, which is the
// failure mode that gets gates switched off.
const judgePrompt = `You are scoring one automated agent run against a written expectation.

EXPECTATION:
%s

WHAT THE AGENT DID:
%s

FINAL ANSWER:
%s

Did the run satisfy the expectation? Judge ONLY that — not style, not
efficiency, not whether you would have done it differently. If the run
plausibly satisfies the expectation, pass it; only fail when it clearly did
not.

Reply with JSON and nothing else:
{"pass": true|false, "reason": "<one sentence>"}`

// LLMJudge scores runs with a language model.
//
// Advisory by construction: nothing here feeds Result.Passed. It answers "did
// the behavior still do the right thing" for fixtures where the exact
// tool-call shape is not the point — a refusal, a summarization, an
// explanation — which the structural tier cannot see at all.
type LLMJudge struct {
	Client llm.Client
}

// NewLLMJudge builds a judge over any configured provider.
func NewLLMJudge(c llm.Client) *LLMJudge { return &LLMJudge{Client: c} }

// Judge implements Judge.
func (j *LLMJudge) Judge(ctx context.Context, expectation string, trace RunTrace) (JudgeVerdict, error) {
	if j == nil || j.Client == nil {
		return JudgeVerdict{}, fmt.Errorf("evalharness: judge has no client")
	}
	prompt := fmt.Sprintf(judgePrompt,
		strings.TrimSpace(expectation),
		renderActions(trace.ToolCalls),
		strings.TrimSpace(trace.FinalText),
	)
	resp, err := j.Client.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return JudgeVerdict{}, fmt.Errorf("judge call: %w", err)
	}
	return parseVerdict(resp.Content)
}

// renderActions lists what the run did, for the judge's context.
func renderActions(calls []ToolCallSummary) string {
	if len(calls) == 0 {
		return "(no tool calls)"
	}
	lines := make([]string, 0, len(calls))
	for i, c := range calls {
		lines = append(lines, fmt.Sprintf("%d. %s", i+1, c.String()))
	}
	return strings.Join(lines, "\n")
}

// parseVerdict reads the judge's answer, tolerating the fenced-code wrapper
// models habitually add around JSON.
func parseVerdict(raw string) (JudgeVerdict, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		if nl := strings.IndexByte(s, '\n'); nl >= 0 {
			s = s[nl+1:]
		}
		if e := strings.Index(s, "```"); e >= 0 {
			s = s[:e]
		}
		s = strings.TrimSpace(s)
	}
	if i := strings.IndexByte(s, '{'); i > 0 {
		s = s[i:]
	}
	if e := strings.LastIndexByte(s, '}'); e >= 0 && e < len(s)-1 {
		s = s[:e+1]
	}
	var v struct {
		Pass   bool   `json:"pass"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return JudgeVerdict{}, fmt.Errorf("judge returned unparseable verdict %q: %w", truncate(raw, 200), err)
	}
	return JudgeVerdict{Pass: v.Pass, Reason: strings.TrimSpace(v.Reason)}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
