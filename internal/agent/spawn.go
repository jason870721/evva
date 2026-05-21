package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
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

	subProfile, err := a.subagentProfile(req.Kind)
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
		WithMaxIterations(int(a.maxIters.Load())), // share iters with child
		WithAsync(req.AsyncMode),
		WithAgentRegistry(a.agentRegistry), // subagents inherit the parent's registry
		WithPersona(req.Kind),              // record the subagent kind for ProfileName()
		WithPermissionMode(a.PermissionMode()),
		WithPermissionStore(a.permissionStore),
		WithPermissionBroker(a.permissionBroker),
	)
	if err != nil {
		return "", fmt.Errorf("spawn: new agent: %w", err)
	}

	group := a.ToolState().AgentGroup()
	group.Add(child.Name, child.ID, subProfile.Type.String(), req.Desc, req.AsyncMode)

	if child.IsAsync() {
		// Detach: run the child in a goroutine, mark the group entry on
		// exit. The ParentID's main loop picks the result up via
		// DrainCompleted between turns. We deliberately pass the ParentID's
		// ctx so a top-level cancel reaches the child.
		go func() {
			resp, runErr := child.Run(ctx, req.Prompt)
			if runErr != nil {
				// Mark the panel entry so DrainCompleted can deliver
				// the failure back to the parent's next turn. iter-limit
				// is a distinct phase from a real crash — surface both.
				if errors.Is(runErr, ErrIterLimit) {
					group.Crush(child.ID, "[subagent paused at iteration limit]", runErr)
				} else {
					group.Crush(child.ID, "[subagent crushed]", runErr)
				}
				a.logger.Error("subagent crashed", "name", child.Name, "err", runErr)
				return
			}
			a.logger.Debug("subagent done", "name", child.Name, "resp", truncateSummary(resp, 100))
			// Report the result so the parent's loop drains it on its
			// next iteration. Do NOT Remove here — async results live
			// in the panel until DrainCompleted picks them up.
			group.Report(child.ID, resp)
		}()
		return fmt.Sprintf("subagent %s(%s) spawned in background; its done will be delivered on a later turn (do not assume any result here).", child.Name, child.ID), nil
	}

	// Sync path: block on the child. Result is delivered via this return
	// value (which the tool dispatcher hands back to the model as the
	// AGENT tool_result). The group entry is short-lived — we update the
	// phase, then Remove so DrainCompleted never sees a sync entry.
	resp, runErr := child.Run(ctx, req.Prompt)

	if runErr != nil {
		if errors.Is(runErr, ErrIterLimit) {
			// iters max
			group.Crush(child.ID, "[subagent paused at iteration limit]", runErr)
			group.Remove(child.ID)
			return resp + "\n [subagent paused at iteration limit]", nil
		}
		// sys crush
		group.Crush(child.ID, "[subagent crushed]", runErr)
		group.Remove(child.ID)
		return "[subagent crushed due to system error]", runErr
	}
	a.logger.Debug("subagent done", "name", child.Name, "resp", truncateSummary(resp, 100))
	// success report
	group.Report(child.ID, resp)
	group.Remove(child.ID)
	return resp, nil
}

