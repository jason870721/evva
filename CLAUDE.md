# evva — Project Vision and Roadmap

## Important

We are in dev phase now, evva is not released, we could revamp and change api anytime if it should be.

## Vision

`evva` is a ReAct coding agent for the terminal, written in Go. The architecture follows Claude Code in spirit but keeps the moving parts small on purpose: one narrow `llm.Client` interface bridging multiple providers (Anthropic, DeepSeek, OpenAI, Ollama), one `tools.Tool` interface, one observable store fanning state to any UI implementation, one agent loop.

The unifying idea is **one runtime, many personas, swappable UI**:

- A **persona** is a main-tier agent definition — its own tools, system prompt, model preference, and personality. `evva` (a professional software engineer) is one persona. `nono` (a financial manager), `noen` (a math teacher), and any others a user creates are siblings, not subclasses.
- The same runtime drives every persona. Switching personas is `/profile <name>`, not a new binary.
- A persona can spawn another persona as a subagent for cross-domain work — `evva` can delegate a costing question to `nono` without leaving the session.
- Adding a new LLM provider, tool family, persona, or UI implementation is a one-package change.

`evva` is **not** trying to be a drop-in Claude Code. It borrows the harness shape because that shape is what current frontier models behave best under, and it ports tool descriptions verbatim where reasonable so the model sees prompts close to what it was trained on. Where Go semantics, terminal constraints, or evva's narrower scope justify divergence, it diverges intentionally.

The reference TypeScript source lives at `evva/ref/src/`. Treat it as the source of truth for tool descriptions, harness structure, and agent definitions — port from it, don't reinvent.

---

## Agent definitions

All agents — main personas and subagent kinds alike — share one on-disk layout:

```
<EVVA_HOME>/agents/{name}/
├── system_prompt.md
├── tools.yml          # { active: [...], deferred: [...] }
└── meta.yml           # { as: [main, subagent], model: ..., when_to_use: ... }
```

Built-in agents (Main / Explore / Plan / GeneralPurpose) ship as Go-defined `AgentDefinition` structs. User-authored agents are loaded from disk at startup; the loader merges Go + disk into one registry. `agent_type` is a string, not a closed enum, so external projects can register their own personas (e.g. a future `nono` web service registers as a remote agent endpoint).

The `as:` field controls where an agent shows up:

| `as:` value | Visible as |
| --- | --- |
| `[main]` | `/profile` startup picker only |
| `[subagent]` | Agent tool's `subagent_type` list only |
| `[main, subagent]` | Both — used for personas that other personas can delegate to (the `evva → nono` pattern) |

One schema, one loader, two visibility surfaces. This is also the seam Phase 6 (profile switch) uses to enumerate personas.

---

## Roadmap

Phases are ordered by dependency — earlier phases unblock later ones. Each phase is one focused chunk of work: Go ports of the reference TypeScript, plus the connective tissue (memory, permissions, hooks) that ties the harness together.

### Phase 0 — Sysprompt rework + EVVA.md + USER_PROFILE.md ✅️

Foundation. Every later phase ships prompt strings, so the prompt scaffold needs to be stable first.

- Refactor `internal/agent/sysprompt/` from section toggles to **per-agent prompt builders**. Each agent owns its full harness, mirroring `ref/src/tools/AgentTool/built-in/*Agent.ts`.
- New `internal/memdir/` package. Loads `<workdir>/EVVA.md` and `<EVVA_HOME>/USER_PROFILE.md` and injects them into the sysprompt at session start.
- Rewrite the harness / tool-guide sections against `ref/src/constants/prompts.ts` and the per-tool prompt files.
- Wire cross-references (Read ↔ Edit, Agent ↔ subagent_type list, plan-mode ↔ AskUserQuestion) through string constants so descriptions stay consistent as tools evolve.

### Phase 1 — Filesystem parity (Read / Write / Edit / Glob) ✅️

Port `ref/src/tools/FileReadTool / FileEditTool / FileWriteTool / GlobTool` descriptions verbatim; drop evva current Write/Edit/Read tools (many bug in current evva fs tools), can copy claude code design.

- Port descriptions + parameter schemas + implement from `ref/src/tools/Read/Edit/Write/`.
- New `internal/tools/fs/glob.go` — mtime-sorted file matching. Today evva has `shell.Grep` + `shell.Tree` but no dedicated Glob.
- TUI diff render parity for `Edit` and `Write` — match Claude Code's hunk layout.
- Tighten `ReadTracker` semantics to match Claude Code's "must Read before Edit / overwrite-Write."

### Phase 3 — Permission system + Bash classifier + safe/auto modes ✅️

Unblocks plan mode (Phase 7) and worktree (Phase 10). Plan mode is a permission mode, not a standalone tool pair.

Design questions resolved at the start of this phase:

