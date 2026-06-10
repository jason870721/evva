# evva — Project Vision and Roadmap

## Vision

`evva` is a ReAct coding agent for the terminal, written in Go. The architecture follows Claude Code in spirit but keeps the moving parts small on purpose: one narrow `llm.Client` interface bridging multiple providers (Anthropic, DeepSeek, OpenAI, Ollama), one `tools.Tool` interface, one observable store fanning state to any UI implementation, one agent loop.

The unifying idea is **one runtime, many personas, swappable UI**:

- A **persona** is a main-tier agent definition — its own tools, system prompt, model preference, and personality. `evva` (a professional software engineer) is one persona. `nono` (a financial manager), `noen` (a math teacher), and any others a user creates are siblings, not subclasses.
- The same runtime drives every persona. Switching personas is `/profile <name>`, not a new binary.
- A persona can spawn another persona as a subagent for cross-domain work — `evva` can delegate a costing question to `nono` without leaving the session.
- Adding a new LLM provider, tool family, persona, or UI implementation is a one-package change.

`evva` is **not** trying to be a drop-in Claude Code. It borrows the harness shape because that shape is what current frontier models behave best under, and it ports tool descriptions verbatim where reasonable so the model sees prompts close to what it was trained on. Where Go semantics, terminal constraints, or evva's narrower scope justify divergence, it diverges intentionally.

The reference TypeScript source lives at `ref/src/`. Treat it as the source of truth for tool descriptions, harness structure, and agent definitions — port from it, don't reinvent.

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

One schema, one loader, two visibility surfaces. This is also the seam that profile switching uses to enumerate personas.

---

## Public SDK (`pkg/`)

`pkg/` is the stable public API surface. Downstream projects can embed evva as a library by importing `pkg/agent`, implementing `pkg/ui.UI`, and wiring in their own event sink. The SDK is intentionally narrow — each package exposes a minimal interface and a constructor.

| Package | Role | Key exports |
|---|---|---|
| `pkg/agent` | Agent constructor + controller interface | `New(Config) (Agent, error)`, `Agent` interface (~20 methods matching `ui.Controller`) |
| `pkg/llm` | LLM provider abstraction | `Client` interface, `Registry`, `Message`, `Response`, `Chunk`, `ChunkSink` |
| `pkg/llm/builtins` | Provider registration | `init()` registers Anthropic, DeepSeek, OpenAI, Ollama factories |
| `pkg/llm/claude` | Anthropic Messages API | Implements `Client` |
| `pkg/llm/deepseek` | DeepSeek API (OpenAI-compatible) | Implements `Client` |
| `pkg/llm/openai` | OpenAI Chat Completions API | Implements `Client` |
| `pkg/llm/ollama` | Ollama local API | Implements `Client` |
| `pkg/tools` | Tool interface + shared types | `Tool` interface, `Result`, `Call`, `Descriptor`, `State`, `ToolName` constants |
| `pkg/tools/fs` | Filesystem tools | `read`, `write`, `edit`, `glob` (+ `diff`, `notebook_edit`, PDF reading) |
| `pkg/tools/shell` | Shell tools | `bash` (sync + background), `grep`, `tree` |
| `pkg/tools/web` | Web tools | `web_search` (Tavily), `web_fetch` |
| `pkg/tools/lsp` | Language Server Protocol | `lsp_request` (go-to-definition, references, hover, symbols) |
| `pkg/tools/repl` | Python/JS scratch REPL | `repl` tool |
| `pkg/tools/daemon` | Long-running unit abstraction | `Daemon` interface, `DaemonState`, `DaemonKind` constants |
| `pkg/tools/monitor` | Per-line stream watcher | `monitor` tool |
| `pkg/tools/cron` | Scheduled prompts | `cron_create`, `cron_list`, `cron_delete` |
| `pkg/tools/todo` | Task list | `todo_write` |
| `pkg/tools/notebook` | Jupyter notebook editing | `notebook_edit` |
| `pkg/tools/util` | Utility tools | `json_query`, `calc` |
| `pkg/tools/kits` | Tool kit bundling | Groups related tools for profile construction |
| `pkg/toolset` | Tool registry + catalog | `Registry` (name→factory), `Tags`, `Hints` |
| `pkg/event` | Event envelope + sink contract | `Event`, `Kind`, `Sink` interface, `Multi`, `Discard`, `BubbleUp` |
| `pkg/observable` | Pub/sub mixin for backing stores | `Observable` embedded in stores; auto-fans changes to UI |
| `pkg/config` | Configuration loading | `Load(LoadOptions) (*Config, error)`, YAML + `.env` |
| `pkg/constant` | Enums and sentinels | `AgentStatus`, `LLMProvider`, `Model` |
| `pkg/ui` | UI plugin contract | `UI` interface, `Controller` interface |
| `pkg/ui/bubbletea` | Reference Bubble Tea TUI | `New(evvaHome)` terminal UI implementation |
| `pkg/ui/lp` | Low-profile terminal UI | Compact line-based UI |
| `pkg/permission` | Permission gate | Rule store, broker, matcher |
| `pkg/skill` | User-installed skills | Markdown-based skill loader |
| `pkg/hooks` | Lifecycle hooks | Shell + HTTP backends for 6 event types |
| `pkg/mcp` | MCP client | Server config, stdio + Streamable-HTTP transports, OAuth |
| `pkg/banner` | Startup branding | ASCII art banner rendering |
| `pkg/version` | Build version | `Version` constant — the full current tag name incl. leading `v` (e.g. `"v1.4.5-beta.2"`) |
| `pkg/update` | Self-update mechanism | `Check()` / `Apply()` |
| `pkg/common` | Shared utilities | Small helpers used across packages |

