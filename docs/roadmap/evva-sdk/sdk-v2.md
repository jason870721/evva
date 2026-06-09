# evva-sdk v2 — Hardening to a stable v1.0

> Status: **shipped in `v1.0.0`.** v2 was a **completeness + hardening**
> arc that closed the remaining embedding gaps and cut `v1.0.0` (folded
> together with the LSP tool integration). All phases v2.1–v2.6 are done.

## Context

The v1 public surface already shipped through Phases 13–19
(`v0.2.7-beta.1`): two constructors (`agent.New` / `NewWithProfile`),
provider / tool / skill registries, `tools/kits`, declarative
`config.LoadOptions`, session storage + `/resume`, compaction,
background tasks + monitors, stability tiers, `CHANGELOG.md`,
`extending.md`, and the `minimal-host` example. The "friday" consumer's
`sdk-feedback.md` punch-list is **closed**.

What's left is not features — it's **completeness**. Three seams still
force a real host to reach into `internal/`, and the flagship app
(`cmd/evva`) does not yet build on its own SDK. v2 closes those seams,
proves completeness by rebuilding `cmd/evva` on `pkg/` alone, and tags
`v1.0.0`.

**Beneficiaries — both at once:** external developers get a surface that
can actually host a full app (the friday pattern, no `internal/`
imports); evva herself gets the architectural payoff of a flagship that
consumes its own SDK — the only reliable test that the SDK is complete.

## North star — the completeness oracle

> `cmd/evva` imports **zero `internal/`** for any agent / UI / SDK
> concern. If the bundled reference TUI and CLI can be built on the
> public contract, a third party's can too.

Today `cmd/evva/main.go:16-21` imports `internal/agent`,
`internal/memdir`, `internal/permission`, `internal/question`,
`internal/ui/bubbletea_v2`; and `pkg/agent` itself re-wraps
`internal/permission` + `internal/memdir`. v2 removes that.

## Out of scope (a later "expansion" arc / v3)

- **MCP client support** — tagged "v2 tier" in `CLAUDE.md`, but net-new; deferred.
- **Server / HTTP-gRPC adapter, cross-language / FFI bindings** — v2 targets in-process Go hosts only.
- **Multi-agent teams / SendMessage** — already `CLAUDE.md` out-of-scope.

## The three gaps that block hosting

1. **UI read-models leak internal types.** `pkg/ui.Controller.Session()`
   returns `*internal/session.Session` and `ToolState()` returns
   `*internal/toolset.ToolState` (`pkg/ui/ui.go:79,85`). A
   separate-module UI cannot name these types.
2. **Permissions / questions are hand-wired in the host.**
   `cmd/evva/main.go:175-291` builds the broker, calls
   `permission.SetOnRequest`, and hand-rolls `buildApprovalEvent` /
   `buildQuestionEvent`. `internal/permission` + `internal/question` are
   private; there is no public broker.
3. **Personas are internal.** `AgentDefinition`, `AgentRegistry`,
   `BuildAgentRegistry`, `ResolveMainProfile`, the disk loader, and
   `memdir` all live in `internal/` (`main.go:100-117`).
   `pkg/agent.New` hardcodes `WithPersona("evva")`.

## Phases (dependency-ordered)

### v2.1 — Public UI read-models  *(unblocks dogfooding the TUI)*

Make `pkg/ui.Controller` return only public types. The needed types are
**already public**, so this is signature work, not a big move:

- Replace `Session() *session.Session` with `Messages() []llm.Message`
  (`pkg/llm.Message` is public) alongside the existing `SessionInfo`
  (counts + usage).
- Replace `ToolState() *toolset.ToolState` with type-safe accessors over
  already-public stores: `Todos() []todo.Todo`,
  `Daemons() []daemon.DaemonSnapshot`, and
  `Subscribe(func(observable.Change))` — `pkg/observable`,
  `pkg/tools/todo`, `pkg/tools/daemon` are already public.
- `internal/session` + `internal/toolset` stay internal; only the read
  *views* go public.
- Files: `pkg/ui/ui.go`, `pkg/agent/agent.go` + `types.go` (adapter
  accessors), `internal/agent` (back the new methods).

**Acceptance:** a UI in a separate module compiles against `Controller`
with no `internal/` import.

### v2.2 — Pluggable permissions + questions  *(absorb the broker)*

- Promote `internal/permission` → **`pkg/permission`**: `Store`, `Rule`,
  `Mode`, `Decision`, `ApprovalRequest`, `Broker`, `ParseMode`, `Load`,
  `NewBroker`, `SetOnRequest`, the `Behavior*` / `Source*` constants, and
  the classifier hints.
