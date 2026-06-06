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
| Task tools | `task_create`, `task_assign`, `task_update_status`, `task_verify`, `task_list` | `my_tasks`, `task_get` (read-only) |
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
#   cron: "*/5 * * * *"     # every 5 minutes
#   # every: "30s"          # or a fixed interval
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

### Restart & resume

The swarm is crash-safe. After `evva service stop` (or a crash) and a fresh
`evva service start`:

- every previously-registered space is **rebuilt from disk**,
- each member's **transcript resumes** where it left off,
- **unread messages are re-queued** (nothing lost),
- the **task ledger is intact** (a task left `running` is still `running`),
- **frozen members come back frozen**.

You don't do anything special — it just continues.

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
- Every web/API request needs the **session token** (printed on start, stored at
  `~/.evva/service/token`). The browser asks you to paste it once.
- In `permission_mode: default`, write/shell-class tools route through the
  approval overlay — you stay in the loop. Use `bypass` only when you trust the
  task and the workdir.

---

## 11. Reference

### CLI

| Command | What it does |
| --- | --- |
| `evva service start` | Start the `:8888` host as a background daemon (prints the token). |
| `evva service status` | Report running/stopped, pid, address, token location. |
| `evva service stop` | Stop the daemon (spaces are preserved for the next start). |
| `evva swarm .` | Register the current directory's `evva-swarm.yml` as a new space. |
| `evva swarm ls` | List registered spaces. |
| `evva swarm stop <id>` | Stop (and drop) one space. |
| `evva swarm add <id> <member>` | Hot-load a worker (`agents/sub/<member>/`) into a space. |

### Environment variables

| Var | Effect |
| --- | --- |
| `EVVA_SERVICE_ADDR` | Override the listen/target address (default `127.0.0.1:8888`). |
| `EVVA_SERVICE_HOME` | Override the runtime dir (default `<AppHome>/service/`: pidfile, token, addr, log). |

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
