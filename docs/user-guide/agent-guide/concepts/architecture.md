# Architecture: the pieces you touch

You build a swarm by writing files. The runtime turns those files into live agents and injects a
large amount of operational scaffolding so you don't have to. Knowing the boundary — **authored vs.
auto-injected** — is the difference between a clean swarm and one full of duplicated, drifting
instructions.

## On-disk layout

A swarm is one directory (the *workdir*). Everything is config; nothing is code.

```
my-swarm/                          ← the workdir (commit this to git)
├── evva-swarm.yml                 ← the manifest: declares the team + settings
├── agents/
│   ├── skills/                    ← OPTIONAL space-shared skills (every member loads these)
│   │   └── <skill-name>/SKILL.md
│   ├── main/
│   │   └── <leader-name>/         ← exactly one leader dir
│   │       ├── system_prompt.md   ← REQUIRED: the member's persona (identity + judgment)
│   │       ├── profile.yml        ← optional: model, effort, when_to_use, memory, schedule
│   │       ├── tools/
│   │       │   ├── active.yml     ← optional: domain tools exposed every turn
│   │       │   └── deferr.yml     ← optional: domain tools fetched on demand
│   │       └── skills/            ← optional: this member's private skills
│   └── sub/
│       ├── <worker-1>/            ← same structure as the leader
│       └── <worker-2>/
└── .vero/                         ← runtime-owned ledger (SQLite) + events + archive — NEVER edit
```

Every member — leader and workers alike — uses the **same** directory structure. The
leader/worker distinction lives in the *manifest* (who is `leader:` vs. in `workers:`), not in the
folder layout.

## The runtime pieces

When you register a swarm, evva's **service** (a local daemon on `127.0.0.1:8888`) reads the manifest,
builds every member, and starts the team. The pieces, from your point of view:

| Piece | What it is | You interact via |
| --- | --- | --- |
| **Service** | The process host: a registry of spaces + an HTTP/WebSocket server | `evva service start`, then a browser |
| **Space** | One running swarm (leader + workers + bus + ledger) | `evva swarm .` to register; the web console |
| **Ledger** | The per-space SQLite store under `.vero/` (tasks, messages, proposals, schedules) | The task board / timeline in the web UI — never the file directly |
| **Bus** | The private message channel between members | `send_message`; visible in the timeline |
| **Roster** | The live "who's here and what are they doing" list | `list_members`; the left pane in the web UI |
| **Web console** | The operator's window: roster, member consoles, task board, timeline, metrics | a browser on the same machine |

You author the files; the service runs everything else. See
[../operations/running.md](../operations/running.md) for the commands.

## Authored vs. auto-injected

This is the most important section in the guide.

### What YOU author

1. **The manifest** (`evva-swarm.yml`) — who is on the team, and the space-wide guardrails.
2. **Each member's persona** (`system_prompt.md`) — its *identity* and *domain judgment*: "you are a
   backend engineer; favor simple, tested solutions; the API spec lives in `docs/`." For the leader,
   the persona also carries the team's **coordination policy** (the order members are consulted, the
   stage gates, the state-file format) — see [../building/personas.md](../building/personas.md).
3. **Each member's domain tools** (`tools/active.yml`, `tools/deferr.yml`) — the file/shell/web/etc.
   tools the member needs to do its job.
4. **Optional overrides** (`profile.yml`) and **skills** (`skills/`).

### What the RUNTIME injects (do NOT write these)

For every member, at construction, the runtime appends to the persona and wires up tools:

- **Swarm identity** — which space it's in, its own name, its role.
- **The communication protocol** — the two-channel rule (output text → operator; `send_message` →
  teammates) and how to handle messages from `user`, a teammate, or a `webhook`.
- **The collaboration tools, by role** — the leader gets the task-ledger writers
  (`task_create`/`task_assign`/`task_update_status`/`task_verify`/`task_list`), proposal handling,
  `schedule_set`/`schedule_clear`, and `skill_publish`; workers get `my_tasks`/`task_get`/`task_propose`;
  *everyone* gets `send_message`, `list_members`, and one-shot alarms (`alarm_set`/`alarm_clear`).
  See [../tools/collaboration-tools.md](../tools/collaboration-tools.md).
- **The role protocol** — the leader's plan→delegate→verify loop and the state machine; the worker's
  do-the-work-then-report-once discipline.
- **Tool-mechanics grounding** — a short how-to derived from the member's *actual* active/deferred
  tool list.
- **The long-term memory protocol** — but only for members that have a file-writing tool (`write`
  or `edit`); a read-only member can't keep memory, so it isn't taught the protocol.
- **The `skill` tool** and (if `advertise_skills: true`) a listing of the member's skills.

**Consequence:** never hand-write tool how-tos, the memory rules, the message-channel rules, or
prompt-injection warnings into a persona, and never list a collaboration tool in `active.yml`. They
would duplicate the injected text and drift from it over time. Your persona should read like a job
description, not an operations manual.

## Long-term memory (automatic)

At first boot, the runtime creates a `memory/` directory (with a `MEMORY.md` index) inside each
member's folder. That is the member's private, durable memory — it survives restarts and context
compaction. Members tend their own notes; a built-in deny fence stops one member from writing into
another's memory, even in `bypass` mode. You do not scaffold or pre-fill it.

## Prompt stability (why grounding has no clock)

A swarm can run for weeks. The injected identity section deliberately carries **no date/time** —
a drifting timestamp would invalidate the prompt cache on every rebuild. The current time arrives in
each *wake* message instead. This is why personas should also avoid baking in "today is…": state
durable facts, and let the wake message carry the clock.

## Next

- The exact manifest fields: [../building/manifest.md](../building/manifest.md).
- The member directory in detail: [../building/agent-definition.md](../building/agent-definition.md).
- The tool model: [../tools/README.md](../tools/README.md).