---

## Internal packages (`internal/`)

`internal/` contains implementations that are specific to evva's runtime and are not part of the stable public API. Downstream embedders should not import these directly.

| Package | Role |
|---|---|
| `internal/agent` | Agent struct, main loop, subagent spawn, profile definitions, session persistence |
| `internal/agent/sysprompt` | System prompt builders per agent type |
| `internal/agent/loader` | Disk agent definition loader (merges Go + YAML) |
| `internal/agent/attachments` | Plan-mode per-turn reminders |
| `internal/tools` | evva-runtime-specific tool families |
| `internal/tools/meta` | `agent` (subagent spawn), `tool_search`, `schedule_wakeup` |
| `internal/tools/mode` | `enter_plan_mode`, `exit_plan_mode`, `enter_worktree`, `exit_worktree` |
| `internal/tools/ux` | `ask_user_question`, `push_notification` |
| `internal/tools/config` | `config` tool (read/write evva settings) |
| `internal/tools/dev` | `feedback` (dev-mode only) |
| `internal/toolset` | `ToolState` implementation, `Build()`, `Describe()`, builtin registration |
| `internal/ui` | Bubble Tea v2 TUI implementation (terminal UI) |
| `internal/session` | LLM message history + cumulative usage tracking + compaction |
| `internal/hooks` | User-authored lifecycle hooks (shell commands / HTTP webhooks) |
| `internal/permission` | Permission gate implementation (store, broker, matcher) |
| `internal/question` | Question broker for `ask_user_question` |
| `internal/memdir` | Typed memory directory loader (user/feedback/project/reference) |
| `internal/swarm` | Veronica multi-agent swarm subsystem (service, webapi, agentdef, space) |
| `internal/skills` | Bundled skill content (embedded via go:embed) |
| `internal/logger` | Structured `slog` wrapper + pretty console formatter |
| `internal/update` | Self-update mechanism |

---

## Project conventions

- **Public vs. private:** Reusable abstractions live in `pkg/`. evva-runtime-specific implementations live in `internal/`. If a package is useful to downstream embedders, it belongs in `pkg/`.
- **One package per tool family.** Examples: `pkg/tools/fs/`, `pkg/tools/shell/`, `internal/tools/meta/`. A new tool either goes in an existing family or starts a new family package.
- **One package per LLM provider.** `pkg/llm/claude/`, `pkg/llm/deepseek/`, `pkg/llm/ollama/`. Each implements the `llm.Client` interface from `pkg/llm/`. New providers register via `init()` in `pkg/llm/builtins/`.
- **Tests live next to the code they cover** (`*_test.go`). No parallel `tests/` tree.
- **No comments that restate the code.** Only comment WHY when the WHY is non-obvious.
- **Port tool descriptions from `ref/src/tools/*/prompt.ts` verbatim** when reasonable. Diverge only with a clear reason.
- **Minimize external dependencies.** Non-stdlib dependencies: `golang.org/x/sync` (singleflight), the Bubble Tea TUI stack, and `github.com/modelcontextprotocol/go-sdk` (MCP client, added in v1.3.0). Protocol implementations (JSON-RPC, LSP types) are hand-written to avoid dependency chains.

---

Key boundaries:

- `agent` knows about `event.Sink`, never about a concrete UI.
- `pkg/tools/*` and `internal/tools/*` packages produce `tools.Result` (text + opaque `Metadata`); the UI type-asserts on `Metadata` to render structured payloads.
- `pkg/observable` has no dependencies on agent or UI — it's a pure pub/sub mixin.
- `pkg/ui` defines narrow interfaces (`UI`, `Controller`); implementations live under `internal/ui/`.
- `pkg/llm` defines the `Client` interface; each provider is a separate package implementing it.
- Downstream embedders import `pkg/agent`, implement `pkg/ui.UI`, and never touch `internal/`.

