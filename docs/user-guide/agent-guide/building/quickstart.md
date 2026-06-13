# Quickstart: the smallest working swarm

This builds a complete 3-member swarm — a **leader**, a **builder**, and a **reviewer** — from an
empty folder, and runs it. Copy the files verbatim, then adapt. Total: one manifest + three member
directories.

## Before you write anything: two questions

The user's answers are the spec. Ask them (use your `ask_user_question` tool if you have one):

1. **What should this team DO?** This dictates the leader's job, the worker roles, and how many
   workers. "Take a GitHub issue and ship a PR" → a larger team (pm, designer, backend, frontend,
   qa). "Build something small, then review it" → leader + builder + reviewer (this quickstart).
   *Don't invent members the user didn't ask for.*
2. **Where should it live?** A swarm is a directory. It should be git-tracked or at least persistent
   (the `.vero/` ledger lives there). If they don't have one, create it: `mkdir -p my-swarm`.

If a [shipped example](../patterns/examples.md) matches the goal, copy and adapt it instead of
starting from scratch.

## The directory you're about to create

```
my-swarm/
├── evva-swarm.yml
└── agents/
    ├── main/
    │   └── lead/
    │       ├── system_prompt.md
    │       └── tools/active.yml
    └── sub/
        ├── builder/
        │   ├── system_prompt.md
        │   └── tools/active.yml
        └── reviewer/
            ├── system_prompt.md
            └── tools/active.yml
```

`profile.yml`, `tools/deferr.yml`, and `skills/` are all optional — omitted here for the minimum.

## 1. The manifest — `my-swarm/evva-swarm.yml`

```yaml
name: my-swarm
workdir: .

leader:
  agent: lead

workers:
  - agent: builder
  - agent: reviewer

settings:
  permission_mode: default   # every file write / shell command asks for approval in the web UI
  max_iterations: 40
```

## 2. The leader — `agents/main/lead/`

`system_prompt.md`:

```markdown
# Lead

You orchestrate a small build-and-review team. Break the user's goal into concrete tasks and
delegate each to the right member: implementation work goes to `builder`, review work to `reviewer`.
Verify every result yourself before reporting back to the operator. You do not write code yourself —
you plan, delegate, and verify.

Run the loop: create a task for the build, assign it to builder; when builder reports done, create a
review task and assign it to reviewer; when both are verified, summarize the outcome for the operator.
```

`tools/active.yml` (domain tools only — the runtime injects all the `task_*`/`send_message` tools):

```yaml
- read
- bash
```

The leader here only needs to *read* results and run the occasional `bash` check (e.g. confirm a file
exists). It does not need `write`/`edit` — it isn't doing the work.

## 3. The builder — `agents/sub/builder/`

`system_prompt.md`:

```markdown
# Builder

You implement code changes. Read the task spec, write the implementation, run the tests, and report
back to the leader when it's done and green. Favor simple, working solutions over clever ones.
```

`tools/active.yml`:

```yaml
- read
- write
- edit
- glob
- grep
- bash
```

## 4. The reviewer — `agents/sub/reviewer/`

`system_prompt.md`:

```markdown
# Reviewer

You review the builder's work for correctness and clarity. Read the changed files, check them against
the task spec, run the tests yourself, and report findings to the leader: what's solid, what needs
fixing, and the exact file:line for each issue. You suggest changes; you do not make them.
```

`tools/active.yml`:

```yaml
- read
- glob
- grep
- bash
```

The reviewer is read-only by tool choice (no `write`/`edit`) — it inspects and reports.

## 5. Run it

These are commands the **user** runs in their terminal (you may not be able to run them for them):

```bash
evva service start          # start the local daemon (127.0.0.1:8888); prints the session token location
cd my-swarm
evva swarm .                # read evva-swarm.yml, build the team, start the space; prints a web URL
```

Open the printed URL (like `http://127.0.0.1:8888/?space=<id>`) in a browser on the same machine.

## 6. Verify it works

1. In the web console, pick **lead** in the left roster — its console opens.
2. Send the leader a first goal, e.g. *"Build a hello-world CLI in Go in this folder, then have it
   reviewed."*
3. Watch the **task board**: tasks move `pending → running → verifying → completed`.
4. Because `permission_mode: default`, an **approval overlay** pops when builder wants to write files
   or run shell — click *Allow* (or *Always allow* for the session).
5. Open the **timeline** to see the messages flowing between lead, builder, and reviewer.

A fast iteration loop from the terminal: `evva swarm send my-swarm lead "status report, please"` —
the leader wakes and replies in the web console.

## Where to go from here

- Add guardrails (budgets, watchdogs, autonomous `bypass`): [scheduling-and-guardrails.md](scheduling-and-guardrails.md)
  and [permissions.md](permissions.md).
- Give a member exactly the right tools: [../tools/recipes-by-role.md](../tools/recipes-by-role.md).
- Pick a better-fitting team shape: [../patterns/topologies.md](../patterns/topologies.md).
- The full manifest reference: [manifest.md](manifest.md).
