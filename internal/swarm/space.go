package swarm

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

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
	"github.com/johnny1110/evva/pkg/tools/alarm"
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
	// schedMeta records WHERE a live schedule came from (RP-20): an entry
	// exists only for runtime-set schedules (schedule_set / web edit, or a
	// store row applied at rebuild); absence means the schedule is the
	// manifest/profile seed. Lazily allocated so hand-built test spaces
	// need no init.
	schedMeta map[string]ScheduleOrigin
	// personaMembers marks members sourced from a registry persona (RP-29):
	// their skill catalog merges the persona's own dirs with the space's, so
	// construction and skill reload must agree on the source set. Lazily
	// allocated in registerPersonaDef.
	personaMembers map[string]bool

	// permOverrides holds RUNTIME-SET permission-mode switches (web per-member
	// control) — overrides ONLY, never the construction-time seeds, so a
	// manifest edit stays authoritative for members the operator never touched
	// (the RP-20 schedules lesson). Persisted into runtime.json (PermModes) and
	// reapplied by Reload; a fresh register discards it. Lazily allocated.
	permOverrides map[string]string

	// budgets holds manifest member-level daily-budget overrides (RP-13);
	// members without an entry inherit settings.DailyBudgetTokens. meter is the
	// live daily ledger the supervisor feeds at run boundaries (see usage.go).
	budgets map[string]int
	meter   usageMeter

	// metrics counts the scheduler lifecycle per member (RP-17). nil on
	// hand-built spaces — every counting method is nil-safe.
	metrics *spaceMetrics

	// alarmSched is the space's shared one-shot alarm scheduler (alarm_set /
	// alarm_clear). Lazy-allocated under mu on first AlarmScheduler() access; a
	// fired alarm is delivered as a durable bus message to its target member.
	alarmSched *alarm.Scheduler

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
		budgets:   budgetOverrides(m),
		metrics:   newSpaceMetrics(),
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
	for i := range loaded {
		if err := sp.registerDef(&loaded[i]); err != nil {
			sp.Shutdown()
			return nil, fmt.Errorf("swarm: space %q: %w", id, err)
		}
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
// forcing main-tier so agent.New can resolve it as a root agent. Persona
// members (RP-29) are composed from the registry instead and may fail when
// the referenced persona does not exist or is not main-tier.
func (sp *SwarmSpace) registerDef(ld *agentdef.Loaded) error {
	if ld.FromPersona {
		return sp.registerPersonaDef(ld)
	}
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
	// The memory protocol (RP-25) rides along for members that can actually
	// maintain memory files — i.e. carry a file-write tool.
	canWriteMemory := slices.Contains(def.ActiveTools, tools.WRITE_FILE) ||
		slices.Contains(def.ActiveTools, tools.EDIT_FILE)
	def.SystemPrompt = injectTeamProtocol(def.SystemPrompt, def.Name, sp.Name, ld.Role, canWriteMemory)
	sp.mu.Lock()
	sp.reg.Register(def)
	sp.mu.Unlock()
	return nil
}

// registerPersonaDef composes a persona member's definition from the space
// persona registry (built-ins + <appHome>/agents): swarm-harden it the same
// way dir members are (LongRunning, AdvertiseSkills, main-tier), apply the
// manifest's when_to_use/model overrides, and attach the team protocol as
// PromptSuffix — the seam that survives internally-assembled prompts and
// every re-render (RP-29). The composed def is registered space-locally
// (solo evva is untouched — each space builds its own registry copy) and
// written back onto ld so constructMember and the roster read the effective
// values.
func (sp *SwarmSpace) registerPersonaDef(ld *agentdef.Loaded) error {
	name := ld.Def.Name
	base, ok := sp.reg.Get(name)
	if !ok {
		return fmt.Errorf("persona member %q: no such persona in the registry (built-ins + <appHome>/agents)", name)
	}
	if !base.IsMain() {
		return fmt.Errorf("persona member %q: not a main-tier persona", name)
	}
	def := base
	def.As = ensureMain(def.As)
	def.LongRunning = true
	def.AdvertiseSkills = true
	// Built-ins carry empty tool lists ("constructor defaults", which already
	// include the skill tool); only a disk persona's explicit list needs the
	// swarm-forced skill tool (RP-10-1).
	if len(def.ActiveTools) > 0 {
		def.ActiveTools = ensureTool(def.ActiveTools, tools.SKILL)
	}
	if w := ld.Def.WhenToUse; w != "" {
		def.WhenToUse = w
	}
	if m := ld.Def.Model; m != "" {
		def.Model = m
	}
	canWrite := len(def.ActiveTools) == 0 ||
		slices.Contains(def.ActiveTools, tools.WRITE_FILE) ||
		slices.Contains(def.ActiveTools, tools.EDIT_FILE)
	def.PromptSuffix = teamProtocolSuffix(name, sp.Name, ld.Role, canWrite)
	sp.mu.Lock()
	sp.reg.Register(def)
	if sp.personaMembers == nil {
		sp.personaMembers = map[string]bool{}
	}
	sp.personaMembers[name] = true
	sp.mu.Unlock()
	ld.Def = def
	return nil
}

// isPersonaMember reports whether name was assembled from a registry persona.
func (sp *SwarmSpace) isPersonaMember(name string) bool {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return sp.personaMembers[name]
}

// memberSkillRegistry resolves a member's full skill catalog from disk. Dir
// members load (shared, own) — the RP-26 order. Persona members additionally
// start from the persona's OWN catalog (bundled + appHome + workdir skills,
// via agent.LoadSkillCatalog) with the space layers on top, precedence
// low→high: bundled < home < workdir < shared < member-local. Construction
// and Supervisor.ReloadMemberSkills both call this, so the two never drift.
func (sp *SwarmSpace) memberSkillRegistry(fromPersona bool, role agentdef.Role, name string) *skill.Registry {
	shared := agentdef.SharedSkillsDir(sp.Workdir)
	own := agentdef.SkillsDir(sp.Workdir, role, name)
	if fromPersona {
		return agent.LoadSkillCatalog(sp.cfg, shared, own)
	}
	reg, _ := skill.LoadRegistry(shared, own)
	return reg
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

	// Per-member permission stance (RP-24): the manifest's coarse trust knob,
	// member field > settings — so "analysts default, trader bypass" composes
	// in one space. The yaml path already fail-fasted on a bad value at
	// LoadManifest; this guard covers programmatic manifests (the effort-pin
	// precedent). Layering with RP-11 rules is decided in pkg/permission:
	// deny rules bind in every mode, bypass included.
	permMode := sp.settings.PermissionMode
	if pm := strings.TrimSpace(ld.PermissionMode); pm != "" {
		if !permission.Mode(pm).Valid() {
			return fmt.Errorf("swarm: member %q: invalid permission_mode %q (want default|accept_edits|plan|bypass)", name, pm)
		}
		permMode = pm
	}

	// Member-native long-term memory (RP-25): home this member's writable
	// memory at its own <agentDir>/memory/ — created here so first boot AND
	// hot-add both leave an empty dir ready for the model's first write. The
	// global auto-memory toggles are forced OFF on the clone: the solo prompt
	// sections would advertise <appHome>/memory (the wrong store), and the
	// per-turn recall side-query is redundant cost for a swarm member — the
	// wake-injected index + read-on-demand is the member protocol. The
	// WithMemoryDir override (below) keeps the write carve-out targeting the
	// member dir independent of those toggles.
	memDir := agentdef.MemoryDir(sp.Workdir, ld.Role, name)
	if err := os.MkdirAll(memDir, 0o755); err != nil {
		return fmt.Errorf("swarm: member %q: create memory dir: %w", name, err)
	}
	acfg.EnableAutoMemory = false
	acfg.EnableMemoryRecall = false

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

	// Persona members compose their catalog live (persona-own + shared +
	// member-local, RP-29); dir members carry the loader's (shared, own) set.
	skillsReg := ld.Skills
	if ld.FromPersona {
		skillsReg = sp.memberSkillRegistry(true, ld.Role, name)
	}

	opts := []agent.Option{
		agent.WithSink(sink),
		agent.WithSkillRegistry(skillsReg),
		agent.WithName(name),
		agent.WithMemoryDir(memDir),
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
		PermissionMode:  permMode,
		MaxIters:        sp.settings.MaxIterations,
		PermissionStore: permStore,
	}, opts...)
	if err != nil {
		return fmt.Errorf("construct agent %q: %w", name, err)
	}

	// Roster stores the TRUE effective stance read back off the agent —
	// agent.New finishes the fallback chain (member > settings > app config >
	// "default"), so an all-empty chain still displays its real mode.
	if err := sp.Roster.add(name, ld.Role, ld.Def.WhenToUse, ag.PermissionModeName(), ag.Controller()); err != nil {
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

// WebhookSecret returns the space's external-event shared secret ("" = unset,
// RP-9 loopback trust). Exported for the service's webhook auth check (RP-15).
func (sp *SwarmSpace) WebhookSecret() string {
	return sp.settings.WebhookSecret // set once at construction, never mutated
}

// RetentionDays returns the space's ledger retention window in days (0 =
// retention disabled). Exported for the service's manual vacuum default (RP-16).
func (sp *SwarmSpace) RetentionDays() int {
	return sp.settings.RetentionDays // set once at construction, never mutated
}

// TaskStaleThreshold returns the space's RP-22 task-age reminder line (0 =
// disabled). Exported so task_list can tag over-threshold tasks as stale.
func (sp *SwarmSpace) TaskStaleThreshold() time.Duration {
	return sp.settings.TaskStaleThreshold // set once at construction, never mutated
}

// ScheduleOrigin says where a member's live schedule came from: the manifest
// (the zero value) or a runtime change, with the unix-milli instant of that
// change so list_members can render "set 2026-06-11" (RP-20 §2.5).
type ScheduleOrigin struct {
	Runtime bool
	SetAt   int64
}

// ScheduleFor returns a member's declared timer schedule, if any. Exported so
// list_members (internal/swarm/tools) can render each member's crontab.
func (sp *SwarmSpace) ScheduleFor(name string) (agentdef.Schedule, bool) {
	sch, _, ok := sp.ScheduleInfoFor(name)
	return sch, ok
}

// ScheduleInfoFor is ScheduleFor plus provenance — manifest seed vs runtime
// override — so the leader and the operator can tell at a glance whose hand
// set a cadence (RP-20 §2.5).
func (sp *SwarmSpace) ScheduleInfoFor(name string) (agentdef.Schedule, ScheduleOrigin, bool) {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	s, ok := sp.schedules[name]
	return s, sp.schedMeta[name], ok
}

// setRuntimeSchedule installs a runtime-sourced schedule in the live maps.
// Shared by the supervisor's SetSchedule and the restart rebuild (Reload
// applying store rows) so both stamp identical provenance.
func (sp *SwarmSpace) setRuntimeSchedule(name string, sch agentdef.Schedule, setAt int64) {
	sp.mu.Lock()
	sp.schedules[name] = sch
	if sp.schedMeta == nil {
		sp.schedMeta = map[string]ScheduleOrigin{}
	}
	sp.schedMeta[name] = ScheduleOrigin{Runtime: true, SetAt: setAt}
	sp.mu.Unlock()
}

// dropScheduleEntry removes a member's live schedule (and its provenance).
func (sp *SwarmSpace) dropScheduleEntry(name string) {
	sp.mu.Lock()
	delete(sp.schedules, name)
	delete(sp.schedMeta, name)
	sp.mu.Unlock()
}

// recordPermOverride remembers a runtime permission-mode switch so
// persistRuntime carries it into runtime.json and a restart rebuild reapplies
// it. Override-only by design — construction-time seeds never land here.
func (sp *SwarmSpace) recordPermOverride(name, mode string) {
	sp.mu.Lock()
	if sp.permOverrides == nil {
		sp.permOverrides = map[string]string{}
	}
	sp.permOverrides[name] = mode
	sp.mu.Unlock()
}

// DiscardRuntimePermModes wipes every runtime permission-mode override so the
// manifest is authoritative again — DiscardRuntimeSchedules' sibling, called
// by the service on a FRESH register only; restart rebuilds keep them.
func (sp *SwarmSpace) DiscardRuntimePermModes() {
	sp.mu.Lock()
	sp.permOverrides = nil
	sp.mu.Unlock()
	rs := loadRuntime(sp.Workdir)
	if rs.PermModes != nil {
		rs.PermModes = nil
		writeRuntime(sp.Workdir, rs)
	}
}

// DiscardRuntimeSchedules wipes every runtime schedule override — the store
// rows and the legacy runtime.json field — so the manifest is authoritative
// again. The service calls it on a FRESH register (`evva swarm .`), the
// operator's explicit "take the manifest as written" (RP-20 §2.4); restart
// rebuilds (Reconcile / RunSpace / reset) never do. Must run BEFORE Reload,
// which is what applies the rows.
func (sp *SwarmSpace) DiscardRuntimeSchedules() error {
	if err := sp.Store.ClearSchedules(); err != nil {
		return err
	}
	// Strip only the legacy schedules field; membership/meter in runtime.json
	// belong to Reload and must survive untouched.
	rs := loadRuntime(sp.Workdir)
	if rs.Schedules != nil {
		rs.Schedules = nil
		writeRuntime(sp.Workdir, rs)
	}
	return nil
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

// AlarmScheduler returns the space's shared one-shot alarm scheduler, allocating
// it on first use. A fired alarm is delivered by deliverAlarm as a durable bus
// message to its Target (the member to wake) — so it flows through the same
// mailbox path as a teammate message: the target's run loop wakes and drains it.
// Durable alarms persist beside the space store and are re-armed by the
// supervisor on Start.
func (sp *SwarmSpace) AlarmScheduler() *alarm.Scheduler {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if sp.alarmSched == nil {
		sp.alarmSched = alarm.New(alarm.Config{
			StorePath: filepath.Join(sp.Workdir, "alarms.json"),
			OnFire:    sp.deliverAlarm,
		})
	}
	return sp.alarmSched
}

// deliverAlarm is the alarm scheduler's fire callback (runs on a timer
// goroutine): route a fired one-shot alarm to its target member as a durable
// bus message, sent on behalf of the member that set it. A self-alarm (no
// target) goes back to its origin. A target that left the team between arming
// and firing is dropped rather than dead-lettered to a mailbox no loop drains.
func (sp *SwarmSpace) deliverAlarm(f alarm.Fired) {
	target := f.Target
	if target == "" {
		target = f.Origin
	}
	if target == "" {
		return
	}
	if _, ok := sp.Roster.membership(target); !ok {
		return
	}
	sender := f.Origin
	if sender == "" {
		sender = target
	}
	subject := "⏰ alarm"
	if f.Label != "" {
		subject = "⏰ alarm: " + f.Label
	}
	_, _ = sp.Bus.Send(store.Message{
		Sender:    sender,
		Recipient: target,
		Subject:   subject,
		Body:      f.Message(),
	})
}

// stopAlarms halts pending alarm timers at teardown. Durable alarms stay on disk
// and are re-armed next start. No-op when no scheduler was allocated.
func (sp *SwarmSpace) stopAlarms() {
	sp.mu.Lock()
	s := sp.alarmSched
	sp.mu.Unlock()
	if s != nil {
		s.Stop()
	}
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

// memoryWakeReminderCap bounds the index text injected per wake. The solo
// convention caps the index at ~200 lines; 16 KiB is far above any healthy
// index and merely stops a runaway file from flooding a wake prompt.
const memoryWakeReminderCap = 16 * 1024

// memoryWakeReminder renders the block the scheduler hangs inside a member's
// wake <system-reminder> (RP-25): the member's own MEMORY.md index, labeled
// with its on-disk path so the model knows where the linked files live. ""
// when the member has no index yet (or it is empty/unreadable) — a member
// that never saved a memory gets zero wake noise. Read fresh per wake: the
// index is the member's own latest write, never cached.
func (sp *SwarmSpace) memoryWakeReminder(name string) string {
	role, ok := sp.Roster.roleOf(name)
	if !ok {
		return ""
	}
	dir := agentdef.MemoryDir(sp.Workdir, role, name)
	b, err := os.ReadFile(filepath.Join(dir, "MEMORY.md"))
	if err != nil {
		return ""
	}
	idx := strings.TrimSpace(string(b))
	if idx == "" {
		return ""
	}
	if len(idx) > memoryWakeReminderCap {
		idx = idx[:memoryWakeReminderCap] + "\n… (index truncated — prune it)"
	}
	rel, err := filepath.Rel(sp.Workdir, dir)
	if err != nil {
		rel = dir
	}
	return "Your memory index (" + filepath.ToSlash(rel) + "/MEMORY.md — read a linked file before relying on it):\n" + idx
}

// MemoryFile is one file of a member's memory dir, served read-only to the
// web (RP-25): the User's transparency window onto the team's mind.
type MemoryFile struct {
	Name    string // dir-relative path, slash-separated (e.g. "MEMORY.md", "project_x.md")
	Content string
}

// MemberMemoryFiles lists a member's memory directory for the web's read-only
// Memory view (RP-25). Reads the disk fresh — the dir is the single source of
// truth the member itself writes. Only *.md files surface (the memdir
// convention); each is capped like memdir.MaxFileBytes so one runaway file
// cannot balloon the response. An empty dir yields an empty list, not an error.
func (sp *SwarmSpace) MemberMemoryFiles(member string) ([]MemoryFile, error) {
	role, ok := sp.Roster.roleOf(member)
	if !ok {
		return nil, fmt.Errorf("swarm: unknown member %q", member)
	}
	dir := agentdef.MemoryDir(sp.Workdir, role, member)
	const maxFileBytes = 64 * 1024
	var out []MemoryFile
	// Walk errors (missing dir, unreadable entry) skip silently: an absent or
	// half-readable memory dir is a state to display as empty, not an error.
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		if len(b) > maxFileBytes {
			b = b[:maxFileBytes]
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			rel = d.Name()
		}
		out = append(out, MemoryFile{Name: filepath.ToSlash(rel), Content: string(b)})
		return nil
	})
	// Index first, then the rest alphabetically — the order a reader wants.
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Name == "MEMORY.md") != (out[j].Name == "MEMORY.md") {
			return out[i].Name == "MEMORY.md"
		}
		return out[i].Name < out[j].Name
	})
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

