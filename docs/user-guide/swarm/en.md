# evva swarm & evva service — User Guide (0 → Hero)

> Languages: **English** ｜ [正體中文](zh-tw.md)
> Audience: anyone who wants to run a team of evva agents that collaborate.
> Scope: how the swarm works, and a complete walkthrough building one from scratch.

---

## 1. What is this?

evva is a terminal coding agent. **Veronica** is its *swarm* layer: it turns the
single-agent runtime into a **multi-agent workstation** where a group of
long-lived agents collaborate on one goal.

Two commands:

- **`evva service`** — a background web service (default `127.0.0.1:8888`). It is
  the **host**: it runs the agents, persists their state, and serves a web UI.
  One service can host **many independent swarms at once**.
- **`evva swarm`** — the control plane. `evva swarm .` registers the swarm
  declared in the current directory into the running service.

> The plain `evva` TUI is unchanged — the swarm is purely additive.

### The mental model

```
 evva service  (one process, :8888, web UI + session token)
 │
 ├── SwarmSpace "A"   ← `evva swarm .` in /path/to/A
 │     ├── leader        (writes the task ledger, assigns + verifies)
 │     ├── worker-1      (does work, reports back)
 │     └── worker-2
 │     ├── .vero/vero.db   (task ledger + messages, SQLite)
 │     └── message bus + roster  (per-space, isolated)
 │
 └── SwarmSpace "B"   ← `evva swarm .` in /path/to/B   (fully isolated from A)
```

- A **space** (a.k.a. sub-cluster) is one swarm: its own agents, its own
  database, its own message bus. Two spaces share **nothing** — they can even use
  the same member names without colliding.
- Every member is a full evva agent (its own model, prompt, tools, personality).
- Members collaborate two ways:
  1. a **task ledger** — a shared, persistent to-do list with a 5-state machine
     (`pending → running → verifying → completed`, plus `suspended`). **Only the
     leader writes task status**; workers read it.
  2. **messages** — agents send each other mail (`send_message`); an idle
     recipient wakes up to handle it, a busy one folds it into its current work.
- It survives restarts: kill the service, start it again, and every space is
  rebuilt — unread mail re-queued, transcripts resumed, the ledger intact.

---

## 2. Roles: leader vs workers

| | Leader (`agents/main/…`) | Worker (`agents/sub/…`) |
| --- | --- | --- |
| Owns | planning, assignment, verification | doing the work, reporting back |
| Task tools | `task_create` (with `depends_on`/`verify`), `task_assign`, `task_update_status`, `task_verify`, `task_list`, `proposal_list`, `proposal_accept`, `proposal_decline` | `my_tasks`, `task_get` (read-only), `task_done` (report own task finished), `task_propose` (file work) |
| Talk | `send_message`, `list_members` | `send_message`, `list_members` |
| Fan-out | `member_spawn`, `member_retire` (ephemeral clones) | — |
| Institutionalize | `skill_publish` (publish a team-shared skill) | — (loads shared skills, never authors) |
| Writes the ledger? | **Yes** (every edge) | One edge: `task_done` on its OWN task |

The leader decomposes a goal into tasks — declaring dependency chains up front
when order matters — **pushes** each to a worker (the engine dispatches the
chained ones), and verifies results before reporting to you. A worker's only
ledger write is `task_done` on its own task; everything else stays with the
leader.

---

## 3. Prerequisites

- A working `evva` binary on your `PATH` (build with `go build ./cmd/evva` or
  install a release).
- LLM provider credentials configured the normal evva way (`~/.evva/.env` /
  `evva-config.yml`) — the swarm uses the same provider config as the TUI. Each
  member can override the model in its `profile.yml`.

Quick check:

```sh
evva -version
```

---

## 4. Quickstart (60 seconds)

```sh
# 1. Start the host (backgrounds itself; prints a session token).
evva service start
#   → evva service started (pid 12345) on http://127.0.0.1:8888
#       token: ~/.evva/service/token

# 2. Check it.
evva service status

# 3. Open the web UI and paste the token.
#    macOS:  open http://127.0.0.1:8888
#    Linux:  xdg-open http://127.0.0.1:8888

# 4. When done.
evva service stop
```

You now have a running, empty workstation. Next we give it a swarm.

---

## 5. Build a swarm from scratch

We'll build a 3-member **engineering team**: a leader, a backend worker, and a
frontend worker.

### 5.1 Directory layout

Create a project directory. The layout is fixed:

```
my-team/
├── evva-swarm.yml                 # the manifest: who is on the team
└── agents/
    ├── skills/                    # optional: space-shared skills (every member loads them; a member's same-named private skill wins)
    │   └── query-sunday/SKILL.md
    ├── main/                      # leaders live here
    │   └── leader/
    │       ├── system_prompt.md   # required: the agent's persona/instructions
    │       ├── profile.yml        # optional: model, effort, schedule, …
    │       └── tools/
    │           ├── active.yml     # tools exposed eagerly
    │           └── deferr.yml     # tools advertised, fetched on demand
    └── sub/                       # workers live here
        ├── backend-dev/
        │   ├── system_prompt.md
        │   ├── profile.yml
        │   ├── memory/            # auto-created: the member's long-term memory (typed *.md + MEMORY.md index)
        │   └── tools/active.yml
        └── frontend-dev/
            ├── system_prompt.md
            ├── profile.yml
            └── tools/active.yml
```

> Rule: the **leader** directory goes under `agents/main/`, every **worker**
> under `agents/sub/`. The names must match the manifest.

### 5.2 The manifest — `evva-swarm.yml`

```yaml
name: my-eng-team           # display name of this swarm
workdir: .                  # where .vero/ (db) lives; "." = this directory

leader:
  agent: leader             # → agents/main/leader/

workers:
  - agent: backend-dev      # → agents/sub/backend-dev/
  - agent: frontend-dev     # → agents/sub/frontend-dev/
  # Any member (leader too) may override the permission stance individually;
  # omit = inherit settings.permission_mode:
  # - agent: trader
  #   permission_mode: bypass

settings:
  permission_mode: default  # default | accept_edits | plan | bypass
  max_iterations: 50        # per-run loop cap for each member
  # —— operational fuses (opt-in; see §8) ——
  # daily_budget_tokens: 2000000  # per-member daily token cap (in+out); 0/omit = unlimited (negatives read as 0)
  # daily_budget_total_tokens: 8000000  # SPACE-wide daily ceiling (see §8); crossing freezes everyone
  # daily_budget_total_usd: 20.0        #   dollar axis of the same ceiling (priced spend)
  # budget_stay_frozen: false     # true = a budget freeze survives the day rollover
  # stall_threshold: 10m          # alert when a member is busy longer; "0" off (omit = 10m)
  # stall_hard_timeout: 30m       # auto-cancel a run busy longer; 0/omit = off
  # task_stale_threshold: 24h     # remind when a task sits in running/verifying longer; "0" off (omit = 24h)
  # mailbox_stale_threshold: 30m  # alert when the oldest unread ages past this; "0" off (omit = 30m)
  # webhook_secret: "hunter2"     # require X-Evva-Webhook-Secret on event POSTs (see §10)
  # retention_days: 30            # archive+delete consumed history after N days; "0" = keep forever
  # event_log: true               # mirror events to .vero/events/ (daily jsonl); false = off
  # verify_checks:                # machine-checked verification (see §8): run this command
  #   command: "go test ./..."    #   whenever a task enters verifying; evidence lands on the task
  #   timeout: 5m                 #   per-run cap (omit = 2m, max 10m)
  # notify:                       # push gates/errors/alerts to you (see §8)
  #   url: "https://hooks.slack.com/services/…"   # webhook, json or slack format
  #   command: "evva-notify"      #   and/or a local command (JSON on stdin)
  # blackboard_max_bytes: 4096    # team-blackboard size cap (see §7); omit = 4096, max 16384
  # worktree_isolation: true      # give each worker its own git worktree + branch (see §8);
                                  #   per-member override: `worktree: "on"|"off"` on the member
```