---

## Release workflow

### Branch strategy

Each release branch owns exactly one tag tier: **`main` ships stable tags, `pre-release` ships beta tags.** There is no alpha tier.

```
main  ← production (stable tags only: vX.Y.Z; GitHub "Latest")
  ↑ promote (command: release)        ← hotfix/* cut from main (command: hotfix release)
pre-release  ← staging (beta tags only: vX.Y.Z-beta.N; GitHub pre-release)
  ↑ ship (commands: pre-release feature / hotfix pre-release)
dev  ← integration
  ↑ feature PR, squash/merge after review
feature/*  ← topic branches (cut from dev)
```

This is the seam the `evva update` command rides on: `evva update` resolves GitHub's **Latest** release — the newest stable on `main` — while `evva update v<X>.<Y>.<Z>-beta.<N>` pins to a beta published from `pre-release` (see `pkg/update`). `go install ...@latest` ignores `-beta.N` tags entirely — only stable tags move `@latest`.

**Backflow rule:** after EVERY tag (beta or stable), merge the tagged branch back into `dev` and push. This keeps `pkg/version/version.go` and `CHANGELOG.md` converged across all three branches. Skipping it is exactly why dev's version constant once drifted four releases behind and why every dev → pre-release merge used to hit a CHANGELOG conflict.

### Daily development