// SharedSkills lists the space-shared skill dir (RP-26) fresh from disk — the
// same source-of-truth-on-disk read as MemberSkills, for the web GET and the
// skill_publish result. A space without the dir yields an empty list.
func (sp *SwarmSpace) SharedSkills() []agent.Skill {
	reg, _ := skill.LoadRegistry(agentdef.SharedSkillsDir(sp.Workdir), "")
	list := reg.List()
	out := make([]agent.Skill, 0, len(list))
	for _, m := range list {
		out = append(out, agent.Skill{Name: m.Name, Description: m.Description})
	}
	return out
}

// PublishSharedSkill writes a skill into the space-shared dir and fans the
// reload out to EVERY member (RP-26 Part B) — each picks the new catalog up at
// its own next run boundary (an idle member on the spot, a busy one when its
// current run ends). The two callers are the leader's skill_publish tool and
// the operator's web POST; both go through here so the write+reload-all pairing
// can't be skipped. overwrite gates replacing an existing version
// (agentdef.ErrSkillExists otherwise).
func (sp *SwarmSpace) PublishSharedSkill(name, description, body string, overwrite bool) error {
	if err := agentdef.WriteSharedSkill(sp.Workdir, name, description, body, overwrite); err != nil {
		return err
	}
	return sp.reloadAllSkills()
}