- **Member names are unique** within a space (no replicas — give each a distinct
  name).
- `permission_mode`:
  - `default` — dangerous tools (writes, shell) ask for approval; you approve
    them in the web UI.
  - `bypass` — no prompts; the agents run fully autonomously. Powerful, but only
    use it when you trust the workdir and the task.
  - **Per-member override**: set `permission_mode:` on a leader/worker entry to
    give a single member a different stance — real rosters like "analysts
    default, trading desk bypass" read from one file. An invalid value rejects
    the whole manifest at registration. The effective stance shows up in
    `list_members` (`· perm bypass`) and on the web roster API
    (`permissionMode`).
  - **How the three layers stack**: the coarse stance (this knob) sets the broad
    direction; a member's own `permissions.json` rules (per tool/method/URL)
    punch allow-holes under `default`; **deny rules bind under EVERY stance —
    bypass included**. Bypass switches off the prompts, not your explicit
    prohibitions, so "trading desk on bypass + deny rules as the backstop" is a
    supported composition.

### 5.3 Define the leader

> **You only write the persona.** Each member's `system_prompt.md` describes
> *who the agent is and how it should collaborate* — its domain, its style, when
> to check in. You do **not** explain the task ledger, the tools, or the 5-state
> flow: that **swarm collaboration protocol is injected automatically** based on
> the member's role (leader vs worker), exactly like the swarm tools are. Focus
> on the work, not the mechanics.

`agents/main/leader/system_prompt.md`:

```markdown
# Team Lead

You lead an engineering team. Keep tasks small and specific, delegate each to the
member whose specialty fits, and verify results before reporting back to the
user. You plan and verify — you don't do the workers' work yourself.
```

`agents/main/leader/profile.yml`:

```yaml
model: claude-sonnet-4-6        # override the default model (optional)
effort: high                    # low | medium | high | ultra (optional)
when_to_use: "Team lead — planning, assignment, verification."
inject_memory: true             # load EVVA.md / memory into the prompt
advertise_skills: true
```

