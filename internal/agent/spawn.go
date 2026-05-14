package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/internal/agent/event"
	//"github.com/johnny1110/evva/internal/agent/profiles"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/meta"
)

// Spawn implements meta.SubagentSpawner. The AGENT tool's lookup resolves
// to *Agent (the root agent registers itself via SetSubagentSpawner in New).
//
// Spawn:
//  1. Rejects calls from a subagent (the "main only" invariant).
//  2. Picks a Profile via subagentProfile, inheriting the parent's
//     provider, options, and any baseline preferences.
//  3. Overrides the model based on req.Level via
//     LLMProvider.ModelForLevel — 1 is the normal tier, 2 is the big tier.
//  4. Constructs a child agent with event.BubbleUp routing its events back
//     to the parent's Sink, tagged with the parent's AgentID.
//  5. Emits KindSubagent {Started} → child.Run → KindSubagent {Ended}.
//  6. Returns the child's final assistant text on success. ErrIterLimit on
//     a subagent is downgraded to a soft note appended to the partial reply
//     so the parent run can recover.
func (a *Agent) Spawn(ctx context.Context, req meta.SpawnRequest) (string, error) {
	if a.IsSubagent() {
		return "", meta.ErrSubagentForbidden
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return "", fmt.Errorf("spawn: empty prompt")
	}

	subProfile, err := subagentProfile(a.profile, req.Kind)
	if err != nil {
		return "", err
	}
	// Pick the model for the requested capability tier. Level 0 (the JSON
	// zero when the field is omitted) defaults to 1 inside ModelForLevel.
	subProfile.LLMModel = a.profile.LLMProvider.ModelForLevel(req.Level)

	childSink := event.BubbleUp{Parent: a.Sink(), ParentID: a.ID}
	// new a sub agent
	child, err := New(subProfile,
		WithName(req.Name),
		WithSink(childSink),
		AsSubagent(a.ID),
		WithMaxIterations(a.maxIters),
		WithAsync(req.AsyncMode),
	)
	if err != nil {
		return "", fmt.Errorf("spawn: new agent: %w", err)
	}

	summary := truncateSummary(req.Prompt, 100)

	// ------------------------------------------------------------
	a.ToolState().AgentGroupPanel().Add(child.Name, child.ID, subProfile.Type.String(), summary)
	emitSubagent(child, req.Kind, summary, event.SubagentInit)
	// TODO: In evva v2.0 support sync/async agent mode as new feature, if req.AsyncMode = true
	// update subagent status in toolState
	resp, runErr := child.Run(ctx, req.Prompt)
	emitSubagent(child, req.Kind, summary, event.SubagentEnded)
	if runErr != nil {
		a.ToolState().AgentGroupPanel().Crush(child.ID, runErr)
	} else {
		a.ToolState().AgentGroupPanel().Done(child.ID, resp.Content)
	}
	// ------------------------------------------------------------

	if runErr != nil {
		// Subagent paused at iter limit — return the partial so the parent
		// can keep working. Other errors propagate.
		if errors.Is(runErr, ErrIterLimit) {
			return resp.Content + "\n[subagent paused at iteration limit]", nil
		}
		return "", runErr
	}

	return resp.Content, nil
}

// subagentProfile builds a Profile for a subagent of the given kind,
// inheriting the parent's LLM provider/model/options. Subagent profiles
// deliberately do NOT include the AGENT tool — the "subagents cannot
// spawn subagents" invariant is enforced both here (no AGENT in tool list)
// and in Spawn itself (IsSubagent check).
//
// Unknown kinds are an error the caller surfaces to the model.
func subagentProfile(parent Profile, kind string) (Profile, error) {

	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "explore":
		// read-only
		return Explore(parent.LLMProvider, parent.LLMModel, parent.LLMOptions), nil
	case "general-purpose", "general", "":
		toolNames := []tools.ToolName{
			tools.READ_FILE, tools.WRITE_FILE, tools.EDIT_FILE,
			tools.BASH, tools.WEB_SEARCH, tools.WEB_FETCH,
		}
		return General(parent.LLMProvider, parent.LLMModel, parent.LLMOptions, toolNames...), nil
	case "teammate":
		// TODO: a strong agent, not a typical subagent. It have same permission as main agent, and free to do his own job in async mode.
		return Profile{}, fmt.Errorf("not support subagent_type in current version %q (want \"explore\" or \"general-purpose\")", kind)
	default:
		return Profile{}, fmt.Errorf("unknown subagent_type %q (want \"explore\" or \"general-purpose\")", kind)
	}
}

func emitSubagent(a *Agent, kind, summary string, phase event.SubagentPhase) {
	a.emit(event.KindSubagent, func(e *event.Event) {
		e.Subagent = &event.SubagentPayload{
			SubagentID:    a.ID,
			AgentType:     kind,
			PromptSummary: summary,
			Phase:         phase,
		}
	})
}

func truncateSummary(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