- Rule grammar — glob? regex? per-tool? Reference: `ref/src/utils/permissions/permissionRuleParser.ts`.
- Storage scope — project (`.evva/permissions.json`) + per-session (design session storage in `<EVVA_HOME>/sessions/{session_id}/` prepare for phase 13).
- permit pattern list in project permissions.json is always bypass.
- Lifecycle — ask-once vs allow in this session vs allow in this project vs deny(with optional user input reason); mode transitions (`default: accept_edits` → `plan` → `bypass(auto)`).
- Override flow — equivalent of `--dangerously-skip-permissions`, sandbox flag, etc.
- Subagent inheritance — follow the ref source code design maybe (I have no idea about this).

Work:

- New `internal/permission/` — rule grammar, mode state machine, pre-tool-use hook in the agent loop.
- Port `ref/src/tools/BashTool/bashClassifier.ts` + `dangerousPatterns.ts` into `internal/tools/shell/classifier.go`.
- TUI: approval prompt component under `components/approval/`, mode indicator in the status bar.
- Modes: `default = accept_edits | plan | bypass | auto`.

### Phase 4 — Hooks system ✅️

Compositional with permissions. Lets users wire validation, auto-format, custom logging, or block known-bad commands without touching evva's source.

- New `internal/hooks/` — event types (`SessionStart`, `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Stop`, `Notification`), dispatcher, settings-file bindings.
- Wire hook invocations into `internal/agent/loop.go` between iterations and around tool dispatch.

### Phase 5 — TodoWrite (replaces current task_* tools) ✅️

evva's current `internal/tools/task/` is **conceptually TodoWrite** — in-session ephemeral planning. The six-tool layout (`task_create`, `task_get`, `task_list`, `task_update`, `task_output`, `task_stop`) doesn't match Claude Code's design and conflates planning with background-process management. Rebuild it.

- Delete `internal/tools/task/` (six tools).
- Delete the `mainTaskPlanningSection()` function from `internal/agent/sysprompt/main_agent.go` and drop `nameTaskCreate` / `nameTaskUpdate` / `nameTaskList` from `internal/agent/sysprompt/toolnames.go`. (Phase 0 moved the task-planning copy out of `sections.go` and into the main-agent builder; the old `sections.go` no longer exists.)
- New `internal/tools/todo/` — single `todo_write` tool: `todos: [{content, activeForm, status}]`, full-list-replacement semantics. Port description from `ref/src/tools/TodoWriteTool/prompt.ts`. Add `nameTodoWrite` to `toolnames.go` and a new `mainTodoSection()` fragment in `main_agent.go`.
- Rename `internal/ui/bubbletea_v2/components/tasks/` → `components/todos/`. Reuse the existing observable store wiring (just rename `TaskGroup` → `TodoStore`).
- The "real" process tools (`Monitor`, `task_output`, `task_stop`) come back in a future phase tied to `Bash run_in_background`.

### Phase 6 — Profile manager + `/profile` switch + cross-persona delegation ✅️

This is the **payoff phase** for everything in Phases 0–2: evva, nono, noen become first-class swappable personas, and `evva → nono` delegation works.

- `/profile` slash command + TUI picker (lists every agent in the registry with `as: [evva, nono, ...]`) also rename Main profile to Evva profile, make a default profile into evva-config.yml.
- Profile switch resets the session — provider-locked state (Anthropic `ThinkingSignature`, DeepSeek `reasoning_content`) can't carry across personas, and the system prompt is fully different anyway.
- The Agent tool's `subagent_type` enum becomes the union of every agent with `as: [subagent, ...]` — including personas marked `as: [main, subagent]`. That union is how `evva` ends up able to spawn `nono` as a subagent.
- The "subagents cannot spawn subagents" invariant stays in place.
- TUI refine, add main agent profile name to the status bar (replace curren hardcode evva).

### Phase 7 — Plan mode (EnterPlanMode / ExitPlanMode) ✅️

Bundled with Phase 3. Plan mode is `permission_mode: plan` plus a `plan_file` workflow, not a freestanding feature.

- Port `ref/src/tools/EnterPlanModeTool/prompt.ts` + `ExitPlanModeTool/prompt.ts`.
- plan docs can put in project scope, {workdir}/.evva/plans/{plan_name}.md or can follow ref source code.
- Implement the Plan agent profile — read-only tools only, plan-file output. The skeleton already exists at `internal/agent/profiles.go`.
- Wire `ExitPlanMode` to restore the previous permission mode (`default` or whatever was active before enter plan mode).
- add user-guide in docs/user-guide to teach user how to use plan mode.

### Phase 8 — AskUserQuestion ✅️

UI-heavy port. The tool surface is small; the TUI does most of the work.

