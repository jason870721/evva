# EVVAgent (evva)

A ReAct coding agent in your terminal. Multi-provider LLM, parallel tool dispatch, async sub-agents, swappable UI.

---

## What is EVVAgent?

`evva` runs a tool-using LLM agent in your terminal. It speaks Anthropic Claude, DeepSeek, OpenAI, and Ollama through one `llm.Client` interface; dispatches multiple tool calls per turn in parallel; tracks tasks and sub-agents through an observable store; and renders into a bubbletea TUI or a plain-text CLI sink.

The architecture is small on purpose — adding a new LLM provider, panel, or UI implementation is roughly one package each.

---

## Install

```bash
git clone https://github.com/johnny1110/evva
cd evva
make install
```

Default install target is `$GOBIN` (or `$GOPATH/bin` when `GOBIN` is unset) — usually already on a Go developer's `PATH`. The `make install` output tells you whether to add it.

Override the location if you want it elsewhere:

```bash
sudo make install PREFIX=/usr/local/bin     # system-wide
make install PREFIX=$HOME/.local/bin        # user-local
```

Verify:

```bash
which evva
evva --help-ish    # any flag triggers the usage line
```

Uninstall removes only the binary; your `~/.evva/` config is preserved:

```bash
make uninstall
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

## Using EVVAgent

### TUI at a glance

```
┌──────────────────────────────────────────────────────────────┐
│ banner box / transcript                                      │
│                                                              │
│  ▶ user prompt                                               │
│  assistant text…                                             │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│ ▰ TASKS         (only when non-empty)                        │
│   ▶ wire migration                                           │
├──────────────────────────────────────────────────────────────┤
│ ‹⠹ explorer› ‹▶ writer› ‹✔ reviewer›   ← active sub-agents   │
├──────────────────────────────────────────────────────────────┤
│ overlay panels: /config · /model · approval · suggestions    │
├──────────────────────────────────────────────────────────────┤
│ > input                                                      │
├──────────────────────────────────────────────────────────────┤
│ ‹⠋ RUN› ◆ evva ◆ ▸ model ◆ in N out M ◆ CTX ▰▰▱…▱ 12%       │
└──────────────────────────────────────────────────────────────┘
```

Panels collapse to zero height when empty. Status bar always at the bottom.

### Slash commands

Type `/` at the start of the input and a suggestion panel appears. As you type more characters, the list filters by case-insensitive prefix match. When the typed input is an **exact match** for a command, that row turns green with a `✓` — pressing Enter executes it.

| key | effect |
| --- | --- |
| `Tab` | autocomplete to the highlighted suggestion |
| `↑` / `↓` | move the highlighted suggestion |
| `Enter` | submit the current input (executes if it's a valid command) |
| `Esc` | dismiss the suggestion panel for this typing session |

Available commands:

| command | what it does |
| --- | --- |
| `/config` | open the settings form (see below) |
| `/model` | switch LLM provider / model — **clears conversation history** |
| `/clear` | clear the transcript (keeps the banner) |
| `/exit`, `/quit` | quit |

### `/config` — runtime settings

Opens a bordered form listing every editable setting:

```
┌─ /CONFIG ────────────────────────────────────────┐
│ ▶ max_iterations           30                    │
│   max_tokens               4096                  │
│   auto_compact_threshold   0.8                   │
│   display_thinking         true                  │
│   fetch_max_bytes          100000                │
│   tavily_api_key           ****wxyz              │
│   anthropic.api_key        (empty)               │
│   …                                              │
│ [↑↓] navigate · [Enter] edit/toggle · [Esc] close│
└──────────────────────────────────────────────────┘
```

| key | effect |
| --- | --- |
| `↑` / `↓` | move the cursor |
| `Enter` | edit the focused field (booleans toggle in-place, no editor needed) |
| `Enter` (in editor) | apply and save |
| `Esc` | cancel the edit (or close the panel from list mode) |

API key fields open a password-masked editor; pasting works (display stays masked).

**Live-applied** (takes effect immediately):
- `max_iterations` — the loop's safety cap; mutates the running agent
- `display_thinking` — toggles thinking blocks in the transcript
- `auto_compact_threshold` — when context compaction kicks in

**Persisted but next-launch only** (would require rebuilding `llm.Client` / web tools):
- `max_tokens`, `fetch_max_bytes`, `tavily_api_key`, all `<provider>.api_key`, all `<provider>.api_url`

Every edit writes immediately to `~/.evva/config/evva-config.yml`. Closing the panel is a no-op — there's nothing left to commit.

### `/model` — switch provider/model

Opens a flat list of every `(provider, model)` pair the binary knows about, cursor pre-positioned on the active one. Up/Down to navigate, Enter to switch, Esc to cancel.

```
┌─ /MODEL ─────────────────────────────────────────────────────┐
│ Swapping clears the conversation — provider-specific state   │
│ (thinking signatures) can't carry across providers.          │
│                                                              │
│   ollama / qwen3.6                                           │
│   anthropic / claude-sonnet-4-6                              │
│   anthropic / claude-opus-4-7                                │
│ ▶ deepseek / deepseek-v4-pro  (current)                      │
│   deepseek / deepseek-v4-flash                               │
│   openai / gpt-5.5                                           │
│                                                              │
│ [↑↓] navigate · [Enter] switch · [Esc] cancel                │
└──────────────────────────────────────────────────────────────┘
```

**Important:** switching always clears the session. Anthropic's `ThinkingSignature` is provider-locked — carrying old history across a swap would 400 on the next request. The new choice is also persisted as `default_provider` + `default_model` so your next launch starts there.

Switching is refused if a run is in flight; press Esc first to cancel, then `/model` again.

### Keybindings (main input)

| key | effect |
| --- | --- |
| `Enter` | submit |
| `Ctrl+J` / `Alt+Enter` | insert newline (multi-line composition) |
| `↑` / `↓` | walk prompt history (when input empty or already navigating) |
| `Esc` | cancel running task / dismiss panel |
| `Ctrl+C` | once: cancel running task · idle: quit |
| `Ctrl+D` | quit (when input is empty) |
| `Ctrl+O` | toggle expand-all tool results (fold/unfold long bash + read output) |
| `Ctrl+Y` | open **yank mode** — pick a block and copy its clean content (see below) |
| `Ctrl+F` | open **transcript search** — type a query, `Enter`/`n` cycles matches |
| `Shift+Tab` | cycle the **permission mode** — `default → accept_edits → plan → bypass → …` (see below) |
| `PgUp` / `PgDown` / `Home` / `End` | scroll transcript |
| mouse wheel | scroll transcript |

### Copying from the transcript — **yank mode**

The transcript renders each block with a left-edge timeline gutter (`│`, `├─`, etc.) so the conversation reads as a structured stream. The downside: a normal terminal drag-select copies whatever is visually on screen — gutter glyphs included. Pasting that into another window gives you something like:

```
▶ who are you?
│
│ I'm evva — an interactive coding assistant…
│
```

To copy clean content without the chrome, evva ships a **yank mode** that knows about block boundaries. It's the canonical clean-copy path; on terminals that don't fully support clipboard escapes, it's also the only one that works at all.

**Open with `Ctrl+Y`.** A cyan-bold gutter accent appears on one block at a time; the contextual hint above the status bar shows your cursor position (`yank 3/5`) and the key map.

| key | effect |
| --- | --- |
| `j` / `↓` | next block (newer) |
| `k` / `↑` | previous block (older) |
| `g` | jump to the first block |
| `G` | jump to the last block |
| `Enter` / `c` | copy the focused block's clean text to the system clipboard |
| `e` | toggle expand-all on this block only (handy for long tool results before copying) |
| `q` / `Esc` | exit yank mode (clears the accent) |
| `Ctrl+C` | exit + quit evva |

**What gets copied.** Each block exposes a `PlainText()` view that strips ANSI escapes and gutter glyphs. For a user prompt that's the prompt text. For assistant text it's the markdown source (not the rendered output). For a tool block it's the call head (`◢ name(...)`) followed by the result body. The status bar flashes `copied N chars` on success.

**How it gets there — OSC52.** Yank mode writes the payload to your clipboard using the [OSC52](https://wezfurlong.org/wezterm/escape-sequences.html#operating-system-command-sequences) terminal escape sequence. No external library, no `pbcopy` shell-out. The terminal forwards the escape to the OS clipboard.

| terminal | works out of the box? |
| --- | --- |
| **iTerm2** | yes (default) |
| **kitty** | yes |
| **WezTerm** | yes |
| **Alacritty** | yes |
| **Ghostty** | yes |
| **Apple Terminal.app** | no by default — enable `Edit → Allow clipboard access` or switch terminals |
| **tmux** | yes if `set -g set-clipboard on` |
| **GNU screen** | mostly broken; use Ctrl+Y from inside a host terminal instead |

If the write fails (payload too large at >100 KB, terminal blocked it), the status bar shows `clipboard: <error>` and yank mode stays open so you can try a different block.

**Why not native drag-select?** evva turns on mouse capture so the wheel can scroll the transcript. That trade-off means drag-and-drop copy stops happening natively — and even when modern terminals honor a `Shift`/`Alt`+drag escape hatch, the resulting selection still includes the rendered gutter glyphs (since they're part of what's painted on screen). Yank mode is the workflow that round-trips clean content out of the program.

### Permission modes

evva gates every tool call through a **permission mode**. Four modes, cycled with `Shift+Tab`:

| mode | auto-allowed without asking | best for |
| --- | --- | --- |
| **`default`** | Read-only access. Read tools (`read`, `tree`, `grep`, `glob`, `web_*`, `json_query`, `calc`), agent self-coordination (`agent`, `task_*`, `todo_write`, `tool_search`, `skill`, `ask_user_question`), and **read-only bash commands** (`ls`, `cat`, `head`, `grep`, `git status`, `git log`, …). File writes and any other bash command **ask**. | Beginners, sensitive work, default stance |
| **`accept_edits`** | Same as `default` + file edits (`edit`, `write`, `notebook_edit`) + common filesystem bash commands (`mkdir`, `touch`, `mv`, `cp`, `rmdir`, `ln`, `chmod`, `chown`). | Iterating on code under review |
| **`plan`** | Same read-only safelist as `default`. Anything outside that set is **denied outright** (no prompt). | Exploring a codebase before deciding what to change |
| **`bypass`** | Everything. Dangerous-command classification still logs in the background, but never blocks. | **Isolated containers and VMs only** — propagates to subagents |

**Read-only ≠ no subagents.** "Read-only access" only protects the filesystem. Agent meta-tools — spawning a subagent, creating tasks, writing a todo list — are agent-internal coordination and remain auto-allowed in `default` and `plan`. The model can still plan and delegate; it just can't write files or run shell unless you approve.

The active mode shows in the status bar as a colored badge (`⛨ plan`, `⛨ bypass`, …). `default` collapses the cell so the bar isn't noisy.

#### Starting in a specific mode

```bash
evva -permission-mode=plan                # safest: investigate first
evva -permission-mode=accept_edits        # auto-apply edits + safe fs cmds
evva -permission-mode=bypass              # no prompts; sandboxed envs only
```

CLI flag takes precedence; persistent default lives in `evva-config.yml`:

```yaml
permission_mode: default     # default | accept_edits | plan | bypass
```

### Approval prompts

In `default` / `accept_edits` / `plan` / `auto` modes, anything that needs your approval opens a modal:

```
┌─ APPROVAL ─────────────────────────────────────────┐
│ tool: bash                                         │
│ mode: default  risk: dangerous (sudo)              │
│ reason: matches dangerous prefix                   │
│                                                    │
│ input: sudo rm /tmp/evil-file                      │
│                                                    │
│ ▶ [1] Allow once                                   │
│   [2] Allow for this session                       │
│   [3] Deny                                         │
│                                                    │
│ [↑↓] choose · [Enter] confirm · [Esc] deny         │
└────────────────────────────────────────────────────┘
```

| key | effect |
| --- | --- |
| `↑` / `↓` | move between buttons |
| `1` / `a` | Allow once — runs this call only |
| `2` / `s` | Allow for this session — also adds an in-memory rule so similar calls don't re-prompt |
| `3` / `d` | Deny — Enter again to type an optional reason for the model |
| `Enter` | confirm the highlighted choice (or commit a deny reason) |
| `Esc` | shortcut for deny |
| `Ctrl+C` | deny + quit |

**"Allow for this session"** picks a sensible rule shape from the call: for `bash` it stores the first token (so approving `git status` allows future `git …` calls, not arbitrary commands); for `read`/`write`/`edit` it stores the file path; other tools become tool-wide. Session rules vanish when you quit; persist them by hand-editing `permissions.json` (next section).

Parallel approvals (the agent emitting two `bash` calls in one turn) stack — resolve the top one and the next surfaces.

### Permission rules — `.evva/permissions.json`

Rules persist your approvals so you don't see the same prompt twice across runs. Two scopes:

- `<workdir>/.evva/permissions.json` — **project**: lives with the repo, share via git if you want
- `~/.evva/permissions.json` — **user**: applies in every working directory

Format mirrors Claude Code's settings.json `permissions` block:

```json
{
  "permissions": {
    "allow": [
      "bash(git:*)",
      "bash(npm:*)",
      "read(src/**)",
      "edit",
      "tree"
    ],
    "deny": [
      "bash(sudo:*)",
      "bash(rm -rf /)"
    ],
    "ask": [
      "bash(npm publish)"
    ]
  }
}
```

**Rule grammar**: `ToolName` matches every call to that tool. `ToolName(content)` adds a content match — tool-specific:

| tool | content syntax | examples |
| --- | --- | --- |
| `bash` | `prefix:*`, `pattern *`, `git *`, or exact command | `bash(git:*)`, `bash(npm install *)`, `bash(make build)` |
| `read`, `write`, `edit`, `notebook_edit` | doublestar glob against the `file_path` | `read(src/**)`, `write(./tmp/*.txt)`, `edit(**/*.go)` |
| anything else | exact string match against the raw input | rare; prefer tool-wide rules |

**Precedence**:

1. `bypass` mode — always allow, rules ignored.
2. **deny rules** — checked first, win over allow in every non-bypass mode.
3. **ask rules** — force a prompt even if a broader allow (or mode safelist) would have matched.
4. `plan` mode + tool not in read-only safelist → **deny** (no prompt).
5. Read-only / self-coordination safelist → allow.
6. Bash + classifier says read-only (`ls`, `cat`, `git status`, …) → allow.
7. `accept_edits` only: `edit`/`write`/`notebook_edit` → allow; bash common-fs command (`mkdir`/`mv`/`cp`/…) → allow.
8. **allow rules** — match → run.
9. Fallback — ask.

Source priority within each behavior (deny/ask/allow) is `session > project > user`, so a session "allow for this session" beats a user-scope rule but never beats a deny.

### Sub-agents

The root agent can spawn sub-agents (`explore` for read-only inspection, `general-purpose` for write-capable). Active sub-agents appear as chips in a horizontal strip above the input. Async sub-agents finish in the background — their summaries land as a synthetic user message at the top of the next iteration, so the conversation picks them up automatically.

You don't drive sub-agents yourself; the model decides when to spawn one. Two-layer hierarchy by design (sub-agents can't spawn sub-agents).

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
LOG_DIR=               # empty → stdout; path → write log files there
SKILLS_DIR=skills      # subpath under ~/.evva/
USER_PROFILE=user_profile.md
```

