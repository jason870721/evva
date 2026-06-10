# EVVAgent (evva)

A ReAct coding agent in your terminal. Multi-provider LLM, parallel tool dispatch, async sub-agents, swappable UI.

---

## What is EVVAgent?

![evva_logo.png](docs/assets/logo-3.jpg)

`evva` runs a tool-using LLM agent in your terminal. It speaks Anthropic Claude, DeepSeek, OpenAI, and Ollama through one `llm.Client` interface; dispatches multiple tool calls per turn in parallel; tracks tasks and sub-agents through an observable store; and renders into a bubbletea TUI or a plain-text CLI sink.

The architecture is small on purpose — adding a new LLM provider, panel, or UI implementation is roughly one package each.


---

## Install

Requires Go 1.25+. Currently **macOS and Linux only** — Windows support is not yet available.

### Quick install (recommended)

```bash
go install github.com/johnny1110/evva/cmd/evva@latest
```

The binary lands in `$GOBIN` (or `$GOPATH/bin`). Make sure it's on your `PATH`.

### Build from source

```bash
git clone https://github.com/johnny1110/evva
cd evva
make install
```

Override the location if you want it elsewhere:

```bash
sudo make install PREFIX=/usr/local/bin     # system-wide
make install PREFIX=$HOME/.local/bin        # user-local
```

### Verify

```bash
evva -version
```

Uninstall removes only the binary; your `~/.evva/` config is preserved:

```bash
make uninstall
```

---

## Update

To update evva to the latest version without Go:

```bash
evva update                  # newest stable release (GitHub "Latest")
evva update latest           # same as above
evva update v1.4.3           # pin to an exact stable version
evva update v1.4.3-beta.1    # opt into a specific beta (pre-release)
```

With no argument (or `latest`) this resolves GitHub's Latest release — the newest **stable** build on `main`. Passing a tag pins to that exact build, including a `-beta.N` pre-release or an older version to downgrade. In every case it downloads the pre-built binary for your OS/arch and replaces the current one atomically. No Go toolchain required.

You can also check for updates from inside the TUI with `/update`.

To see your current version:

```bash
evva -version
```

---

## First run

Just type `evva` from any directory. On the first launch evva auto-creates:

```
~/.evva/
├── config/
│   └── evva-config.yml      # user-tunable settings (auto-created with defaults)
└── skills/                  # optional skill scripts (your own)
```

A one-line stderr notice fires the first time only:

```
evva: wrote new config to ~/.evva/config/evva-config.yml — fill in your API keys to use cloud providers.
```

`~/.evva/.env` is **optional**. If you want to override deployment knobs (`LOG_LEVEL`, `LOG_DIR`, `APP_ENV`, `LOG_FORMAT`, `SKILLS_DIR`, `USER_PROFILE`), create it; otherwise the built-in defaults apply.

### Adding an API key

Two ways:

1. **From inside the TUI:** type `/config`, navigate to `<provider>.api_key`, press Enter, paste your key, press Enter again. Saved immediately.
2. **By hand:** open `~/.evva/config/evva-config.yml` and fill in `providers.<provider>.api_key`.

Cloud providers (Anthropic, DeepSeek, OpenAI) need a key; Ollama is local and key-less.

---

## TUI reference

![tui.png](docs/assets/tui.png)

---

## User Guide

Full usage documentation covering the TUI interface, slash commands, keybindings, yank mode, the permission system, sub-agents, and all configuration options:

- [English](docs/user-guide/en/user-guide.md)
- [正體中文](docs/user-guide/zh-tw/user-guide.md)

### Swarm & service (multi-agent workstation)

Run a team of collaborating agents with `evva service` + `evva swarm`. A 0→hero
walkthrough — concepts, building a swarm from scratch, the web workstation,
day-2 ops, restart-resume:

- [English](docs/roadmap/veronica/user-guide-en.md)
- [简体中文](docs/roadmap/veronica/user-guide-zh.md)

Or just try the ready-to-run [example swarm](docs/roadmap/veronica/example-swarm/) — copy
it out, `evva swarm .`, and watch a 3-agent team build a small site.

