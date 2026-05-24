package agent

import (
	"context"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/question"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/hooks"
	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

// customToolEntry pairs a custom tool name with the factory that builds
// instances of it. Collected via WithCustomTool, registered on
// pkg/toolset.DefaultRegistry by agent.New before tools.Build runs.
type customToolEntry struct {
	name    tools.ToolName
	factory pubtoolset.ToolFactory
}

// Option mutates an Agent during construction. Options are applied after the
// profile is materialized but before the LLM client is initialized so any
// option can influence either side without ordering surprises.
type Option func(*Agent)

// WithSink installs the event consumer. nil sinks become event.Discard at
// emit-time; pass event.Multi{...} to fan out to several consumers.
func WithSink(s event.Sink) Option {
	return func(a *Agent) {
		a.sink = s
	}
}

func WithName(name string) Option {
	return func(a *Agent) {
		a.Name = name
	}
}

func WithAsync(async bool) Option {
	return func(a *Agent) {
		a.asyncMode = async
	}
}

// WithStream toggles streaming completions for this agent. Overrides the
// Profile's Stream field; useful for tests and one-off callers that want to
// force the buffered or chunked path without editing the profile.
func WithStream(stream bool) Option {
	return func(a *Agent) {
		a.profile.Stream = stream
	}
}

// WithSkillRegistry installs the merged skill catalog on the agent's
// ToolState before the first turn. The SKILL tool reads through this
// registry at Execute time; passing nil leaves the SKILL tool with no
// skills available.
//
// The same pointer is shared with subagents when the spawner forwards
// it explicitly — today subagent profiles omit SKILL, so this is
// primarily a root-agent concern.
func WithSkillRegistry(r *skill.Registry) Option {
	return func(a *Agent) {
		a.toolState.SetSkillRegistry(r)
	}
}

// WithMaxIterations overrides the agent's loop cap. Pass 0 to keep the
// cfg-derived default (applied after options run in agent.New). Values
// in (0, 2) are clamped to 2 (single-turn agents would never observe a
// tool result).
func WithMaxIterations(n int) Option {
	return func(a *Agent) {
		switch {
		case n == 0:
			// leave a.maxIters at 0 so the New() defaulter picks up cfg.DefaultMaxIterations
		case n < 2:
			a.maxIters.Store(2)
		default:
			a.maxIters.Store(int64(n))
		}
	}
}

// WithConfig installs the runtime configuration the agent reads from.
// Subagents inherit the parent's config via spawn; downstream apps that
// run multiple agents with different home dirs pass distinct *config.Config
// pointers per agent.
//
// Omitting WithConfig boots the agent against config.Get() — the
// historical singleton — so cmd/evva and existing callers don't need to
// change.
func WithConfig(cfg *config.Config) Option {
	return func(a *Agent) {
		a.cfg = cfg
	}
}

// WithCustomTool registers a downstream-authored tool on the
// pkg/toolset.DefaultRegistry and adds it to the agent's active list.
// The factory receives the agent's pkg/tools.State at build time so the
// tool can read Config() and Workdir().
//
// Registration is idempotent across agents — calling WithCustomTool with
// the same name in two New calls registers the factory once and reuses
// it. Use this for tool factories that are private to the downstream
// app; tools authored as part of a shared library should be registered
// directly on pkg/toolset.DefaultRegistry at process startup instead.
//
// The tool's name MUST be unique across all registered tools (built-ins
// and prior customs alike). Re-registering an existing name during agent
// construction does NOT error — the original factory keeps serving.
func WithCustomTool(name tools.ToolName, factory pubtoolset.ToolFactory) Option {
	return func(a *Agent) {
		a.customTools = append(a.customTools, customToolEntry{name: name, factory: factory})
	}
}

// WithPermissionMode sets the agent's initial permission stance. Subagents
// inherit the parent's mode at spawn time; the runtime cycle (Shift+Tab)
// uses Agent.SetPermissionMode.
func WithPermissionMode(m permission.Mode) Option {
	return func(a *Agent) {
		if !m.Valid() {
			return
		}
		a.permissionMode.Store(m)
	}
}

// WithPermissionStore installs the rule store. One process-wide Store is
// built in cmd/evva/main.go and threaded into the root agent and every
// subagent so session rules added in one place are visible everywhere.
func WithPermissionStore(s *permission.Store) Option {
	return func(a *Agent) {
		a.permissionStore = s
	}
}

// WithPermissionBroker installs the approval back-channel. Same pattern
// as WithPermissionStore: one Broker per process, shared by all agents.
// The TUI registers its onRequest callback on this Broker at startup.
func WithPermissionBroker(b permission.Broker) Option {
	return func(a *Agent) {
		a.permissionBroker = b
	}
}

// WithQuestionBroker installs the question back-channel. Same pattern as
// WithPermissionBroker: one Broker per process, shared by all agents.
// The TUI registers its OnRequest callback on this Broker at startup.
func WithQuestionBroker(b question.Broker) Option {
	return func(a *Agent) {
		a.questionBroker = b
	}
}

// WithHookRegistry installs the lifecycle-hook registry. nil is safe —
// the dispatcher noops when no registry is present. Shared across the
// root and its subagents (subagents inherit via spawn.go) so one
// settings.json load drives the whole agent tree.
func WithHookRegistry(r *hooks.Registry) Option {
	return func(a *Agent) { a.hookRegistry = r }
}

// WithAgentRegistry installs the merged built-in + disk agent registry on
// the agent. Subagent spawn resolves through this registry: kinds the
// AGENT tool's schema enum accepts ("explore", "general-purpose") plus any
// disk-loaded subagent the registry surfaces once Phase 6 opens the schema.
//
// Subagents inherit the same pointer when the spawner forwards it
// explicitly — see spawn.go where the parent's registry is threaded into
// child.New so a delegated query (Phase 6's evva → nono pattern) can still
// look up the disk catalog without rebuilding it.
//
// nil clears the registry; in practice the root agent always installs one
// at startup and subagents inherit it, so nil only appears in tests.
func WithAgentRegistry(r *AgentRegistry) Option {
	return func(a *Agent) {
		a.agentRegistry = r
	}
}

// WithPersona records the active persona's wire name on the agent.
// Callers set this from profile-resolution so ProfileName() and the TUI
// status bar render the right label. Empty leaves the field as-is (the
// bootstrap caller is expected to set it explicitly).
func WithPersona(name string) Option {
	return func(a *Agent) {
		if name != "" {
			a.activePersona = name
		}
	}
}

// WithSkillRefs stashes the skill snapshot the agent was bootstrapped
// with so SwitchProfile can rebuild the system prompt with the same
// skill catalog. The snapshot is a flat slice the sysprompt builder
// consumes; the agent does not call into the skill package directly.
func WithSkillRefs(refs []sysprompt.SkillRef) Option {
	return func(a *Agent) {
		a.skillRefs = refs
	}
}

// WithMemorySnapshot stashes the EVVA.md + USER_PROFILE.md snapshot
// loaded at startup. Reused by SwitchProfile when constructing a new
// persona's system prompt — the on-disk files are read once at boot.
func WithMemorySnapshot(snap memdir.Snapshot) Option {
	return func(a *Agent) {
		a.memSnap = snap
		a.memSnapSet = true
	}
}

// WithRootContext installs the agent-lifetime context. The signal pump
// goroutine binds to this ctx; cancelling it (or calling
// Agent.Shutdown) tears the pump down. Background bash tasks and
// Monitor goroutines also bind their per-process ctx as a child of
// this one, so a host that cancels the root ctx cleans up every
// detached worker the agent ever spawned.
//
// When omitted, agent.New derives a fresh background context internally
// — the agent is still healthy but the host loses the ability to shut
// down the pump from outside. cmd/evva and friday-style hosts thread
// the session ctx (signal.NotifyContext) through here so Ctrl-C
// reaches every long-lived goroutine.
func WithRootContext(ctx context.Context) Option {
	return func(a *Agent) {
		if ctx == nil {
			return
		}
		a.rootCtx, a.rootCancel = context.WithCancel(ctx)
	}
}
