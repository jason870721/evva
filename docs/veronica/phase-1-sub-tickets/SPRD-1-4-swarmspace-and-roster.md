# SPRD-1-4 — SwarmSpace assembly + per-space Roster + event-sink wiring

> Milestone: M0 ｜ Status: TODO ｜ Owner: (unassigned) ｜ Depends on: 1-1, 1-3
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

- [ ] `SwarmSpace` + `Roster` + tagged event wiring; agents idle & addressable.
- [ ] Per-space isolation + per-space name scoping proven by test (invariant #2).
- [ ] Tool injection via `ToolSet` interface (no hard dep on 1-7).
- [ ] `-race` clean; no `internal/agent` import (invariant #1).