- **Absorb the default wiring into the agent**: the agent emits
  `KindApprovalNeeded` / `KindQuestionNeeded` itself (payloads already in
  `pkg/event`) and accepts `RespondPermission` / `RespondQuestion`
  (already on the Controller). Delete the host's `buildApprovalEvent` /
  `buildQuestionEvent` / `SetOnRequest` boilerplate (`main.go:175-291`).
- `internal/question` folds into the agent (no public broker needed —
  the question flow is event + `RespondQuestion` only). Keep the public
  `pkg/event` `QuestionItem` / `QuestionOption` as the wire shape.
- Public options promoted to `pkg/agent`: `WithPermissionStore`,
  `WithPermissionBroker`; keep `WithHeadlessBypass` and the safe
  auto-deny default.

**Acceptance:** `minimal-host` implements a real allow/deny callback with
no `internal/` import; the agent emits approval events with no host
broker wiring.

### v2.3 — Multi-persona / subagent SDK  *(the evva→nono seam)*

- Promote persona types to `pkg/agent`: `sysprompt.AgentDefinition` →
  `pkg/agent.AgentDefinition`; `AgentRegistry` + `BuildAgentRegistry` +
  `ResolveMainProfile` → public (`pkg/agent/registry.go`);
  `internal/agent/loader.Load` → `pkg/agent.LoadDiskAgents`
  (the `<EVVA_HOME>/agents/{name}/` schema from `CLAUDE.md`).
- **Absorb memory** (acts on the in-code TODO at `main.go:81`): fold
  `memdir.Load` into the agent — auto-load `EVVA.md` + `USER_PROFILE.md`
  from config, inject into the resolved persona's prompt, and surface
  warnings as events instead of host `stderr`. `config.GetEnableAutoMemory`
  already exists; add a `WithMemory` escape hatch.
- Public options: `WithPersonaRegistry`, `WithPersona`.
  `ListMainProfiles()` / `SwitchProfile()` already exist on the
  Controller — they light up once the registry is public.

**Acceptance:** a host registers a custom persona (in-code + on-disk) and
spawns it as a subagent; the `/profile` picker works in a third-party UI.

### v2.4 — One constructor that absorbs the bootstrap  *(de-hardcode cmd/evva)*

- Converge `pkg/agent.New(Config)` and `NewWithProfile` so the Config
  path resolves a persona (with `evva` fallback), auto-loads memory +
  skills, and wires the permission store / broker / mode + question flow
  — all from config + options, none hand-wired. This collapses
  `main.go:82-138`'s ~130 lines of bootstrap.
- The features called out for review — **`/resume`, `/compact`, profile
  switch, permission cycle, effort** — already exist on the `Agent`
  interface (`pkg/agent/types.go`). This phase makes them work
  **out-of-the-box from one constructor**, with no host plumbing.

**Acceptance:** a ~40-line host gets TUI + personas + permissions +
resume + compaction.

### v2.5 — Move the reference TUI to `pkg/` + rebuild cmd/evva  *(north-star payoff)*

- Move `internal/ui/bubbletea_v2` → **`pkg/ui/bubbletea`**, rebuilt to
  depend only on `pkg/ui` + `pkg/event` + `pkg/agent` (possible only
  after v2.1).
- Rewrite `cmd/evva/main.go` to import only `pkg/*`: the converged
  constructor (v2.4), `pkg/ui/bubbletea`, `pkg/permission`.
  `internal/update` (self-update) is evva-product glue — promote to
  `pkg/update` or document it as the single carve-out.
- Rewrite `examples/minimal-host` as the canonical **full-host** example
  mirroring `cmd/evva` at a fraction of the size; keep a tiny-host too.

**Acceptance:** `go list -deps ./cmd/evva` shows zero `internal/` (modulo
the documented self-update carve-out); evva runs identically.

### v2.6 — Cut v1.0

- Promote stability tiers (`pkg/ui`, `pkg/toolset`, `pkg/permission` →
  Stable) in `docs/sdk-stability.md`; add persona / permission /
  read-model sections to `docs/extending.md`; CHANGELOG the breaking
  moves; tag `v1.0.0`.

## Verification

- **Compile gate (the oracle):** a new `examples/full-host` (separate
  package — Go forbids its `internal/` imports) that reproduces the
  `cmd/evva` experience on `pkg/` only. CI runs `go build ./examples/...`.
- **Dep check:** a Make / CI target asserting
  `go list -deps ./cmd/evva ./pkg/ui/bubbletea | grep internal/` is empty
  (modulo the carve-out).
- **Behavior:** build evva, run the TUI — `/profile`, permission prompts
  + Shift+Tab cycle, `/resume`, `/compact`, and background tasks all work
  as before.
- **Tests:** existing `*_test.go` pass; add separate-module compile tests
  per the existing `pkg/agent/downstream_test.go` pattern for each new
  public package.
