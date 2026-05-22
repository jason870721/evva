# Changelog

All notable changes to the evva SDK surface (`pkg/*`) are documented
here. Format roughly follows [Keep a Changelog](https://keepachangelog.com/).

Stability tiers are defined in [`docs/sdk-stability.md`](docs/sdk-stability.md).

## [Unreleased]

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

[Unreleased]: https://github.com/johnny1110/evva/compare/v0.2.4-alpha.2...HEAD
[v0.2.4-alpha.2]: https://github.com/johnny1110/evva/releases/tag/v0.2.4-alpha.2
[v0.2.4-alpha.1]: https://github.com/johnny1110/evva/releases/tag/v0.2.4-alpha.1