// RemoveSharedSkill deletes a shared skill and fans the reload out — the
// User's final-arbiter delete (web), so a bad publish is reversible.
func (sp *SwarmSpace) RemoveSharedSkill(name string) error {
	if err := agentdef.RemoveSharedSkill(sp.Workdir, name); err != nil {
		return err
	}
	return sp.reloadAllSkills()
}

// reloadAllSkills is reloadSkills for the whole roster (shared-skill changes).
func (sp *SwarmSpace) reloadAllSkills() error {
	sp.mu.Lock()
	s := sp.super
	sp.mu.Unlock()
	if s == nil {
		return fmt.Errorf("swarm: skill reload unavailable (space has no running supervisor)")
	}
	return s.ReloadAllMemberSkills()
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
	delete(sp.schedMeta, name)
	delete(sp.permOverrides, name)
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
	sp.stopAlarms()
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

// budgetOverrides collects the manifest's member-level daily-budget overrides
// (RP-13). Only non-zero entries are kept: 0 means "inherit the space default",
// so storing it would shadow a later settings change for no reason.
func budgetOverrides(m agentdef.Manifest) map[string]int {
	out := make(map[string]int)
	if m.Leader.BudgetTokens != 0 {
		out[m.Leader.Agent] = m.Leader.BudgetTokens
	}
	for _, w := range m.Workers {
		if w.BudgetTokens != 0 {
			out[w.Agent] = w.BudgetTokens
		}
	}
	return out
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
