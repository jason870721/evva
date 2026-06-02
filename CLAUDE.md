# evva — Project Vision and Roadmap

---

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

## Important

`v1.0.0` is cut: the SDK v2 arc is complete and the Stable-tier surface
promise in `docs/sdk-stability.md` is in force — breaking changes to Stable
`pkg/*` packages now require a major bump. Experimental-tier packages
(`pkg/ui/bubbletea`, `pkg/tools/lsp`, `pkg/observable`, `pkg/tools/kits`)
may still change in minor versions.

---

## Core direction (post-v1.0.0): Veronica — the swarm subsystem

**As of 2026-06-02, evva's single core development goal is Veronica** — an
in-repo subsystem that grows evva from a single-agent runtime into a
multi-agent **swarm workstation**. `evva service start` runs a background
`:8888` web service (vue.js); `evva swarm .` registers a cluster of
long-lived agents that collaborate through a message bus + a shared SQLite
ledger, coordinated by a Leader agent. **All other roadmap items (the v1.x
feature phases below) are paused** until Veronica's first two phases land.
This is a long-term, carefully-planned arc — **quality over speed**, not a
sprint.

Two phases:

1. **Phase 1 — the swarm itself.** The infrastructure: supervisor /
   scheduler / roster, message bus + mailboxes, the `.vero/` SQLite task
   ledger + 5-state machine, the `:8888` service + vue.js UI. Built on the
   public `pkg/*` surface only, so it doubles as evva's **multi-agent
   completeness oracle** (if evva's own swarm can be built on `pkg/*`
   alone, a third party's can too).
2. **Phase 2 — the trader-team validation.** A crypto trading-strategy
   swarm (friday / trader / analyst / risk-monitor / reviewer) that proves
   the swarm is practical on a real, continuous, multi-role workload.

The framing inverts: **evva exists to serve Veronica.** The swarm consumes
only public `pkg/*`; the single runtime change it needs — a loop-level
*inbox-drainer* seam so a busy agent folds incoming messages mid-run —
lands as a public, additive `pkg/agent` extension (it generalizes the
existing `KindDrainBackgroundTask` mechanism). The "one runtime, many
personas" Vision still holds — each swarm member *is* a persona — Veronica
is its extension into the multi-agent dimension, and where new work goes
first.

**Authoritative docs (read in this order):**

- Design / architecture: `docs/veronica/veronica-design-v1.md`
- Roadmap (both phases, milestone gates): `docs/veronica/roadmap.md`
- Phase 1 PRD (swarm): `docs/veronica/prd-phase1-swarm.md`
- Phase 2 PRD (trader-team): `docs/veronica/prd-phase2-trader-team.md`

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

## Roadmap (post-v1.0.0)

> **⏸ PAUSED (2026-06-02)** — superseded as the *active* priority by the
> Veronica swarm subsystem (see "Core direction" above). The v1.x feature
> phases below stay as the record of intent and resume after Veronica's
> Phase 1–2 land. Some may already have shipped since this list was
> written — verify against `docs/extending.md` and the `pkg/` tree before
> resuming any of them.

`v1.0.0` shipped a complete agent harness and a Stable SDK surface. The
post-v1 roadmap is ordered by one principle, not by dependency:
**finish before expand, integrity before power.** Earlier phases matter
more — a half-wired feature the system prompt *already advertises* is a
worse liability than any missing net-new capability, so finishing those
comes first. Every phase below is additive to the Stable surface, so each
lands as a **minor** release (`v1.1`, `v1.2`, …) under the semver promise
now in force.

### State of v1.0.0 — the evidence base for the order

**Solid / Stable** — agent loop + profiles + subagent spawn; `fs`,
`shell`, `web`, `notebook`, `util` tools; `todo`, `cron`, `daemon`
(background tasks) + `monitor`; plan mode + git `worktree`;
`ask_user_question`; memory (auto-load `EVVA.md` / `USER_PROFILE.md` +
`update_*` tools); pluggable `pkg/permission`; session store + snapshot +
`/compact` + `/resume`; the skill framework (`pkg/skill`); the full SDK v2
surface (one-call `agent.New`, separate-module host proof).

**Experimental** — `pkg/tools/lsp` (~9k LOC + 8 test files — the most
mature), `pkg/ui/bubbletea`, `pkg/observable`, `pkg/tools/kits`.

**Half-built / dangling — these set the priority order below:**

- **Hooks** (`internal/hooks`, ~1185 LOC, 9 files): a complete six-event
  lifecycle engine — SessionStart, UserPromptSubmit, PreToolUse,
  PostToolUse, Stop, Notification; shell + HTTP backends; designed to
  compose with permissions — that **nothing imports**, so it never fires.
  Yet `sysprompt/fragments.go` already tells the model hooks work. 0 tests,
  private. The worst kind of debt: an advertised promise the runtime
  silently breaks.
- **OpenAI provider**: `pkg/constant/llm.go` declares the `OPENAI`
  provider and a model, but there is **no `pkg/llm/openai`** and
  `pkg/llm/builtins` never registers it — selecting OpenAI fails at
  factory lookup. The Vision lists OpenAI as a first-class provider.
- **MCP**: absent entirely. The tool-search layer is already MCP-aware
  (`meta/fuzzy.go` + `toolsearch.go` parse `mcp__server__tool` names), but
  there is no client, config, discovery, or the four MCP tools.
- **Bundled skills**: only `/commit` ships; `/review`, `/security-review`,
  `/simplify` (named in the old Phase 3) do not — the framework is done,
  only the content is missing.

### v1.1 — Finish the hooks system  *(integrity: deliver an advertised feature)*

The system prompt promises hooks; the engine exists; the only thing
missing is the wiring. Highest priority because every session ships a
prompt that lies to the model today.

- Dispatch from the agent loop: **PreToolUse** *before* the permission
  gate (may return allow/deny/ask to override the gate, or `updatedInput`
  to mutate args first); **PostToolUse** after a tool result (append
  `additionalContext` for the next turn); **SessionStart**,
  **UserPromptSubmit**, **Stop**, **Notification** at their points.
- Load hook config from settings via `pkg/config` (the `hooks:` block:
  matcher → command/http entries).
- Compose with `pkg/permission` (PreToolUse decision precedes the gate).
- Promote `internal/hooks` → **`pkg/hooks`** — it composes with the now-
  public permission store, so downstream hosts need it public.
- Tests: the package has **0** today — add matcher / dispatcher-precedence
  / subprocess / http unit tests plus a loop integration test.

**Acceptance:** a configured PreToolUse `command` hook blocks a `bash`
call before the permission gate; a PostToolUse hook injects context the
model sees next turn; the prompt's hooks promise is finally true; tests green.

### v1.2 — OpenAI provider  *(integrity: complete the Vision's provider matrix)*

Small, cheap, and it removes a crash path. The constant already promises
OpenAI; this makes the promise real.

- New `pkg/llm/openai`: `ProviderName`, `Factory`, and a `Client`
  implementing all six `llm.Client` methods incl. `SupportsDeferLoading()`
  (OpenAI lacks Anthropic's `defer_loading` → return `false`, keeping the
  tools array stable for caching). `pkg/llm/deepseek` is the closest
  template (OpenAI-compatible chat/tools/streaming).
- Register in `pkg/llm/builtins`; reconcile the placeholder model ids in
  `pkg/constant/llm.go` with real ones.

**Acceptance:** `evva` runs a full ReAct turn against OpenAI; provider
parity tests pass; no constant promises an unimplemented provider.

### v1.3 — MCP client support  *(power: the headline net-new capability)*

The last major Claude Code parity gap and the biggest single lever on
"powerful." Framework only — bundled vendor servers stay out (see below).

- MCP server config in `pkg/config` (`mcpServers: {name: …}`), stdio +
  SSE/HTTP transports.
- A client that connects, runs `initialize`, and lists tools + resources.
- **Dynamic registration**: discovered tools register as **deferred**
  tools under the `mcp__server__tool` naming the search layer already
  scores, so `tool_search` surfaces them on demand and prompt caching is
  preserved.
- Port the four tools from `ref/src/tools/`: `MCPTool` (invoke),
  `McpAuthTool` (OAuth/token), `ListMcpResourcesTool`, `ReadMcpResourceTool`.

**Acceptance:** configure a real MCP server (e.g. a filesystem server);
its tools appear via `tool_search` and execute; list/read resources work.

### v1.4 — Bundled skills  *(cheap daily value; framework already exists)*

- Port `/review`, `/security-review`, `/simplify` (`/commit` already
  ships) as on-disk `SKILL.md` under the bundled-skills dir, drawing from
  `ref/src/skills/bundled/` and Claude Code's review skills. No framework
  changes — `pkg/skill` already loads them.

**Acceptance:** all four bundled skills are invocable in the TUI.

### v1.5 — ConfigTool  *(power: give the model a typed handle on its own settings)*

Today the model can only change evva's settings by asking the user to
open `/config` or hand-edit `evva-config.yml`. ConfigTool is the
model-facing analogue of that overlay: one tool, `{setting, value?}`,
that reads when `value` is omitted and writes when it is set. The
permission posture mirrors `ref/src/tools/ConfigTool/`: auto-allow on
read, ask on write.

- New `internal/tools/config/`: a `SUPPORTED_SETTINGS` registry that wraps
  every typed `Set*` accessor on `*pkg/config.Config` (`SetMaxIterations`,
  `SetDisplayThinking`, `SetEnableAutoMemory`, `SetFetchMaxBytes`,
  `SetTavilyAPIKey`, `SetProviderAPIKey/URL`, `SetDefaultEffort`, etc.)
  plus per-setting metadata (`type`, `description`, `options`, optional
  `validate`) cribbed from `ref/src/tools/ConfigTool/supportedSettings.ts`.
- New `tools.CONFIG` constant in `pkg/tools/name.go`; factory in
  `internal/toolset/builtins.go`; added to the Main profile's
  `ActiveTools` (concurrency-safe; read is `isReadOnly`).
- Permission gate: read (`value` omitted) → auto-allow; write → `ask`
  with a "Set `<key>` to `<value>`" message.
- Prompt generated dynamically from the registry (the "Configurable
  settings list" block in `ref/src/tools/ConfigTool/prompt.ts`) so the
  source of truth is one Go map, not duplicated documentation.

**Acceptance:** the model can ask for and change every setting the
`/config` overlay exposes; reads land without a prompt; writes go
through the permission broker; unknown settings return a clean error;
options-validated settings reject out-of-range values.

### v1.6 — (open slot)

Reserved for the next phase the team prioritises. Candidates: harden
Experimental→Stable per-package review (the deferred v1.0-era item);
a `/dream` / background-consolidation memory phase; provider rate-limit
& retry middleware; whatever surfaces from v1.1–v1.5 usage.

### v1.7 — BriefTool  *(integrity: a dedicated, visible reply channel)*

evva today emits assistant text as plain `Content` on each turn. The TUI
renders it, but the model has no way to **mark** a turn as "the answer
the user should read" vs. "interstitial work I'm narrating". Port
`ref/src/tools/BriefTool/` as evva's `send_user_message` tool so the
model has one explicit channel for messages the user must see — with
a `status` flag (`normal` | `proactive`) downstream code can route on,
and an `attachments` list for inline file references.

- New `pkg/tools/brief/` (Stable-candidate; downstream agents will want
  this surface): `BriefTool` with the input shape
  `{message, status, attachments?}` ported from
  `ref/src/tools/BriefTool/BriefTool.ts`.
- New `tools.SEND_USER_MESSAGE` constant in `pkg/tools/name.go`;
  factory in `internal/toolset/builtins.go`; tool is **read-only,
  concurrency-safe**, and enabled by default on the Main profile.
- The tool emits a new `event.KindUserMessage` (or repurposes an
  existing assistant-text event) so the TUI can render Brief messages
  with their `status` and attachments visible, distinct from plain
  narration.
- System-prompt fragment lifted from
  `ref/src/tools/BriefTool/prompt.ts:BRIEF_PROACTIVE_SECTION` (the
  "Talking to the user" guidance) so the model learns when to use the
  channel.
- Attachment resolution (file path → metadata blob) ports from
  `ref/src/tools/BriefTool/attachments.ts`, scoped to the local
  filesystem (no Claude.ai upload — out of scope; see below).

**Acceptance:** the model uses `send_user_message` for every reply the
user is expected to read; `status:"proactive"` messages are visibly
distinct in the TUI; attachments resolve to relative paths the user
can click; plain assistant text outside the tool still renders but is
deprioritised in the UI.

---

## Out of scope (revisit after v1.x)

Listed so contributors don't propose them as phase additions.

- **Cross-machine Teams / SendMessage bridge** — *in-process* multi-agent
  swarms are now in scope via Veronica (one process, in-memory bus; see
  "Core direction"). What stays out of scope is the *cross-machine* bridge
  layer (UDS sockets, remote control, JWT, cross-machine session
  forwarding); Veronica v1 deliberately stays single-process to avoid it
  (the process-model "C" evolution in the design doc revisits this).
- **Bundled vendor MCP integrations** (Atlassian, Figma, IDE diagnostics)
  — v1.3 ships the MCP *framework*; specific servers are user-configured,
  not bundled, until there's demand.
- **Cross-platform shell** (Windows PowerShell, `ref/src/tools/PowerShellTool`)
  — evva is bash-first; revisit if Windows demand appears.
- **Minor ref tools** — `REPLTool` only (Python/JS scratch REPL): no
  current demand; port if a use case shows up. (`ConfigTool` and
  `BriefTool` were promoted to v1.5 and v1.7 respectively.)

---

## Release workflow

### Branch strategy

```
main  ← production (beta = latest; no stable release yet)
  ↑ Sat fast-forward merge
pre-release  ← staging (weekly feature accumulation, alpha tag)
  ↑ Sat merge
dev  ← integration
  ↑ feature PR, squash/merge after review
feature/*  ← topic branches (cut from dev)
```

### Daily development

1. Branch off `dev`: `git checkout -b feature/<ticket-or-name>`.
2. Commit with conventional prefixes (`feat`, `fix`, `chore`, `docs`, `refactor`, `test`).
3. Push, open a PR targeting `dev`, wait for merge review.

### Weekly release (Saturday morning)

Currently early-stage — all releases are beta (latest), alpha tags are pre-release only.

**Beta (pre-release → main):**

```bash
git checkout main && git merge pre-release --ff-only
```
Before tagging, verify `pkg/version/version.go` has the correct beta version and `CHANGELOG.md` is updated with the matching version.
```bash
git tag -a v<X>.<Y>.<Z>-beta.<N> -m "..."
git push origin v<X>.<Y>.<Z>-beta.<N>
gh release create v<X>.<Y>.<Z>-beta.<N> --target main --title "..."
```

**Alpha (dev → pre-release):**

```bash
git checkout pre-release && git merge dev
```
Before tagging, verify `pkg/version/version.go` has the correct alpha version. Alpha releases do not get a separate CHANGELOG entry, but the version should reflect the scope accumulated on dev.
```bash
git tag -a v<X>.<Y>.<Z>-alpha.<N> -m "..."
git push origin v<X>.<Y>.<Z>-alpha.<N>
gh release create v<X>.<Y>.<Z>-alpha.<N> --target pre-release --prerelease --title "..."
```

### Version numbering

`vX.Y.Z`: X = major (new direction), Y = minor (features), Z = patch (bug fixes + small adjustments).

Pre-release suffix: `-beta.<N>` on main, `-alpha.<N>` on pre-release. N starts at 1 per base version.

### CHANGELOG

Only beta releases get a changelog entry. Bump `## [Unreleased]` → `## [vX.Y.Z-beta.N]`, add a new `[Unreleased]` section, summarize under `### Added / Fixed / Changed / Breaking`, update comparison URLs.

### Key rules

- `pkg/version/version.go` stores the current version constant; bump in a separate commit before tagging.
- Always ask before pushing tags or releases.
- `gh release create` targets `main` for beta, `pre-release` for alpha.

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