Running it 24/7? `evva service install-unit` wires the host into launchd /
systemd so it survives crashes and reboots —
[setup runbook (EN)](docs/user-guide/en/service-autostart.md) ·
[正體中文](docs/user-guide/zh-tw/service-autostart.md).

**CLI quick reference** (`evva swarm help` for the full list). Spaces are
Docker-style: a stable id plus a unique **name**, and every `<ref>` below accepts
either:

| Command | What it does |
| --- | --- |
| `evva swarm . [--name <n>]` | register `./evva-swarm.yml` as a new space (name: `--name` → manifest `name:` → generated) |
| `evva swarm ls` | list spaces — running and stopped (like `docker ps -a`) |
| `evva swarm run <ref>` | (re)start a stopped space, under its same id / URL |
| `evva swarm stop <ref>` | stop a space but keep it (restart with `run`) |
| `evva swarm rm <ref>` | forget a space entirely (its workdir data is left intact) |
| `evva swarm reset <ref>` | wipe a space — fresh ledger + cleared agent context, same id |
| `evva swarm add <ref> <m>` | hot-load member `<m>` into a space |

**External-event webhook.** An outside app can drive a swarm's leader by POSTing
an event — a webhook is just a message dropped on the leader's mailbox, so the
leader wakes (or folds it into its current run) and decides what to do. The
endpoint is **unauthenticated** (the service binds loopback only; this is for
local integrations in the current phase):

```
POST http://127.0.0.1:8888/api/swarm/<ref>/event
Body: { "title"?, "body" (required), "source"?, "data"?, "to"? (default leader), "idempotency_key"? }
→ 202 { "messageId": "<id>" }   (200 if the idempotency_key was already seen)
```

```python
# in your engine — one function is all you need
import requests
EVVA = "http://127.0.0.1:8888/api/swarm/trader/event"
def notify(title, body, data=None):
    requests.post(EVVA, json={"title": title, "body": body, "data": data}, timeout=2)

notify("BTC volatility spike", "BTC 1m vol > 3σ; 64210→66800 in 4m", {"symbol": "BTC", "z": 3.4})
```

```bash
curl -XPOST http://127.0.0.1:8888/api/swarm/trader/event \
  -d '{"title":"BTC volatility spike","body":"vol>3σ","data":{"symbol":"BTC","z":3.4}}'
```

---

## How to integrate EVVA agent in your Go project?

- [English](docs/user-guide/en/integration.md)

---

## LSP integration

- [English](docs/user-guide/en/lsp.md)

---


## Configuration

### `~/.evva/config/evva-config.yml`

User-tunable settings. Created automatically on first launch. Edit live via `/config` in the TUI, or by hand:

```yaml
# Agent loop
max_iterations: 30
max_tokens: 4096
auto_compact_threshold: 0.8
display_thinking: true

# Default model used at startup (overwritten by /model swap)
default_provider: deepseek
default_model: deepseek-v4-pro

# Permission stance at startup. Cycle at runtime with Shift+Tab; -permission-mode CLI flag overrides.
permission_mode: default     # default | accept_edits | plan | bypass

# Web tooling
fetch_max_bytes: 100000
tavily_api_key: ""

# Per-provider credentials. Empty api_url falls back to the constant's default.
providers:
  anthropic: { api_key: "", api_url: "" }
  deepseek:  { api_key: "", api_url: "" }
  openai:    { api_key: "", api_url: "" }
  ollama:    { api_url: "" }
```

### `.env` (optional)

Place in your working directory or at `~/.evva/.env`. Only used for deployment / logging knobs — never user preferences:

```bash
APP_ENV=dev            # dev | prod
LOG_LEVEL=info         # debug | info | warn | error
LOG_FORMAT=text        # text | json
LOG_DIR=               # unset → $EVVA_HOME/logs (default); path → custom dir; explicit empty → stdout-only
SKILLS_DIR=skills      # subpath under ~/.evva/
USER_PROFILE=user_profile.md
```

### CLI flags