### CLI flags

```bash
evva                                # interactive TUI (when stdout is a TTY)
evva -temp 0.7                      # sampling temperature (default unset)
evva -max-tokens 2048               # per-completion output cap (overrides YAML)
evva -max-iters 40                  # loop iteration cap (overrides YAML)
evva -permission-mode=plan          # boot in plan mode (read-only; see "Permission modes")
evva -permission-mode=bypass        # boot with the gate disabled
evva -no-tui "explain loop.go"      # one-shot plain-text mode
echo "list files in /tmp" | evva -no-tui   # piped prompt
```

---

## Modes

**Interactive TUI** (default when stdout is a TTY). Transcript, panels, status bar, the works.

**Plain CLI** (`-no-tui`, or when stdout is piped). One-shot flow: read a prompt from args/stdin → run the agent → stream events as plain text → exit. CLI mode has no interactive approval surface — any call that would prompt is **denied automatically** with a hint to pass `-permission-mode=bypass` or add a rule to `permissions.json`. Useful for scripts and CI.

---

## Logs

Per-agent JSON logs land under `log/<agent-id>/<agent-id>.log` by default. Set `LOG_DIR` in `.env` to redirect, or leave it unset to also stream to stdout. `LOG_LEVEL=debug` exposes every iteration's `turn.start` / `llm.call` / `tool.dispatch` / `tool.result` lines — handy when debugging an agent that's stuck or looping.

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
- Sub-agent hierarchy is exactly two layers (no nested spawning).
- Token counts depend on provider reporting — Ollama only reports prompt / eval, not cache or reasoning splits.
- The TUI transcript grows unbounded in a long session; compaction is on the list above.

---

## License

See [LICENSE](LICENSE).