- Port `ref/src/tools/AskUserQuestionTool/prompt.ts`.
- TUI: question/answer overlay with single-select, multi-select, and side-by-side preview support (mockups, code snippets, diagrams).
- Wire the answers + annotations back into the tool result envelope.
- Integrate with the plan mode, before make the final plan can ask user several questions with suggest answers/solutions (can adjust EnterPlanMode tool desc).
- Port ref source code UX, allow user choose question's answer or fill by themself, user can edit all answer before submit all. using left right key to switch questions.

### Phase 9 — User-profile and project memory ✅️

The agent that maintains 

- `<EVVA_HOME>/USER_PROFILE.md` -> global user profile (about user info)
- `<EVVA_HOME>/projects/{project-name}/MEMORY.md` -> global project memory (about project info)

Design points:

- **Trigger** — whenever agent want to do (need update evva's system prompt).
- **Tools** — `update_user_profile` (writes to `USER_PROFILE.md`). `update_project_memory` (writes to `MEMORY.md`).
- **Output shape** — fixed sections (`## Preferences`, `## Working style`, `## Recurring topics`) so updates merge cleanly. Free-form rewrites drift and become useless within a few sessions.
- **Opt-out** — enabled by default; one-line notice on first session; `/config` toggles it off.
- Port ref source code tool desgin and prompt (especially agent system prompt),
- I'm not sure should we inject project memory into the system prompt, or just let agent know where to find it.

### Phase 10 — Worktree tools (EnterWorktree / ExitWorktree) ✅️

Niche. Ship after the higher-leverage phases.

- Port `ref/src/tools/EnterWorktreeTool/prompt.ts` + `ExitWorktreeTool/prompt.ts`.
- Implement `git worktree add / remove` plumbing.
- Wire AgentTool's `isolation: "worktree"` parameter to the same code path.
- Update agent(Main, General) system prompt 

### Phase  11 - Refine the Agent System Prompt  ✅️

Currently evva is kind of stupid like strange to all the tools the feature we built so far. 

- port ref/ source code claude code system prompt to evva, make evva stronger on tool usage and enhance work/coding ability. (including Explore, General Subagent System Prompt)
- port ref claude code all system prompt 1:1 (except for evva name and tool name interpolation, which is already handled by `toolnames.go`), and add evva style prompt (mix them together)
- plan mode refine: plan mode is important, learn from ref source code, how they design plan mode workflow and system prompt. When user enter plan mode by manual, 
- the plan mode system prompt hint should inject into user's input prompt(first prompt during plan mode), and since agent exit plan mode, the mode should be reset to default and also tui mode display should be sync with agent current mode.

More about plan mode use experience:

- I tried to use plan mode by manual, agent's first attempt is using write (she didn't know she is in plan mode), and then she try to exit plan mode and exit_plan tool result tell her no current.md in plan dir can't exit, then she try to plan something and put into plan/current.md
- and she is not try to explore and thinking during the plan mode, she just write some easy plan and exit plan mode.

Those are the main reason why I think plan mode is important to refine. 


### Phase 12 - Model Efforts ✅️

- support switch Model effort in TUI with `/effort` slash command
- 4 class of model effort:
  - `low`:
  - `medium` (default)
  - `high`
  - `ultra`
- each llm implement can convert the effort to the provider's API request params. if provider only support 2 class of effort, map `low` → "fast" and `medium`/`high`/`ultra` → "best" (or equivalent).

### Phase 13 - BIG Revamp EVVA to support other open source project  ✅️

Currently, evva is just a ReAct Agent with tui, all code stay in internal mostly.

