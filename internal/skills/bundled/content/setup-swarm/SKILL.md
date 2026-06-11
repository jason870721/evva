# setup-swarm Guide a user through creating an evva swarm from scratch

Use this skill when the user wants to CREATE or SCAFFOLD a multi-agent swarm team: "set up a swarm", "create a swarm team", "scaffold a swarm", "add swarm agents", "how do I make a swarm", "I need a team of agents", "setup-swarm". Also use it when the user asks for help UNDERSTANDING swarm structure — the directory layout, the manifest, the agent definition files — or CONFIGURING a swarm's guardrails: permission modes, token budgets, watchdogs, retention, shared skills, external webhooks. Do NOT use for runtime operations (`evva swarm run/stop/rm/reset`) on an already-registered space.

## Before you start

Ask the user two questions (use `ask_user_question` if the answer isn't clear):

1. **What should this team DO?** The answer determines the leader's role description, the worker roles, and how many workers. Examples: "a software engineering team that takes a GitHub issue and ships a PR" → lead + pm + designer + backend + frontend + qa. "a simple build-and-review pipeline" → lead + builder + reviewer. The user's answer IS the spec — don't invent members they didn't ask for.

2. **Where should the swarm live?** A swarm is a directory: `<workdir>/evva-swarm.yml` + `<workdir>/agents/{main,sub}/<name>/...`. It must be a git-tracked folder (or at least a persistent one — the `.vero/` ledger is stored there). If the user doesn't have a folder yet, create one with `mkdir -p`.

## Step 1 — Create the evva-swarm.yml manifest

This is the top-level file that declares the team. Write it at the root of the workdir:

```yaml
name: <team-name>            # e.g. my-eng-team (optional — the service assigns one if omitted)
workdir: .                   # always "." — it means "this directory"

leader:
  agent: <leader-agent-name> # e.g. lead — must match the folder under agents/main/

workers:
  - agent: <worker-1-name>   # e.g. builder — must match the folder under agents/sub/
  - agent: <worker-2-name>   # e.g. reviewer
    permission_mode: bypass  # optional per-member override (see below)
    budget_tokens: 500000    # optional per-member daily token cap (see below)

settings:
  permission_mode: default    # default | accept_edits | plan | bypass (see below)
  max_iterations: 40          # per-run iteration cap before the agent pauses
```

**Permission modes** — `settings.permission_mode` is the space-wide stance; a member-level `permission_mode:` overrides it for that member:

- `bypass` — runs hands-off; file writes and shell execute with no approval. Use in trusted/throwaway folders or CI-like pipelines.
- `default` — every file write and shell command pops an approval in the web UI (click Allow, or "Always allow" to allow a tool for the session).
- `accept_edits` — file writes auto-allow; shell commands still ask.
- `plan` — read-only analysis; rarely what you want for a swarm member.

The common pattern is `default` for the team plus `bypass` for one trusted high-frequency worker (a watchdog, a trader). For fine-grained rules, add a Claude-Code-compatible `permissions.json` next to a member's definition (`agents/{main,sub}/<name>/permissions.json`): allow rules open holes in `default` mode, and **deny rules bind in EVERY mode — bypass included**, so "bypass + a deny fence" is the supported autonomous-but-fenced pattern. Ask rules deliberately do NOT fire in bypass (an unattended member must never stall on a question).

**Token budgets (cost control):** `settings.daily_budget_tokens` caps each member's daily spend — input+output tokens per local day; 0 or omitted = unlimited. A member that crosses its cap is frozen until the day rolls over (`budget_stay_frozen: true` keeps it frozen until the operator unfreezes manually). The member-level `budget_tokens:` overrides the space default: `>0` = own cap, `-1` = exempt (unlimited even when the space sets a default).

**Schedules (optional):** Add a `schedule:` block to any leader or worker entry to make it wake on a timer:

```yaml
workers:
  - agent: qa
    schedule:
      cron: "*/30 * * * *"   # every 30 minutes
      prompt: "scan the latest build for regressions"
  - agent: monitor
    schedule:
      every: "60s"            # cron alternative: Go duration string
      prompt: "check system health"
```

Schedule `cron` and `every` are mutually exclusive; `prompt` is the synthetic user message injected on each wake. Schedules changed later at runtime (the leader's `schedule_set` tool or the web schedule editor) are durable — they survive a service restart; re-registering with `evva swarm .` resets every runtime schedule back to this manifest's baseline.

**Operational guardrails** — all optional `settings:` knobs with sane defaults; `"0"` disables a duration/day knob:

```yaml
settings:
  stall_threshold: 10m          # one RUN busy this long → stall alert       (default 10m)
  stall_hard_timeout: 1h        # one run busy this long → auto-cancel       (default off)
  task_stale_threshold: 24h     # a TASK parked in running/verifying → alert (default 24h)
  mailbox_stale_threshold: 30m  # unread mail aging this long → alert        (default 30m)
  retention_days: 30            # archive+delete old read mail / done tasks  (default 30)
  event_log: true               # mirror events to .vero/events/*.jsonl      (default true)
  webhook_secret: <secret>      # require X-Evva-Webhook-Secret on event POSTs
```

The stall knob watches a single hung run; the two stale thresholds are the workflow watchdog — work nobody is moving (a forgotten board card, a frozen member's mailbox backlog). Each alert mails the leader and the operator once per episode with a suggested action.

## Step 2 — Create the agent definition directories

Every member (leader AND workers) needs its own directory with this exact structure:

```
agents/
├── main/
│   └── <leader-name>/          # e.g. lead
│       ├── profile.yml         # model, effort, when_to_use, inject_memory, schedule
│       ├── system_prompt.md    # the agent's identity and instructions
│       ├── tools/
│       │   ├── active.yml      # tools eagerly exposed to the LLM
│       │   └── deferr.yml      # deferred tools (advertised by name; fetched via tool_search)
│       └── skills/             # optional per-member skills (see Step 3)
└── sub/
    ├── <worker-1-name>/        # same structure as leader
    │   ├── profile.yml
    │   ├── system_prompt.md
    │   └── tools/
    │       ├── active.yml
    │       └── deferr.yml
    └── ...
```

**`profile.yml`** — optional per-member overrides. All fields are optional; absent = inherited from the service defaults:

```yaml
model: claude-sonnet-4-6       # override the LLM model for this member
effort: high                    # low | medium | high | ultra (thinking effort)
when_to_use: "Backend implementation: APIs, DB schema, migrations."
                                # shown to the leader so it knows when to delegate
inject_memory: true             # inject EVVA.md / USER_PROFILE.md into the prompt
advertise_skills: true          # list this member's skills in the prompt
```

**`system_prompt.md`** — the agent's persona. First line must be a markdown heading (`# Name`). Keep it to identity + domain judgment ONLY. The runtime auto-injects everything operational: the collaboration protocol (communication tools, task semantics, member list), a tool-mechanics guide grounded in the member's actual active/deferred tool list, long-term-memory discipline, and untrusted-content framing for web results. Do NOT hand-write tool how-tos, memory rules, or prompt-injection warnings into a persona — they would duplicate, and drift from, what the runtime injects:

```markdown
# Builder

You implement code changes: read the task spec, write the implementation,
run the tests, and report back when done. Favor simple, working solutions.
```

**`tools/active.yml`** — tools exposed to the LLM in every turn. The swarm auto-injects its own collaboration tools (`task_create`/`task_assign`/`task_verify` for the leader, `my_tasks`/`task_get` for workers, plus `send_message`/`list_members` for all, plus `skill`). Only list DOMAIN tools here:

```yaml
# Leader's and workers' active.yml alike
- read
- write
- edit
- glob
- grep
- bash
```

**`tools/deferr.yml`** — tools the LLM knows about by name but must fetch with `tool_search` before using. Use for rarely-needed tools to keep the active set small. A member with any deferred tools gets `tool_search` wired automatically:

```yaml
- web_search
- web_fetch
- repl
```

**Member memory (automatic):** at first boot the runtime creates a `memory/` directory (with a `MEMORY.md` index) inside each member's folder — its private long-term memory. Don't scaffold or pre-fill it: members tend their own notes on wake, and a built-in deny fence stops one member writing a sibling's memory even in bypass mode.

## Step 3 — Add skills: per-member and space-shared

**Per-member skills** live under the member's directory at `skills/<name>/SKILL.md`. The swarm web UI can also add/remove them at runtime (the operator clicks "skills" on a member in the roster). The first line is always `# <name> <description>`:

```markdown
# standup Run a daily standup and summarize blockers.

Ask each active member for status, collect blockers, and post a short summary to
the user.
```

The skill tool is auto-injected; `advertise_skills: true` in `profile.yml` lists the skill in the prompt.

**Space-shared skills** live at the workdir root in `agents/skills/<name>/SKILL.md` — one copy that EVERY member loads. On a name clash the member's private skill shadows the shared one. Seed it with team-wide know-how (runbooks, conventions); at runtime the leader can institutionalize a recurring pattern with its `skill_publish` tool, and the operator can add or delete shared skills from the web UI — either path hot-reloads every member.

## Step 4 — Start the service and register the swarm

These are shell commands the user runs in their terminal — NOT something you execute (they may need to start the service in their own session):

1. **Start the service:** `evva service start` — a background daemon on `127.0.0.1:8888`. Each start mints a fresh session token and prints where it lives (`evva service status` shows it again). A browser on the same machine signs in automatically (loopback bootstrap); only remote browsers and scripts need the token from that file. Never expose the port beyond loopback without `--allow-remote` — and then every endpoint requires the token.

2. **Register the swarm:** `cd <workdir> && evva swarm .` — reads `evva-swarm.yml`, validates it, and POSTs the workdir to the service, which builds all agents and starts the team. Re-run it after manifest edits: re-registration applies the new manifest and resets runtime schedules to its baseline.

3. **Open the web UI:** the register output prints a URL like `http://127.0.0.1:8888/?space=<id>` — open it in a browser.

Other swarm commands:
- `evva swarm ls` — list all registered spaces
- `evva swarm run <ref>` — restart a stopped space
- `evva swarm stop <ref>` — stop (freeze all agents)
- `evva swarm rm <ref>` — forget a space
- `evva swarm reset <ref>` — wipe ledger + clear context, same id
- `evva swarm add <ref> <member>` — hot-load a new member from `agents/sub/<member>/`
- `evva swarm send <ref> <member|leader> <text|->` — message a member as the operator (`-` reads stdin); fire-and-forget, prints the message id
- `evva swarm vacuum <ref> [--days N] [--dry-run]` — run a ledger retention pass now (archives to `.vero/archive/`)

## Step 5 — Verify the team works

After the user opens the web UI:

1. **Pick a member** in the left roster → its console opens in the center pane.
2. **Send a message** to the leader with the first task (e.g. "build a hello-world CLI").
3. **Watch the board** — tasks move through pending → running → verifying → completed.
4. **Approve tool calls** if using `default` permission mode — the approval overlay pops when an agent wants to write files or run shell.
5. **Check the timeline** tab for the message flow between members.
6. **Scriptable smoke test** — from the terminal: `evva swarm send <ref> leader "status report, please"`. An idle member wakes; a busy one folds the message into its current run; either way it appears in the web transcript as a user message. This is the fast persona-iteration loop: send → watch the member's console → tune the persona → send again.

## Day-2 operations — what to show the user once it runs

- **Proposals (bottom-up work):** workers raise trackable work with `task_propose`; the leader accepts (`proposal_accept` — atomically creates the matching task) or declines with a note. The web UI's Proposals tab is the operator's read-only window onto that queue.
- **Cost:** the roster shows each member's context meter and daily token spend vs budget; the space metrics view adds per-member run-token histograms, and every `run_end` line in `.vero/events/*.jsonl` carries that run's exact usage — the data for "is my watchdog's per-run cost creeping up?".
- **Watchdog alerts:** a stall notice means one run is stuck; a stale-task reminder means the board isn't moving; a mailbox-backlog alert means a member isn't draining mail (often frozen or suspended). Each names a suggested action — tune the thresholds in `settings:` if they page too often.
- **Memory:** each member's `memory/` notes are visible read-only in the web UI (open a member → Memory) — the transparency window onto what the team has learned. Members curate their own files; the operator edits on disk if something must go.
- **External events (webhook):** other systems can wake the team by POSTing JSON `{"body": "..."}` to `/api/swarm/<ref>/event`. Set `settings.webhook_secret` and send it as the `X-Evva-Webhook-Secret` header; without a secret only same-machine callers are accepted. Optional fields: `to` (default: the leader), `source`, `title`, `idempotency_key` (collapses retries).

## Reference: complete minimal example

A working 3-agent swarm (leader + builder + reviewer) in a folder called `my-swarm/`:

```
my-swarm/
├── evva-swarm.yml
└── agents/
    ├── skills/                        # space-shared skills (optional)
    │   └── house-style/
    │       └── SKILL.md
    ├── main/
    │   └── lead/
    │       ├── profile.yml
    │       ├── system_prompt.md
    │       └── tools/
    │           ├── active.yml
    │           └── deferr.yml
    └── sub/
        ├── builder/
        │   ├── profile.yml
        │   ├── system_prompt.md
        │   └── tools/
        │       ├── active.yml
        │       └── deferr.yml
        └── reviewer/
            ├── system_prompt.md          # profile.yml is optional
            └── tools/
                ├── active.yml
                └── deferr.yml
```

Content of each file is as shown in Steps 1-3 above. The leader's `system_prompt.md` should include: "You orchestrate the team via task_create, task_assign, and task_verify. Break the user's goal into concrete tasks and assign each to the right worker. Verify results before reporting back."

## Guardrails

- Agent names must be unique within a swarm — no two members can share the same name.
- The `agent:` field in `evva-swarm.yml` must match the folder name under `agents/main/` or `agents/sub/`.
- All member folders use the SAME structure regardless of role (leader vs worker). The leader/worker distinction is in the manifest, not the folder layout.
- The swarm runtime auto-injects: the collaboration system prompt and team protocol, per-role tools (`task_create`/`task_assign`/`task_update_status`/`task_verify`/`task_list`, schedule and proposal handling, and `skill_publish` for the leader; `my_tasks`/`task_get`/`task_propose` for workers; `send_message`/`list_members` and one-shot alarms for all), the `skill` tool, tool-mechanics grounding, memory discipline, and `advertise_skills`. Never hand-write any of that into a persona or list collaboration tools in `active.yml`.
- Deny rules from a member's `permissions.json` bind in every permission mode — bypass included. Prefer "bypass + deny fence" over hand-rolled caution prompts when a member must run unattended.
- NEVER touch the `.vero/` directory — it's the swarm's internal SQLite ledger (plus `events/` forensics and `archive/` retention exports). Deleting it resets the space.
- The service binds `127.0.0.1:8888` by default — never expose it to a network without `--allow-remote`, which makes every endpoint require the session token.
- If `evva swarm .` says "service unreachable", the user needs to run `evva service start` first.
