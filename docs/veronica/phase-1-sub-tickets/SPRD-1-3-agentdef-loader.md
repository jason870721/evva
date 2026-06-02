# SPRD-1-3 — Agentdef loader: `agents/{main,sub}/` → AgentDefinition + skills + schedule

> Milestone: M0 (min) / M3 (schedule) ｜ Status: TODO ｜ Owner: (unassigned) ｜ Depends on: 1-1
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 4) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §4.5

## 1. Goal

A **re-callable** loader that turns one on-disk agent directory into the public
SDK objects needed to construct a live agent — using `pkg/*` only. Re-callability
is what makes dynamic hot-load (1-6) and restart-rebuild (1-11) trivial.

## 2. Scope

**In:**
- Parse `evva-swarm.yml` (manifest): `name`, `workdir`, `leader.agent`,
  `workers[].agent`, `settings{permission_mode,max_iterations}`. **No replicas**;
  duplicate worker names are a load error.
- Parse one agent dir `agents/{main,sub}/{name}/`:
  `system_prompt.md`, `tools/active.yml`, `tools/deferr.yml`, `profile.yml`
  (`model`, `effort`, **`schedule`**), `skills/*/SKILL.md`.
- Produce `agent.AgentDefinition` (As mapped: main→["main"], sub→["subagent"] —
  but note both are roots in Veronica) + a `*skill.Registry` per agent.
- Surface a parsed `Schedule` (cron string or interval) for the scheduler (1-6).

**Out:** constructing the actual `agent.New` (that is 1-4); validating tool
names against the registry beyond shape (warn, don't fail).

## 3. Dependencies & what this unblocks

- Depends on: 1-1.
- Unblocks: 1-4 (space assembly), 1-6 (timer schedule, hot-load), 1-11 (rebuild).

## 4. Technical design

Package `internal/swarm/agentdef`.

```go
type Manifest struct { Name, Workdir string; Leader Member; Workers []Member; Settings Settings }
type Member  struct { Agent string }
type Loaded  struct {
    Def      agent.AgentDefinition
    Skills   *skill.Registry
    Schedule *Schedule  // nil if no schedule
    Role     Role       // leader | worker
}

func LoadManifest(path string) (Manifest, error)
func (l *Loader) Build(dir string, role Role) (Loaded, error)   // ONE dir → one Loaded (re-callable)
func (l *Loader) BuildAll(workdir string, m Manifest) ([]Loaded, []Warning, error)
```

- `Schedule`: support `cron: "*/5 * * * *"` and `every: "30s"` forms; parse to a
  `next(time.Time) time.Time`.
- Reuse `skill.LoadRegistry(filepath.Join(dir,"skills"))`.
- Fixtures: ship a `testdata/agents/` tree (a leader + 2 workers, one with a schedule).

## 5. Acceptance criteria

1. `LoadManifest` parses a valid `evva-swarm.yml`; missing/duplicate worker names
   error with a clear message.
2. `Build` produces an `AgentDefinition` with the right `Name`, `SystemPrompt`
   (verbatim file body), `ActiveTools`/`DeferredTools` (from the two yml files),
   `Model` (from profile), and a non-nil `Skills` registry when `skills/` exists.
3. `schedule` is parsed for both `cron:` and `every:` forms; absence → nil.
4. `Build` is **pure/re-callable**: calling it twice on the same dir yields equal
   results and has no side effects.

## 6. Verification

- Unit tests over `testdata/` fixtures: manifest parse (happy + duplicate-name +
  missing-file), `Build` field-by-field, schedule parse matrix, re-callability.
- No global state; loader holds no process-wide singletons.

## 7. Definition of Done

- [ ] Manifest + per-dir `Build` + `BuildAll`; re-callable, side-effect-free.
- [ ] Schedule parsing (cron + interval) with `next()`.
- [ ] `testdata/agents/` fixtures + table tests green.
- [ ] Only `pkg/agent` + `pkg/skill` imported for SDK objects (invariant #1).
