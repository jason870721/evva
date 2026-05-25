# EVVAgent — User Guide

## Table of Contents

- [1. Overview — TUI at a Glance](#1-overview--tui-at-a-glance)
- [2. Slash Commands](#2-slash-commands)
  - [/config — Runtime Settings](#config--runtime-settings)
  - [/model — Switch Provider/Model](#model--switch-providermodel)
  - [/profile — Switch Persona](#profile--switch-persona)
  - [/effort — Thinking Effort](#effort--thinking-effort)
  - [/resume — Resume a Previous Session](#resume--resume-a-previous-session)
  - [Bundled skills](#bundled-skills)
- [3. Keybindings](#3-keybindings)
- [4. Yank Mode — Copying from the Transcript](#4-yank-mode--copying-from-the-transcript)
- [5. Transcript Search](#5-transcript-search)
- [6. Permission System](#6-permission-system)
  - [Permission Modes](#permission-modes)
  - [Plan Mode (`enter_plan_mode` / `exit_plan_mode`)](#plan-mode-enter_plan_mode--exit_plan_mode)
  - [Worktrees (`enter_worktree` / `exit_worktree`)](#worktrees-enter_worktree--exit_worktree)
  - [Approval Prompts](#approval-prompts)
  - [Permission Rules](#permission-rules)
- [7. Sub-agents and Personas](#7-sub-agents-and-personas)
- [8. Hooks](#8-hooks)
  - [Where Hooks Live](#where-hooks-live)
  - [File Shape](#file-shape)
  - [Events](#events)
  - [Payload and Decision](#payload-and-decision)
- [9. Configuration Reference](#9-configuration-reference)
  - [evva-config.yml](#evva-configyml)
  - [.env](#env-optional)
  - [CLI Flags](#cli-flags)
- [10. Modes — TUI vs CLI](#10-modes--tui-vs-cli)
- [11. Logs](#11-logs)
- [12. Building on evva — the SDK (for developers)](#12-building-on-evva--the-sdk-for-developers)
  - [Quickstart — a full host in ~40 lines](#quickstart--a-full-host-in-40-lines)
  - [Extension points at a glance](#extension-points-at-a-glance)
  - [Stability & where to go deeper](#stability--where-to-go-deeper)

---

## 1. Overview — TUI at a Glance

```
┌──────────────────────────────────────────────────────────────┐
│ banner box / transcript                                      │
│                                                              │
│  ▶ user prompt                                               │
│  assistant text…                                             │
│                                                              │
├──────────────────────────────────────────────────────────────┤
│ ▰ TODOS         (only when non-empty)                        │
│   ▶ wire migration                                           │
├──────────────────────────────────────────────────────────────┤
│ ‹⠹ explorer› ‹▶ writer› ‹✔ reviewer›   ← active sub-agents   │
├──────────────────────────────────────────────────────────────┤
│ overlay panels: /config · /model · /profile · approval · …   │
├──────────────────────────────────────────────────────────────┤
│ > input                                                      │
├──────────────────────────────────────────────────────────────┤
│ ‹⠋ RUN› ◆ EVVA ◆ ▸ model ◆ in N out M ◆ CTX ▰▰▱…▱ 12%       │
└──────────────────────────────────────────────────────────────┘
```

Panels collapse to zero height when empty. The status bar is always visible at the bottom; the `EVVA` cell shows the active persona name uppercased — it changes to `NONO`, `MY-PERSONA`, etc. after a `/profile` switch.

---

## 2. Slash Commands

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
| `/config` | open the settings form |
| `/model` | switch LLM provider / model — **clears conversation history** |
| `/profile` | switch agent persona (evva, nono, …) — **clears conversation history** |
| `/effort` | set thinking effort (low / medium / high / ultra) |
| `/compact` | compact the transcript — pick micro or full |
| `/resume` | resume a previous session from this workdir |
| `/clear` | clear the transcript (keeps the banner) |
| `/exit`, `/quit` | quit |

User-installed skills appear here too — anything you've dropped in `~/.evva/skills/<name>/SKILL.md` or `<workdir>/.evva/skills/<name>/SKILL.md` shows up as `/<name>` in the same panel.

### /config — Runtime Settings

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
| `Enter` | edit the focused field (booleans toggle in-place) |
| `Enter` (in editor) | apply and save |
| `Esc` | cancel the edit (or close the panel from list mode) |

API key fields open a password-masked editor; pasting works (display stays masked).

**Live-applied** (takes effect immediately):

- `max_iterations` — the loop's safety cap
- `display_thinking` — toggles thinking blocks in the transcript
- `auto_compact_threshold` — when context compaction kicks in

**Persisted but next-launch only** (would require rebuilding the client / web tools):

- `max_tokens`, `fetch_max_bytes`, `tavily_api_key`, all `<provider>.api_key`, all `<provider>.api_url`

Every edit writes immediately to `~/.evva/config/evva-config.yml`.

### /model — Switch Provider/Model

Opens a flat list of every `(provider, model)` pair the binary knows about, cursor pre-positioned on the active one:

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
│   openai / gpt-5.4-mini                                      │
│   openai / gpt-5.5                                           │
│                                                              │
│ [↑↓] navigate · [Enter] switch · [Esc] cancel                │
└──────────────────────────────────────────────────────────────┘
```

| key | effect |
| --- | --- |
| `↑` / `↓` | navigate the list |
| `Enter` | switch to the highlighted model |
| `Esc` | cancel |

**Important:** switching always clears the session. Anthropic's `ThinkingSignature` is provider-locked — carrying old history across a swap would 400 on the next request. The new choice is also persisted as `default_provider` + `default_model` so your next launch starts there.

Switching is refused if a run is in flight; press Esc first to cancel, then `/model` again.

### /profile — Switch Persona

Switches the agent's persona — different identity, system prompt, and tool surface. Built-in `evva` (the full-kit software-engineer persona) ships with the binary; you can add more by dropping an `agents/<name>/` directory under `~/.evva/`:

```
~/.evva/agents/nono/
├── system_prompt.md   # the persona body (required)
├── tools.yml          # { active: [...], deferred: [...] }
└── meta.yml           # { as: [main|subagent|both], when_to_use, inject_memory, advertise_skills }
```

`meta.yml` keys:

| key | meaning |
| --- | --- |
| `as` | one of `[main]`, `[subagent]`, or `[main, subagent]`. `main` makes it appear in `/profile`; `subagent` makes it callable via the Agent tool's `subagent_type` enum |
| `when_to_use` | one-sentence blurb the picker shows next to the name |
| `inject_memory` | when `true`, the persona receives the `EVVA.md` + `USER_PROFILE.md` snapshot in its system prompt. Default `false` |
| `advertise_skills` | when `true`, the persona's prompt advertises the installed skill catalog. Default `false` |

The picker lists every persona with `as:` containing `main`:

```
┌─ /PROFILE ───────────────────────────────────────────────────┐
│ Switching clears the conversation — each persona has its own │
│ system prompt and tool surface.                              │
│                                                              │
│ ▶ evva  (current)  — full-kit software-engineer              │
│   nono             — finance / numbers persona               │
│                                                              │
│ [↑↓] navigate · [Enter] switch · [Esc] cancel                │
└──────────────────────────────────────────────────────────────┘
```

On switch the transcript clears, the status-bar label updates to the new persona's uppercased name, and the new persona is persisted as `default_profile` so next launch boots into it.

A persona declared `as: [main, subagent]` is **also** callable from the running root agent via the Agent tool — that's the cross-persona delegation path (e.g. `evva` asking `nono` a finance question without leaving the session).

Switching is refused if a run is in flight; press Esc first to cancel, then `/profile` again.

### /effort — Thinking Effort

Adjusts the model's reasoning depth. Four tiers:

| tier | use when |
| --- | --- |
| `low` | quick lookups, "what's the syntax for X" |
| `medium` | default — most coding tasks |
| `high` | non-trivial reasoning, multi-step refactors |
| `ultra` | architectural calls, subtle bug hunts |

Each provider maps these onto its own knob — Anthropic effort levels, DeepSeek thinking on/off + tier, OpenAI reasoning effort, etc. Providers with only a coarse on/off switch map `low` → off and the rest → on. The chosen tier persists as `default_effort` and is shown in the status bar (`▸ model · ⚡high`).

### /resume — Resume a Previous Session

Reload a previous session from this workdir. Every iteration's state is persisted to `~/.evva/sessions/<workdir-slug>/<session-id>.json`, so closing the TUI and reopening it doesn't lose work — `/resume` brings the conversation back exactly where you left it.

The picker lists the 10 most-recently-touched sessions per page, sorted by last-write time descending. Each row shows the first user prompt of that session as a one-line preview plus the persona, message count, and model:

```
┌─ /RESUME ────────────────────────────────────────────────────┐
│ Reload a previous session — same workdir only, most recent   │
│ first. Resuming clears the live transcript and replaces it   │
│ with the saved one.                                          │
│                                                              │
│ ▶ wire up the /resume slash command and overlay              │
│     5m ago · evva · 42 msgs · claude-opus-4-7                │
│   add update_user_profile + update_project_memory tools      │
│     2h ago · evva · 87 msgs · claude-opus-4-7                │
│   verify the multi-platform release workflow                 │
│     1d ago · evva · 18 msgs · deepseek-v4-pro                │
│   …                                                          │
│                                                              │
│ page 1 / 3                                                   │
│ [↑↓] cursor · [←→] page · [Enter] resume · [Esc] cancel      │
└──────────────────────────────────────────────────────────────┘
```

| key | effect |
| --- | --- |
| `↑` / `↓` | move the cursor within the current page |
| `←` / `→` | flip to the previous / next page (10 entries per page) |
| `Enter` | resume the highlighted session |
| `Esc` | cancel |

**What gets restored:**

- The full message history — every user prompt, assistant reply, thinking block, tool call, and tool result is replayed into the transcript so you can scroll up and read prior work.
- The persona, provider, and model the session was running under. If any of those are no longer available (you deleted the persona, swapped to a build without that model) the resume falls back to `evva` / your current default and logs a warning.
- The session-id — subsequent saves overwrite the same file rather than creating a new one, so a resumed session keeps a single entry in the picker.
- The cumulative usage and context bar in the status pill.

**Scope:** sessions are scoped to the workdir they were started from. Running `evva` in a different directory shows that directory's sessions; the global pool lives under `~/.evva/sessions/` organised by workdir slug (e.g. `-Users-alice-lab-myrepo`).

**Save cadence:** the file is rewritten after every loop iteration (i.e. after each tool round-trip) so a crashed evva loses at most one in-flight LLM call's worth of work.

**Compact behavior:** a full `/compact` overwrites the same session file with the post-compact brief — the picker still shows one entry, now containing the summary instead of the original transcript.

**Subagents:** only the root agent's session is persisted. Subagents spawned via the Agent tool are ephemeral by design and never appear in `/resume`.

Resuming is refused if a run is in flight; press Esc first to cancel, then `/resume` again.

### Bundled skills

evva ships five **bundled skills** out of the box — first-party instruction documents the agent can invoke. The model uses them automatically when a request matches, and you can invoke any of them yourself by typing `/<name>`:

| Skill | What it does |
| --- | --- |
| `/commit` | Draft and create a git commit for the current diff, authored as evva. |
| `/review` | Review a GitHub pull request (uses `gh`). |
| `/security-review` | Focused security pass on the branch's pending changes. |
| `/simplify` | Three-reviewer cleanup pass (reuse / quality / efficiency), then applies the fixes. |
| `/setup-hooks` | Walk you through authoring a lifecycle hook in `.evva/settings.json` (see §8). |

Bundled skills are the **lowest-precedence** tier: drop your own `SKILL.md` at `~/.evva/skills/<name>/SKILL.md` or `<workdir>/.evva/skills/<name>/SKILL.md` with the **same name** to override the built-in body silently. Skills load at startup — restart evva after adding or editing one. For authoring custom skills and the SDK path, see [Building on evva](#12-building-on-evva--the-sdk-for-developers) and `docs/extending.md`.

---

## 3. Keybindings

| key | effect |
| --- | --- |
| `Enter` | submit |
| `Ctrl+J` / `Alt+Enter` | insert newline (multi-line composition) |
| `↑` / `↓` | walk prompt history (when input empty or already navigating) |
| `Esc` | cancel running task / dismiss panel |
| `Ctrl+C` | once: cancel running task · idle: quit |
| `Ctrl+D` | quit (when input is empty) |
| `Ctrl+O` | toggle expand-all tool results (fold/unfold long bash + read output) |
| `Ctrl+Y` | open **yank mode** — pick a block and copy its clean content |
| `Ctrl+F` | open **transcript search** — type a query, `Enter`/`n` cycles matches |
| `Shift+Tab` | cycle the **permission mode** — `default → accept_edits → plan → bypass → …` |
| `PgUp` / `PgDown` / `Home` / `End` | scroll transcript |
| mouse wheel | scroll transcript |

---

## 4. Yank Mode — Copying from the Transcript

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

---

## 5. Transcript Search

Press `Ctrl+F` to open the search bar. Type your query and press `Enter` to jump to the first match. Press `n` to cycle forward through matches, or `N` (Shift+n) to cycle backward. Press `Esc` to close the search bar.

---

## 6. Permission System

### Permission Modes

evva gates every tool call through a **permission mode**. Four modes, cycled with `Shift+Tab`:

| mode | auto-allowed without asking | best for |
| --- | --- | --- |
| **`default`** | Read-only tools (`read`, `tree`, `grep`, `glob`, `web_*`, `json_query`, `calc`, `daemon_list`, `daemon_output`), agent self-coordination (`agent`, `todo_write`, `skill`, `tool_search`, `ask_user_question`), and **read-only bash commands** (`ls`, `cat`, `head`, `grep`, `git status`, `git log`, …). File writes and any other bash command **ask**. | Beginners, sensitive work, default stance |
| **`accept_edits`** | Same as `default` + file edits (`edit`, `write`, `notebook_edit`) + common filesystem bash commands (`mkdir`, `touch`, `mv`, `cp`, `rmdir`, `ln`, `chmod`, `chown`). | Iterating on code under review |
| **`plan`** | Same read-only safelist as `default`. Anything outside that set is **denied outright** (no prompt). | Exploring a codebase before deciding what to change |
| **`bypass`** | Everything. Dangerous-command classification still logs in the background, but never blocks. | **Isolated containers and VMs only** — propagates to subagents |

The active mode shows in the status bar as a colored badge (`⛨ plan`, `⛨ bypass`, …). `default` collapses the cell so the bar isn't noisy.

**Starting in a specific mode:**

```bash
evva -permission-mode=plan                # safest: investigate first
evva -permission-mode=accept_edits        # auto-apply edits + safe fs cmds
evva -permission-mode=bypass              # no prompts; sandboxed envs only
```

The CLI flag takes precedence; a persistent default lives in `evva-config.yml`:

```yaml
permission_mode: default     # default | accept_edits | plan | bypass
```

### Plan Mode (`enter_plan_mode` / `exit_plan_mode`)

Plan mode is `permission_mode: plan` with two model-callable tools that automate the workflow. The model can flip itself into plan mode for non-trivial tasks (new features, architectural decisions, multi-file refactors); you can also enter manually with `Shift+Tab`.

**The workflow:**

1. **Enter** — model calls `enter_plan_mode` (or you cycle to `plan` via `Shift+Tab`). The status bar reads `⛨ plan`. Every write is denied **except** to a single dedicated plan file.
2. **Plan file** — `<workdir>/.evva/plans/current.md`. One plan per session. `enter_plan_mode` creates / truncates this file; the model writes its plan there as markdown using normal `write` / `edit` calls. The permission gate carves out this exact path; any other write target still hard-denies with *"plan mode forbids writes — Shift+Tab to exit plan mode."*
3. **Explore** — `read`, `grep`, `glob`, `tree`, `agent` (spawning an `explore` subagent) all auto-allow. The model investigates the codebase, drafts the plan, iterates.
4. **Exit** — when the plan is ready, the model calls `exit_plan_mode`. evva reads the plan file from disk and pops a **Plan Approval** overlay showing the markdown body:

```
┌─ PLAN APPROVAL ────────────────────────────────────┐
│ tool: exit_plan_mode                               │
│ mode: plan                                         │
│ reason: Plan approval — review and approve to exit │
│                                                    │
│ plan:                                              │
│   # Phase 7 — Plan Mode                            │
│   ## Context                                       │
│   …                                                │
│   ## Design                                        │
│   …                                                │
│                                                    │
│ ▶ [1] Allow once     (approve plan, exit mode)     │
│   [2] Allow for…     (rarely useful for plans)     │
│   [3] Deny           (reject — model iterates)     │
└────────────────────────────────────────────────────┘
```

- **Approve** (`1` / Enter) — plan mode exits, the previous mode is restored (`default` / `accept_edits` / whatever was active before `enter_plan_mode`), the model proceeds to implementation.
- **Deny** (`3` / Esc) — type a one-line reason; the model receives `"User requested changes: <reason>"`, stays in plan mode, and iterates on the plan file.

**Notes:**

- The model is told `exit_plan_mode` IS the approval signal — it must not call `ask_user_question` to ask "is this plan okay?".
- Subagents can't flip the parent session's plan mode — `enter_plan_mode` / `exit_plan_mode` are root-agent only.
- Plan files persist after exit; the next `enter_plan_mode` truncates them. To keep a plan around, copy `current.md` out of `.evva/plans/` before re-entering plan mode.

### Worktrees (`enter_worktree` / `exit_worktree`)

A worktree is a parallel checkout of the same git repository on a separate branch, living in its own directory. Use one when you want a sandbox: a risky refactor, a destructive experiment, a parallel feature branch you can throw away.

The model **only** invokes these tools when you explicitly say "worktree" — phrases like *"start a worktree"*, *"work in a worktree called demo"*, *"exit the worktree"*. Anything more ambiguous (*"branch off"*, *"refactor this"*) keeps the session on the original workdir.

**The workflow:**

1. **Enter** — model calls `enter_worktree` (optionally with a `name`). evva runs `git worktree add -b worktree-<slug> <repo>/.evva/worktrees/<slug>/ HEAD` and switches the session's working directory to the new worktree. Subsequent `read` / `edit` / `write` / `bash` calls run in the worktree — the original directory is untouched.
2. **Work** — drive the session normally. Reads, edits, commits all happen inside the worktree on its own branch.
3. **Exit** — when done, model calls `exit_worktree` with `action: "keep"` or `action: "remove"`:
   - `"keep"` — the worktree directory and branch stay on disk. Useful if you want to come back to the work or merge it later.
   - `"remove"` — runs `git worktree remove --force` and deletes the branch. If the worktree has uncommitted changes the tool refuses unless you explicitly say *"remove, discard the changes"* (the model re-invokes with `discard_changes: true`).
4. The session is restored to the original directory; EVVA.md and the system prompt rebuild against the original workdir.

**Subagent isolation** — the `agent` tool accepts `isolation: "worktree"`. Spawning a subagent with that flag creates a per-subagent worktree under `.evva/worktrees/agent-<id>/` and the child runs entirely inside it. On a clean exit (no file changes, no commits) evva auto-removes the worktree; otherwise it stays on disk and the subagent's result reports `worktree_path:` / `worktree_branch:` so you can inspect or merge.

**Notes:**

- Worktrees live at `<repo>/.evva/worktrees/<slug>/`. Add `.evva/` to `.gitignore` if you don't already.
- Plan mode denies `enter_worktree` / `exit_worktree` (they're not in the read-only safelist). Exit plan mode first if you want to start one.
- Subagents can't enter a worktree mid-session — only the root agent can. The AgentTool's `isolation` parameter is the way to put a subagent inside a worktree.
- Worktrees have no `.worktreeinclude` support in v1 — gitignored files (`.env`, local config) are NOT copied into the new worktree. Set them up by hand in the worktree if needed.

### Approval Prompts

In `default` / `accept_edits` / `plan` modes, anything that needs your approval opens a modal:

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

**"Allow for this session"** picks a sensible rule shape from the call: for `bash` it stores the first token (so approving `git status` allows future `git …` calls, not arbitrary commands); for `read`/`write`/`edit` it stores the file path; other tools become tool-wide. Session rules vanish when you quit; persist them by hand-editing `permissions.json`.

Parallel approvals (the agent emitting two `bash` calls in one turn) stack — resolve the top one and the next surfaces.

### Permission Rules

Rules persist your approvals so you don't see the same prompt twice across runs. Two scopes:

- `<workdir>/.evva/permissions.json` — **project**: lives with the repo, share via git if you want
- `~/.evva/permissions.json` — **user**: applies in every working directory

Format:

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

**Rule grammar**: `ToolName` matches every call to that tool. `ToolName(content)` adds a content match:

| tool | content syntax | examples |
| --- | --- | --- |
| `bash` | `prefix:*`, `pattern *`, `git *`, or exact command | `bash(git:*)`, `bash(npm install *)`, `bash(make build)` |
| `read`, `write`, `edit`, `notebook_edit` | doublestar glob against the `file_path` | `read(src/**)`, `write(./tmp/*.txt)`, `edit(**/*.go)` |
| anything else | exact string match against the raw input | rare; prefer tool-wide rules |

**Precedence:**

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

---

## 7. Sub-agents and Personas

The root agent can spawn sub-agents. Two flavors are built in:

- **`explore`** — read-only inspection. Tools are limited to `read`, `grep`, `tree`, `glob`, `web_search`, `json_query`. The model uses this for "where is X defined / which files reference Y" lookups without risk of mutation.
- **`general-purpose`** — write-capable. Carries the fs + shell + web + util tool surface.

Active sub-agents appear as chips in a horizontal strip above the input. Async sub-agents finish in the background — their summaries land as a synthetic user message at the top of the next iteration, so the conversation picks them up automatically. The hierarchy is exactly two layers deep: sub-agents can't spawn sub-agents.

**User-authored sub-agents.** Drop an `agents/<name>/` directory under `~/.evva/` (same layout as `/profile` — see above) with `as: [subagent]` in `meta.yml`. The disk-loaded agent automatically appears in the Agent tool's `subagent_type` enum without restart-on-recompile.

**Cross-persona delegation.** A persona declared `as: [main, subagent]` is both selectable at `/profile` and callable from the running root agent. That's how the built-in `evva` ends up able to delegate a finance question to a user-authored `nono` persona — the root invokes the Agent tool with `subagent_type: "nono"`, the spawner builds a child agent under `nono`'s system prompt + tools, runs it once, and surfaces the summary back into evva's transcript.

You don't drive sub-agents yourself; the model decides when to spawn one.

---

## 8. Hooks

Hooks are user-authored shell commands or HTTP webhooks that fire at six well-defined points in the agent loop. Use them for: validation before tool calls, auto-formatting after edits, custom logging, blocking known-bad commands, or piping notifications to Slack / a desktop notifier on long-running approvals.

### Where Hooks Live

Two files, both optional, merged at startup:

- `<workdir>/.evva/settings.json` — **project** hooks. Lives with the repo; share via git if you want.
- `~/.evva/settings.json` — **user** hooks. Apply in every working directory.

Project hooks fire first; a project hook that returns `"continue": false` short-circuits the user hooks for that fire. Malformed entries become warnings on stderr at startup — the rest of the file still loads.

### File Shape

JSON layout (compatible with Claude Code's `settings.json` `hooks` block, so files written for either tool load in the other unchanged):

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "hooks": [
          { "type": "command", "command": "/path/to/check.sh", "timeout": 30 }
        ]
      }
    ],
    "PostToolUse": [
      {
        "matcher": "edit|write",
        "hooks": [
          { "type": "command", "command": "goimports -w \"$EVVA_TOOL_INPUT_PATH\"" }
        ]
      }
    ],
    "Notification": [
      {
        "hooks": [
          { "type": "http", "url": "https://hooks.slack.com/...", "method": "POST", "async": true }
        ]
      }
    ]
  }
}
```

**Matcher**: doublestar glob against the tool name. Empty matcher = match all. Supports alternation (`bash|grep`) and wildcards (`tool_*`). Events that don't carry a tool name (SessionStart, Stop, Notification) ignore the matcher.

**Hook entry fields**:

| field | applies to | meaning |
| --- | --- | --- |
| `type` | both | `"command"` (shell subprocess) or `"http"` (HTTP request) |
| `command` | command | shell command. Stdin is the JSON payload; stdout is the optional decision |
| `url` | http | endpoint to POST the payload to |
| `method` | http | HTTP method, default `POST` |
| `headers` | http | optional headers map |
| `timeout` | both | seconds (1–600). Default per-event |
| `async` | both | fire-and-forget. Default `false` for command, `true` for http |

Subprocess hooks receive `EVVA_PROJECT_DIR` in their environment.

### Events

| event | fires | typical use |
| --- | --- | --- |
| `SessionStart` | once at agent boot | warm caches, inject extra context into the first prompt |
| `UserPromptSubmit` | before each user prompt is appended to the session | prompt validation, secret redaction |
| `PreToolUse` | before the permission gate runs | block bad calls, mutate args, override the gate |
| `PostToolUse` | after a tool returns | auto-format, persist logs, append context for the next turn |
| `Stop` | when the main agent reaches a terminal turn (no more tool calls) | summary export, audit logging |
| `Notification` | iteration limit, internal errors, approval-needed | Slack ping, desktop notify on long-running approvals |

### Payload and Decision

Every hook receives a JSON payload (on stdin for commands, as the HTTP body for webhooks). Common envelope:

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "cwd": "/abs/working/dir",
  "permission_mode": "default",
  "agent_id": "uuid",
  "agent_type": "main",
  "hook_event_name": "PreToolUse"
}
```

Event-specific fields:

- `SessionStart`: `source` (`"startup"`), `model`
- `UserPromptSubmit`: `prompt`
- `PreToolUse`: `tool_name`, `tool_input` (raw JSON the model emitted), `tool_use_id`
- `PostToolUse`: `tool_name`, `tool_input`, `tool_use_id`, `tool_response`, `is_error`
- `Stop`: `last_assistant_message`, `stop_hook_active`
- `Notification`: `message`, `title`, `notification_type`

A command hook can write a JSON object to stdout to influence the loop:

```json
{
  "continue": false,
  "decision": "block",
  "reason": "lint failed: see stderr",
  "systemMessage": "ran golint, found 3 issues",
  "hookSpecificOutput": {
    "permissionDecision": "deny",
    "permissionDecisionReason": "vendor directory is read-only",
    "additionalContext": "the next turn should retry the edit elsewhere",
    "updatedInput": { "file_path": "/safer/path.go" }
  }
}
```

Effect by event:

- **PreToolUse**: `hookSpecificOutput.permissionDecision` (`"allow"` / `"deny"` / `"ask"`) overrides the gate. `updatedInput` mutates the tool's args before the gate runs. `decision: "block"` or `continue: false` blocks the call outright with the given `reason`.
- **PostToolUse**: `additionalContext` is appended to the tool result the LLM sees next turn. `block` / `continue` are ignored — post-tool hooks can't unsend a tool.
- **UserPromptSubmit**: `additionalContext` is appended to the user prompt. `block` / `continue: false` drops the prompt entirely.
- **Stop**: `block` / `continue: false` re-enters the loop once (the `stop_hook_active` flag prevents infinite re-entry).
- **SessionStart**: `additionalContext` and `hookSpecificOutput.initialUserMessage` are prepended to the first user prompt.
- **Notification**: stdout is ignored — purely a side-channel signal.

A hook with empty stdout (or non-JSON output) means "no opinion, pass through." Exit code 2 from a command hook is interpreted as a hard block with the message read from stderr.

Subprocesses exceeding `timeout` are killed; their decisions are discarded. HTTP hooks default to async fire-and-forget — failures are logged but never block the loop.

---

## 9. Configuration Reference

### evva-config.yml

Path: `~/.evva/config/evva-config.yml`. Created automatically on first launch. Edit live via `/config` in the TUI, or by hand:

```yaml
# Agent loop
max_iterations: 30
max_tokens: 4096
auto_compact_threshold: 0.8
display_thinking: true

# Default model used at startup (overwritten by /model swap)
default_provider: deepseek
default_model: deepseek-v4-pro

# Default thinking effort: low | medium | high | ultra. Overwritten by /effort.
default_effort: medium

# Default persona that boots — must match an agent name in the registry
# (built-in "evva" or a user-authored agent under ~/.evva/agents/<name>/).
# Overwritten by /profile. Empty falls back to "evva".
default_profile: evva

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

### .env (optional)

Place in your working directory or at `~/.evva/.env`. Only used for deployment / logging knobs — never user preferences:

```bash
APP_ENV=dev            # dev | prod
LOG_LEVEL=info         # debug | info | warn | error
LOG_FORMAT=text        # text | json
LOG_DIR=               # unset → $EVVA_HOME/logs (default); path → custom dir; explicit empty → stdout-only
SKILLS_DIR=skills      # subpath under ~/.evva/
USER_PROFILE=user_profile.md
```

### CLI Flags

```bash
evva                                # interactive TUI (when stdout is a TTY)
evva -temp 0.7                      # sampling temperature (default unset)
evva -max-tokens 2048               # per-completion output cap (overrides YAML)
evva -max-iters 40                  # loop iteration cap (overrides YAML)
evva -permission-mode=plan          # boot in plan mode (read-only)
evva -permission-mode=bypass        # boot with the gate disabled
evva -no-tui "explain loop.go"      # one-shot plain-text mode
echo "list files in /tmp" | evva -no-tui   # piped prompt
```

---

## 10. Modes — TUI vs CLI

**Interactive TUI** (default when stdout is a TTY). Transcript, panels, status bar, the works.

**Plain CLI** (`-no-tui`, or when stdout is piped). One-shot flow: read a prompt from args/stdin → run the agent → stream events as plain text → exit. CLI mode has no interactive approval surface — any call that would prompt is **denied automatically** with a hint to pass `-permission-mode=bypass` or add a rule to `permissions.json`. Useful for scripts and CI.

---

## 11. Logs

Per-agent text logs land under `$EVVA_HOME/logs/<agent-id>/<agent-id>.log` by default — no setup needed after `make install`. To redirect to a custom directory, set `LOG_DIR=/your/path` in `.env`. To revert to the old stdout-only dev mode (logs streamed to the terminal instead of disk), set `LOG_DIR=` explicitly to empty. `LOG_LEVEL=debug` exposes every iteration's `turn.start` / `llm.call` / `tool.dispatch` / `tool.result` lines — handy when debugging an agent that's stuck or looping.

---

## 12. Building on evva — the SDK (for developers)

Everything above describes evva as an app. evva is *also* an embeddable Go
SDK: another program can `import "github.com/johnny1110/evva/pkg/agent"`
and run its own ReAct agent — custom LLM providers, custom tools, its own
personas, permission policy, and UI — without forking and without touching
the agent loop.

The whole public surface lives under `pkg/*`. Go's `internal/` rule
enforces the boundary at compile time: a downstream module that reaches
into `evva/internal/...` won't build. As of `v1.0.0` the flagship
`cmd/evva` itself is built on `pkg/*` alone, so anything the bundled app
does, your app can do too.

```bash
go get github.com/johnny1110/evva@v1.0.0
```

### Quickstart — a full host in ~40 lines

One declarative `agent.Config` plus a couple of options gives you the
complete experience — the bundled terminal UI, persona `/profile`
switching, permission prompts, `/resume`, and `/compact`. `agent.New`
absorbs the bootstrap: it resolves the persona (falling back to `evva`),
auto-loads `EVVA.md` / `USER_PROFILE.md` memory and the skill catalog,
loads the permission store, and installs the approval/question brokers.

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "syscall"

    "github.com/johnny1110/evva/pkg/agent"
    "github.com/johnny1110/evva/pkg/config"
    _ "github.com/johnny1110/evva/pkg/llm/builtins" // register anthropic/deepseek/openai/ollama
    "github.com/johnny1110/evva/pkg/ui/bubbletea"
)

func main() {
    cfg := config.Get() // or config.Load(config.LoadOptions{AppName: "myapp", AppHome: ...})

    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    tui := bubbletea.New(cfg.AppHome) // the bundled reference TUI (satisfies ui.UI)

    ag, err := agent.New(agent.Config{AppConfig: cfg},
        agent.WithSink(tui),          // agent emits events into the UI
        agent.WithRootContext(ctx),   // Ctrl-C tears down every background worker
    )
    if err != nil {
        panic(err)
    }
    defer ag.Shutdown()

    tui.Attach(ag.Controller()) // hand the UI the controller view of the agent
    _ = tui.Run(ctx)
}
```

Headless? Drop the TUI: build with `agent.New(agent.Config{AppConfig: cfg,
PermissionMode: "bypass"})`, then call `ag.Run(ctx, "your prompt")`. With
no sink the agent auto-denies approvals (so a request never hangs);
`"bypass"` auto-allows for trusted/CI runs.

### Extension points at a glance

Every piece is swappable through a `pkg/*` seam:

| Want to… | Use |
| --- | --- |
| Add an LLM provider | Register a factory on `llm.DefaultRegistry()`; your `llm.Client` satisfies `Name` / `Model` / `SupportsDeferLoading` / `Complete` / `Stream` / `Apply`. |
| Add a tool | Implement `tools.Tool`; pass `agent.WithCustomTool(name, factory)` or register on `toolset.DefaultRegistry()`. |
| Add a persona | `agent.BuildAgentRegistry` + `reg.Register(agent.AgentDefinition{...})` (or drop files under `<AppHome>/agents/<name>/`); pass `Config.Personas` + `Config.Persona`. Drives `/profile` and subagents. |
| Control approvals | `Config.PermissionMode`, `Config.PermissionStore`, or a custom `agent.WithPermissionBroker` (build with `permission.NewBroker` + `SetOnRequest`). |
| Build a custom UI | Implement `ui.UI`; drive the agent through the fully-public `ui.Controller`. Or embed `pkg/ui/bubbletea`. |
| Ship skills | `skill.NewRegistry()` + `Add(...)` (programmatic) or drop `SKILL.md` files; pass `agent.WithSkillRegistry`. |
| Add lifecycle hooks | Add a `hooks` block to `.evva/settings.json`; hooks fire at SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop, and Notification events. See [Lifecycle Hooks](#lifecycle-hooks). |
| Use a custom home dir | `config.Load(config.LoadOptions{AppName, AppHome, ...})` → `Config.AppConfig`. |

### Stability & where to go deeper

`v1.0.0` puts the **Stable** tier under the major-version promise:
`pkg/agent`, `pkg/config`, `pkg/event`, `pkg/llm`, `pkg/tools`,
`pkg/toolset`, `pkg/permission`, `pkg/ui`, `pkg/skill`, `pkg/constant`.
Experimental packages (`pkg/ui/bubbletea`, `pkg/tools/lsp`,
`pkg/observable`, `pkg/tools/kits`) may still change in minor versions.

- [`integration.md`](integration.md) — step-by-step integration walkthrough.

### Lifecycle Hooks

Hooks are user-authored shell commands or HTTP webhooks that fire at six
points in the agent loop. Configure them in `.evva/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "hooks": [
          {
            "type": "command",
            "command": "jq '.tool_input' | grep -q dangerous && exit 2 || exit 0",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

**Events:**
- `SessionStart` — fires once when the agent first runs
- `UserPromptSubmit` — fires before each user prompt is appended
- `PreToolUse` — fires before every tool execution; can block, mutate input, or override permission
- `PostToolUse` — fires after tool execution; can append context to the result
- `Stop` — fires when the agent reaches a terminal turn; can re-enter the loop once
- `Notification` — fires on out-of-band events (iteration limit, etc.)

**Hook types:**
- `type: "command"` — shell command, JSON payload on stdin. Exit 0 → parse stdout as decision; exit 1 → non-blocking error (logged); exit 2 → block.
- `type: "http"` — HTTP POST. Async by default.

**Decision JSON (exit 0 stdout):**
```json
{
  "continue": true,
  "decision": "approve",
  "hookSpecificOutput": {
    "permissionDecision": "allow",
    "updatedInput": { "command": "echo safe" },
    "additionalContext": "extra info for the LLM"
  }
}
```

Project hooks (`.evva/settings.json`) fire before user hooks
(`<APP_HOME>/settings.json`). A malformed settings file produces
startup warnings; the agent still boots.
- [`docs/extending.md`](../../extending.md) — the full reference: every public package, every extension point, and what you can't override.
- [`docs/sdk-stability.md`](../../sdk-stability.md) — the per-package stability tiers and how to depend on evva.
- [`examples/full-host/`](../../../examples/full-host/main.go) — runnable full host (separate module, TUI + personas + permissions).
- [`examples/minimal-host/`](../../../examples/minimal-host/main.go) — runnable tiny host (custom provider + tool + skill).
