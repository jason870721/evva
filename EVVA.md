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
| `pkg/version` | Build version | `Version` constant (semver, no leading `v`) |
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

```
main  ← production (stable + beta tags; latest = stable)
  ↑ Sat merge (pre-release must be a descendant; if diverged, use --no-ff)
pre-release  ← staging (weekly feature accumulation, alpha tag)
  ↑ Sat merge
dev  ← integration
  ↑ feature PR, squash/merge after review
feature/*  ← topic branches (cut from dev)
```

### Daily development

1. Branch off `dev`: `git checkout -b feature/<ticket-or-name>` (e.g. `feature/PRD-11`, `feature/bundle-skill`).
2. Commit changes with conventional commit prefixes: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`.
3. Push to GitHub, open a PR targeting `dev`, wait for merge review.

### Weekly release (every Saturday morning)

The project publishes both pre-release (alpha) and release (beta + stable) tags. Beta tags are marked as `latest` on GitHub when no stable tag exists; once a stable tag is cut, `latest` points to it. Alpha tags are pre-release only.

**Step 1 — Alpha release (dev → pre-release)**

```bash
git checkout pre-release
git merge dev
```

Before tagging, verify:

1. `pkg/version/version.go` 中的 `Version` 常數已更新為正確的 alpha 版號（例如 `v1.4.3-alpha.1`）。
2. Alpha release 不另寫 CHANGELOG，但版號應與 dev 分支累積的變更範圍一致。

Then bump the version and tag:

```
git tag -a v<X>.<Y>.<Z>-alpha.<N> -m "v<X>.<Y>.<Z>-alpha.<N> — <summary>"
git push origin pre-release v<X>.<Y>.<Z>-alpha.<N>
gh release create v<X>.<Y>.<Z>-alpha.<N> --target pre-release --prerelease --title "v<X>.<Y>.<Z>-alpha.<N> — <summary>"
```

**Step 2 — Beta release (pre-release → main)**

```bash
git checkout main
git merge pre-release --ff-only   # if diverged (e.g. changelog commits on main), use --no-ff merge instead
```

Before tagging, verify:

1. `pkg/version/version.go` 中的 `Version` 常數已更新為正確的 beta 版號（例如 `v1.4.3-beta.1`）。
2. `CHANGELOG.md` 中的 `[Unreleased]` 已改名為對應的 beta 版號，版號與內容一致。

Then bump the version and tag:

```
git tag -a v<X>.<Y>.<Z>-beta.<N> -m "v<X>.<Y>.<Z>-beta.<N> — <summary>"
git push origin main v<X>.<Y>.<Z>-beta.<N>
gh release create v<X>.<Y>.<Z>-beta.<N> --target main --title "v<X>.<Y>.<Z>-beta.<N> — <summary>"
```

**Step 3 — Stable release (optional, on main)**

When the project is ready for a stable release, promote the latest beta:

```bash
git checkout main
# Update pkg/version/version.go: drop -beta.N suffix (e.g. v1.4.3-beta.1 → v1.4.3)
# Update CHANGELOG.md: rename [vX.Y.Z-beta.N] → [vX.Y.Z], update comparison URLs
git add pkg/version/version.go CHANGELOG.md
git commit -m "chore: promote v<X>.<Y>.<Z>-beta.<N> to stable v<X>.<Y>.<Z>"
git tag -a v<X>.<Y>.<Z> -m "v<X>.<Y>.<Z> — <summary>"
git push origin main v<X>.<Y>.<Z>
gh release create v<X>.<Y>.<Z> --target main --title "v<X>.<Y>.<Z> — <summary>"
```

**Important:** `go install ...@latest` ignores pre-release tags (`-alpha.N`, `-beta.N`). A stable tag is needed for `@latest` to resolve to the current version. Without one, `@latest` falls back to the last stable tag (e.g. `v0.2.0`).

### Version numbering

`vX.Y.Z` where:

| Component | Meaning |
|---|---|
| **X** (major) | Breaking changes, new direction |
| **Y** (minor) | Feature updates |
| **Z** (patch) | Bug fixes + small adjustments |

Pre-release suffix: `-beta.<N>` (on main), `-alpha.<N>` (on pre-release). N starts at 1 per base version.

### CHANGELOG

Only beta and stable releases get changelog entries (they're the user-facing releases). Each entry summarizes the features and fixes accumulated since the last release. Alpha releases do not get separate changelog entries.

When a release is published, edit `CHANGELOG.md`:

1. Rename `## [Unreleased]` → `## [v<X>.<Y>.<Z>]` (or `[v<X>.<Y>.<Z>-beta.<N>]`).
2. Insert a fresh `## [Unreleased]` section at the top.
3. Add a summary of what this release contains under `### Added`, `### Fixed`, `### Changed`, `### Breaking`.
4. Update the comparison URLs at the bottom of the file.

Then commit:

```
git add CHANGELOG.md && git commit -m "chore: changelog for v<X>.<Y>.<Z>"
```

### Key rules

- `pkg/version/version.go` stores the *current* version constant.
- Bump the version in a separate commit before tagging.
- Always ask before pushing tags or releases — pushing is a shared-state operation.
- `gh release create` targets `main` for beta/stable, `pre-release` for alpha.

---

User-facing documentation (install, TUI keybindings, config file shape, log paths) lives in `README.md` and `docs/user-guide/`. This file is for project vision, architecture, and the development roadmap.
