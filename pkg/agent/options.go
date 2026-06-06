package agent

import (
	"context"

	agent_impl "github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/hooks"
	"github.com/johnny1110/evva/pkg/mcp"
	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

// Option mutates an Agent during construction. Downstream callers build
// options via the public With* functions in this package; the internal
// Option type is aliased here so options compose with the bundled cmd/evva
// wiring without duplicate type definitions.
type Option = agent_impl.Option

// WithSink installs the event consumer the agent emits into. nil sinks
// become event.Discard at emit time; pass event.Multi{...} to fan out to
// several consumers (e.g. a TUI plus a JSON-over-stdout bridge).
func WithSink(s event.Sink) Option {
	return agent_impl.WithSink(s)
}

// WithConfig installs the runtime configuration the agent reads from.
// Subagents inherit the parent's config; downstream apps that run
// multiple agents with different AppHome dirs pass distinct *config.Config
// pointers per agent.
//
// Omitting WithConfig boots the agent against config.Get() — the
// historical singleton — so cmd/evva and existing callers don't need to
// change.
func WithConfig(cfg *config.Config) Option {
	return agent_impl.WithConfig(cfg)
}

// WithMaxIterations overrides the agent's loop cap. Pass 0 to keep the
// cfg-derived default. Values in (0, 2) are clamped to 2.
func WithMaxIterations(n int) Option {
	return agent_impl.WithMaxIterations(n)
}

// WithName sets a human-readable label on the agent. Surfaced in logs
// and in subagent panels.
func WithName(name string) Option {
	return agent_impl.WithName(name)
}

// WithStream toggles streaming completions. Overrides the Profile.Stream
// field; useful for tests and downstream apps that want to force the
// buffered or chunked path without rebuilding the profile.
func WithStream(stream bool) Option {
	return agent_impl.WithStream(stream)
}

// WithCustomTool registers a downstream-authored tool on the
// pkg/toolset.DefaultRegistry and adds it to the agent's active list.
// The factory receives the agent's pkg/tools.State at build time so the
// tool can read Config() and Workdir().
//
// Registration is idempotent across agents — calling WithCustomTool with
// the same name in two New calls registers the factory once and reuses
// it.
func WithCustomTool(name tools.ToolName, factory pubtoolset.ToolFactory) Option {
	return agent_impl.WithCustomTool(name, factory)
}

// WithRootContext installs the agent-lifetime context. The signal pump
// goroutine, background bash tasks, and Monitor goroutines all bind to
// this ctx, so cancelling it (or calling Agent.Shutdown) cleans up
// every detached worker the agent ever spawned.
//
// Pass the host's session-level cancellable ctx (e.g. signal.NotifyContext)
// so Ctrl-C reaches every long-lived goroutine.
func WithRootContext(ctx context.Context) Option {
	return agent_impl.WithRootContext(ctx)
}

// WithSkillRegistry installs a pre-built skill catalog on the agent's
// ToolState. The SKILL tool reads through it at Execute time, and the
// agent's system prompt advertises every registered skill on the
// available-skills list.
//
// When omitted, agent.New auto-loads from cfg.AppHomeSkillsDir and
// cfg.WorkDirSkillsDir — the same disk path cmd/evva uses. Pass an
// override for either of:
//
//  1. Programmatic-only catalogs: build with skill.NewRegistry() + Add(...)
//     to ship skills inside the binary via go:embed, fetch them at boot,
//     or generate them on the fly.
//  2. Mixed catalogs: start from skill.LoadRegistry(home, workdir) and
//     Add programmatic extras alongside.
//  3. Suppression: pass skill.NewRegistry() to disable disk auto-load
//     when the host wants the SKILL tool to surface no skills at all.
func WithSkillRegistry(r *skill.Registry) Option {
	return agent_impl.WithSkillRegistry(r)
}

// WithPermissionStore installs the rule store the permission gate consults.
// Build one with permission.NewStore() (empty) or permission.Load(workdir,
// home) to read project + user rules from disk. One Store is shared by the
// root agent and every subagent it spawns.
//
// Omit it to run with no pre-seeded rules — the active permission mode's
// safelist still governs which calls auto-allow, ask, or deny.
func WithPermissionStore(s *permission.Store) Option {
	return agent_impl.WithPermissionStore(s)
}

// WithPersonaRegistry installs the persona catalog the agent resolves through:
// the /profile picker (Agent.ListMainProfiles / SwitchProfile) and the Agent
// tool's subagent kinds. Build one with BuildAgentRegistry (built-ins + disk)
// and Register your own personas before passing it here. A nil registry leaves
// the agent with only the built-in "evva".
func WithPersonaRegistry(r *AgentRegistry) Option {
	var inner *agent_impl.AgentRegistry
	if r != nil {
		inner = r.inner
	}
	return agent_impl.WithAgentRegistry(inner)
}

// WithPersona records the active persona's wire name (e.g. "nono") so
// Agent.ProfileName and the UI render the right label. Pair it with an initial
// Profile from ResolveMainProfile for that same name.
func WithPersona(name string) Option {
	return agent_impl.WithPersona(name)
}

// WithPermissionBroker installs a custom approval back-channel — the seam for
// a downstream allow/deny policy. Build one with permission.NewBroker() and
// register a callback via permission.SetOnRequest that inspects each
// permission.ApprovalRequest and calls Broker.Respond with a Decision.
//
// Omit it and the agent installs a default broker: when a sink is present
// (WithSink) it emits an approval event for an interactive UI to resolve via
// Agent.RespondPermission; with no sink it auto-denies. One Broker is shared
// by the root agent and every subagent.
func WithPermissionBroker(b permission.Broker) Option {
	return agent_impl.WithPermissionBroker(b)
}

// WithHookRegistry installs the lifecycle-hook registry. Populate it with
// hooks.Load(workdir, appHome) to read from .evva/settings.json (project)
// and <appHome>/settings.json (user). nil is safe — the dispatcher noops
// when no registry is present. Shared across the root agent and every
// subagent so one settings.json load drives the whole agent tree.
func WithHookRegistry(r *hooks.Registry) Option {
	return agent_impl.WithHookRegistry(r)
}

// WithMcpManager installs a pre-built MCP connection manager, suppressing
// the one-call agent.New's auto-load. Build it with mcp.Load + mcp.Open and
// call mgr.RegisterFactories(toolset.DefaultRegistry()) before passing it
// here when you want a custom logger, OAuth prompt, or a manager shared
// across agents. Omit it to let New load + open the manager from
// settings.json (and wire the bundled ask_user_question OAuth flow)
// automatically. nil is safe — MCP tools and resources just have nothing
// to surface. Subagents inherit the parent's manager.
func WithMcpManager(m *mcp.Manager) Option {
	return agent_impl.WithMcpManager(m)
}

// Drainer is a pluggable source of out-of-band messages that a running agent
// folds into its current run at each loop iteration boundary. It generalises
// the built-in background-task / monitor drains into a public seam: supply one
// (e.g. a swarm mailbox reader) so a *busy* agent reacts to an incoming message
// mid-run instead of only between runs.
//
// Implementations MUST be non-blocking — return ok=false immediately when there
// is nothing to fold — and are called at most once per boundary on the loop
// goroutine. See WithInboxDrainer.
//
// Experimental: this seam may evolve in a minor release.
type Drainer = agent_impl.Drainer

// WithInboxDrainer installs the inbox Drainer the loop polls at every iteration
// boundary, folding any returned message in as a synthetic user turn before the
// next LLM call. A nil drainer is a no-op, so single-agent callers are
// unaffected. This is the seam evva's swarm uses for mid-run message delivery
// (drain B); see docs/extending.md.
func WithInboxDrainer(d Drainer) Option {
	return agent_impl.WithInboxDrainer(d)
}