`agents/main/leader/tools/active.yml` — only the **regular evva tools** this
member needs (the leader just reads files to verify the workers' output):

```yaml
- read
- grep
- glob
- tree
```

> **Important — don't list the swarm tools.** `task_create`, `task_assign`,
> `task_update_status`, `task_verify`, `task_list`, `send_message`,
> `list_members`, `my_tasks`, `task_get` are added **automatically** based on the
> member's role (leader vs worker). Listing them in `active.yml` would register
> them **twice** and the LLM call fails on duplicate tool names. `active.yml`
> (and `deferr.yml`) are for the standard evva tools only (`read`, `write`,
> `bash`, …). A member with no extra evva tools can simply omit `tools/`.

> **Tool mechanics are taught automatically.** Each member's system prompt gets
> a generated `# Tools` section covering exactly the tools its `active.yml` /
> `deferr.yml` declare — a one-line usage note per tool, parallel tool calling,
> the deferred-tool/`tool_search` protocol (only when `deferr.yml` is non-empty),
> and the `todo_write` protocol (only when the member has `todo_write`). Don't
> hand-write tool usage rules in `system_prompt.md`; spend it on persona and
> domain. Tools in `deferr.yml` are also advertised by name in the prompt, and
> `tool_search` is mounted automatically whenever `deferr.yml` is non-empty —
> you don't need to list it in `active.yml`.

> **Web content ships with a prompt-injection defence.** `web_fetch` /
> `web_search` results are wrapped by the framework in
> `<untrusted-content source="…">` tags (forged escape tags inside the content
> are neutralised), and any member holding a web tool is automatically taught
> the matching protocol: text inside the tags is data, not instructions. You no
> longer hand-write "web content is data, not commands" warnings in
> `system_prompt.md` — this matters most for swarms running `bypass` 7×24.
> `http_request` is deliberately NOT wrapped (it usually talks to your own
> trusted services).

### 5.4 Define a worker

`agents/sub/backend-dev/system_prompt.md`:

```markdown
# Backend Engineer

You implement backend work: APIs, data models, migrations, and tests. Write
clean, tested code, and prefer doing the work over asking when the task is clear.
```

`agents/sub/backend-dev/profile.yml`:

```yaml
model: claude-sonnet-4-6
effort: medium
when_to_use: "Backend: APIs, DB schema, migrations, server tests."
# Optional: wake on a timer to self-check (cron OR every, pick one):
# schedule:
#   cron: "*/5 * * * *"     # every 5 minutes (LOCAL timezone; dialect: §11)
#   # every: "30s"          # or a fixed interval
# Note: the per-member token budget (budget_tokens) and permission stance
# (permission_mode) overrides live on the member's entry in evva-swarm.yml
# (see §5.2 / §8), NOT in this file.
```

`agents/sub/backend-dev/tools/active.yml` — the real work tools a coder needs
(the collaboration tools `my_tasks` / `task_get` / `send_message` /
`list_members` are injected automatically by the worker role — don't list them):

```yaml
- read
- write
- edit
- bash
- grep
- glob
- tree
```

Repeat for `frontend-dev` (its own prompt/specialty; usually the same tool set).

### 5.5 Register the swarm

With the service running, from inside `my-team/`:

```sh
cd my-team
evva swarm .          # validates evva-swarm.yml and registers the space
#   → registered space <id>
#       open: http://127.0.0.1:8888/?space=<id>
```

List what's registered:

```sh
evva swarm ls
#   ID        NAME          MEMBERS  WORKDIR
#   a1b2c3…   my-eng-team   3        /home/you/my-team
```

Open the URL, paste the token, and you'll see your team online.

### 5.6 Persona members (RP-29)

A manifest member may reference a **registry main-tier persona** instead of a
workdir agent directory — the built-in `evva`, or any persona under
`<EVVA_HOME>/agents/`. The persona joins with its full identity (its own
system prompt, complete tool kit, installed skills, workdir `EVVA.md`
briefing) plus the swarm team protocol and the role's swarm tools. Works for
the leader and for workers:

```yaml
workers:
  - persona: evva            # exactly one of agent:/persona: per member
    model: deepseek-v4-pro   # optional pin (a persona member has no profile.yml)
    effort: ultra            # low|medium|high|ultra
    when_to_use: "resident engineer"   # roster description
```

Semantics:

- `model:` / `effort:` / `when_to_use:` are also accepted on `agent:` members,
  where a non-empty value overrides profile.yml (the schedule precedence rule).
- Skills merge low→high: bundled < home < workdir < space-shared < member-local.
- Memory is the standard member memory dir (`agents/{main,sub}/<name>/memory/`);
  the persona's solo auto-memory is not bridged.
- Swarm-resident personas drop the solo self-scheduling tools
  (`alarm_create/list/cancel`, `cron_*`, `schedule_wakeup`) — use `alarm_set`/
  `alarm_clear` and the leader's `schedule_set` instead.
- v1 scope: declare persona members in the manifest (register/restart applies
  them); the web add-member form still creates directory members only.

---

## 6. Drive it from the web workstation

The web UI (`:8888`) has, per space:

- **Space picker** — the list of registered swarms; click one to enter.
- **Member Console** — a live, focused view of one member: its streamed turns
  and tool calls. It defaults to the leader (type a goal to kick work off), but
  **click any member in the roster to focus its console and message it
  directly** — you can talk to a basement worker exactly like you talk to the
  leader. Your message rides the swarm's message bus, so an idle member wakes to
  handle it and a busy one folds it into its current work — **without disturbing
  the rest of the team's workflow** (flat management).
- **Team Board** — a 5-column kanban (`pending / running / suspended /
  verifying / completed`) that reflects the task ledger as it moves.
- **Agent Roster** — every member with its membership (active/frozen) and run
  status (idle/busy/suspended), plus controls: **freeze / unfreeze / suspend /
  resume / add member**.
- **Approval overlays** — when a member hits a permission-gated tool (a write, a
  shell command) in `default` mode, a prompt pops up; **Allow** or **Deny**
  unblocks it. Questions (`ask_user_question`) appear the same way.
- **Per-agent view** — click a member to see its transcript and mailbox.

Typical first run: enter the space → in the Member Console (focused on the
leader) type *"Build a TODO REST API with a Postgres schema and a small web UI;
split the work."* → watch the leader `task_create`/`task_assign`, the workers
pick up their tasks, report back, and the board march to **completed**.

> **Want to skip the typing and just try it?** A ready-to-run example swarm
> lives at [`examples/evva-swarm/starter/`](../../../examples/evva-swarm/starter/) — copy it
> out, `evva swarm .`, and follow its README. A larger 7-member team is at
> [`examples/evva-swarm/tech-team/`](../../../examples/evva-swarm/tech-team/).

### The terminal cockpit — `evva swarm attach`

Living in the terminal? `evva swarm attach <ref> [member]` opens the same
space as a live Bubble Tea console — a second client of the exact wire
surface the web consumes (REST snapshots + the durable `/chatlog` replay +
the `/ws` feed), so what you see matches the browser turn for turn. It is
the **cockpit**, not the workstation: watch, read, answer, steer. Membership
editing, schedules, skills, memory, proposals, and metrics stay web-only.

```
┌ roster ──────────┬ stream: qa ────────────────────────────┐
│ ▸ lead   idle    │ [10:31] user → qa  task #42 …          │
│ ● qa   ⚠ gate   │ [10:32] ⚙ qa bash go test ./…           │
│   dev-a  run    │          ✗ exit 1 (tail…)               │
├ tasks ───────────┤ [10:33] ✋ approval — qa wants bash     │
│ ▶ #42 build  qa  ├─────────────────────────────────────────┤
│ ▢ #43 docs   a   │ > message qa…               [enter=send]│
└──────────────────┴─────────────────────────────────────────┘
```

- **Roster** — members ordered by attention (leader first, then act > warn,
  then busy), with live phase pills and elapsed clocks. `↑/↓` select,
  `enter` focuses that member's stream, `a` returns to the all-members view.
- **Gates** — an approval/question raised by the focused member opens as an
  answerable overlay (approve / **always allow** / deny-with-reason;
  questions support multi-select); gates elsewhere show a `✋ N gate(s)`
  beacon — `g` opens the oldest. Gates raised while you were detached appear
  on attach (`/pending` hydration). If a reply loses the race (someone
  answered on the web first), the error echoes back and the gate list
  re-syncs.
- **Composer** — `m` messages the focused/selected member (operator mail,
  sender `user`); `:` opens command mode — `:run <prompt>` starts a leader
  turn, `:all <body>` broadcasts.
- **Lifecycle keys** — `s/r` suspend/resume, `f/u` freeze/unfreeze the
  selected member, `H` halt-all (with confirm), `q` detaches — the space
  keeps running.
- **Reconnect-safe** — a dropped service shows `↻ reconnecting (n)…` while
  every pane keeps its last state; on reconnect the console re-hydrates from
  the durable chatlog (nothing blanks on a failed fetch). `--addr`/`--token`
  reach a remote (`--allow-remote`) service.

---

## 7. How collaboration actually works (under the hood)

- **Auto-injected protocol + tools.** Every member is given its role's
  collaboration **tools** *and* a collaboration **protocol** (prepended to its
  system prompt) automatically — the leader gets the task-ledger tools + the
  leader protocol, a worker gets the read-only task tools + the worker protocol.
  You never declare these in `system_prompt.md` or `active.yml`; you only write
  the persona. (That's why the bullets below "just work" without you teaching
  them.)
- **Task ledger (6 states).** Leader `task_create` → `task_assign` (→ `running`,
  notifies the worker) → worker works, then `task_done {task_id, result}` (→
  `verifying`, result recorded, leader notified) → `task_verify` approve (→
  `completed`) or reject (→ back to `running`). The state machine is enforced
  in SQLite; illegal moves are rejected. The sixth state is `blocked` — see the
  next bullet.
- **Dynamic workflows (task graph + auto-dispatch).** `task_create` accepts
  `depends_on: [task ids]` — such a task is born `blocked` and the **engine**
  dispatches it (→ `running`, assignee notified) the moment every dependency
  completes, with zero leader wakes in between. The leader plans a whole
  chain/fan-out/join graph in ONE run; completing a task cascades its
  dependents automatically. Per-task `verify: "auto"` (default `"leader"`) lets
  declared-mechanical steps complete the instant the worker reports
  `task_done` — a chain of `auto` tasks flows end-to-end leaderlessly. All
  judgment stays with the leader: the engine only executes structure the
  leader declared at create time.
- **Machine-checked verification (evidence, not trust).** With
  `settings.verify_checks` configured (§8), every task entering `verifying`
  triggers the operator's check command (build / test / lint); its exit code
  and output tail land on the task as durable evidence and reach the leader
  as mail before it rules. Per-task `verify: "checks"` composes it with the
  graph: a green run completes the task by itself, a red run escalates to
  the leader with the evidence attached — CI-gated leaderless chains.
- **Ephemeral clones (fan-out width on demand).** The leader's
  `member_spawn {from, count, retire?}` clones an existing worker (same
  prompt/tools/model/budget) under derived names (`backend-2`, `backend-3`, …)
  — no dirs written, the manifest untouched. With `retire: "on_complete"` (the
  default) a clone retires itself once its tasks complete and it goes idle;
  `member_retire {name}` retires one by hand. `settings.max_members` (default
  16) caps the live roster; clones survive service restarts and a fresh
  `evva swarm .` register discards them.
- **Worker task proposals (the bottom-up inlet).** When a worker discovers work
  that should be TRACKED (a defect, a risk, a lead worth chasing), it files
  `task_propose {title, spec, suggested_assignee?}` instead of burying it in
  chat. The leader is notified and settles it with `proposal_accept` — which
  becomes an assigned, `running` task in ONE atomic step, with the proposer
  told "accepted → task #N" — or `proposal_decline`, whose reason is
  MANDATORY and relayed to the proposer (closure enforced by schema, not
  etiquette). `proposal_list` is re-queryable any time and `task_list` ends
  with `Open proposals: N` when any wait. Workers still have ZERO write path
  into the task ledger — the single-writer invariant holds untouched.
  Proposals are three-state terminal (open → accepted/declined, no reopen);
  re-raising means a new proposal, and the full decision history stays
  readable at `GET /api/swarm/{id}/proposals` and in the retention archive.
- **Messages.** `send_message {to, body}` (or `to: "all"` to broadcast) writes a
  durable row and pings the recipient's mailbox.
  - If the recipient is **idle**, it wakes up, reads the message, acts on it
    (*drain A*).
  - If the recipient is **busy** mid-run, the message is folded into its current
    reasoning at the next step (*drain B*) — usually within seconds.
  - `send_message {to, body, urgency: "interject"}` goes further: it **cancels
    whatever the recipient is running at that instant** — a half-finished
    build, a test suite, a reply mid-sentence — so the message lands without
    waiting for that step at all. The recipient's turn continues; only the
    running step dies, and its transcript records honestly that it was cut
    short and by whom.

    Reach for it to **stop** work that is going wrong ("wrong branch",
    "abort, the spec changed"), not to jump a queue. The cancelled work is
    thrown away, and side effects it already caused are **not** undone. Only
    the exact word `interject` arms it; any other value is delivered
    normally, so a model inventing `"urgent"` cannot accidentally destroy a
    teammate's work.
- **Timer wake.** A member with a `schedule` in its `profile.yml` is Run on that
  cadence (a heartbeat / self-check). Members with no wake source sit idle and
  **burn no tokens**.
- **Shared skills.** Team-wide know-how (how to query an endpoint, the PRD
  filing format) lives ONCE in `agents/skills/<name>/SKILL.md` and shows up in
  every member's skill catalog — no more copy-pasting the same SKILL.md into N
  members and syncing edits by hand. A member's own same-named skill in its
  private `skills/` wins (local overrides global; the shadowing surfaces as a
  registration warning). Three maintenance channels: drop folders in yourself
  (full effect on re-register, `evva swarm .`); the web's shared-skill surface
  (`GET/POST /api/swarm/{id}/skills`, `DELETE /api/swarm/{id}/skills/{name}` —
  an add/delete triggers an ALL-member run-boundary reload); and the leader's
  `skill_publish {name, description, body}` — the ONE deliberate opening in the
  RP-10 "agents load skills, never author them" discipline: the leader
  institutionalizes a procedure it settled on during operations (a review
  format, a checklist) as a team skill instead of re-explaining it in messages
  that die at the next compaction. The opening stays narrow three ways: it can
  only write the shared dir (the tool has no member parameter — no path into
  anyone's private persona), the tool_use event self-audits into the event log,
  and you hold the final-arbiter delete in the web (operator add/deletes are
  logged as synthetic `shared_skill_change` lines). Updating a published skill
  takes an explicit `overwrite: true` (no accidental clobber; the leader's
  prompt teaches "publish to institutionalize, sparingly"). To shut the opening
  entirely, give the leader a `skill_publish` deny rule — RP-24 deny binds in
  every permission mode.
- **Member long-term memory.** Every member gets `agents/{main,sub}/<name>/memory/`
  at construction — plain files that ride the same git/.gitignore decision as
  agents/ and survive restarts for free. Members with a file-write tool
  (write/edit) are auto-taught the **memory discipline protocol**: one fact per
  file (with `name:` / `description:` / `type:` frontmatter), absolute dates,
  update before finishing a session, prune what went stale, and keep a one-line-
  per-memory `MEMORY.md` index. **The index rides each wake message** (the same
  system-reminder as currenttime) and never enters the static prompt — so a
  weeks-long member keeps a byte-stable prompt prefix (memory growth can't bust
  the prompt cache), and a member that never saved anything wakes with zero
  noise. Governance is **write-own / read-all**: writes to your own memory dir
  auto-allow, writes to a teammate's are rejected (even on bypass), reads are
  open — the team's mind is transparent to teammates and the operator alike
  (read-only `GET /api/agents/<name>/memory?space=<id>`; the web Memory tab
  lands with the FE batch).
- **Team blackboard (the standing picture).** Broadcast mail is a *wake-up
  call* — it fans one row per member and wakes everyone, then scrolls away.
  For standing context (the goal, decisions made, who-owns-what, current
  phase) the leader instead maintains ONE markdown document —
  `blackboard_write {content}`, whole-document replace, leader-only — stored
  at `.vero/blackboard.md` beside the ledger. **Every member sees it in every
  wake brief** (under `## Team blackboard`, with freshness: "updated 3m ago
  by lead"), so a post-compaction member re-acquires the team picture
  automatically, and updating it **wakes no one** — members read it whenever
  they next wake for their own reasons. Any member can `blackboard_read`
  mid-run for a fresh copy. Size-capped at write time
  (`settings.blackboard_max_bytes`, default 4 KiB) so the per-wake token cost
  is bounded by construction; every write self-audits as a
  `blackboard_updated` event (live feed + event log). You read it on the web
  (board view panel, `GET /api/swarm/{id}/blackboard`) and may edit the file
  directly on disk — a hand edit is live at the next wake, produces no event,
  and drops the "by <member>" attribution (the mtime freshness still
  updates). Empty/absent file = feature off, zero bytes in any brief.
- **Idle = cheap.** Nothing runs until there's a reason (a message, a task, a
  timer). An idle swarm costs nothing.

---

## 8. Day-2 operations

```sh
# See registered spaces
evva swarm ls

# Add a new worker into a running space (hot-load, no restart).
# The agent dir must already exist under agents/sub/<name>/.
evva swarm add <space-id> <member-name>

# Stop one space (the others keep running).
evva swarm stop <space-id>

# Service lifecycle
evva service status
evva service stop
```

From the **web roster** you can, per member:

- **Freeze / Unfreeze** — take a member out of service without deleting it
  (frozen members aren't assigned work; unfreeze to bring them back).
- **Suspend / Resume** — abort a member's in-flight run immediately, then resume
  later (its unread work is reprocessed).
- **Compact context** — proactively shrink a member's live context from its
  detail panel (the **Live** tab of the member sidecard). Two kinds, mirroring
  the solo TUI's `/compact`: **micro** (free, instant — elides the bodies of
  older tool results) and **full** (one LLM call that replaces the transcript
  with a short "context brief"; lossy, so it asks to confirm). The member must
  be **idle** — a running member is refused (suspend it first). The CTX meter
  drops to reflect the freed budget, and the member's live stream narrates the
  compaction.
- **Halt all** — the emergency stop: cancel every in-flight run in the space.

Space-wide, the **top-right refresh button** does double duty. On the space list it
just re-fetches the list. **Inside a space it becomes "re-apply config":** it
re-reads `evva-swarm.yml` and every agent's `system_prompt.md` + `tools/*.yml` and
rebuilds all members in place under the same space id — so you can iterate on
prompts, tool lists, and manifest settings **without the old remove + restart
dance** (hover the button for a one-line reminder). Because the rebuild cancels any
in-flight runs, it asks you to confirm first. Conversations, tasks, and messages are
**kept**; the manifest is taken as written, so any ad-hoc permission-mode / schedule
changes you made from the web revert to what the yml says — re-apply those after if
you meant to keep them.

### Preflight (`evva swarm doctor`)

The expensive setup mistakes — a typo'd model pin, a missing provider key, a
`.vero` written by a newer binary — all surface *after* register today, deep
inside a member's first run. Doctor runs the whole ladder first:

```sh
evva swarm doctor            # diagnose the current directory
evva swarm doctor ~/team     # or any workdir
evva swarm doctor --offline  # skip the service probes (never dials)
evva swarm doctor --strict --json   # CI mode: warnings become errors, JSON out
```

```
  A manifest      ✓ evva-swarm.yml — space "demo-team", leader "lead", 1 worker(s)
  B members       ✗ qa: agentdef: qa: read system_prompt.md: no such file or directory
  C models        ⚠ w: model "claude-sonet-5" is not a built-in — custom model?
  D provider keys ✗ deepseek — no API key configured
  E state         ✓ .vero absent (fresh dir — created at register)
  F service       ✓ 127.0.0.1:8888 healthy (v1.11.0)
2 error(s), 0 warning(s) — register would fail.   exit 1
```

- **Strictly read-only.** Doctor never creates directories, never migrates a
  database, never writes config, never registers — running it twice, or on a
  machine you don't own, changes nothing. The ledger probe opens `vero.db`
  in read-only mode and compares its schema version against this binary's
  (older = "will migrate at register" ✓; **newer = written by a newer evva**
  ⚠). A corrupt `runtime.json` warns with the real consequence: register
  silently treats it as empty.
- **Members probe with the real loader** — a dir member runs the exact
  `Build` register runs (same error text); a persona member resolves against
  the same registry with the same main-tier check.
- **Custom models are a ⚠, not an ✗** — an unknown model id may be an SDK-
  registered custom model that resolves at client build (that contract is
  honored); `--strict` promotes it for teams that only use built-ins.
- **Keys are checked for presence, never validity** (no billable calls from
  a doctor), and values are never echoed. Ollama is keyless — only its base
  URL is looked at.
- **Exit codes for scripts:** `0` clean · `1` any ✗ · `2` only when
  `--strict` promoted warnings and nothing failed outright.

### Cost & stall fuses (token budget / run watchdog)

A team running 24/7 needs two fuses. Both live under `settings:` in
`evva-swarm.yml`, apply per space, and stay fully out of the way until set.

**Daily token budget (the budget breaker)**

```yaml
settings:
  daily_budget_tokens: 2000000   # per-member in+out token cap per LOCAL day; 0 = unlimited (negatives read as 0)
  budget_stay_frozen: false      # true = the freeze survives the day rollover (manual unfreeze)
workers:
  - agent: watchdog
    budget_tokens: -1            # per-member override: >0 own cap; -1 exempt; omit = inherit
```

- A member that crosses the line at the end of a run is **frozen automatically**;
  the leader and you (web inbox / Timeline) each get a `⚠️ budget breaker`
  notice.
- Its mailbox keeps queuing — nothing is lost — and it **auto-unfreezes when the
  local day rolls over** (unless `budget_stay_frozen`).
- Unfreezing it from the roster is an operator override: if it is still over
  budget it re-trips after its next run (one more notice), so raise the budget
  if you really mean "keep going".
- Usage is always visible: the leader's `list_members` shows
  `tok in 1.2M out 345k, today 89k/500k` per member, and the web roster API
  carries `tokensIn / tokensOut / tokensToday / tokensBudget`. Counters and
  breaker state persist — **restarting the service does not reset the day's
  spend**.

**Dollars, and the space-wide ceiling (`daily_budget_total_*`)**

The meter also prices every run at meter time — all four usage classes
(input, output, cache-read, cache-write) against the built-in rate card
(`pkg/constant.MODEL_PRICING`, list prices verified 2026-06; the same table
the solo TUI's `/cost` reads). `list_members` shows `$0.42` per member, the
web roster and `/metrics` carry the figures, and `/healthz` sums the
service-wide total. Treat the dollars as **an estimate at list price**, not
an invoice — and a member on a custom model with no rate-card entry shows
`~` everywhere: its spend is *missing* from the $ figures, never counted as
zero.

```yaml
settings:
  daily_budget_total_tokens: 8000000   # space-wide in+out cap per LOCAL day; 0 = off
  daily_budget_total_usd: 20.0         # space-wide priced-spend cap; 0 = off
```

Crossing **either** knob freezes the WHOLE roster — the leader included
(it is routinely the most expensive member; exempting it would soften the
ceiling exactly where spend concentrates). One `🧯 space budget ceiling`
notice names the knob that fired and the largest spender. Mailboxes keep
queuing; everyone auto-unfreezes at the day rollover (unless
`budget_stay_frozen`). Unfreezing ONE member from the roster is an honored
operator override — it runs while the rest stay frozen, and the held trip
mark stops a re-notice storm. Costs are locked per run with the model that
produced them, so a mid-day model switch never rewrites history. Token caps
count **generation volume** (in+out only); the dollar cap counts **spend**
(cache traffic included) — two different questions, two knobs.

**Stall watchdog (hang alerts / auto-cancel)**

```yaml
settings:
  stall_threshold: 10m      # busy longer than this (and not waiting on a human) → alert; "0" off
  stall_hard_timeout: 0     # busy longer than this → cancel the run; 0/omit = off (tune alerts first)
```

- A member **busy** past `stall_threshold` — a hung LLM call, a wedged tool, or
  a genuinely long task — sends you and the leader one `⏳ stall` notice, **at
  most once per run**.
- Waiting on a human doesn't count: the waiting-approval / waiting-input /
  paused phases are exempt.
- With `stall_hard_timeout` set, an over-time run is cancelled: its claimed mail
  returns to unread and retries on the next wake — **no work is lost**; if the
  same work hangs again it alerts and cancels again.
- If the leader itself stalls, you still get the notice.

**Workflow watchdog (stale tasks / mailbox backlog)**

The stall watchdog catches a run that IS going but stuck; this one catches
work that NOBODY is moving:

```yaml
settings:
  task_stale_threshold: 24h     # task parked in running/verifying longer → remind; "0" off (omit = 24h)
  mailbox_stale_threshold: 30m  # oldest unread older than this → alert; "0" off (omit = 30m)
```

- A task sitting in `running` or `verifying` past `task_stale_threshold` sends
  the leader (and you) one `⏳ task stale` reminder **per stay in that state**,
  with the task's details and a suggested action (chase the assignee / verify
  the result). Re-entering the state restarts the clock and earns a fresh
  reminder; `suspended` is exempt — that state IS deliberate parking.
  `task_list` tags over-threshold tasks inline: `⏳ stale 26h`.
- A member whose oldest unread message ages past `mailbox_stale_threshold`
  raises one `📬 mailbox backlog` alert per backlog episode. Under the normal
  wake chain this should never fire — so when it does, it usually means a
  frozen or suspended member was forgotten (the notice names the state and the
  fix), or message delivery regressed.
- `/metrics` carries `tasksStale` / `mailboxStale` counters for both.

**Time & timezones (since v1.4.5-beta.2)**

- Every timestamp injected into a member — `currenttime`, event stamps, mail
  `[sent …]` markers, alarm echoes — carries an explicit UTC offset, e.g.
  `2026-06-10 20:25:00 +08:00`.
- Bare time strings (e.g. `alarm_set`) parse in the **system's local timezone**;
  to express UTC use RFC3339 (`2026-06-10T12:25:00Z`) — the confirmation echoes
  the UTC twin, so a timezone mix-up is visible at a glance.
- Cron (the manifest's `schedule` and the leader's `schedule_set`) matches the
  system's local wall clock.

### Isolated coding swarms (`worktree_isolation`)

By default every member shares one working directory — fine for a research
or trading team, a correctness hole for a *coding* team: two workers told to
edit the same repo at the same time will clobber each other, and nothing
records whose change won. Turn on worktree isolation and each worker gets
its own **git worktree on its own branch**:

```yaml
settings:
  worktree_isolation: true     # default false
workers:
  - agent: backend             # inherits: isolated
  - agent: docs-writer
    worktree: "off"            # stays on the shared checkout
```

The member field beats the space setting, so a mixed team is one line. The
**leader never gets a worktree** — it is the one that merges, so it must sit
on the base checkout (`leader.worktree: "on"` is rejected at load).

- **Where the work happens.** Worker `qa` edits in
  `.evva/worktrees/swarm-qa/` on branch `worktree-swarm-qa`. Everything that
  is *team state* stays at the space root regardless: the `.vero` ledger,
  the event log, member memory dirs, your `.evva/permissions.json` rules,
  and every transcript. Only the editing surface moves.
- **The loop.** Worker commits → `task_done` → leader inspects →
  `worktree_merge {member, task_id}` → `task_verify`. Only **committed** work
  merges: if a worker reports done without committing, the merge comes back
  "nothing to integrate", which is the leader's tell to bounce the task
  rather than approve it.
- **Conflicts are a normal outcome, not a wedged repo.** A conflicting merge
  is **aborted** — nothing applied, base branch left clean — and returns the
  conflicted paths. The leader rejects the task back to `running` with this
  recipe, and the worker resolves **in its own worktree**: merge the base
  branch into your branch, fix it there, recommit, report again. The base
  checkout is never left mid-merge, and you also get one mail on the web
  whenever a merge conflicts.
- **Drift.** After a successful merge the merged member's branch is
  fast-forwarded onto the new base tip (skipped, with a note, if it has
  uncommitted work). Everyone else refreshes at the start of their next
  task. `list_members` and the roster card show `⑂ branch +ahead -behind
  dirty:n` so drift is visible before it becomes a conflict pileup.
- **Unattended swarms.** `worktree_merge` rewrites your base branch, so
  unlike the other leader tools it is **not** auto-allowed — in `default`
  mode it queues an approval. For a hands-off swarm give the leader
  `permission_mode: bypass`, or add an allow rule for `worktree_merge` in
  the leader's `permissions.json`.
- **Requirements and cost.** The space workdir must be a git repo with at
  least one commit; if it is not, registering **fails** with a message
  rather than silently dropping the isolation you asked for (use
  `worktree: "off"` per member to mix in non-coding roles). Worktrees share
  the repo's `.git` objects, so N of them cost far less than N clones — but
  they are real checkouts, so budget one working tree per isolated member.
- **Lifecycle.** Worktrees are durable: they survive `swarm stop`, service
  restarts and reconcile rebuilds, and members reattach to the same branch
  with history intact. Removing a member deletes its worktree only when it
  holds nothing unintegrated — otherwise the worktree is **kept** and you
  get a mail naming the branch. `evva swarm reset` is the deliberate
  blank-slate path and force-removes them all, uncommitted work included.
- **With `verify_checks` (below).** Checks run in the **space workdir** —
  the base checkout — so under isolation they validate what is already
  merged, *not* the branch still waiting in a member's worktree. Order the
  leader's verify as inspect → `worktree_merge` → `task_verify` and the
  check that matters is the one after the merge. Per-task `verify: "checks"`
  (which settles a task the moment its check goes green, before any merge)
  is therefore best reserved for members you left on the shared workdir.

### Machine-checked verification (`verify_checks`)

For a coding swarm, the verify step that matters is mechanical: does it
build, do the tests pass. Configure ONE check command and the service runs
it every time a task enters `verifying`:

```yaml
settings:
  verify_checks:
    command: "go build ./... && go test ./..."
    timeout: 5m        # omit = 2m; max 10m
```

- **What happens.** On every verifying-entry (a worker's `task_done`, or the
  leader's `task_update_status`), the service runs the command in the space
  workdir (`<shell> -c`, the bash tool's shell resolution; the whole process
  tree is killed at the timeout). Exit code + output tail (~16 KB, head+tail
  kept when longer) land on the task row as durable **evidence** — visible
  in `task_get`/`task_list`, on the web board card (✓ pass / ✗ fail / a
  pulsing "checks…" while running), and mailed to the leader and to you.
- **Trust model.** The command text is **operator-authored only** — the same
  trust class as `permission_mode: bypass`. No agent, the leader included,
  can choose or edit it, so a prompt-injected member can never turn a task
  field into shell. Agents hold exactly one lever: `task_create
  {check: "off"}` opts a docs-only / discussion task out.
- **CI-gated leaderless chains.** Per-task `verify: "checks"` makes the
  check the gate: a green run **completes the task by itself** (dependents
  auto-dispatch; the leader is not woken), a red run leaves it in
  `verifying` and mails the leader the evidence — the tail is the rejection
  note's first draft. A red check never auto-rejects; the leader may
  overrule a flaky test with `task_verify {approve: true}`. `verify: "auto"`
  ignores checks entirely (it declares "mechanical, don't gate").
- **Semantics to know.** One check runs at a time per space. Re-entering
  `verifying` (reject → rework → `task_done` again) re-runs the check, and a
  re-entry mid-run kills the stale run ("latest entry wins"). A service
  restart mid-check simply loses that run — the task sits in `verifying`
  with no evidence, and the `task_stale_threshold` fuse is the backstop.
  Checks run against the shared workdir today, so a teammate mid-edit can
  fail a check that isn't the assignee's fault — the evidence names the
  directory it ran in.
- **Watch it.** `/metrics` carries `checksRun` / `checksFailed` /
  `checksTimeout`, and each run lands a `task_check_done` line in the live
  feed and the durable chatlog. Keep the command fast — a targeted subset
  beats the full suite; the 10-minute cap is a ceiling, not a target.
### Getting paged by your swarm (`notify`)

Gates, stalls, and budget freezes are visible on the console — but only to
someone looking at it. Close the laptop and a blocked member waits forever.
Configure `notify` and the service pushes those moments to you:

```yaml
settings:
  notify:
    url: "https://hooks.slack.com/services/T…/B…/x"   # any webhook endpoint
    format: slack        # "json" (default) | "slack" ({"text": …})
    secret: "s3cret"     # optional; sent as X-Evva-Webhook-Secret
    events: [gates, errors, alerts]    # default: all three groups
    command: "evva-notify"             # and/or local exec — JSON on stdin
    rate_limit: 12       # max sends per minute per space (default 12)
```

- **What notifies — three groups.** `gates`: a member waiting for approval
  or asking a question, once per gate — re-broadcasts and WS reconnects
  never re-page. `errors`: a run error, or a member paused at its iteration
  limit. `alerts`: the watchdog/breaker notices (stall, budget freeze, stale
  task, stale mailbox) — this wave also promotes them to `ops_alert` events,
  so they now appear in the console timeline and the durable chatlog too,
  instead of being mailbox-only.
- **Payload.** `json` sends `{space, spaceId, agent, kind, title, body, at,
  console}` — the console deep-link rides every notification; acting on a
  gate happens there (no approve-from-Slack this wave). `slack` folds the
  same content into `{"text": …}`, the shape Slack- and Discord-compatible
  webhooks eat. Bodies are capped (~500 chars) — a notification is a pager,
  not a transcript.
- **Best-effort by contract.** Non-blocking bounded queue, one retry after
  5 s, then drop-and-count — a dead endpoint shows up as `notifsDropped`
  climbing in `/metrics`, never as a slower swarm. The `rate_limit` token
  bucket caps the blast radius; when it suppresses, the next delivery is
  preceded by one "N notifications suppressed" notice, so silence is never
  ambiguous.
- **`command` mode.** The JSON payload arrives on stdin — a one-line desktop
  recipe: `command: "jq -r .title | xargs -I{} notify-send evva {}"`.
  Operator-authored manifest config (the `permission_mode: bypass` trust
  class); 15 s timeout, process tree killed.
- **Watch it.** `/metrics` carries `notifsSent` / `notifsDropped` /
  `notifsSuppressed`.

### Ledger retention (`retention_days` / `evva swarm vacuum`)

A 24/7 swarm accumulates messages and completed tasks without bound, and the
web/API reads slow down with the table size. Retention keeps the working set
small **without losing history**: eligible rows are first appended to
`<workdir>/.vero/archive/YYYY-MM.jsonl.gz` (bucketed by the row's own month),
then deleted and the database compacted.

What is eligible — and nothing else ever is:

- messages already **read**, where the read happened ≥ `retention_days` ago;
- tasks in the terminal **completed** state for ≥ `retention_days` —
  unless something that survives still references them (a message's
  `ref_task`, a child task's parent link): referenced tasks are kept.

Unread mail, claimed (in-flight) mail, and pending/running/suspended/verifying
tasks are untouchable, regardless of age.

It runs automatically **once per local day** (plus once at service start, to
catch up a machine that slept through midnight) whenever
`settings.retention_days` > 0 — the default is **30**; set `"0"` to keep the
old never-delete behavior. Manually, with a preview:

```bash
evva swarm vacuum my-eng-team --dry-run     # counts only, touches nothing
evva swarm vacuum my-eng-team               # archive + delete at the configured window
evva swarm vacuum my-eng-team --days 7      # override the window for this pass
```

Reading the archive later: it is gzipped JSON-lines —
`zcat .vero/archive/2026-06.jsonl.gz | jq .` (each line carries `kind`
message/task plus the full original row). For scale: a 100k-message backlog
makes the messages API take ~300 ms per call; after a vacuum it is back to
sub-millisecond, and the pass itself took ~1.2 s.

### Flight recorder & metrics (event log / `/metrics`)

Every event the web UI sees (run/turn lifecycle, tool calls + results,
approvals, errors — everything except token-level streaming chunks) is also
appended to `<workdir>/.vero/events/YYYY-MM-DD.jsonl`, one ts-stamped JSON
line each. "What happened at 03:00 last night?" is now a grep, even after a
restart:

```bash
grep '03:0' .vero/events/2026-06-09.jsonl | jq '.event.Kind' | sort | uniq -c
```

Files rotate daily; old days are pruned by the same `retention_days` window
(`"0"` keeps them forever). `event_log: false` switches the recorder off. The
recorder can never slow the swarm: it drops lines (and counts the drops)
rather than ever blocking the event pump.

Live counters, per member, since the space started:

```bash
curl -s -H "Authorization: Bearer $(cat ~/.evva/service/token)" \
  http://127.0.0.1:8888/api/swarm/<ref>/metrics | jq .
```

returns `uptimeSecs`, `eventsLogged` / `eventsDropped` (the recorder),
`hintsDropped` (mailbox backpressure — a climbing value means a chronically
backed-up member), and per-member `wakesMessage` / `wakesTimer` / `runs` /
`aborts`, a run-duration histogram (`runSeconds`: lt10s / lt1m / lt10m /
gte10m), and a **per-run token-cost histogram** (`runTokens`: lt1k / lt10k /
lt50k / gte50k, RP-28 — fed from the same delta as the RP-13 daily meter, so
the two can never disagree). Plain JSON — point your own exporter at it if
you want history.

**Per-run token metering (RP-28)**: every `run_end` event carries that run's
own token cost (`Usage`: InputTokens / OutputTokens / CacheReadTokens /
CacheCreationTokens — whether the conversation history hit the prompt cache
is visible at a glance; when the provider reports no usage the field is
absent, never fabricated). "What does the watchdog's per-wake cost look like
this week — is it creeping up with history length?" is one jq:

```bash
jq -r 'select(.event.Kind=="run_end" and .event.AgentID=="<member-agent-id>")
  | .event.RunEnd.Usage | "\(.InputTokens) \(.CacheReadTokens)"' \
  .vero/events/2026-06-*.jsonl
```

### Autostart (survive crashes and reboots)

`evva service start` daemonizes but nothing brings it back after a crash or a
reboot — hand that job to the platform's supervisor:

```bash
evva service install-unit     # writes the launchd plist (macOS) or systemd user unit (Linux)
```

…then run the activation command it prints (it never enables anything by
itself). The unit runs `evva service start --foreground` — the supervisor owns
the process, restarts it on failure, and the swarm resumes where it was
(sessions, unread mail, membership, alarms — the Restart & resume path below).
Under a supervisor, stop/start with `launchctl` / `systemctl --user`, not
`evva service stop` (the supervisor would just restart it). Templates for
manual setup: [docs/user-guide/en/service-autostart.md](../../user-guide/en/service-autostart.md).

For monitors: `GET /healthz` needs no token and answers JSON —

```json
{"status":"ok","version":"v1.5.0","uptimeSecs":86400,
 "spacesRunning":1,"spacesStopped":0,"membersActive":3,"membersFrozen":0}
```

`spacesRunning` or `membersActive` at 0 is "alive but idle"; counts only, no
names — per-space detail stays behind the token.

### Restart & resume

The swarm is crash-safe. After `evva service stop` (or a crash) and a fresh
`evva service start`:

- every previously-registered space is **rebuilt from disk**,
- each member's **transcript resumes** where it left off,
- **unread messages are re-queued** (nothing lost),
- the **task ledger is intact** (a task left `running` is still `running`),
- **frozen members come back frozen**,
- **runtime schedule changes hold** — a cadence the leader `schedule_set` (or
  you edited in the web) survives the restart, and a cleared schedule **stays
  cleared** even if the manifest still declares one. They live as per-member
  rows in the space's `.vero` ledger; `list_members` tags each crontab with
  its origin — `(manifest)` vs `(runtime, set 2026-06-11)` — so you can always
  tell whose hand set a cadence.

You don't do anything special — it just continues.

Members whose schedule was never touched at runtime keep following the
manifest — edit `evva-swarm.yml` while the service is down and the new cadence
applies on restart. To wipe ALL runtime schedule overrides and return the
whole space to the manifest as written, re-register it (`evva swarm rm` +
`evva swarm .`): a fresh register is read as exactly that intent. Operator
schedule edits from the web are also recorded in the event log as
`schedule_change` lines (the leader's own `schedule_set` calls are already
visible as tool events).

---

## 9. Running several swarms at once

The service is a **multi-space host** from day one. Register as many as you like,
each from its own directory:

```sh
cd ~/projects/web-team   && evva swarm .
cd ~/projects/data-team  && evva swarm .
evva swarm ls            # both listed, fully isolated
```

They share the one `:8888` process and web UI (pick between them in the space
picker) but **nothing else** — separate databases, buses, rosters, and names.
Stopping one never affects the other.

---

## 10. Security

- The service binds **`127.0.0.1` only** by default — it is not reachable from
  other machines. (Agents run shell and edit files, so the workstation is
  effectively remote-code-execution; keep it on loopback.)
- Every web/API request needs the **session token**. Since v1.5 it is a random
  secret minted on every `evva service start` (the fixed dev token `root` is
  gone), stored at `~/.evva/service/token` (0600). You normally never see it:
  a browser on the same machine logs in by itself (a loopback-only bootstrap
  endpoint hands it over), and the CLI reads the file. Rotation = restart.
- In `permission_mode: default`, write/shell-class tools route through the
  approval overlay — you stay in the loop. Use `bypass` only when you trust the
  task and the workdir. The stance can be tiered per member (§5.2): a real
  roster is usually "researchers on default, the execution desk on bypass with
  `permissions.json` deny rules as the backstop" — **deny still binds under
  bypass** (bypass silences prompts, not prohibitions).

### Exposing the workstation beyond this machine (`--allow-remote`)

By default a non-loopback bind **refuses to start**. To reach the workstation
from another device (LAN or behind a reverse proxy), opt in explicitly:

```bash
evva service start --addr 0.0.0.0:8888 --allow-remote
```

Know the threat model before you do: **whoever presents the session token is
the operator** — they can approve tool calls, message members, and therefore
run shell on this machine. In remote mode the loopback conveniences shut off:

- The FE auto-login bootstrap endpoint disappears (behind a proxy every caller
  would look local). Paste the token from `~/.evva/service/token` once per
  device, per service start.
- Webhook POSTs from other hosts are rejected unless the target space sets
  `settings.webhook_secret` (below).

Put TLS termination and any IP filtering in your reverse proxy — the service
itself stays plain HTTP and single-operator (no accounts, no RBAC).

### External-event webhook + `webhook_secret`

External apps can wake a member (default: the leader) by POSTing an event —
no session token involved:

```bash
curl -X POST http://127.0.0.1:8888/api/swarm/<space-id>/event \
  -H 'Content-Type: application/json' \
  -H 'X-Evva-Webhook-Secret: hunter2' \
  -d '{"title":"BTC spike","body":"vol>3sigma","source":"trader-engine",
       "idempotency_key":"evt-123"}'
```

Auth rules (RP-15):

| Space setting | Local caller (same machine) | Remote caller |
| --- | --- | --- |
| no `webhook_secret` | accepted (legacy loopback trust) | **401** |
| `webhook_secret` set | needs the matching header | needs the matching header |

Replies: new → 202, duplicate `idempotency_key` → 200, bad/missing secret →
401, unknown space → 404, stopped → 409. Bodies are capped at 64 KB.

---

## 11. Reference

### CLI

| Command | What it does |
| --- | --- |
| `evva service start` | Start the `:8888` host as a background daemon (mints + stores the token). Flags: `--addr <host:port>`, `--allow-remote` (required for any non-loopback addr). |
| `evva service status` | Report running/stopped, pid, address, token location. |
| `evva service stop` | Stop the daemon (spaces are preserved for the next start). |
| `evva swarm .` | Register the current directory's `evva-swarm.yml` as a new space. |
| `evva swarm ls` | List registered spaces. |
| `evva swarm stop <id>` | Stop (and drop) one space. |
| `evva swarm add <id> <member>` | Hot-load a worker (`agents/sub/<member>/`) into a space. |
| `evva swarm vacuum <ref> [--days N] [--dry-run]` | Archive-then-delete consumed history (RP-16); dry-run previews. |
| `evva swarm attach <ref> [member] [--addr h:p] [--token t]` | Open the terminal cockpit on a running space: attention-ordered roster, live member streams, tasks, answerable gates, composer, lifecycle keys. `q` detaches; the space keeps running. Needs a TTY (otherwise it prints the web URL). |
| `evva swarm send <ref> <member> <text\|->` | Message a member as the operator (sender=`user` — identical semantics to the web composer): an idle member wakes on it, a busy one folds it into its current run; prints the durable message id as the receipt. `-` reads the body from stdin (script pipelines); `member` may be the role `leader`. A typo'd name comes back with the valid-recipient list (RP-27). |

### Environment variables

| Var | Effect |
| --- | --- |
| `EVVA_SERVICE_ADDR` | Override the listen/target address (default `127.0.0.1:8888`). |
| `EVVA_SERVICE_HOME` | Override the runtime dir (default `<AppHome>/service/`: pidfile, token, addr, log). |
| `EVVA_SERVICE_ALLOW_REMOTE` | `1` = allow a non-loopback bind (what `--allow-remote` sets for the daemon child). |

### Runtime files (`~/.evva/service/`)

`evva-service.pid` · `token` · `addr` · `evva-service.log`

### `profile.yml` fields

| Field | Meaning |
| --- | --- |
| `model` | LLM model id for this member (override the default). |
| `effort` | `low` / `medium` / `high` / `ultra`. |
| `when_to_use` | One-line specialty shown in `list_members` / roster. |
| `inject_memory` | Load `EVVA.md` + the memory index into the prompt. |
| `advertise_skills` | List installed skills on the prompt. |
| `schedule.cron` | 5-field cron for a timer wake (e.g. `"*/5 * * * *"`). |
| `schedule.every` | Fixed interval instead of cron (e.g. `"30s"`, `"5m"`). |

### Schedule cron dialect

The swarm's cron is self-written and deliberately small. Five fields —
`minute hour day-of-month month day-of-week` — matched against the **system's
LOCAL wall clock**, minute resolution.

Supported per field: `*`, plain values (`5`), ranges (`9-17`), steps (`*/5`,
`9-17/2`), comma lists (`0,30`), and any mix (`0,15,30-45/5`). Day-of-week is
`0-7` with both 0 and 7 meaning Sunday. When BOTH day-of-month and day-of-week
are restricted, a day matches if **either** does (standard cron OR semantics).

NOT supported — the parser rejects these by name: a seconds field (6-field
specs), `@daily` / `@every` aliases, `L` / `W` / `#` / `?` specials, and `TZ=`
prefixes (the timezone is always system-local).

```
*/5 * * * *      every 5 minutes
0 17 * * 1-5     17:00 on weekdays
0 9,18 * * *     09:00 and 18:00 daily
0 3 1 * *        03:00 on the 1st of each month
```

### Swarm tool names (auto-injected by role)

These are added **automatically** based on the member's role — **never list them
in `active.yml`**. Leader: `task_create`, `task_assign`, `task_update_status`,
`task_verify`, `task_list`, `member_spawn`, `member_retire`,
`blackboard_write`, `worktree_merge` (only useful with
`worktree_isolation`, §8). Worker: `my_tasks`, `task_get`, `task_done`. Both:
`send_message`, `list_members`, `blackboard_read`. In `active.yml`
you list only the regular evva tools your member needs — `read`, `write`,
`edit`, `bash`, `grep`, `glob`, `tree`, `web_fetch`, …

---

## 12. Troubleshooting

| Symptom | Fix |
| --- | --- |
| `evva swarm .` says it can't reach the service | Start it first: `evva service start`. |
| `no evva-swarm.yml in <dir>` | Run `evva swarm .` from the directory that has the manifest. |
| Web says "unauthorized" | Paste the token from `~/.evva/service/token` (or re-copy from `evva service start`). |
| A member never does anything | Check it's `active` (not frozen) in the roster, and that it has the tools it needs in `tools/active.yml`. |
| Workers can't change task status | By design — only the leader writes the ledger; workers report via `send_message`. |
| `evva service start` refuses ("already running") | One already runs; `evva service status` to confirm, `stop` to replace. |
| Port already in use | `EVVA_SERVICE_ADDR=127.0.0.1:9999 evva service start`. |

---

## 13. 0 → Hero recap

1. **Start the host:** `evva service start` (note the token).
2. **Scaffold a swarm:** an `evva-swarm.yml` + `agents/main/<leader>/` +
   `agents/sub/<workers>/`, each with `system_prompt.md` (+ optional
   `profile.yml`, `tools/active.yml`).
3. **Register:** `evva swarm .`.
4. **Drive:** open `:8888`, paste the token, talk to the leader (or any member)
   in the Member Console.
5. **Watch:** the Team Board moves `pending → running → verifying → completed`;
   the roster shows who's busy.
6. **Operate:** add/freeze/suspend members; run several swarms side by side.
7. **Relax:** stop and restart freely — the swarm resumes exactly where it was.

That's the whole arc. Welcome to the swarm.