1. Branch off `dev`: `git checkout -b feature/<ticket-or-name>` (e.g. `feature/RP-15`, `feature/bundle-skill`).
2. Commit with conventional prefixes: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`.
3. Push to GitHub, open a PR targeting `dev`, merge after review.

### Version numbering

`vX.Y.Z[-beta.N]`:

| Component | Rule |
|---|---|
| **X** (major) | Breaking change to the `pkg/` SDK surface or CLI/config behavior, or a direction-level milestone (v0 → v1). Deliberate and rare. |
| **Y** (minor) | **One roadmap wave = one minor.** A wave claims its minor in its planning doc at planning time; the first release containing that wave's work bumps Y (Z=0). |
| **Z** (patch) | Within-wave increments after the wave's debut: fixes, docs, small follow-ups. |
| **-beta.N** | Only on `pre-release`. N counts cuts of the SAME target base version, starting at 1; it resets when the base changes. A stable tag is ALWAYS a verbatim promotion of the last beta of that base. |

One-line litmus: **does the release contain work from a new roadmap wave → bump Y; otherwise → bump Z.**

**Base-version decision** (run top-down when cutting a new beta):

1. Contains work from a roadmap wave that has never shipped → that wave's claimed minor, `Z=0`. (Any unpromoted older beta is superseded; its content rides along, since dev is cumulative.)
2. Else, the current beta's base is still unpromoted → keep that base; this cut is its `-beta.(N+1)`.
3. Else → newest stable's `Z+1`, `-beta.1`.

Wave → minor map (append a row whenever a new wave is planned):

| Minor | Wave |
|---|---|
| v1.3 | MCP client |
| v1.4 | Typed memory + Veronica Phase 1 (refine waves 1–3, timezone discipline) |
| v1.5 | Veronica wave 4 — operational hardening (RP-13..RP-18) |
| v1.6 | Explore track (EX-1..EX-6) — confirm scope when the first EX ships |

### The four release commands

The operator triggers every release with one of four phrases — match on intent, however the sentence is phrased:

| Command | Meaning | Version |
|---|---|---|
| **`pre-release feature`** | ship dev's accumulated work as a new beta | base-version decision above; first cut of a base → `-beta.1` |
| **`hotfix pre-release`** | the current beta broke; re-cut it with fixes | same base, `-beta.(N+1)` |
| **`release`** | promote the newest beta to stable | strip `-beta.N` |
| **`hotfix release`** | critical fix straight onto stable | newest stable `Z+1`, no beta |

**Each phrase IS the full authorization** to execute its playbook end-to-end, including pushing branches and tags — do not ask again. Stop and report instead of pushing only when a precondition fails: dirty tree, failing tests, or the actual dev delta contradicting the command's intent (e.g. `hotfix pre-release` requested but features are present).

#### Playbook: `pre-release feature`

1. Preflight: clean tree; `git fetch origin`; `go test ./...` green on dev.
2. Pick the target version with the base-version decision (check `git tag --sort=-creatordate | head` for current state).
3. `git checkout pre-release && git pull && git merge dev` (`--no-ff` is fine when diverged).
4. `pkg/version/version.go`: set `Version` to the full tag name (e.g. `"v1.5.0-beta.1"`).
5. `CHANGELOG.md`: rename `[Unreleased]` → `[vX.Y.Z-beta.1] — <date>`; insert a fresh `[Unreleased]` on top; update the comparison URLs at the bottom.
6. `git add pkg/version/version.go CHANGELOG.md && git commit -m "chore: changelog and version bump for vX.Y.Z-beta.1"`.
7. `git tag -a vX.Y.Z-beta.1 -m "vX.Y.Z-beta.1 — <one-line summary>"`.
8. `git push origin pre-release vX.Y.Z-beta.1` — the tag push triggers `.github/workflows/release.yml` (tag contains `-` → published as a GitHub pre-release).
9. Backflow: `git checkout dev && git merge pre-release && git push origin dev`.
10. Report: tag, what shipped, release URL.

#### Playbook: `hotfix pre-release`

Premise: the fix is already merged to dev via the normal `feature/*` flow (if not, do that first). Verify the dev → pre-release delta is fixes-only; if features snuck in, report and suggest `pre-release feature` instead.

1. Version = same base as the current beta, `-beta.(N+1)`.
2. Steps 3–10 as in `pre-release feature`, with one CHANGELOG difference: do NOT open a new entry — fold the fix lines into the existing `[vX.Y.Z-beta.N]` entry and rename its heading to `-beta.(N+1)` (one entry per base version; the eventual stable entry is cumulative).

#### Playbook: `release`

1. Identify the newest beta on `pre-release`; report its soak time (days since the tag) for the record.
2. `git checkout main && git pull && git merge pre-release` (`--ff-only` when possible, else `--no-ff`).
3. `pkg/version/version.go`: drop `-beta.N` (e.g. `"v1.5.0"`).
4. `CHANGELOG.md`: rename `[vX.Y.Z-beta.N]` → `[vX.Y.Z] — <date>`; update the comparison URLs.
5. `git add pkg/version/version.go CHANGELOG.md && git commit -m "chore: promote vX.Y.Z-beta.N to stable vX.Y.Z"`.
6. `git tag -a vX.Y.Z -m "vX.Y.Z — <one-line summary>"` then `git push origin main vX.Y.Z` — a bare tag publishes as **Latest** (`evva update` and `@latest` move to it).
7. Backflow: `git checkout dev && git merge main && git push origin dev` (pre-release converges at its next cut from dev).
8. Report: tag, soak time, release URL.

#### Playbook: `hotfix release`

For a critical bug in the current stable while `pre-release` may already carry the next wave.

1. `git checkout main && git pull && git checkout -b hotfix/<name>`; apply the fix (or cherry-pick it from dev); `go test ./...`.
2. Merge `hotfix/<name>` into `main`.
3. Version = newest stable `Z+1`, tagged stable DIRECTLY — the only path that skips a beta. If that number is already claimed by an unpromoted beta, the hotfix still takes it; the superseded beta's content re-ships later under the next free number (never delete or re-point existing tags).
4. `pkg/version/version.go` + a new `[vX.Y.Z] — <date>` CHANGELOG entry (typically just `### Fixed`), committed together.
5. `git tag -a vX.Y.Z -m "vX.Y.Z — <summary>"` then `git push origin main vX.Y.Z`.
6. Backflow: `git checkout dev && git merge main && git push origin dev`. The fix reaches `pre-release` at its next cut from dev.
7. Report.

### CHANGELOG rules

- **One entry per base version.** It is born as `[vX.Y.Z-beta.1]` at the first beta; each later beta of the same base folds its lines in and renames the heading; promotion renames it to `[vX.Y.Z]`. A hotfix-release entry is born stable directly.
- `[Unreleased]` always sits on top between releases; sections are `### Added` / `### Fixed` / `### Changed` / `### Breaking`.
- Update the comparison URLs at the bottom on every rename.
- Merge-conflict rule (legacy drift only): keep `[Unreleased]` from dev on top, released entries below, dedupe lines.

### Key rules

- `pkg/version/version.go`'s `Version` constant carries the FULL tag name including the leading `v` (e.g. `"v1.4.5-beta.2"`). It is the dev-build fallback for `evva update`'s current-version check; release binaries get the real tag injected via ldflags (`pkg/config.Version`). Invariant: tag name == `Version` constant == CHANGELOG heading.
- The four release commands carry push authorization. Any tag or release push OUTSIDE these four playbooks still requires asking first — pushing is a shared-state operation.
- Never skip the backflow merge into `dev` after a tag.
- Releases are published by `.github/workflows/release.yml` on tag push (cross-compiles binaries, attaches them, generates notes): tag containing `-` → `--prerelease`; bare `vX.Y.Z` → Latest. No manual `gh release create`.

---

User-facing documentation (install, TUI keybindings, config file shape, log paths) lives in `README.md` and `docs/user-guide/`. This file is for project vision, architecture, and the development roadmap.
