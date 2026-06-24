# evva — Vision & Architecture

> This document is the canonical reference for evva's **vision and architecture**.
> Coding conventions, the release workflow, and CHANGELOG rules live in the agent
> instruction files **[CLAUDE.md](../CLAUDE.md)** (for Claude Code) and its twin
> **[EVVA.md](../EVVA.md)** (for evva). Contributor onboarding (build, test, branch/PR flow)
> lives in **[CONTRIBUTING.md](../CONTRIBUTING.md)**. User-facing docs live in
> **[README.md](../README.md)** and **[docs/](README.md)**.

## Vision

`evva` is a ReAct coding agent for the terminal, written in Go. The architecture follows Claude Code in spirit but keeps the moving parts small on purpose: one narrow `llm.Client` interface bridging multiple providers (Anthropic, DeepSeek, GLM, OpenAI, Ollama), one `tools.Tool` interface, one observable store fanning state to any UI implementation, one agent loop.

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
| `pkg/llm/builtins` | Provider registration | `init()` registers Anthropic, DeepSeek, GLM, OpenAI, Ollama factories |
| `pkg/llm/claude` | Anthropic Messages API | Implements `Client` |
| `pkg/llm/glm` | GLM (Zhipu/z.ai) via Anthropic-compatible endpoint | Self-contained copy of the claude engine with Bearer auth; implements `Client` |
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
| `internal/repomap` | LSP-backed repo-map builder + `repo_map` zoom tool — a session-open prompt context surface (peer to memory/skills), gated on `enable_repo_map` |
| `internal/swarm` | Veronica multi-agent swarm subsystem (service, webapi, agentdef, space) |
| `internal/skills` | Bundled skill content (embedded via go:embed) |
| `internal/logger` | Structured `slog` wrapper + pretty console formatter |
| `internal/update` | Self-update mechanism |

---

## Key boundaries

- `agent` knows about `event.Sink`, never about a concrete UI.
- `pkg/tools/*` and `internal/tools/*` packages produce `tools.Result` (text + opaque `Metadata`); the UI type-asserts on `Metadata` to render structured payloads.
- `pkg/observable` has no dependencies on agent or UI — it's a pure pub/sub mixin.
- `pkg/ui` defines narrow interfaces (`UI`, `Controller`); implementations live under `internal/ui/`.
- `pkg/llm` defines the `Client` interface; each provider is a separate package implementing it.
- Downstream embedders import `pkg/agent`, implement `pkg/ui.UI`, and never touch `internal/`.

The public/private split, the SDK stability tiers, and how to embed evva are documented in
[docs/contributing/](contributing/README.md).
