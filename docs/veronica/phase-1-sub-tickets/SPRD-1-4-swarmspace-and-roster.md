# SPRD-1-4 — SwarmSpace assembly + per-space Roster + event-sink wiring

> Milestone: M0 ｜ Status: IN REVIEW ｜ Owner: (unassigned) ｜ Depends on: 1-1, 1-3
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 3,6) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §3.1, §5.2

## 1. Goal

Turn a loaded agent set into a **live, isolated `SwarmSpace`**: construct each
member via `agent.New`, build the **per-space roster**, and wire each agent's
`event.Sink` so events are tagged `(spaceID, AgentID)`. After this ticket a space
exists with all agents **idle** and addressable — no scheduling yet.

## 2. Scope

**In:**
- `SwarmSpace` type owning: `spaceID`, `name`, `workdir`, `*store.Store`,
  `Roster`, and the constructed `agent.Agent` handles (Controllers).
- Construct agents from `[]agentdef.Loaded` via `agent.New(...)` with per-agent
  `*config.Config` (own workdir), `WithSink`, `WithSkillRegistry`, and an injected
  **tool-factory set** (DI — the swarm tools come from 1-7; accept them as a param
  so this ticket has no hard dep on 1-7).
- `Roster` (per-space): `name → RosterEntry{Controller, role, membership, runStatus, currentTask, whenToUse}`;
  thread-safe; the single source for `list_members` + webapi.
- Event-sink adapter: wrap each agent's sink to stamp `spaceID` (+ pass through
  `AgentID`) before forwarding to the space's out-channel.
- **Per-space name scoping**: names unique within the space; collisions error.

**Out:** scheduler/wake logic (1-6), the tools themselves (1-7), HTTP (1-8).

## 3. Dependencies & what this unblocks

- Depends on: 1-1, 1-3. (Soft: consumes a `ToolSet` interface that 1-7 implements.)
- Unblocks: 1-6 (drives the roster), 1-8 (serves the roster + event stream).

## 4. Technical design

Package `internal/swarm` (`space.go`, `roster.go`).

```go
type SwarmSpace struct {
    ID, Name, Workdir string
    Store  *store.Store
    Roster *Roster
    out    chan event.Event   // tagged (spaceID, AgentID); service fans out
    agents map[string]agent.Agent
}

type ToolSet interface { // implemented by 1-7; injected here
    For(name string, role agentdef.Role, sp *SwarmSpace) []agent.Option // WithCustomTool(...)
}

func NewSpace(id string, m agentdef.Manifest, loaded []agentdef.Loaded, ts ToolSet, cfg *config.Config) (*SwarmSpace, error)

type RosterEntry struct { Role agentdef.Role; Membership Membership; Run RunStatus; CurrentTask int64; WhenToUse string; ctl ui.Controller }
func (r *Roster) Snapshot() []RosterEntry  // for list_members + /api/swarm
```

- Each agent built with its own `config.Config` clone (own `WorkDir`), enabling
  per-space provider/key/budget (invariant: isolation).
- `out` channel is per-space; the service (1-8) selects across spaces.

## 5. Acceptance criteria

1. `NewSpace` constructs N agents (leader + workers) all reachable by name via
   the roster, all `membership=active, run=idle`.
2. Each agent's emitted events arrive on the space `out` channel stamped with the
   correct `spaceID` and `AgentID`.
3. Two `SwarmSpace`s built in the same process with the **same agent names** do
   not collide (separate rosters, separate stores) — isolation holds.
4. Duplicate names **within one** space error at construction.
5. Roster `Snapshot()` returns accurate role/membership/run for every member.

## 6. Verification

- Unit/integration (using a stub LLM provider via `pkg/llm` registry or a fake)
  so `agent.New` constructs without real API calls: assert roster contents +
  event tagging.
- Two-space isolation test: same names, asserts no cross-talk and independent stores.
- `-race` clean.

## 7. Definition of Done

- [x] `SwarmSpace` + `Roster` + tagged event wiring; after `NewSpace` every member is **active + idle** and addressable by name (`TestNewSpaceConstructsRoster`). Events arrive on `Events()` stamped `(SpaceID, AgentID)` (`TestSpaceEventTagging`).
- [x] Per-space isolation + per-space name scoping proven (`TestTwoSpaceIsolation`: two spaces, same member names, distinct controllers / stores / workdirs / AgentIDs; `TestNewSpaceDuplicateNameErrors`).
- [x] Tool injection via the `ToolSet` interface (DI; `nil` → `noToolSet`), so no hard dep on SPRD-1-7.
- [x] `-race` clean; dep-check green (no DIRECT `internal/agent` import — reaches the runtime only through `pkg/agent`/`pkg/config`/`pkg/event`/`pkg/ui`).

### Implementation design / decisions

- **Construction path:** one `agent.BuildAgentRegistry(cfg.AppHome)` per space, then `Register` each member's `AgentDefinition`, then `agent.New(Config{Persona, Personas, AppConfig: cfg.Clone(), PermissionMode, MaxIters}, WithSink, WithSkillRegistry, WithName, WithRootContext, …toolset)`. Each agent gets its **own `cfg.Clone()`** because `agent.New` mutates `DefaultProvider`/`DefaultModel`.
- **`As` is forced to include `main` at registration** (`ensureMain`). In Veronica all members are root agents, but `ResolveMainProfile` only resolves main-tier personas; the leader/worker distinction is carried by the Roster's `Role`, not `As`.
- **Event stamping:** `event.Event` has no SpaceID field, so each agent's sink wraps emissions in `SpacedEvent{SpaceID, Event}` (AgentID is already on the event). The space `out` channel is buffered (1024) with a blocking send (backpressure over loss, per the `pkg/event` contract); the service/consumer must drain `Events()`.
- **Roster** keeps the two orthogonal dimensions (membership active|frozen, run idle|busy|suspended) and a private `ui.Controller` handle; `Snapshot()` returns handle-free `MemberView`s for `list_members` + webapi. The `set*` mutators are the seam the scheduler/supervisor (SPRD-1-6) drives; tested here via the roster unit test.
- **Out of scope (per ticket):** no scheduling/wake and no per-run `recover()` guard yet — those land with the Supervisor/Scheduler in SPRD-1-6 (this ticket leaves all agents idle).