// subagentProfile builds a Profile for a subagent of the given kind,
// inheriting the parent's LLM provider/model/options. Resolution routes
// through the parent's agentRegistry — built-ins (Explore, General) and
// disk-loaded definitions are looked up the same way. The Agent tool's
// schema enum stays closed in Phase 2, so in practice only built-ins
// arrive here from the wire; Phase 6 opens the enum to disk agents.
//
// Subagent profiles deliberately do NOT include the AGENT tool — the
// "subagents cannot spawn subagents" invariant is enforced both here
// (no AGENT in tool list) and in Spawn itself (IsSubagent check).
//
// The system prompt is intentionally NOT inherited from the parent —
// each subagent profile builds its own via the sysprompt package so a
// subagent never accidentally adopts the root's full harness.
//
// Unknown kinds are an error the caller surfaces to the model.
func (a *Agent) subagentProfile(kind string) (Profile, error) {
	cfg := config.Get()
	// Strip any system-prompt option the parent picked up. The subagent
	// constructor will append its own WithSystem; leaving the parent's in
	// the slice would let it override the subagent's via "last write wins"
	// in llm.Apply.
	inherited := withoutSystemOption(a.profile.LLMOptions)
	k := strings.ToLower(strings.TrimSpace(kind))

	// Empty kind defaults to general-purpose, matching the AGENT tool's
	// documented behavior.
	if k == "" || k == "general" {
		k = "general-purpose"
	}

	// Teammate is a future agent class with main-agent permissions; reject
	// explicitly so the model can't accidentally invoke it before its
	// implementation lands (currently planned for a post-Phase-6 phase).
	if k == "teammate" {
		return Profile{}, fmt.Errorf("subagent_type %q is reserved and not yet implemented", kind)
	}

	// Built-in fast paths. Explore, General, and Plan are constructed via
	// their dedicated Profile constructors which carry hard-coded tool
	// lists and sysprompt builders — duplicating that here would diverge
	// over time.
	switch k {
	case "explore":
		return Explore(cfg, a.profile.LLMProvider, a.profile.LLMModel, inherited), nil
	case "plan":
		return Plan(cfg, a.profile.LLMProvider, a.profile.LLMModel, inherited), nil
	case "general-purpose":
		toolNames := []tools.ToolName{
			tools.TREE,
			tools.READ_FILE, tools.WRITE_FILE, tools.EDIT_FILE,
			tools.BASH, tools.WEB_SEARCH, tools.WEB_FETCH,
			tools.JSON_QUERY, tools.CALC,
		}
		return General(cfg, a.profile.LLMProvider, a.profile.LLMModel, inherited, toolNames...), nil
	}

	// Disk-loaded subagent path. Requires an AgentRegistry; without one
	// (test harness, legacy callers) we can't resolve disk agents and the
	// kind is unknown.
	if a.agentRegistry == nil {
		return Profile{}, fmt.Errorf("unknown subagent_type %q (want \"explore\", \"plan\", or \"general-purpose\")", kind)
	}
	def, ok := a.agentRegistry.Get(k)
	if !ok || !def.IsSubagent() {
		return Profile{}, fmt.Errorf("unknown subagent_type %q (want \"explore\", \"plan\", or \"general-purpose\")", kind)
	}
	return profileFromDiskAgent(def, cfg, a.profile.LLMProvider, a.profile.LLMModel, inherited), nil
}

// withoutSystemOption filters out any llm.WithSystem entries from opts. The
// agent profile constructors append a fresh WithSystem at the end of the
// option list, but the parent's slice may already carry one — if we passed
// the parent's WithSystem and our own to the same Apply, the subagent
// could end up with the wrong prompt because option ordering across slices
// is not guaranteed beyond "last applied wins". Stripping up front
// guarantees the subagent's own prompt is the only one in play.
//
// Detection is by sentinel: build a probe LLMParams, apply each option,
// and drop any option that touches System. Cheap; runs once per spawn.
func withoutSystemOption(opts []llm.Option) []llm.Option {
	if len(opts) == 0 {
		return nil
	}
	out := make([]llm.Option, 0, len(opts))
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		var probe llm.LLMParams
		opt(&probe)
		if probe.System != "" {
			continue
		}
		out = append(out, opt)
	}
	return out
}

func truncateSummary(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// profileFromDiskAgent constructs a Profile from a disk-loaded
// AgentDefinition. The definition supplies the system prompt body
// (system_prompt.md) and the tool lists (tools.yml); LLM provider / model
// inherit from the parent's profile so the disk agent runs under whatever
// the user picked at session start. The model override from meta.yml is
// ignored in Phase 2 — provider-specific model strings need a resolver
// that doesn't exist yet (Phase 6 may add it).
//
// Lives in spawn.go so the sysprompt package doesn't depend on the agent
// package (Profile lives here; AgentDefinition lives in sysprompt).
func profileFromDiskAgent(def sysprompt.AgentDefinition, _ *config.AppConfig, provider constant.LLMProvider, model constant.Model, inherited []llm.Option) Profile {
	ctx := sysprompt.PromptContext{} // disk agents capture their body verbatim; PromptContext is unused for them
	sp := def.BuildSystemPrompt(ctx)
	opts := append(inherited, llm.WithSystem(sp))
	return Profile{
		Type:          GENERAL_PURPOSE, // closest existing label; Phase 6 may introduce DISK_AGENT
		SystemPrompt:  sp,
		ActiveTools:   def.ActiveTools,
		DeferredTools: def.DeferredTools,
		LLMProvider:   provider,
		LLMModel:      model,
		LLMOptions:    opts,
	}
}