Now we need allow other projects borrow the agent (don't expose agent core) 

  What we need support? 
  - Developers can create a openclaw style long live agent with evva's agent pkg.
  - Developers Customize agent profile
  - Share all system tools (read, write, edit, web_search...), and allow developers customize agent profile and also allow them create their own tools and register into ToolRegistry
  - Customize config (MAX_TOKEN, MAX_ITER, LOG_DIR, LOG_LEVEL, MODEL, EFFORT, MODE...) -> decoupling AppConfig
  - Allow other developers create their own tui with evva agent core
  - Allow other developers create their own llm client implements as a llm model option.

  What should not support?
  - Developers can't change emit event kind, and agent loop logic.

Phase 13 is a big change of evva, this Phase can make to multi sub phase 13a, 13b, 13c, 13d ...

Write them down below:

Ship order: **13b → 13a → 13c → 13d → 13e**. 13b is small and focused (LLM registry) so we prove the extension pattern early. 13a (config DI + `AppHome` rename) follows because 13c (public tool families) blocks on it.

#### Phase 13b — Public LLM provider registry  ✅️

Mirror the tool registry pattern for LLM clients. Built-in providers move public per the user's "max reuse" choice.

- New `pkg/llm/` carrying the leaf files (`client.go`, `message.go`, `params.go`, `stream.go`, `tool.go`, `usage.go`, `errors.go`) currently under `internal/llm/`.
- Move the three provider packages: `internal/llm/{claude,deepseek,ollama}` → `pkg/llm/{claude,deepseek,ollama}`.
- New `pkg/llm/api.go`: `APIConfig{ApiURL, ApiSecret, Models}` replaces `configs.LLMProviderAPIConfig`. The `configs` package keeps a type alias for one release.
- New `pkg/llm/registry.go`: `Registry.Register(name, ClientFactory)` + `DefaultRegistry()` pre-populated by `pkg/llm/builtins.go`.
- Replace `internal/llmfactory/factory.go:Of`'s hardcoded switch with `pkg/llm.DefaultRegistry().Build(...)`. Keep `Of`'s signature unchanged so 13a can do the config DI work cleanly.
- Downstream extension: `pkg/llm.DefaultRegistry().Register("gemini", geminiFactory)` before `agent.New`.

#### Phase 13a — Decouple `AppConfig` from the singleton, rename `EvvaHome` → `AppHome`  ✅️

Every `configs.Get()` call becomes a value passed by reference. The singleton goes away (or becomes a thin shim). `EvvaHome*` fields rename to `AppHome*` and accept a constructor argument so downstream apps can choose their own home dir.

- Move `configs/app_config.go` → `pkg/config/config.go` (struct + getters/setters; no `Get()`).
- Split loader: `pkg/config/load.go` exposes `Load(appName, appHome, workdir string) (*Config, error)` and `LoadDefault() (*Config, error)` (the latter computes `appName="evva"`, `appHome=~/.evva/` for backward compat). `AppName` becomes a constructor parameter, not a package-level const.
- Field renames: `EvvaHome` → `AppHome`, `EvvaHomeSkillsDir` → `AppHomeSkillsDir`, `EvvaHomeUserProfile` → `AppHomeUserProfile`, `EvvaHomeConfigFile` → `AppHomeConfigFile`.
- New `WithConfig(cfg)` agent option. The internal agent stashes `a.cfg`; every internal package that today calls `config.Get()` (agent loop files, llmfactory, logger, tools/fs, tools/web, tools/dev) reads from `a.cfg` instead.
- Tools that need config reach it through `*toolset.ToolState`, which gains a `Config()` accessor (same late-bound pattern as the skill registry / subagent spawner).
- Delete `internal/llmfactory/` once the registry is callable directly from `internal/agent/`.

#### Phase 13c — Public tool + toolset surface (tool families go public too)  ✅️

Tool family packages move public per the user's "max reuse" choice.

- Move tool-interface files to `pkg/tools/`: `tool.go`, `name.go`, `descriptor.go`, `stub.go`.
- Move tool family packages to `pkg/tools/`: `fs/`, `shell/`, `web/`, `util/`, `notebook/`, `monitor/`, `cron/`, `todo/`. Stay internal: `meta/`, `mode/`, `skill/`, `ux/`, `dev/` (they reference evva-specific runtime — spawner, plan controller, skill registry, question broker).
- Move `internal/toolset/{registry.go, tags.go}` → `pkg/toolset/`. `ToolState` stays internal because it carries internal references.
- New `pkg/tools.State` interface — the narrow surface a custom tool factory consumes (`Logger()`, `Config()`, `Workdir()`). `internal/toolset/ToolState` satisfies it.
- New `WithCustomTool(name, factory)` agent option.

#### Phase 13d — Public agent + UI surface  ✅️

- Move `internal/agent/event/` → `pkg/event/`. Every payload, Kind constant, Sink, Multi, BubbleUp, Discard, SinkFunc.
- Move `internal/observable/` → `pkg/observable/`.
- Move `internal/ui/ui.go` → `pkg/ui/ui.go` (UI, Controller, Skill, ProfileChoice, PermissionDecision, QuestionResponse). Bubbletea v1/v2 implementations stay internal.
- Expand `pkg/agent`: public `Profile` type with `NewProfile(...)` constructor; new options `WithSink`, `WithLLMRegistry`, `WithToolRegistry`, `WithConfig`, `WithPermissionMode`.
- The agent loop and event emission internals stay in `internal/agent/`. Downstream apps cannot change `Kind` values or add new ones — this is the Phase 13 invariant.

#### Phase 13e — Rewire reference TUI + downstream example ✅️

- Rewrite `cmd/evva/main.go` to use only `pkg/` for the agent construction path.
- `internal/ui/bubbletea_v2/` becomes a downstream-style consumer: imports `pkg/event`, `pkg/ui`, `pkg/agent` only (besides its own components).
- Add `examples/minimal-host/main.go` (~50 lines) demonstrating custom home dir + custom tool + custom LLM provider + custom sink.
- Add `docs/extending.md` covering all the extension points.

### Phase 14 - Session Storage (/resume) ✅️

- support `/resume` slash command to resume a session from a previous session file.
- store session file in `<EVVA_HOME>/sessions/{session-id}/{timestamp}.json}`.
- resume list can show the first 150 chars of user input prompt for every session.
- store the session at the end of each agent loop end.
- if compact, clean the session file and restart again.
- notice should store the llm signature for claude. and thinking block for deepseek.
- correct me if anything is wrong above.

### Phase 15 - Proof evva can work as a agent SDK ✅️

In Phase 13 we revamp evva to let it become a golang agent SDK as well. now we need using evva to build a agent project just like evva
to proof her ability.

Requirement: build a ReAct agent with evva SDK out of evva workdir -> `/mnt/friday`

Goal: proof evva is easy to use as SDK and fast build. and find something we can improve for evva SDK.

- simple tui (bubbletea) allow user chat with agent friday and give agent order.
- llm provider default is deekseek + deepseekv4-pro model
- friday global home:` ~/.friday/` workdir home: `<repo>/.friday/` 
- configurable agent param through ~/.friday/.env like (LOGDIR, LOGLEVEL, APIKEY, MAX_ITERS...)
- customized agent profile with same tool registry as general type (read, write, edit, bash...) create a simple system prompt for friday.

### Phase 16 - Bash `run_in_background` param implement

Goal: port ref source code to implement evva's bash `run_in_background`

- ToolState add BackgroundTasks which maintain a task list
- Background tasks status should show on tui to let user see it (subagents bg task should bubble up), Remove when bg task complete or failed (before remove print "task-xxx completed." on the transcript) 
- crete a `chan` for agent, if a background task finished, return result through this `chan` and proactive invoke agent loop (event driven) and also emit `KindBgResult` by Sink to let tui react.
  - this `chan` will be shared with Phase-17 monitor, so the sync data should have a type: `bg_result` or `monitor_event`
  - If agent current status in idle: trigger agent loop with prompt `<system-reminder>background task complete, result: exit code 0 \n {result} </system-reminder>`
  - If agent current status is busy, update the BackgroundTasks state (store the bg result) wait for next iter `drainBackgroundTaskResult()`
  - `drainBackgroundTaskResult()` drain background task final result on every agent loop begin, drain a complete or failed result into prompt `<system-reminder>` and remove it from BackgroundTasks, emit event `KindDrainBackgroundTask`
- be careful about agent status changing (race problem).
- update agent loop, check drain queue is empty before leave, if not empty, keep that loop again.

### Phase 17 -  MonitorTool

Goal: port ref source code MonitorTool.ts let evva can be a event driven agent.

- Integrate with ToolState.MonitorTasks task status is `monitoring` and MonitorEventQueue.
- use same `chan` as Phase 16 created. sync event through this chan type is `monitor_event`
- If agent current status in idle: trigger agent loop with prompt `<system-reminder>monitor task event: {event} </system-reminder>`
- If agent current status is busy, push the event into MonitorEventQueue waiting for next iter `drainBackgroundTaskResult()`
- `drainMonitorEvent()` drain monitor event result on every agent loop begin, drain prompt `<system-reminder>` and remove it from BackgroundTasks, emit event `KindDrainBackgroundTask`
- be careful about agent status changing (race problem).

### Phase 18 — MCP support + bundled skills (v2 tier)

Closes the gap with Claude Code's plugin/skill ecosystem.

- MCP server config + discovery; dynamic tool registration as deferred tools (so `ToolSearch` picks them up).
- Port `ListMcpResources` / `ReadMcpResource`.
- Bundle a small set of skills inspired by `ref/src/skills/bundled/`: `/commit`, `/review`, `/security-review`, `/simplify`.

### Phase 19 — SDK Support (professional-grade Agent SDK) ✅️

Phase 15 proved evva works as an SDK by building friday on top of it. Building it surfaced a concrete list of friction points (see `docs/evva-sdk/sdk-feedback.md` for the per-finding breakdown). Phase 19 turns those findings — and the cross-cutting "what makes a Go SDK feel professional" items they imply — into shipped surface.

Three principles guide every sub-phase:

1. **Typed > stringly-typed**. Every public enum gets a typed Go type, not a `string` with magic values. Typos become compile errors.
2. **Helpers > raw maps**. Mutating internal state through a public map slot (today: `cfg.LLMProviderConfig[...] = ...`) is replaced by a thread-safe setter that validates on write.
3. **Examples are docs**. `example_test.go` files in every public pkg surface make the canonical usage show up on pkg.go.dev — the first place a consumer reads the API.

Phase 19 collapses every legacy / parallel API into its canonical form in one release. evva is still pre-1.0 (dev mode), so a one-line caller migration is cheaper than carrying deprecation aliases through a grace period. The breaking changes from this phase are catalogued in `CHANGELOG.md` under "Breaking" / "Removed."

Ship order: **19a → 19b → 19c → 19d → 19e → 19f → 19g**. Each sub-phase is independently testable and shippable. 19a–19f shipped as evva `v0.2.4-alpha.2`; 19g (round-2 friday follow-up) shipped as `v0.2.4-alpha.3` after a fresh friday rebuild surfaced day-2 ergonomics gaps.

#### Phase 19a — Event surface polish ✅️

Addresses friday findings #1, #2, #10.

- `pkg/event/event.go`: `ErrorPayload` keeps the typed `Err error` field for callers who need the wrapped error; add a sibling `Message string` populated at emit time so consumers that just want the rendered string don't have to nil-check + call `.Error()`.
- Rename `IterLimitPayload.Reached` → `IterLimitPayload.Iters` (matching `RunEndPayload.Iters`). Old name removed; one-line caller migration documented in `CHANGELOG.md`.
- New `func (e Event) Payload() any` method that returns the payload pointer matching `e.Kind` — consumers can switch on `e.Payload().(type)` instead of grepping which of the 20 pointer fields goes with which Kind.
- Add a single-line doc comment to every `Kind*` const and every `*Payload` struct field so editor hover surfaces the contract.

#### Phase 19b — Config layer polish ✅️

Addresses friday findings #3, #5, #9.

- `pkg/config/config.go`: new `(c *Config) SetProviderCredentials(name, apiURL, apiKey string) error` that takes `c.mu` and assigns the `APIConfig`. Use this over direct `LLMProviderConfig[...]` map assignment.
- `pkg/config/load.go`: when `LoadFileConfig` seeds a new YAML on first run, write `default_profile: <opts.AppName>` instead of the hardcoded `evva`. Friday-flavoured config no longer leaks evva's name. `LoadFileConfig` signature is now `(path, appName string)` — see CHANGELOG.
- `pkg/config/load.go`: new `LoadOptions.EnvAliases map[string]string` (e.g. `{"LOGDIR": "LOG_DIR"}`) applied *before* godotenv runs so the user's preferred names map cleanly to evva's canonicals. New `LoadOptions.EnvOverrides []func(*Config) error` runs after Load so vars without a YAML hook (`MAX_ITERS`, etc.) can be folded in without a post-Load shim.
- Document each new option in `docs/extending.md`.

#### Phase 19c — Agent option ergonomics ✅️

Addresses friday findings #4, #7, #12.

- New exported `agent.PermissionMode` typed string in `pkg/agent`, with constants `agent.PermissionDefault`, `agent.PermissionAcceptEdits`, `agent.PermissionPlan`, `agent.PermissionBypass`. `WithPermissionMode` now takes the typed value — string callers convert at the boundary with `agent.PermissionMode("...")`.
- New `agent.WithHeadlessBypass()` convenience that bundles `WithPermissionMode(PermissionBypass)` + a strong docstring spelling out *"no approval UI means tool calls auto-succeed; only use in trusted environments."* This is the discoverability fix for the friday footgun.
- `agent.NewProfile` model argument is now `constant.Model` (typed). String callers wrap with `constant.Model("...")`.
- Doc comments on every `SessionInfo` field (closes finding #11).
- Broker promotion (`WithPermissionBroker` / `WithQuestionBroker` to pkg/agent) deferred — needs a `PermissionPrompter` callback design so consumers don't have to import internal broker interfaces. Tracked separately.

#### Phase 19d — Tool kit composition helpers ✅️

Addresses friday finding #6. Every downstream consumer that wants a "general coding agent" today re-types the same `append(fs.Names(), shell.Names()...)` chain. Replace with named kit functions.

- New `pkg/tools/kits/kits.go` (sibling package to avoid a parent-child import cycle with the tool family packages):
  - `GeneralPurposeKit() (active, deferred []ToolName)` — the canonical kit friday composes by hand today: fs + shell + todo + util + tool_search active, web deferred.
  - `ReadOnlyKit() []ToolName` — read, grep, glob, tree, web (search + fetch), json_query. Useful for an audit / explore-only agent.
  - `CodingKit() (active, deferred []ToolName)` — GeneralPurpose + notebook + monitor.
  - `ResearchKit() []ToolName` — read + grep + glob + tree + web + json_query + calc + todo.
- Each kit function carries an inline godoc comment listing its members so the consumer can pick the right kit at a glance.

#### Phase 19e — Godoc-visible examples + extending docs ✅️

Addresses friday findings #8, #11 (docs gaps) and elevates the SDK to "looks professional on pkg.go.dev."

Go test files named `example_*_test.go` automatically render on pkg.go.dev as runnable examples. This is the single biggest discoverability win available. New files:

- `pkg/agent/example_test.go` — ExampleNewProfile, ExampleNewWithProfile, ExampleWithHeadlessBypass.
- `pkg/event/example_test.go` — ExampleSinkFunc (function-shaped sink), ExampleEvent_Payload (type-switch on Payload()), ExampleMulti (fan-out).
- `pkg/config/example_test.go` — ExampleLoad (custom AppHome), ExampleConfig_SetProviderCredentials.
- `pkg/tools/kits/example_test.go` — ExampleGeneralPurposeKit, ExampleReadOnlyKit.
- `pkg/llm/example_test.go` — ExampleRegistry_Register (custom provider).

Documentation pass:

- `docs/extending.md`: section on **Charmbracelet pinning contract** (downstream apps must match evva's bubbletea/bubbles/lipgloss versions or `tea.Program` types diverge — the table of currently-tested versions lives here). Section on **headless permission requirement** with the `WithHeadlessBypass` recommendation. Section on **env-alias usage** for `LoadOptions.EnvAliases`.
- `pkg/agent/types.go`: doc-comment every `SessionInfo` field. Closes finding #11.
- Doc-comment every exported symbol in `pkg/event/`, `pkg/config/`, `pkg/agent/` that's currently bare.

#### Phase 19f — Stability tier + release-engineering ✅️ (in code; tag-cut pending)

The release-engineering pass. Consumers need to know which symbols are stable before they can commit to evva.

- New `docs/sdk-stability.md` declaring each `pkg/` package's tier:
  - **Stable** (post-1.0: breaking changes require a major bump): `pkg/agent`, `pkg/config`, `pkg/event`, `pkg/tools`, `pkg/llm`, `pkg/constant`, `pkg/version`.
  - **Experimental** (may break in minor versions; documented): `pkg/ui`, `pkg/toolset`, `pkg/observable`, `pkg/tools/kits`.
  - **Internal helper** (re-exported but not part of the stability contract): `pkg/common`, `pkg/banner`, `pkg/llm/builtins`.
- New `pkg/version/version.go` with `const Version` + a `BuildStamp` var populated by `-ldflags` at release time. Consumers can log it or assert against it.
- Repo-root `CHANGELOG.md`. Reverse-chronological. The next-release entry catalogues every additive change from 19a–19e plus the dev-mode cleanup (collapsed `NewProfileTyped` → `NewProfile`, removed `IterLimitPayload.Reached`, etc.).
- Tag cut deferred — the SDK surface is ready, but the user decides when to flip from pre-1.0 to a v1.0.0 stability promise.

#### Phase 19g — Round 2 friday follow-up ✅️

Shipped as evva `v0.2.4-alpha.3`. Friday's rebuild on alpha.2 surfaced 6 fresh day-2 findings (`friday/docs/sdk-feedback.md` Round 2). Five landed here; one (R2-6, broker promotion) is carried forward to a future phase along with the same deferral noted in 19c.

Theme: **`LoadOptions` becomes the single declarative surface** for host-driven runtime tuning. Instead of pre/post-Load shim functions sprinkled around `Load()` calls, every "what does this app want from its environment" detail lives inside one `LoadOptions{...}` literal.

- `LoadOptions.EnvOverrides` type changed from `[]func(*Config) error` to `[]EnvOverride{Name, Fn}` (R2-1). Wrapped error names the failing override — `config: EnvOverrides[deepseek_creds]: <err>` — so a host with several overrides can identify the culprit without grepping. Empty Name rejected at Load time.
- New `LoadOptions.ProviderCredentials map[string]ProviderCredsFromEnv` (R2-2). Reads env vars (after EnvAliases promotion) and calls `cfg.SetProviderCredentials` for each entry. Replaces the alias-env + EnvOverride-that-reads-it + setter three-step dance every downstream app used to write by hand.
- New `LoadOptions.SeedEnvTemplate string` (R2-3). Written to `<AppHome>/.env` on first launch when the file is missing; never overwrites. Closes the chicken-and-egg gap where evva auto-created the YAML but left the user to discover `.env` themselves.
- New `kits.GeneralPurposeActive() []ToolName` sibling (R2-4). Returns the active half of the general-purpose kit WITHOUT `tool_search`, for callers who drop the deferred companion (`tool_search` active with nothing deferred is pure overhead). `GeneralPurposeKit()` remains the canonical default.
- New `version.Bare() string` (R2-5). Returns unprefixed semver (`0.2.4-alpha.3`) for hosts that compose their own tag formats (`evva 0.2.4-alpha.3` rather than `evva v0.2.4-alpha.3`). Respects SemVer 2.0 `+<stamp>` build-metadata.
- `docs/extending.md` gains a "LoadOptions — the declarative host surface" section with a per-field table.

Friday's bootstrap shrunk from 135 → 125 LOC adopting 19g (21% smaller since round 1 alpha.1). The `applyDeepSeekCreds` helper is gone entirely — credentials wire inline through `ProviderCredentials`.

R2-6 (broker promotion / `PermissionPrompter` callback shape) stays deferred — same blocker as 19c (the callback type needs design before exposure). Tracked.

#### Out of scope for Phase 19

Saving these for future phases so the scope stays shippable:

- **Skill SDK** (pkg/skill so downstream can ship custom skills). The skill loader has internal coupling that needs its own decoupling pass.
- **Custom AppConfig** Support consumer config key-value into AppConfig (let user keep their own secret or some config in it).
- **Custom Kind events** (consumer-declared event kinds). The Phase 13 invariant — "downstream apps cannot add new Kinds" — stays.
- **Pluggable agent loops**. The loop logic stays in internal/agent for v1.0.
- **gRPC / network surface**. SDK stays in-process for v1.0.

---

## Out of scope (v3+)

These deliberately don't appear in the 0–11 roadmap. Listed so contributors don't propose them as Phase additions.

- **Teams / SendMessage** — Claude Code's multi-agent runtime depends on a bridge layer (UDS sockets, remote control, JWT, cross-machine session forwarding). Premature for evva v1; revisit when there's an actual second agent process to talk to.
- **Process tools (`Monitor`, `task_output`, `task_stop`)** — return as a dedicated phase tied to `Bash run_in_background`. Today no one is asking for it.
- **MCP integrations** (Atlassian, Figma, IDE diagnostics) — out of v1 entirely. The MCP framework support (Phase 11) is enough to unblock community plugins; bundled vendor integrations follow once there's demand.

---

## Project conventions

- All source under `internal/` is private. Public extension points live in `pkg/`.
- One package per tool family (`fs`, `shell`, `meta`, etc.). A new tool either goes in an existing family or starts a new family package. Phase 13c moves the broadly-reusable families (`fs`, `shell`, `web`, `util`, `notebook`, `monitor`, `cron`, `todo`) under `pkg/tools/`; evva-runtime-specific families (`meta`, `mode`, `skill`, `ux`, `dev`) stay under `internal/tools/`.
- One package per LLM provider. After Phase 13b they live at `pkg/llm/{claude,deepseek,ollama}/` and register into `pkg/llm.DefaultRegistry()`. The `llm.Client` interface remains the only public seam.
- Tests live next to the code they cover (`*_test.go`). No parallel `tests/` tree.
- No comments that restate the code. Only comment WHY when the WHY is non-obvious.
- Port tool descriptions from `ref/src/tools/*/prompt.ts` verbatim when reasonable. Diverge only with a clear reason.

---

## Project structure

```
evva/
├── cmd/evva/                  # CLI entry point — wires agent + UI
├── configs/                   # config loading (.env + YAML)
├── docs/                      # design notes, tool docs, system prompts
├── internal/
│   ├── agent/                 # agent loop, profiles, spawn
│   │   ├── event/             # event types + sink contract
│   │   └── sysprompt/         # system prompt builder
│   ├── constant/              # provider / model / status enums
│   ├── llm/                   # llm.Client interface + shared params
│   │   ├── claude/  deepseek/  ollama/  ...
│   ├── llmfactory/            # provider factory keyed by constant
│   ├── logger/                # structured slog wrapper + pretty fmt
│   ├── observable/            # pub/sub framework for stores
│   ├── session/               # conversation history + cumulative usage
│   ├── tools/                 # tool interface (Name/Schema/Execute)
│   │   ├── cron/  dev/  fs/  meta/  mode/  monitor/  notebook/
│   │   ├── shell/  skill/  task/  util/  ux/  web/
│   ├── toolset/               # tool catalog + ToolState registry
│   └── ui/                    # UI plugin contract
│       ├── bubbletea/         # reference TUI implementation — prototype
│       ├── bubbletea_v2/      # reference TUI implementation v2 — refactor v1
│       └── ...                # downstream-customized layouts
├── ref/src/                   # Claude Code reference source (read-only)
├── log/                       # per-agent runtime logs (gitignored)
├── pkg/common/                # small shared utilities
└── scripts/                   # demo / dev scripts
```

Key boundaries:

- `agent` knows about `event.Sink`, never about a concrete UI.
- `tools/*` packages produce `tools.Result` (text + opaque `Metadata`); the UI type-asserts on `Metadata` to render structured payloads.
- `observable` has no dependencies on agent or UI.
- `ui` defines narrow interfaces; implementations live under it.

User-facing documentation (install, TUI keybindings, config file shape, log paths) lives in `README.md`. This file is for project vision and the development roadmap.