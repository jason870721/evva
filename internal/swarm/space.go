package swarm

import (
	"context"
	"fmt"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/event"
)

// SpacedEvent tags an agent event with the space it came from. AgentID is
// already on the event (set by the agent), so (SpaceID, Event.AgentID) is the
// full routing key the service (SPRD-1-8) fans out on.
type SpacedEvent struct {
	SpaceID string
	Event   event.Event
}

// ToolSet is the dependency-injection seam SwarmSpace uses to attach the swarm
// custom tools (send_message, task_*, list_members) to each agent at
// construction. Implemented by internal/swarm/tools (SPRD-1-7) and passed in,
// so this package carries no hard dependency on the concrete tools. For returns
// the agent.Options (typically WithCustomTool(...)) for one member.
type ToolSet interface {
	For(name string, role agentdef.Role, sp *SwarmSpace) []agent.Option
}

// noToolSet attaches nothing — the M0 default until SPRD-1-7 lands.
type noToolSet struct{}

func (noToolSet) For(string, agentdef.Role, *SwarmSpace) []agent.Option { return nil }

// SwarmSpace is one live, isolated sub-cluster: its own workdir, .vero store,
// Roster, and constructed agent handles. Two spaces in one process share
// nothing — separate stores, rosters, and event streams — and member names are
// scoped per space.
type SwarmSpace struct {
	ID      string
	Name    string
	Workdir string
	Store   *store.Store
	Roster  *Roster

	cancel context.CancelFunc
	out    chan SpacedEvent
	agents map[string]agent.Agent
}

// out channel buffer. The service/test must drain Events() continuously; the
// per-agent sink does a blocking send (backpressure beats event loss, per the
// pkg/event contract), so a generous buffer keeps a short run from stalling.
const eventBuffer = 1024

// NewSpace assembles a live space from a manifest and its loaded agent
// definitions: it opens the per-space store, constructs each member via
// agent.New against per-agent config clones, wires each agent's event.Sink to
// stamp the spaceID, and populates the Roster. After this returns, every member
// is active + idle and addressable by name — no scheduling yet (SPRD-1-6).
//
// ts may be nil (no swarm tools attached yet). cfg supplies the AppConfig; each
// agent gets its own clone so per-agent provider/model mutations don't bleed.
func NewSpace(id string, m agentdef.Manifest, loaded []agentdef.Loaded, ts ToolSet, cfg *config.Config) (*SwarmSpace, error) {
	if ts == nil {
		ts = noToolSet{}
	}
	if cfg == nil {
		return nil, fmt.Errorf("swarm: NewSpace requires a non-nil config")
	}

	st, err := store.Open(cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("swarm: open store for space %q: %w", id, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	sp := &SwarmSpace{
		ID:      id,
		Name:    m.Name,
		Workdir: cfg.WorkDir,
		Store:   st,
		Roster:  newRoster(),
		cancel:  cancel,
		out:     make(chan SpacedEvent, eventBuffer),
		agents:  make(map[string]agent.Agent),
	}

	// One persona registry per space, holding every member's definition. In
	// Veronica all members are ROOT agents, so each is registered as
	// main-constructible regardless of its on-disk tier (the leader/worker
	// distinction lives in the Roster's Role, not in As).
	reg, _ := agent.BuildAgentRegistry(cfg.AppHome)
	for _, ld := range loaded {
		def := ld.Def
		def.As = ensureMain(def.As)
		reg.Register(def)
	}

	for _, ld := range loaded {
		name := ld.Def.Name

		acfg := cfg.Clone() // own scalars (agent.New mutates DefaultProvider/Model)
		sink := &spaceSink{spaceID: id, out: sp.out}

		opts := []agent.Option{
			agent.WithSink(sink),
			agent.WithSkillRegistry(ld.Skills),
			agent.WithName(name),
			agent.WithRootContext(ctx),
		}
		opts = append(opts, ts.For(name, ld.Role, sp)...)

		ag, err := agent.New(agent.Config{
			AppConfig:      acfg,
			Persona:        name,
			Personas:       reg,
			PermissionMode: m.Settings.PermissionMode,
			MaxIters:       m.Settings.MaxIterations,
		}, opts...)
		if err != nil {
			sp.Shutdown()
			return nil, fmt.Errorf("swarm: construct agent %q in space %q: %w", name, id, err)
		}

		if err := sp.Roster.add(name, ld.Role, ld.Def.WhenToUse, ag.Controller()); err != nil {
			ag.Shutdown()
			sp.Shutdown()
			return nil, fmt.Errorf("swarm: space %q: %w", id, err)
		}
		sp.agents[name] = ag
	}

	return sp, nil
}

// Events is the space's outbound event stream — every member's events, each
// stamped with this space's ID. The service fans these out per (spaceID,
// AgentID); a consumer must drain it continuously.
func (sp *SwarmSpace) Events() <-chan SpacedEvent { return sp.out }

// Shutdown tears down every agent (cancelling their background workers) and
// closes the store. Safe to call on a partially-constructed space.
func (sp *SwarmSpace) Shutdown() {
	if sp.cancel != nil {
		sp.cancel()
	}
	for _, ag := range sp.agents {
		ag.Shutdown()
	}
	if sp.Store != nil {
		_ = sp.Store.Close()
	}
}

// spaceSink wraps an agent's event stream, stamping the spaceID before
// forwarding to the space's out channel.
type spaceSink struct {
	spaceID string
	out     chan<- SpacedEvent
}

func (s *spaceSink) Emit(e event.Event) {
	s.out <- SpacedEvent{SpaceID: s.spaceID, Event: e}
}

// ensureMain guarantees "main" is present so a def is constructible as a root
// agent via agent.New (which resolves only main-tier personas).
func ensureMain(as []string) []string {
	for _, a := range as {
		if a == "main" {
			return as
		}
	}
	out := make([]string, len(as), len(as)+1)
	copy(out, as)
	return append(out, "main")
}