```bash
evva                                # interactive TUI (when stdout is a TTY)
evva -version                       # print version, commit, and build date
evva update                         # self-update to newest stable (no Go required)
evva update v1.4.3-beta.1           # self-update to a specific tag (stable or beta)
evva -temp 0.7                      # sampling temperature (default unset)
evva -max-tokens 2048               # per-completion output cap (overrides YAML)
evva -max-iters 40                  # loop iteration cap (overrides YAML)
evva -permission-mode=plan          # boot in plan mode (read-only; see "Permission modes")
evva -permission-mode=bypass        # boot with the gate disabled
evva -no-tui "explain loop.go"      # one-shot plain-text mode
echo "list files in /tmp" | evva -no-tui   # piped prompt
```

---

## Features

**Agent loop**
- ReAct-style: LLM call → parallel tool dispatch → tool results → repeat.
- Multiple `tool_use` blocks per turn, executed concurrently.
- Iteration cap surfaces as a pausable state.
- Cancellable via `ctx`; Esc / Ctrl+C honored end-to-end.

**LLM providers**
- Anthropic Claude (extended thinking + cryptographic signature round-trip).
- DeepSeek (OpenAI-compatible chat, reasoning_content echoed back).
- OpenAI.
- Ollama (local).
- Per-provider option pattern (`WithTemperature`, `WithEffort`, ...).

**Tools**
- File system: `read_file`, `write_file`, `edit_file` — strict-absolute paths, structured `*FileDiff` metadata for diff rendering.
- Shell: `bash`, `grep`, `tree`.
- Tasks (six tools sharing one observable `*task.Store`).
- Meta: `agent` (sub-agents), `tool_search` (lazy schema loading), `skill`, `schedule_wakeup`.
- Plus stubs for `web_*`, `cron_*`, `notebook_edit`, `monitor`, `mode`, `ux`.

**Sub-agents**
- `explore` (read-only) and `general-purpose` presets.
- Sync mode (parent blocks) and async mode (parent continues, result lands on next iteration).
- Two-layer hierarchy: sub-agents can't spawn sub-agents.

**Observable store framework** (`internal/observable`)
- One pub/sub primitive any store can embed. Adding a new panel costs zero edits to the agent or event packages.

**Swappable UI** (`internal/ui`)
- Narrow `UI` and `Controller` interfaces. Reference bubbletea TUI under `internal/ui/bubbletea/`. `-no-tui` falls back to a plain CLI sink.

**Streaming completions** (chunked text + thinking).

**2-level compaction**
- micro: compress tool-result blocks when context budget approaches threshold.
- full: summarize the whole session into a single assistant brief.

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
│   │   ├── claude/  deepseek/  ollama/
│   ├── llmfactory/            # provider factory keyed by constant
│   ├── logger/                # structured slog wrapper + pretty fmt
│   ├── observable/            # pub/sub framework for stores
│   ├── session/               # conversation history + cumulative usage
│   ├── tools/                 # tool interface (Name/Schema/Execute)
│   │   ├── cron/  dev/  fs/  meta/  mode/  monitor/  notebook/
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
- `tools/*` packages produce `tools.Result` (text + opaque `Metadata`); the UI type-asserts on `Metadata` to render structured payloads.
- `observable` has no dependencies on agent or UI.
- `ui` defines two narrow interfaces; implementations live under it.

---

## Roadmap

### Planned
- **Multimodal Read**: images, PDFs (with `pages` range), Jupyter notebooks.
- **Overwrite diffs**: proper Myers/Hunt-McIlroy diff for `write_file` overwrites.
- **Per-agent LLM**: sub-agent can use a different provider than its parent.
- **Veronica space**: long-running local sandbox service on `:8080`.
- **Web UI**: a second `UI` implementation served over WebSocket.
- **Session persistence**: `/resume` to reload a session snapshot.

### Known limitations
- Windows is not yet supported. macOS and Linux only.
- Sub-agent hierarchy is exactly two layers (no nested spawning).
- Token counts depend on provider reporting — Ollama only reports prompt / eval, not cache or reasoning splits.
- The TUI transcript grows unbounded in a long session; compaction is on the list above.

---

## License

See [LICENSE](LICENSE).
