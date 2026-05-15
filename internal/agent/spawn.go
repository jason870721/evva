package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/meta"
)

// Spawn implements meta.SubagentSpawner. The AGENT tool's lookup resolves
// to *Agent (the root agent registers itself via SetSubagentSpawner in New).
//
// Spawn:
//  1. Rejects calls from a subagent (the "main only" invariant).
//  2. Picks a Profile via subagentProfile, inheriting the ParentID's
//     provider, options, and any baseline preferences.
//  3. Overrides the model based on req.Level via
//     LLMProvider.ModelForLevel — 1 is the normal tier, 2 is the big tier.
//  4. Constructs a child agent with event.BubbleUp routing its events back
//     to the ParentID's Sink, tagged with the ParentID's AgentID.
//  5. Registers the child in the ParentID's SpawnGroup panel — every
//     mutation is observable through the unified ToolState change stream.
//  6. Runs the child:
//     - Sync mode: blocks until child.Run completes, removes from panel
//     on return, and propagates the child's text through the tool result.
//     - Async mode: spawns a goroutine that runs the child and marks the
//     panel entry Report / Crushed on exit. Returns immediately with an
//     ack message; the ParentID loop will pick up the eventual result via
//     AgentGroup.DrainCompleted between turns.
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
	child, err := New(a, subProfile,
		WithName(req.Name),
		WithSink(childSink),
		WithMaxIterations(a.maxIters), // share iters with child
		WithAsync(req.AsyncMode),
	)
	if err != nil {
		return "", fmt.Errorf("spawn: new agent: %w", err)
	}

	group := a.ToolState().AgentGroup()
	group.Add(child.Name, child.ID, subProfile.Type.String(), req.Desc, req.AsyncMode)

	if req.AsyncMode {
		// Detach: run the child in a goroutine, mark the group entry on
		// exit. The ParentID's main loop picks the result up via
		// DrainCompleted between turns. We deliberately pass the ParentID's
		// ctx so a top-level cancel reaches the child.
		go func() {
			resp, runErr := child.Run(ctx, req.Prompt)
			switch {
			case runErr != nil && errors.Is(runErr, ErrIterLimit):
				group.Report(child.ID, resp.Content+"\n[subagent paused at iteration limit]")
			case runErr != nil:
				group.Crush(child.ID, runErr)
			default:
				group.Report(child.ID, resp.Content)
			}
		}()
		return fmt.Sprintf("subagent %s(%s) spawned in background; its summary will be delivered on a later turn (do not assume any result here).", child.Name, child.ID), nil
	}

	// Sync path: block on the child. Result is delivered via this return
	// value (which the tool dispatcher hands back to the model as the
	// AGENT tool_result). The group entry is short-lived — we update the
	// phase, then Remove so DrainCompleted never sees a sync entry.
	resp, runErr := child.Run(ctx, req.Prompt)

	if runErr != nil {
		if errors.Is(runErr, ErrIterLimit) {
			// iters max
			group.Report(child.ID, resp.Content)
			group.Remove(child.ID)
			return resp.Content + "\n[subagent paused at iteration limit]", nil
		}
		// sys crush
		group.Crush(child.ID, runErr)
		group.Remove(child.ID)
		return "[subagent crushed due to system error]", runErr
	}
	// success report
	group.Report(child.ID, resp.Content)
	group.Remove(child.ID)
	return resp.Content, nil
}

// subagentProfile builds a Profile for a subagent of the given kind,
// inheriting the ParentID's LLM provider/model/options. Subagent profiles
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

func truncateSummary(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
