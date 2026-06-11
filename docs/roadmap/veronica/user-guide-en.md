# evva swarm & evva service — User Guide (0 → Hero)

> Languages: **English** ｜ [中文](user-guide-zh.md)
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
| Task tools | `task_create`, `task_assign`, `task_update_status`, `task_verify`, `task_list`, `proposal_list`, `proposal_accept`, `proposal_decline` | `my_tasks`, `task_get` (read-only), `task_propose` (file work) |
| Talk | `send_message`, `list_members` | `send_message`, `list_members` |
| Writes the ledger? | **Yes** (sole writer) | No |

The leader decomposes a goal into tasks, **pushes** each to a worker, and
verifies the result before reporting to you. Workers can't write task status —
they report progress with `send_message`, and the leader moves the task forward.

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

settings:
  permission_mode: default  # default | accept_edits | plan | bypass
  max_iterations: 50        # per-run loop cap for each member
  # —— operational fuses (opt-in; see §8) ——
  # daily_budget_tokens: 2000000  # per-member daily token cap (in+out); 0/omit = unlimited
  # budget_stay_frozen: false     # true = a budget freeze survives the day rollover
  # stall_threshold: 10m          # alert when a member is busy longer; "0" off (omit = 10m)
  # stall_hard_timeout: 30m       # auto-cancel a run busy longer; 0/omit = off
  # task_stale_threshold: 24h     # remind when a task sits in running/verifying longer; "0" off (omit = 24h)
  # mailbox_stale_threshold: 30m  # alert when the oldest unread ages past this; "0" off (omit = 30m)
  # webhook_secret: "hunter2"     # require X-Evva-Webhook-Secret on event POSTs (see §10)
  # retention_days: 30            # archive+delete consumed history after N days; "0" = keep forever
  # event_log: true               # mirror events to .vero/events/ (daily jsonl); false = off
```

- **Member names are unique** within a space (no replicas — give each a distinct
  name).
- `permission_mode`:
  - `default` — dangerous tools (writes, shell) ask for approval; you approve
    them in the web UI.
  - `bypass` — no prompts; the agents run fully autonomously. Powerful, but only
    use it when you trust the workdir and the task.

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
# Optional: per-member token budget override (see §8): >0 own cap, -1 exempt, omit = inherit
# budget_tokens: 250000
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
> lives at [`example-swarm/`](example-swarm/) — copy it out, `evva swarm .`, and
> follow its README.

---

## 7. How collaboration actually works (under the hood)

- **Auto-injected protocol + tools.** Every member is given its role's
  collaboration **tools** *and* a collaboration **protocol** (prepended to its
  system prompt) automatically — the leader gets the task-ledger tools + the
  leader protocol, a worker gets the read-only task tools + the worker protocol.
  You never declare these in `system_prompt.md` or `active.yml`; you only write
  the persona. (That's why the bullets below "just work" without you teaching
  them.)
- **Task ledger (5 states).** Leader `task_create` → `task_assign` (→ `running`,
  notifies the worker) → worker works + reports → leader `task_update_status`
  → `verifying` → `task_verify` approve (→ `completed`) or reject (→ back to
  `running`). The state machine is enforced in SQLite; illegal moves are
  rejected.
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
    reasoning at the next step, so urgent mail ("stop now") lands immediately
    (*drain B*).
- **Timer wake.** A member with a `schedule` in its `profile.yml` is Run on that
  cadence (a heartbeat / self-check). Members with no wake source sit idle and
  **burn no tokens**.
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
- **Halt all** — the emergency stop: cancel every in-flight run in the space.

### Cost & stall fuses (token budget / run watchdog)

A team running 24/7 needs two fuses. Both live under `settings:` in
`evva-swarm.yml`, apply per space, and stay fully out of the way until set.

**Daily token budget (the budget breaker)**

```yaml
settings:
  daily_budget_tokens: 2000000   # per-member in+out token cap per LOCAL day; 0 = unlimited
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
grep '03:0' .vero/events/2026-06-09.jsonl | jq '.event.event.Kind' | sort | uniq -c
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
`aborts` plus a run-duration histogram (`runSeconds`: lt10s / lt1m / lt10m /
gte10m). Plain JSON — point your own exporter at it if you want history.

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
  task and the workdir.

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
`task_verify`, `task_list`. Worker: `my_tasks`, `task_get`. Both: `send_message`,
`list_members`. In `active.yml` you list only the regular evva tools your member
needs — `read`, `write`, `edit`, `bash`, `grep`, `glob`, `tree`, `web_fetch`, …

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
