package swarm

import (
	"context"
	"fmt"
	"sync"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/bus"
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
// Roster, message Bus, and constructed agent handles. Two spaces in one process
// share nothing — separate stores, rosters, buses, and event streams — and
// member names are scoped per space.
type SwarmSpace struct {
	ID      string
	Name    string
	Workdir string
	Store   *store.Store
	Roster  *Roster
	Bus     *bus.Bus

	cancel context.CancelFunc
	ctx    context.Context
	out    chan SpacedEvent

	// mu guards the live-membership maps so a dynamic AddMember (SPRD-1-6) can
	// run concurrently with the agents' tools reading the space.
	mu        sync.Mutex
	agents    map[string]agent.Agent
	schedules map[string]agentdef.Schedule

	// Construction state retained so AddMember can hot-load a new member through
	// the exact same path NewSpace used (loader.Build -> agent.New -> wire in).
	reg      *agent.AgentRegistry
	cfg      *config.Config
	ts       ToolSet
	settings agentdef.Settings
	loader   *agentdef.Loader
}

// out channel buffer. The service/test must drain Events() continuously; the
// per-agent sink does a blocking send (backpressure beats event loss, per the
// pkg/event contract), so a generous buffer keeps a short run from stalling.
const eventBuffer = 1024

// NewSpace assembles a live space from a manifest and its loaded agent
// definitions: it opens the per-space store and message bus, constructs each
// member via agent.New against per-agent config clones, wires each agent's
// event.Sink to stamp the spaceID, registers a mailbox per member, and
// populates the Roster. After this returns, every member is active + idle and
// addressable by name — no scheduling yet (the Supervisor, SPRD-1-6, starts the
// run loops).
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

	// One persona registry per space, holding every member's definition. In
	// Veronica all members are ROOT agents, so each is registered as
	// main-constructible regardless of its on-disk tier (the leader/worker
	// distinction lives in the Roster's Role, not in As).
	reg, _ := agent.BuildAgentRegistry(cfg.AppHome)

	sp := &SwarmSpace{
		ID:        id,
		Name:      m.Name,
		Workdir:   cfg.WorkDir,
		Store:     st,
		Roster:    newRoster(),
		cancel:    cancel,
		ctx:       ctx,
		out:       make(chan SpacedEvent, eventBuffer),
		agents:    make(map[string]agent.Agent),
		schedules: make(map[string]agentdef.Schedule),
		reg:       reg,
		cfg:       cfg,
		ts:        ts,
		settings:  m.Settings,
		loader:    agentdef.NewLoader(),
	}
	sp.Bus = bus.New(st, sp.Roster)

	// Two phases: register every persona first so each agent.New sees all its
	// siblings in the subagent_type list (a leader can spawn a worker as an
	// intra-task subagent), then construct.
	for _, ld := range loaded {
		sp.registerDef(ld)
	}
	for _, ld := range loaded {
		if err := sp.constructMember(ld); err != nil {
			sp.Shutdown()
			return nil, fmt.Errorf("swarm: space %q: %w", id, err)
		}
	}

	return sp, nil
}

// registerDef adds one member's definition to the space persona registry,
// forcing main-tier so agent.New can resolve it as a root agent.
func (sp *SwarmSpace) registerDef(ld agentdef.Loaded) {
	def := ld.Def
	def.As = ensureMain(def.As)
	sp.mu.Lock()
	sp.reg.Register(def)
	sp.mu.Unlock()
}

// constructMember builds one live agent from a Loaded and wires it into the
// space: agent handle, roster entry (active + idle), mailbox, and timer
// schedule. Shared by NewSpace and the AddMember hot-load path. The persona must
// already be registered (registerDef).
func (sp *SwarmSpace) constructMember(ld agentdef.Loaded) error {
	name := ld.Def.Name

	acfg := sp.cfg.Clone() // own scalars (agent.New mutates DefaultProvider/Model)

	// Bind this member's runtime identity onto its own config so the swarm
	// custom tools (SPRD-1-7) can read who they belong to at build time — a
	// shared WithCustomTool factory can't carry it in a closure.
	BindMemberContext(acfg, MemberContext{Name: name, Role: ld.Role, Space: sp})

	sink := &spaceSink{spaceID: sp.ID, out: sp.out}

	opts := []agent.Option{
		agent.WithSink(sink),
		agent.WithSkillRegistry(ld.Skills),
		agent.WithName(name),
		agent.WithRootContext(sp.ctx),
	}
	opts = append(opts, sp.ts.For(name, ld.Role, sp)...)

	ag, err := agent.New(agent.Config{
		AppConfig:      acfg,
		Persona:        name,
		Personas:       sp.reg,
		PermissionMode: sp.settings.PermissionMode,
		MaxIters:       sp.settings.MaxIterations,
	}, opts...)
	if err != nil {
		return fmt.Errorf("construct agent %q: %w", name, err)
	}

	if err := sp.Roster.add(name, ld.Role, ld.Def.WhenToUse, ag.Controller()); err != nil {
		ag.Shutdown()
		return err
	}

	sp.mu.Lock()
	sp.agents[name] = ag
	if ld.Schedule != nil {
		sp.schedules[name] = *ld.Schedule
	}
	sp.mu.Unlock()

	sp.Bus.Register(name)
	return nil
}

// scheduleFor returns a member's declared timer schedule, if any.
func (sp *SwarmSpace) scheduleFor(name string) (agentdef.Schedule, bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	s, ok := sp.schedules[name]
	return s, ok
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
	sp.mu.Lock()
	ags := make([]agent.Agent, 0, len(sp.agents))
	for _, ag := range sp.agents {
		ags = append(ags, ag)
	}
	sp.mu.Unlock()
	for _, ag := range ags {
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
