package swarm

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/bus"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/tools"
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

	// super is the run engine driving this space, set once by NewSupervisor
	// (before Start, before any tool can fire). It is the seam the leader's
	// schedule tools reach the live run loops through — see SetMemberSchedule.
	// nil for a lite space constructed without a supervisor.
	super *Supervisor

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
	// Every swarm member is long-running: its system-prompt prefix must stay
	// bit-stable across rebuilds (no drifting "- Today:" date) so a weeks-long
	// swarm reuses one cached prompt prefix. Consumed via PromptContext.OmitDate
	// in mainProfileFromDiskAgent (RP-5).
	def.LongRunning = true
	// Every swarm member advertises its own skill catalog (name+desc) in its system
	// prompt AND carries the built-in skill tool to load them — both forced at the
	// swarm layer (RP-10-1), not a per-agent profile.yml opt-in: the operator asked
	// that EVERY agent be skill-capable. The prompt's skill list comes from the
	// member's own registry (WithSkillRegistry in constructMember), kept in lockstep
	// with the tool by the internal/agent skillRefs fix (RP-10-2).
	def.AdvertiseSkills = true
	def.ActiveTools = ensureTool(def.ActiveTools, tools.SKILL)
	// Auto-inject the swarm collaboration protocol for this member's role so the
	// operator never has to hand-write the mechanics (see teamprompt.go). Pairs
	// with the role-based tool injection (ToolSet) — both keyed off ld.Role. The
	// member's space/name/role grounding is prepended to the protocol here too.
	def.SystemPrompt = injectTeamProtocol(def.SystemPrompt, def.Name, sp.Name, ld.Role)
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

	// Per-member model / effort pins (profile.yml `model:` / `effort:`, or the
	// add-agent form). Fixed at creation: applied by pointing the member's own
	// config clone at them, so the normal construction path (ResolveMainProfile
	// reads DefaultProvider/DefaultModel; agent.New picks up DefaultEffort)
	// honors them with no extra plumbing.
	//
	// The model pin is deliberately NOT validated against the built-in constant
	// table: SDK hosts register custom providers/models (the swarm tests pin a
	// stub), so an unknown id may still resolve at LLM-client build — where a
	// truly bad pin fails loudly. A pin that IS a built-in model also switches
	// the provider, so a deepseek member can sit next to anthropic ones.
	if m := strings.TrimSpace(ld.Def.Model); m != "" {
		if p, ok := constant.ProviderOfModel(constant.Model(m)); ok {
			acfg.DefaultProvider = p
		}
		acfg.DefaultModel = constant.Model(m)
	}
	if e := strings.TrimSpace(ld.Effort); e != "" {
		if llm.ParseEffort(e) == 0 {
			return fmt.Errorf("swarm: member %q: invalid effort %q (want low|medium|high|ultra)", name, e)
		}
		acfg.DefaultEffort = e
	}

	// Bind this member's runtime identity onto its own config so the swarm
	// custom tools (SPRD-1-7) can read who they belong to at build time — a
	// shared WithCustomTool factory can't carry it in a closure.
	BindMemberContext(acfg, MemberContext{Name: name, Role: ld.Role, Space: sp})

	sink := &spaceSink{
		spaceID: sp.ID,
		name:    name,
		roster:  sp.Roster,
		deriver: newPhaseDeriver(),
		out:     sp.out,
	}

	opts := []agent.Option{
		agent.WithSink(sink),
		agent.WithSkillRegistry(ld.Skills),
		agent.WithName(name),
		agent.WithRootContext(sp.ctx),
		// Stream tokens live to the web console. Every swarm member is a root
		// persona whose run is watched in the :8888 UI, so the streaming UX win
		// applies — text/thinking/tool deltas reach the console as they happen
		// instead of one buffered dump per turn after the (blocking) LLM call
		// returns. pkg/agent's Profile.Stream defaults off (buffered Complete);
		// the swarm opts in here. The phase deriver + web reduce the same chunk
		// events, so "thinking"/"texting" sub-phases also go live.
		agent.WithStream(true),
		// drain B (SPRD-1-12): fold incoming mailbox messages into a busy
		// member's current run. The mailbox is resolved lazily per Drain, so
		// it works regardless of Bus.Register ordering below.
		agent.WithInboxDrainer(newInboxDrainer(name, sp.Bus, sp.Store)),
	}
	opts = append(opts, sp.ts.For(name, ld.Role, sp)...)

	// Per-member permission store (RP-11): shared project/user rules PLUS this
	// member's own <agentDir>/permissions.json, loaded into ONLY this member's
	// store. That is what scopes a narrow lever to one non-leader — e.g.
	// risk-monitor may "http_request(POST .../halt)" while every other member (and
	// POST .../strategy) still asks. Without this explicit store, agent.New would
	// load the shared files alone (the prior behavior, which this preserves when a
	// member has no permissions.json). Warnings are non-fatal — the member starts
	// without the grant — mirroring agent.New's own discard of Load warnings.
	permStore, _ := permission.LoadMember(acfg.WorkDir, acfg.AppHome,
		agentdef.PermissionsPath(sp.Workdir, ld.Role, name))

	ag, err := agent.New(agent.Config{
		AppConfig:       acfg,
		Persona:         name,
		Personas:        sp.reg,
		PermissionMode:  sp.settings.PermissionMode,
		MaxIters:        sp.settings.MaxIterations,
		PermissionStore: permStore,
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

// ScheduleFor returns a member's declared timer schedule, if any. Exported so
// list_members (internal/swarm/tools) can render each member's crontab.
func (sp *SwarmSpace) ScheduleFor(name string) (agentdef.Schedule, bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	s, ok := sp.schedules[name]
	return s, ok
}

// SetMemberSchedule / ClearMemberSchedule are the tool-facing seam onto the run
// engine: the leader's schedule_set/schedule_clear tools hold only the space, so
// they go through here. We read super under sp.mu and release BEFORE delegating —
// the supervisor methods take s.mu then sp.mu, and holding sp.mu across the call
// would invert that order and risk deadlock.
func (sp *SwarmSpace) SetMemberSchedule(name string, sch agentdef.Schedule) error {
	sp.mu.Lock()
	s := sp.super
	sp.mu.Unlock()
	if s == nil {
		return fmt.Errorf("swarm: scheduling unavailable (space has no running supervisor)")
	}
	return s.SetSchedule(name, sch)
}

// ClearMemberSchedule removes a member's timer schedule via the run engine.
func (sp *SwarmSpace) ClearMemberSchedule(name string) error {
	sp.mu.Lock()
	s := sp.super
	sp.mu.Unlock()
	if s == nil {
		return fmt.Errorf("swarm: scheduling unavailable (space has no running supervisor)")
	}
	return s.ClearSchedule(name)
}

// MemberSkills lists a member's authored skills (RP-10) by re-scanning its on-disk
// skills/ dir — the source of truth POST/DELETE write, so a GET right after an add
// reflects it immediately (the live agent registry lags until the boundary reload).
// A member with no skills dir yields an empty list, not an error.
func (sp *SwarmSpace) MemberSkills(member string) ([]agent.Skill, error) {
	role, ok := sp.Roster.roleOf(member)
	if !ok {
		return nil, fmt.Errorf("swarm: unknown member %q", member)
	}
	reg, _ := skill.LoadRegistry(agentdef.SkillsDir(sp.Workdir, role, member), "")
	list := reg.List()
	out := make([]agent.Skill, 0, len(list))
	for _, m := range list {
		out = append(out, agent.Skill{Name: m.Name, Description: m.Description})
	}
	return out, nil
}

// AddMemberSkill authors a skill on a member (User-only, via the web) and reloads it
// so the new skill enters the member's prompt + skill tool at its next run boundary
// (RP-10). RemoveMemberSkill is the mirror. Both reject an unknown member up front.
func (sp *SwarmSpace) AddMemberSkill(member, name, description, body string) error {
	role, ok := sp.Roster.roleOf(member)
	if !ok {
		return fmt.Errorf("swarm: unknown member %q", member)
	}
	if err := agentdef.WriteSkill(sp.Workdir, role, member, name, description, body); err != nil {
		return err
	}
	return sp.reloadSkills(member)
}

// RemoveMemberSkill deletes a member's skill and reloads it (RP-10).
func (sp *SwarmSpace) RemoveMemberSkill(member, name string) error {
	role, ok := sp.Roster.roleOf(member)
	if !ok {
		return fmt.Errorf("swarm: unknown member %q", member)
	}
	if err := agentdef.RemoveSkill(sp.Workdir, role, member, name); err != nil {
		return err
	}
	return sp.reloadSkills(member)
}

// reloadSkills routes a member skill change to the run engine, which re-scans the
// dir and re-renders the prompt at the next run boundary. Read super under sp.mu and
// release before delegating (super takes s.mu) — the schedule-forwarder pattern.
func (sp *SwarmSpace) reloadSkills(member string) error {
	sp.mu.Lock()
	s := sp.super
	sp.mu.Unlock()
	if s == nil {
		return fmt.Errorf("swarm: skill reload unavailable (space has no running supervisor)")
	}
	return s.ReloadMemberSkills(member)
}

// removeAgent tears down one member's live agent and drops it from the space's
// maps (RP-8 remove): shut the agent's background workers, forget its handle and
// any schedule. The roster entry, mailbox, and run loop are handled by the
// supervisor's RemoveMember; this is the space-owned half. The .vero ledger is
// left untouched (v1 never deletes history).
func (sp *SwarmSpace) removeAgent(name string) {
	sp.mu.Lock()
	ag := sp.agents[name]
	delete(sp.agents, name)
	delete(sp.schedules, name)
	sp.mu.Unlock()
	if ag != nil {
		ag.Shutdown()
	}
}

// agentOf returns a member's live agent handle. The run engine uses it to reach the
// public skill-reload seam (RP-10-4); unexported since only the supervisor (same
// package) needs the concrete agent.Agent.
func (sp *SwarmSpace) agentOf(name string) (agent.Agent, bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	ag, ok := sp.agents[name]
	return ag, ok
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

// spaceSink wraps one member's event stream: it derives that member's fine run
// phase (RP-3) and updates the roster, then stamps the spaceID and forwards the
// event to the space's out channel. Deriving here — where each member's events
// already flow — means the roster (the single source of truth for both
// list_members and the web) carries the live phase without a second consumer.
type spaceSink struct {
	spaceID string
	name    string
	roster  *Roster
	deriver *phaseDeriver
	out     chan<- SpacedEvent
}

func (s *spaceSink) Emit(e event.Event) {
	if s.deriver != nil && s.roster != nil {
		if p, tool, changed := s.deriver.apply(e); changed {
			s.roster.setPhase(s.name, p, tool)
		}
	}
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

// ensureTool appends a tool to a member's active list if absent, returning a fresh
// slice so the swarm-forced skill tool (RP-10-1) never mutates the loaded def's
// backing array or duplicates a tool the member's active.yml already declares.
func ensureTool(list []tools.ToolName, name tools.ToolName) []tools.ToolName {
	for _, t := range list {
		if t == name {
			return list
		}
	}
	return append(append([]tools.ToolName{}, list...), name)
}
