# Changelog

All notable changes to the evva SDK surface (`pkg/*`) are documented
here. Format roughly follows [Keep a Changelog](https://keepachangelog.com/).

Stability tiers are defined in [`docs/sdk-stability.md`](docs/sdk-stability.md).

## [Unreleased]

## [v1.4.0] — Bundled skills

Fills the empty `# Skills` section every fresh install shipped with. The
skill framework (`pkg/skill`) has been complete and Stable since v1.0; this
release adds evva's first batch of first-party Markdown skills, embedded in
the binary and overlaid onto the disk catalog at boot. A user disk skill
with the same name silently overrides the bundled body — bundled is the
lowest-precedence tier.

Versioning note: this release jumps from v1.1.0 to v1.4.0. Bundled skills
(roadmap phase v1.4) shipped ahead of v1.2 (OpenAI provider) and v1.3 (MCP)
by directive — they deliver day-one value and depend on neither. The version
follows shipping order, not roadmap-phase order; v1.2 and v1.3 remain on deck.

### Added

- **Bundled skills** — five tier-1 SKILL.md bodies, embedded via `go:embed`
  (`internal/skills/bundled`) and overlaid onto the disk catalog by
  `agent.New`:
  - `commit` — draft and create a git commit for the current diff, authored
    as evva.
  - `review` — review a GitHub pull request (uses `gh`).
  - `security-review` — focused security pass on the branch's pending
    changes, with parallel subagent false-positive filtering.
  - `simplify` — three-reviewer parallel cleanup pass (reuse / quality /
    efficiency) followed by direct fixes.
  - `setup-hooks` — teaches the model (and through it, the user) how to author
    `pkg/hooks` entries in `.evva/settings.json`: the schema, the decision
    JSON, the six events, and a seven-step verification flow. Completes the
    v1.1 hooks story.
- **`skill.SourceBundled`** — new `SkillSource` constant; the lowest-precedence
  tier (a same-named disk or programmatic skill wins silently).
- **`skill.Registry.AddBundled`** — inserts a skill at `SourceBundled`,
  silently skipping any name already present (user override wins without a
  warning).
- **`skill.ParseTitleLine`** — exported shared title-line parser used by both
  the disk loader and the bundled loader so the two cannot drift.

### Changed

- `internal/agent/skills.go:loadDiskSkillRegistry` now overlays the bundled
  catalog onto the disk-loaded registry. Hosts that inject their own registry
  via `agent.WithSkillRegistry` are unaffected and still skip bundled.
## [v1.2.0] — OpenAI provider

Closes the OpenAI integrity gap. The `constant.OPENAI` provider, the
`openai.api_key` / `openai.api_url` config fields, and the `/model` picker
already promised OpenAI as a bundled provider, but `pkg/llm/builtins` only
registered Anthropic / DeepSeek / Ollama — selecting OpenAI failed with
`"unknown provider"`. This release ships `pkg/llm/openai` (a focused
Chat-Completions port of `pkg/llm/deepseek` with the OpenAI-specific
deviations called out) and registers it via the builtins side-effect, so
every name in `constant.GetAllProviders()` now resolves through the
factory.

### Added

- **`pkg/llm/openai`** — new bundled provider implementing the full
  `llm.Client` contract over OpenAI's Chat Completions API. Supports
  streaming, tool calling, automatic prompt caching (server-side; reported
  via `Usage.CacheReadTokens`), and reasoning-effort levels mapped onto
  OpenAI's `reasoning_effort` enum (`low` / `medium` / `high`).
- **OpenAI factory registered in `pkg/llm/builtins`** — blank-importing
  `pkg/llm/builtins` now wires anthropic, deepseek, openai, **and** ollama.

### Changed

- **`pkg/constant/llm.go`** — replaced the solitary `GPT_5_5` model entry
  with a fast/pro pair (`GPT_5_4_MINI` / `GPT_5_5`). `MODEL_CONTEXT_SIZE`
  updated to match the documented context windows (400K / 1,050K). The
  `GPT_5_5` entry was also corrected from the old 500K placeholder to
  OpenAI's documented 1,050K.

### Notes

- `openai.Client.SupportsDeferLoading()` returns `false`. OpenAI relies on
  automatic prefix-prompt caching; the agent must therefore keep the
  `tools` array stable across turns — same posture as DeepSeek and Ollama.
- Sampling parameters (`temperature`, `top_p`) are silently dropped for
  reasoning-class OpenAI models (the gpt-5 / o-series fix these at 1).
  The non-reasoning allowlist is empty in this release; revisit when the
  first non-reasoning OpenAI model is added to `constant.OPENAI.Models`.
- Reasoning content is **not** streamed (OpenAI Chat Completions does not
  surface it). For reasoning visibility, use the Anthropic or DeepSeek
  providers, both of which emit `llm.ChunkThinking` deltas.

## [v1.1.0] — Lifecycle hooks

Closes the hooks-system integrity gap: the engine was written and merged
in a prior phase but never wired into the agent loop, so the system prompt
advertised hooks that never fired. This release promotes the hooks package
from `internal/` to `pkg/hooks`, constructs a per-agent dispatcher at build
time, and wires the six fire points (SessionStart, UserPromptSubmit,
PreToolUse, PostToolUse, Stop, Notification) into the loop. A
`settings.json` hooks block now works as advertised.

### Added

- **Lifecycle hooks** — six-event lifecycle hook system: `SessionStart`,
  `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `Notification`.
  Hooks are configured in `.evva/settings.json` (project) or
  `<APP_HOME>/settings.json` (user), with `command` (shell subprocess) and
  `http` (webhook) backends. PreToolUse hooks run before the permission gate
  and can block tools, mutate their input, or override the permission
  decision. PostToolUse hooks can append additionalContext to tool results
  for the model's next turn. Stop hooks can re-enter the loop exactly once.
- **`pkg/hooks`** — public package at `Experimental` stability tier. Exports
  `Load`, `Registry`, `Dispatcher`, `BasePayload`, `Decision`,
  `PreToolUseDecision`, and the six `Event` constants.
- **`agent.WithHookRegistry`** — option for `NewWithProfile` hosts that want
  to opt into hooks (the one-call `agent.New` loads them automatically
  alongside `permission.Load`).
- **Subagent hook inheritance** — subagents share the root's `*Registry` and
  construct their own `Dispatcher` with the subagent's `agent_id` /
  `agent_type` in the payload.
- **Tests** — `pkg/hooks` now has unit tests for matcher, decision, loader,
  runner, HTTP, and dispatcher.

### Changed

- `permissionGate` replaced with `permissionGateWithOverride` so the
  PreToolUse hook's `permissionDecision` (allow/deny/ask) can override the
  gate's behavior.

## [v1.0.0] — SDK v2 complete + LSP: the pkg-only milestone

Cuts `v1.0.0`. The SDK v2 arc (v2.1–v2.5) closes every embedding gap that
forced a host into `internal/`, the flagship `cmd/evva` now builds on
`pkg/*` alone, and the Language Server Protocol tool integration lands in
the same release. The Stable-tier promise in
[`docs/sdk-stability.md`](docs/sdk-stability.md) is now in force.

### Added

- **One-call constructor** — `agent.New(Config, ...Option)` absorbs the
  whole bootstrap from a declarative `Config`: persona resolution (with an
  `evva` fallback), `EVVA.md` / `USER_PROFILE.md` memory + skill auto-load,
  permission store + mode, and the approval/question brokers. New `Config`
  fields: `Persona`, `Personas`, `PermissionStore`, `LLMOptions` (plus the
  existing `Provider` / `Model` / `MaxIters` / `PermissionMode` as optional
  overrides). (SDK v2.4)
- **Public persona surface** — `agent.AgentDefinition`, `AgentRegistry`,
  `BuildAgentRegistry`, `LoadDiskAgents`, `ResolveMainProfile`, and the
  `WithPersonaRegistry` / `WithPersona` options. A host can register
  in-code personas, load on-disk ones (`<AppHome>/agents/{name}/`), drive
  the `/profile` picker, and spawn personas as subagents. (SDK v2.3)
- **Public permission system** — `pkg/permission` (`Store`, `Rule`, `Mode`,
  `Decision`, `Broker`, `Load`, `NewBroker`, `SetOnRequest`, `ParseMode`)
  with `WithPermissionStore` / `WithPermissionBroker`; the agent owns the
  default brokers and emits approval/question events to the sink. (SDK v2.2)
- **Public UI read-models** — `ui.Controller` returns only `pkg/*` types
  (`Messages`, `Usage`, `TodoStore`, `DaemonState`, …), so a separate-module
  UI can fully implement and drive it. (SDK v2.1)
- **Bundled reference TUI as a public package** — `pkg/ui/bubbletea`
  (`New(evvaHome)`), moved out of `internal/`. (SDK v2.5)
- `agent.Agent.Controller() ui.Controller` and `agent.Agent.Shutdown()`;
  `agent.ErrIterLimit` re-export. (SDK v2.4/v2.5)
- **LSP integration** — `pkg/tools/lsp` and the deferred `lsp_request` tool
  (`tools.LSP_REQUEST`): go-to-definition, find references, hover, and
  document symbols via lazily-started language servers managed by the
  daemon system.
- **`examples/full-host`** — a separate Go module reproducing the full
  `cmd/evva` experience on `pkg/*` only; Go's internal-visibility rule
  compiler-enforces zero `internal/` imports (the completeness oracle).

### Changed

- **`cmd/evva` rebuilt on `pkg/*` alone** — zero direct `internal/` imports;
  its ~50-line bootstrap collapsed into one `agent.New(Config, ...Option)`
  call. (SDK v2.5)
- **Deferred-tool loading** revamped to match the reference Claude Code
  on-demand schema-loading model (`ToolSearch` fetches schemas lazily).
- Stability tiers promoted to **Stable**: `pkg/ui`, `pkg/permission`,
  `pkg/toolset`. `pkg/ui/bubbletea`, `pkg/tools/lsp` are Experimental;
  `pkg/update` is Internal-helper.

### Breaking

- **`llm.Client` gained `SupportsDeferLoading() bool`** — providers report
  whether they natively support `defer_loading`; the agent only mutates the
  tools array between turns when they do (preserves prompt caching for
  providers that don't). Custom `llm.Client` implementations must add this
  method. The bundled anthropic/deepseek/ollama clients implement it.
- Package moves: `internal/update` → `pkg/update`;
  `internal/ui/bubbletea_v2` → `pkg/ui/bubbletea` (package `bubbletea`).
  Only relevant to code that imported the pre-1.0 internal paths.

## [v0.2.8-alpha.6] — fs edit/write gate ref parity + partial-read fix

Fixes a divergence where evva was stricter than Claude Code's reference
implementation: offset/limit reads would block edits, creating loops where
the agent couldn't edit files it had seen. Adds four safety/robustness
items evva was missing relative to ref.

### Fixed

- **Drop the `IsPartialView` block** from `CanEdit` / `CanWrite`: reading
  a file with offset or a row limit no longer prevents editing it.
  The edit tool already re-reads the full file and requires `old_string`
  to match uniquely, so the block added nothing but friction.

### Added

- **File-size cap on edit** (`MaxEditFileSize` = 1 GiB): rejects files
  that would OOM the process if read into memory. Mirrors ref's
  `MAX_EDIT_FILE_SIZE`.
- **TOCTOU re-stat guard** (`fileChangedSince`): before every write
  (edit main path, edit empty-file path, write overwrite), re-checks
  that the file's mtime hasn't advanced past the initial stat. If it
  has, the operation is aborted rather than clobbering a concurrent
  modification.
- **Content-hash staleness fallback** (`ContentHash [32]byte` /
  `HashContent`): when mtime advanced but the stored SHA-256 matches
  current content, the edit/write proceeds — absorbs touch, formatter,
  and cloud-sync false positives. Hashing rather than storing full
  content bounds memory (deliberate evva divergence from ref).
- **UNC / network-path guard**: `resolvePath` now rejects `//server` and
  `\\server` prefixes before any normalization, preventing NTLM
  credential leaks.
- **Roadmap doc**: `docs/roadmap/fs-edit-gate-parity.md` — planning
  document with root-cause analysis and implementation decisions.

### Changed

- Tool descriptions for `edit_file` and `write_file` updated to remove
  the "partial-view (offset/limit) → re-read" sentence.

## [v0.2.8-alpha.5] — LSP documentation & project roadmap updates

Docs-only release: adds the LSP module feasibility analysis and development
plan to the roadmap, plus EVVA.md project-structure refinements.

### Added

- `docs/roadmap/lsp.md` — LSP Module Integration: feasibility analysis &
  phased development plan
- Expanded LSP documentation with architecture and implementation details

### Changed

- EVVA.md updated with refined project structure and conventions

### Internal

- Dropped stale task_stop/task_list known-issue note from docs
- `pkg/version.Version` bumped to `0.2.8-alpha.5`

## [v0.2.8-alpha.4] — SDK v2.3: multi-persona / subagent SDK + memory absorption

Third slice of the SDK v2 "harden to v1.0" roadmap
(`docs/evva-sdk/sdk-v2.md`). Promotes the persona system to `pkg/agent` so a
downstream host can register its own main persona (the evva → nono pattern)
and drive the /profile picker + subagent catalog from its own registry — and
folds EVVA.md / USER_PROFILE.md memory loading into the agent.

### Added

- **Public persona surface** on `pkg/agent`: `AgentDefinition` (a closure-free
  DTO carrying the prompt as `SystemPrompt`), `AgentRegistry` with `Register` /
  `Get` / `ListMain` / `ListSubagent`, plus `BuildAgentRegistry` and
  `LoadDiskAgents` constructors.
- `agent.WithPersonaRegistry(*AgentRegistry)` and `agent.WithPersona(name)`
  options; `agent.ResolveMainProfile(cfg, reg, name, opts...)` resolves a
  main-tier Profile by name with skills + memory auto-loaded from config.
- The agent auto-loads the EVVA.md / USER_PROFILE.md snapshot from config at
  construction when the host didn't inject one (a host-supplied snapshot still
  wins), so a host no longer has to call memdir.Load.

### Changed

- `cmd/evva` no longer reads memory files itself — it resolves the initial
  profile through the memory-absorbing path and lets the agent auto-load.
  Memory-load warnings now surface on the agent logger rather than stderr.

### Internal

- Persona conversion rides an internal `AgentSpec` seam (`DefinitionFromSpec` /
  `SpecFromDefinition`) so `pkg/agent` imports no `sysprompt`; the internal
  `AgentDefinition` gains a `PromptBody` field so a definition round-trips back
  to the public DTO.

## [v0.2.8-alpha.3] — SDK v2.2: pluggable permissions

Second slice of the SDK v2 "harden to v1.0" roadmap
(`docs/evva-sdk/sdk-v2.md`). Promotes the permission system to a public,
pluggable package and moves the approval / question broker wiring into
the agent: an interactive host gets approvals by just passing a sink, and
any host can supply its own allow/deny policy with no `internal/` import.

### Added

- **`pkg/permission`** (promoted from `internal/permission`): `Mode`,
  `Rule`, `Store`, `Broker`, `Decision`, `ApprovalRequest`, `Decide`,
  `Load`, `NewBroker`, `SetOnRequest`, the `Behavior*` / `Source*`
  constants, and `PlanModeState` are now public.
- `agent.WithPermissionStore(*permission.Store)` and
  `agent.WithPermissionBroker(permission.Broker)` public options — supply
  a custom rule store or approval policy. (`WithPermissionMode` /
  `WithHeadlessBypass` already existed.)
- The agent owns its default approval + question brokers and emits
  `KindApprovalNeeded` / `KindQuestionNeeded` to the sink itself. An
  interactive host resolves them via `RespondPermission` /
  `RespondQuestion`; with no sink the agent auto-denies. No host broker
  wiring required.

### Changed

- `pkg/agent.New` / `NewWithProfile` no longer install non-interactive
  deny stubs — they defer to the agent's default brokers.
  `NewWithProfile` now honors a caller-supplied `WithSink` for real
  interactive approvals (previously it always denied).
- Subagents inherit the root agent's question broker (matching the
  existing permission-broker inheritance), so a subagent can surface
  `AskUserQuestion`.

### Internal

- `cmd/evva` no longer imports `internal/permission` or
  `internal/question`; its headless CLI sink resolves approval / question
  prompts through the public `Controller`. `buildApprovalEvent` /
  `buildQuestionEvent` moved into `internal/agent/approval.go`.

## [v0.2.8-alpha.2] — Plan mode: named plan files + read-only bash

### Added

- `enter_plan_mode` gains optional `plan_name` parameter — plan files
  now live at `<repo>/.evva/plans/<plan-name>.md` instead of a fixed
  `current.md`. The default (`"current"`) preserves backward
  compatibility so existing sessions see no difference.
- Plan mode now allows read-only bash commands (`ls`, `cat`, `grep`,
  `git status`, `find`, etc.) via the shell classifier. The model can
  inspect the codebase with shell tools without exiting plan mode.
  Mutating and dangerous commands remain denied.

### Changed

- `mode.PlanFilePath` signature changed to `PlanFilePath(workdir, planName string)`.
  Empty `planName` defaults to `"current"` — all existing callers that
  relied on the single-argument form must be updated to pass the plan
  name (usually from `PlanModeState.PlanName()`).
- `PlanModeController` interface gains `PlanName() string` and
  `SetPlanName(name string)`. Implementations (`*agent.Agent`,
  test fakes) delegate to `PlanModeState`.
- `PlanModeState` (internal/permission) stores the active plan name.

### Internal

- `permission.Decide()` pipeline: plan-mode block gains a bash
  read-only carve-out before the hard-deny fallback (step 4c).
- `internal/agent/state_machine.go` reads the plan name from
  `planModeState.PlanName()` when constructing the attachment path.

## [v0.2.8-alpha.1] — SDK v2.1: public UI read-models

First slice of the SDK v2 "harden to v1.0" roadmap
(`docs/evva-sdk/sdk-v2.md`). Closes the internal-type leak on the
`pkg/ui.Controller` surface so a UI in a separate module can implement
the contract without importing evva internals.

### Breaking

- `pkg/ui.Controller` no longer exposes `Session()` (returned
  `*internal/session.Session`) or `ToolState()` (returned
  `*internal/toolset.ToolState`). Both named unreachable internal types,
  so a downstream UI could not satisfy the interface. Migrate to the
  public-typed accessors added below:
  - `Session().GetMessages()` → `Messages() []llm.Message`
  - `Session().Usage` → `Usage() llm.Usage`
  - `Session().LastTurnInputTokens()` → `LastTurnInputTokens() int`
  - `ToolState().TodoStore()` → `TodoStore() *todo.TodoStore`
  - `ToolState().DaemonState()` → `DaemonState() *daemon.DaemonState`
    (now returns nil until the first daemon registers — nil-check)
  - `ToolState().UserPromptQueue().Enqueue(p)` → `EnqueueUserPrompt(p string)`

### Added

- `pkg/ui.Controller` gains `Messages`, `Usage`, `LastTurnInputTokens`,
  `TodoStore`, `DaemonState`, and `EnqueueUserPrompt` — every parameter
  and return type is public (`pkg/llm`, `pkg/tools/todo`,
  `pkg/tools/daemon`). The same six methods are implemented on the agent.
- `docs/evva-sdk/sdk-v2.md` — the SDK v2 roadmap (hardening to a stable
  v1.0; public read-models, pluggable permissions, multi-persona SDK,
  and dogfooding `cmd/evva` onto `pkg/`).

### Internal

- Reference TUI (`internal/ui/bubbletea_v2`) migrated to the public
  accessors; the `todos` / `agents` / `bgtasks` / `monitors` components
  and `app/root.go` no longer import `internal/toolset` or
  `internal/session`.
- `pkg/ui/controller_compile_test.go` — new acceptance gate: a stub
  satisfies `ui.Controller` using only public imports, so a regression
  that re-leaks an internal type fails the build.
- `pkg/version.Version` bumped to `0.2.8-alpha.1`.

## [v0.2.6-alpha.2]

### Fixed

- TUI status bar stuck on "Running" after background task or monitor
  event completes (signal-wake path now transitions to Idle).
- Transcript now renders background task completion notifications
  (`BgResultBlock`) and monitor stream events (`MonitorEventBlock`).
- Added debug logging to `agent.done()` for subagent and main-agent
  completion paths.

## [v0.2.6-alpha.1]

Phase 16 + 17 (merged) — Bash `run_in_background`, real MonitorTool,
event-driven agent. The agent gains a long-lived signal channel + pump
goroutine so detached bash tasks and streaming monitors can wake an
idle loop or fold their results into the next iteration when the loop
is busy. Three companion tools (`task_list`, `task_output`,
`task_stop`) let the model introspect/control bg tasks between fire
and notification.

### Added

- `pkg/tools/shell`:
  - `BgTaskStore`, `BgTaskSnapshot`, `BgTaskStatus` (running / completed /
    failed / killed), `BgTaskHost` interface, `GenerateID()`.
  - `NewBashWithHost(workdir, host)` constructor — the production path
    that powers `bash run_in_background:true`.
  - `task_list` / `task_output` / `task_stop` tools.
- `pkg/tools/monitor`:
  - Real `MonitorTool` (replaces the stub). Spawns a shell command,
    streams stdout line-by-line as agent notifications.
  - `MonitorTaskStore`, `MonitorTaskSnapshot`, `MonitorStatus`,
    `MonitorEvent`, `MonitorEventQueue`, `MonitorHost` interface.
- `pkg/tools.TASK_LIST` / `TASK_OUTPUT` / `TASK_STOP` tool-name constants.
- `pkg/event.KindBgResult`, `KindMonitorEvent`,
  `KindDrainBackgroundTask`, `KindDrainMonitorEvents` + matching
  `*Payload` structs; `Event.Payload()` switch updated.
- `pkg/agent.WithRootContext(ctx)` option — installs the agent-lifetime
  context. The signal pump + every detached bg/monitor goroutine binds
  to this ctx; cancelling it (or calling `Agent.Shutdown`) tears them
  all down.
- `Agent.Shutdown()` method on the public surface (idempotent).
- Two new TUI strips: `bgtasks` (background tasks) and `monitors`
  (streaming watchers). Mirror the agents strip; render below it in
  the layout. Empty strips collapse cleanly.

### Behaviour changes

- `Bash` description now teaches the model about `run_in_background`
  (verbatim ref-Claude-Code copy). The schema description for the
  flag explains the task-id return and points at the companion tools.
- The agent loop's iteration-boundary drains gain
  `drainBackgroundTaskResults` and `drainMonitorEvents` alongside the
  existing wakeup / user-prompt drains.
- Terminal turns (no tool_calls) now re-check `BgTaskStore.HasPending`
  + `MonitorEventQueue.HasPending` before returning. Any pending
  signal triggers one more iteration so the model sees the result
  before idle resumes.
- `cmd/evva` threads its session ctx into `agent.WithRootContext(ctx)`
  and defers `Shutdown()` so Ctrl-C cleans up every detached
  goroutine.

### Internal

- `internal/agent/signal.go` — `AgentSignal`, `SignalKind`,
  `signalPump`, `handleSignal`, `runFromSignal`, `composeBgReminder`,
  `composeMonitorReminder`, `signalReminderMessage`.
- `internal/agent/drain_signals.go` — `drainBackgroundTaskResults`,
  `drainMonitorEvents`, `hasPendingSignals`.
- `internal/toolset/toolset.go` — new fields + accessors:
  `BgTaskStore`, `MonitorTaskStore`, `MonitorEventQueue`, plus the
  narrow `SignalSender` bundle the agent installs in `New`. The
  toolset implements both `shell.BgTaskHost` and
  `monitor.MonitorHost`.
- `pkg/version.Version` bumped to `0.2.6-alpha.1`.

---

## [v0.2.5-alpha.1] — Phase 19 (Out of scope) — Skill SDK + Custom AppConfig

Phase 19 (Out of scope) — public Skill SDK, downstream-owned config
slot, and an end-to-skill-registry-bootstrap-from-the-host shift. The
skill catalog now loads itself from inside `agent.New`; downstream
hosts stop hand-wiring `skill.LoadRegistry` + `WithSkillRegistry`
unless they want a programmatic-only catalog.

### Breaking

- `internal/tools/skill` → `pkg/skill`. The Registry, SkillMeta,
  SkillSource constants, LoadRegistry, and SkillTool are now public.
  Downstream apps that imported the internal path update the import to
  `github.com/johnny1110/evva/pkg/skill`. The new path ships the same
  identifiers plus the additive items listed below.
- `agent.New` now auto-loads the skill registry from
  `cfg.AppHomeSkillsDir + cfg.WorkDirSkillsDir` when no
  `WithSkillRegistry` override is provided. Behaviour for hosts that
  passed their own registry is unchanged; hosts that previously
  *didn't* pass one (e.g. the minimal-host example) now get disk
  skills out of the box. Hosts that want zero skills can pass
  `WithSkillRegistry(skill.NewRegistry())`.

### Added

- `pkg/skill.NewRegistry() *Registry` — empty registry constructor for
  programmatic-only catalogs.
- `pkg/skill.Registry.Add(SkillMeta) error` — registers an in-code
  skill. Validates non-empty name, non-nil BodyFunc, duplicate-name
  rejection. The skill's Source is force-set to `SourceProgrammatic`.
- `pkg/skill.SourceProgrammatic` — third SkillSource value alongside
  `SourceHome` / `SourceWorkDir`.
- `pkg/skill.SkillMeta.BodyFunc func() (string, error)` — lazy body
  loader for programmatic skills. When non-nil, `LoadBody` calls it
  instead of reading from `SkillMeta.Path`. Use this to back skills
  with `embed.FS`, network fetches, or generators.
- `pkg/agent.WithSkillRegistry(*skill.Registry) Option` — public
  override path for the auto-load. The internal helper has existed
  since Phase 6; this exposes it on the SDK surface.
- `pkg/config.Config.CustomConfig map[string]any` — downstream-app
  extension slot. Stores arbitrary key/value pairs that round-trip
  through YAML under the `custom:` section. evva itself never reads
  from this map; consumers cast at use-site.
- `pkg/config.Config.GetCustom(key) (any, bool)` / `SetCustom(key, value) error` /
  `DeleteCustom(key) error` — thread-safe accessors guarded by
  `c.mu`. SetCustom persists via SaveFile so values survive restarts.
- `pkg/config.FileConfig.Custom map[string]any` (yaml tag
  `custom,omitempty`) — on-disk representation of the custom slot.

### Internal

- `internal/agent/skills.go` — new file. Exports
  `loadDiskSkillRegistry(cfg)` and `refsFromRegistry(*skill.Registry)`
  helpers shared by `agent.New`'s auto-load path and `Main`'s
  `nil → auto-load` fallback.
- `cmd/evva/main.go`: removed manual `skill.LoadRegistry`,
  `skillRefsFromRegistry`, `agent.WithSkillRegistry`, and
  `agent.WithSkillRefs` wiring. `runTUI` / `runCLI` signatures
  trimmed by ~20 LOC.
- `pkg/config/config.go`: `Clone()` deep-copies `CustomConfig`.
  `SaveFile()` snapshots and writes the `custom:` section through
  `FileConfig.Custom`.

---

## [v0.2.4-alpha.3] — Round 2 friday follow-up

Round 2 of friday's SDK feedback — five fresh ergonomics fixes
landing on top of Phase 19. Each one collapses a multi-step bootstrap
pattern into a declarative `LoadOptions` field.

### Breaking

- `config.LoadOptions.EnvOverrides` type changed from
  `[]func(*Config) error` to `[]EnvOverride{Name string, Fn func(*Config) error}`.
  Empty `Name` is rejected at Load time. Wrapped errors now read
  `config: EnvOverrides[<Name>]: <err>` for diagnostics. Friday-style
  migration: wrap each existing closure as `{Name: "...", Fn: closure}`.

### Added

- `config.LoadOptions.ProviderCredentials map[string]ProviderCredsFromEnv` —
  declarative LLM-credential wiring. Reads env vars (after EnvAliases
  promotion) and calls `cfg.SetProviderCredentials` for each entry.
  Replaces the "alias env var + EnvOverride that reads it + setter"
  three-step dance.
- `config.LoadOptions.SeedEnvTemplate string` — first-run `.env`
  body. Written to `<AppHome>/.env` when missing; never overwrites
  an existing file. Closes the chicken-and-egg gap where the YAML
  was auto-created but the `.env` was left for the user to discover.
- `kits.GeneralPurposeActive() []ToolName` — sibling of
  `GeneralPurposeKit`. Returns the active half WITHOUT `tool_search`,
  for callers who drop the deferred companion. (Active + tool_search +
  no deferred is pure overhead — the model has nothing to discover.)
- `version.Bare() string` — bare semver without the leading `v`
  prefix. Composes cleanly into hosts that produce their own tag
  formats (`evva 0.2.4-alpha.3` rather than `evva v0.2.4-alpha.3`).
- `docs/extending.md`: new "LoadOptions — the declarative host
  surface" section framing `LoadOptions` as the single declarative
  surface for runtime tuning, with a per-field table.

### Internal

- `pkg/config/load.go`: `applyProviderCredentials` walks
  `ProviderCredentials` and installs creds via
  `cfg.SetProviderCredentials`.
- `pkg/config/load.go`: `seedEnvTemplate` writes `<AppHome>/.env` on
  first launch when the file is missing.
- `pkg/version/version.go`: `Version` bumped to `0.2.4-alpha.3`.

---

## [v0.2.4-alpha.2] — Phase 19 SDK Support sweep

evva is still pre-1.0 so the cleanup pass removed the legacy aliases
that Phase 19a–19d carried for one release; the surface is now lean
and typed end-to-end. Downstream consumers pinned to v0.2.4-alpha.1
needed one-line call-site updates when they bumped to alpha.2 (see
"Removed" below).

### Breaking

- `event.IterLimitPayload.Reached` removed. Use `Iters`.
- `agent.NewProfile` signature change: `model string` →
  `model constant.Model`. String callers wrap with
  `constant.Model("...")`.
- `agent.NewProfileTyped` removed (collapsed into `NewProfile` —
  the typed-model signature is now the only one).
- `agent.WithPermissionMode` signature change: `modeName string` →
  `m agent.PermissionMode`. Replace `WithPermissionMode("bypass")`
  with `WithPermissionMode(agent.PermissionBypass)` or use
  `WithHeadlessBypass()` for the discoverable convenience.
- `agent.WithPermissionModeTyped` removed (collapsed into
  `WithPermissionMode`).
- `config.LoadFileConfig` signature change: `(path string)` →
  `(path, appName string)`. Callers that need the old behaviour
  pass `LoadFileConfig(path, "evva")`.
- `config.LoadFileConfigFor` removed (collapsed into `LoadFileConfig`).
- `config.defaultFileConfig` (package-internal): signature now takes
  an appName parameter. No downstream impact — it's unexported.

### Added

- `pkg/event`
  - `ErrorPayload.Message string` — `err.Error()` populated at emit
    time. Consumers that just want the rendered string no longer need
    to nil-check + call `.Error()`.
  - `IterLimitPayload.Iters int` — matches `RunEndPayload.Iters`
    naming. (`Reached` was removed in this same release — see
    Breaking above.)
  - `Event.Payload() any` — type-switch helper that returns the
    pointer matching `e.Kind`.
  - One-line godoc on every `Kind*` constant and every payload struct
    field.
- `pkg/config`
  - `(*Config).SetProviderCredentials(name, apiURL, apiKey string)
    error` — thread-safe setter for LLM credentials. Prefer over
    direct `LLMProviderConfig[...]` map assignment when racing
    concurrent reads matters.
  - `LoadOptions.EnvAliases map[string]string` — promote downstream
    env-var names onto evva's canonical names before godotenv runs.
  - `LoadOptions.EnvOverrides []func(*Config) error` — post-Load
    mutations for env vars without a YAML hook.
  - First-run YAML's `default_profile` now stamps the caller's
    `LoadOptions.AppName` instead of hardcoded `"evva"`.
  - `LoadFileConfig(path, appName)` — appName-aware. (Breaking
    signature change; see Breaking above.)
- `pkg/agent`
  - `PermissionMode` typed string + constants `PermissionDefault`,
    `PermissionAcceptEdits`, `PermissionPlan`, `PermissionBypass`.
  - `WithPermissionMode(PermissionMode)` is now typed end-to-end.
    (Breaking signature change; see Breaking above.)
  - `WithHeadlessBypass()` — convenience option for non-interactive
    hosts; bundles `WithPermissionMode(PermissionBypass)` with a
    security docstring.
  - `NewProfile` now takes `model constant.Model` directly.
    (Breaking signature change; see Breaking above.)
  - Doc comments on every `SessionInfo` field (closes the docs gap
    from friday feedback #11).
- `pkg/tools/kits` — **new package**.
  - `GeneralPurposeKit() (active, deferred []ToolName)` — canonical
    coding-agent toolkit.
  - `ReadOnlyKit() []ToolName` — audit/explore variant.
  - `CodingKit() (active, deferred []ToolName)` — GeneralPurpose +
    notebook + monitor.
  - `ResearchKit() []ToolName` — read + grep + glob + web + util +
    todo.
- `pkg/version` — **new package**.
  - `Version` constant + `BuildStamp` variable + `String()` formatter.
  - Set `BuildStamp` via `-ldflags` at release time for commit hashes.
- Godoc-visible examples:
  - `pkg/agent/example_test.go` — `ExampleNewProfile`,
    `ExampleNewWithProfile`, `ExampleWithHeadlessBypass`.
  - `pkg/event/example_test.go` — `ExampleSinkFunc`,
    `ExampleEvent_Payload`, `ExampleMulti`.
  - `pkg/config/example_test.go` — `ExampleLoad`,
    `ExampleConfig_SetProviderCredentials`.
  - `pkg/tools/kits/example_test.go` — `ExampleGeneralPurposeKit`,
    `ExampleReadOnlyKit`.
  - `pkg/llm/example_test.go` — `ExampleRegistry_Register`.
- Documentation:
  - `docs/sdk-stability.md` — declares stable / experimental /
    internal-helper tiers per `pkg/` package.
  - `docs/extending.md` — new sections: Charmbracelet pinning,
    headless permission requirement, typed PermissionMode, env-var
    aliasing, tool kits, `Event.Payload()` ergonomics.

### Removed

- `event.IterLimitPayload.Reached` (collapsed into `Iters` — see Breaking).
- `agent.NewProfileTyped` (collapsed into `NewProfile` — see Breaking).
- `agent.WithPermissionModeTyped` (collapsed into `WithPermissionMode` — see Breaking).
- `config.LoadFileConfigFor` (collapsed into `LoadFileConfig` — see Breaking).

### Internal

- `internal/agent/state_machine.go` updated to populate the new
  `ErrorPayload.Message` and `IterLimitPayload.Iters`.
- `internal/ui/bubbletea_v2/components/transcript/transcript.go` and
  `internal/ui/bubbletea_v2/components/status/state_test.go` migrated
  to read `IterLimitPayload.Iters`.
- `cmd/evva/main.go` migrated to read `IterLimitPayload.Iters`.

## [v0.2.4-alpha.1] — 2026-05-22

Initial published tag — Phase 13 SDK split + Phase 14 session storage +
Phase 15 friday proof of concept. See `CLAUDE.md` for the per-phase
deliverables.

[Unreleased]: https://github.com/johnny1110/evva/compare/v1.2.0...HEAD
[v1.2.0]: https://github.com/johnny1110/evva/compare/v1.1.0...v1.2.0
[v1.1.0]: https://github.com/johnny1110/evva/compare/v1.0.0...v1.1.0
[v1.0.0]: https://github.com/johnny1110/evva/compare/v0.2.8-alpha.6...v1.0.0
[v0.2.8-alpha.6]: https://github.com/johnny1110/evva/releases/tag/v0.2.8-alpha.6
[v0.2.8-alpha.5]: https://github.com/johnny1110/evva/releases/tag/v0.2.8-alpha.5
[v0.2.8-alpha.4]: https://github.com/johnny1110/evva/releases/tag/v0.2.8-alpha.4
[v0.2.8-alpha.3]: https://github.com/johnny1110/evva/releases/tag/v0.2.8-alpha.3
[v0.2.8-alpha.2]: https://github.com/johnny1110/evva/releases/tag/v0.2.8-alpha.2
[v0.2.8-alpha.1]: https://github.com/johnny1110/evva/releases/tag/v0.2.8-alpha.1
[v0.2.6-alpha.2]: https://github.com/johnny1110/evva/releases/tag/v0.2.6-alpha.2
[v0.2.6-alpha.1]: https://github.com/johnny1110/evva/releases/tag/v0.2.6-alpha.1
[v0.2.5-alpha.1]: https://github.com/johnny1110/evva/releases/tag/v0.2.5-alpha.1
[v0.2.4-alpha.3]: https://github.com/johnny1110/evva/releases/tag/v0.2.4-alpha.3
[v0.2.4-alpha.2]: https://github.com/johnny1110/evva/releases/tag/v0.2.4-alpha.2
[v0.2.4-alpha.1]: https://github.com/johnny1110/evva/releases/tag/v0.2.4-alpha.1
