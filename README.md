# EVVAgent (evva)

A ReAct coding agent in Go. Multi-provider LLM, multi-tool dispatch, async
sub-agents, and a swappable UI layer.

---

## What is EVVAgent?

`evva` runs a tool-using LLM agent in your terminal. It speaks Anthropic
Claude, DeepSeek, and Ollama through a single `llm.Client` interface;
dispatches multiple tool calls per turn in parallel; tracks tasks and
sub-agents through an observable store framework; and exposes the agent
to either a bubbletea TUI or a plain CLI sink — both built on the same
swappable `ui.UI` contract.

The architecture is small on purpose. Adding a new LLM provider, a new
panel, or a new UI implementation should each cost roughly one package.

## Features

**Agent loop**
- ReAct-style: LLM call → parallel tool dispatch → tool results → repeat.
- Multiple `tool_use` blocks per assistant turn, executed concurrently.
- Iteration cap surfaces as a pausable state (UI prompts the user to continue).
- Cancellable via `ctx`; ESC / Ctrl+C honored end-to-end.

**LLM providers**
- Anthropic Claude (with extended thinking + cryptographic signature round-trip).
- DeepSeek (OpenAI-compatible chat, reasoning_content echoed back).
- Ollama (local).
- Per-provider option pattern (`WithTemperature`, `WithEffort`, ...).
- Token usage (`InputTokens` / `OutputTokens` / cache / reasoning) decoded
  on every response and accumulated on the session.

**Tools**
- File system: `read_file` (cat -n format, 2000-line default), `write_file`,
  `edit_file` — all strict-absolute path, all returning structured `*FileDiff`
  metadata for UI rendering.
- Shell: `bash`, `grep`, `tree`.
- Tasks (six tools sharing one observable `*task.Store`).
- Meta: `agent` (spawn sub-agents), `tool_search` (lazy-load deferred tool
  schemas), `skill`, `schedule_wakeup`.
- Plus stubs for `web_*`, `cron_*`, `notebook_edit`, `monitor`, `mode`, `ux`.

**Sub-agents**
- `explore` (read-only) and `general-purpose` presets.
- Sync mode: parent loop blocks; result returned through the tool channel.
- Async mode: child runs in a goroutine; parent loop drains the
  `SpawnGroup` panel at iteration top and injects results as a synthetic
  user message. Results that arrive after the loop exits sit in the panel
  until the user types again.
- Subagents cannot spawn subagents (hierarchy is two layers).
- Bubble-up event routing tags subagent events with `ParentID` so the
  parent's sink renders them in a nested panel.

**Observable store framework** (`internal/observable`)
- One pub/sub primitive any backing store can embed.
- `ToolState` auto-registers stores on first use and fans changes into a
  single `KindStoreUpdate` event stream.
- Adding a new panel (notes, todos, code-review, ...) requires zero edits
  to the agent or event packages — implement `Domain()`, embed
  `Observable`, call `Notify` on mutation.

**Swappable UI** (`internal/ui`)
- `UI` interface: `event.Sink` + `Attach(Controller)` + `Run(ctx)`.
- `Controller` interface: narrow API for `Run` / `Continue` / `Session` /
  `ToolState` / `Logger`.
- Reference TUI in `internal/ui/bubbletea/` — transcript, conditional
  panels (only render when non-empty), input at bottom, status bar with
  cumulative tokens, smart Ctrl+C (cancel run vs quit).
- `-no-tui` flag falls back to a plain-text CLI sink for pipes and CI.

## Project structure

```
evva/
├── cmd/evva/                  # CLI entry point — wires agent + UI
├── configs/                   # config loading (env, workdir, home)
├── docs/                      # design notes, tool docs, system prompts
├── internal/
│   ├── agent/                 # agent loop, profiles, spawn
│   │   └── event/             # event types + sink contract
│   ├── constant/              # provider / model / status enums
│   ├── llm/                   # llm.Client interface + shared params
│   │   ├── claude/            # Anthropic Messages API
│   │   ├── deepseek/          # DeepSeek chat completions
│   │   └── ollama/            # local Ollama
│   ├── llmfactory/            # provider factory keyed by constant
│   ├── logger/                # structured slog wrapper + pretty fmt
│   ├── observable/            # pub/sub framework for stores
│   ├── session/               # conversation history + cumulative usage
│   ├── tools/                 # tool interface (Name/Schema/Execute)
│   │   ├── cron/  fs/  meta/  mode/  monitor/  notebook/
│   │   ├── shell/ task/ ux/   web/
│   ├── toolset/               # tool catalog + ToolState registry
│   └── ui/                    # UI plugin contract
│       └── bubbletea/         # reference TUI implementation
├── log/                       # per-agent runtime logs (gitignored)
├── pkg/common/                # small shared utilities (UUID, ...)
└── scripts/                   # demo / dev scripts
```

Key boundaries:
- `agent` knows about `event.Sink`, never about a concrete UI.
- `tools/*` packages produce `tools.Result` (text + opaque `Metadata`);
  the UI type-asserts on `Metadata` to render structured payloads
  (e.g. `*fs.FileDiff`).
- `observable` has no dependencies on agent or UI — any store can use it.
- `ui` defines two narrow interfaces; implementations live under it
  (`bubbletea/`, future `web/`, ...).

## Quickstart

```bash
# build
go build ./cmd/evva

# interactive TUI (default when stdout is a TTY)
./evva

# one-shot plain CLI (pipes, scripts, CI)
echo "list files in /tmp" | ./evva -no-tui
./evva -no-tui "explain internal/agent/loop.go"

# tuning
./evva -temp 0.7 -max-tokens 2048 -max-iters 40
```

Set provider credentials via env (loaded from `.env` if present):

```
ANTHROPIC_API_KEY=...
DEEPSEEK_API_KEY=...
OLLAMA_URL=http://localhost:11434
```

## Roadmap

### In progress / next up
- Streaming completions (chunked text + thinking).
- 2-level compaction:
  - micro: compress tool-result blocks when context budget approaches threshold.
  - full: summarize the whole session into a single assistant brief.
- TUI: surface model name + provider in the status bar (extend `Controller`).
- TUI: optional markdown rendering of assistant text (behind a config flag).

### Planned
- **Multimodal Read**: images, PDFs (with `pages` range), Jupyter notebooks.
  Requires upgrading `tools.Result` to multipart content blocks and
  threading those through every provider's `tool_result` converter.
- **Overwrite diffs**: proper Myers/Hunt-McIlroy diff for `write_file`
  overwrites (today only file-create has a per-line diff).
- **Per-agent LLM**: a subagent can use a different provider than its
  parent (e.g. parent on Claude, exploration subagent on Ollama).
- **Veronica space**: a long-running local service on `:8080` evva can
  drive through an API tool — sandboxed code execution, persistent state,
  scratch workspace.
- **Web UI**: a second `UI` implementation served over WebSocket;
  identical event stream, different render target.
- **Skill / plugin system**: user-installed slash-commands and skills
  loaded from a known directory.
- **Session persistence**: `/resume` show session list order by create time, reloads a snapshot and continues.

### Known limitations
- Sub-agent hierarchy is exactly two layers (no nested spawning).
- Token counts depend on provider reporting — Ollama only reports prompt
  / eval counts, not cache or reasoning splits.
- The TUI transcript grows unbounded in a long session; compaction is on
  the list above.

## License

See `LICENSE`.
