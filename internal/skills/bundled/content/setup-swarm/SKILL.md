# setup-swarm Guide a user through creating an evva swarm from scratch

Use this skill when the user wants to CREATE or SCAFFOLD a multi-agent swarm team: "set up a swarm", "create a swarm team", "scaffold a swarm", "add swarm agents", "how do I make a swarm", "I need a team of agents", "setup-swarm". Also use it when the user asks for help UNDERSTANDING swarm structure — the directory layout, the manifest, or the agent definition files. Do NOT use for swarm operations (`evva swarm run/stop/rm/reset`) on an already-registered space, nor for configuring skills on existing members.

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
  # ... more workers as needed

settings:
  permission_mode: default    # default | bypass | accept_edits (see below)
  max_iterations: 40          # per-run iteration cap before the agent pauses
```

**Permission modes:**
- `bypass` — agents run hands-off; file writes and shell execute with no approval. Use in trusted/throwaway folders or CI-like pipelines.
- `default` — every file write and shell command pops an approval in the web UI (click Allow, or "Always allow" to allow a tool for the session).
- `accept_edits` — file writes auto-allow; shell commands still ask.

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

Schedule `cron` and `every` are mutually exclusive; `prompt` is the synthetic user message injected on each wake.

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

**`system_prompt.md`** — the agent's persona. First line must be a markdown heading (`# Name`). The swarm runtime auto-injects the collaboration protocol (team communication tools, task semantics, member list), so the user's prompt only needs identity + domain instructions:

```markdown
# Builder

You implement code changes: read the task spec, write the implementation,
run the tests, and report back when done. Favor simple, working solutions.
```

**`tools/active.yml`** — tools exposed to the LLM in every turn. The swarm auto-injects its own collaboration tools (`task_create`/`task_assign`/`task_verify` for the leader, `my_tasks`/`task_get` for workers, plus `send_message`/`list_members` for all, plus `skill`). Only list DOMAIN tools here:

```yaml
# Leader's active.yml
- read
- write
- edit
- glob
- grep
- bash
```

```yaml
# Worker's active.yml
- read
- write
- edit
- glob
- grep
- bash
```

**`tools/deferr.yml`** — tools the LLM knows about by name but must fetch with `tool_search` before using. Use for rarely-needed tools to keep the active set small:

```yaml
- web_search
- web_fetch
- task_list
```

## Step 3 — Optional: add per-member skills

Create a `skills/<name>/SKILL.md` under the member's directory. The swarm web UI can also add/remove skills at runtime (the operator clicks "skills" on a member in the roster). The first line is always `# <name> <description>`:

```markdown
# standup Run a daily standup and summarize blockers.

Ask each active member for status, collect blockers, and post a short summary to
the user.
```

The skill tool is auto-injected; `advertise_skills: true` in `profile.yml` lists the skill in the prompt.

## Step 4 — Start the service and register the swarm

These are shell commands the user runs in their terminal — NOT something you execute (they may need to start the service in their own session):

1. **Start the service:** `evva service start` — a background process on `127.0.0.1:8888`. The first run prints a session token; the web UI asks for it on first connect. Default token in dev mode is `root`.

2. **Register the swarm:** `cd <workdir> && evva swarm .` — reads `evva-swarm.yml`, validates it, and POSTs the workdir to the service, which builds all agents and starts the team.

3. **Open the web UI:** the register output prints a URL like `http://127.0.0.1:8888/?space=<id>` — open it in a browser.

Other swarm commands:
- `evva swarm ls` — list all registered spaces
- `evva swarm run <ref>` — restart a stopped space
- `evva swarm stop <ref>` — stop (freeze all agents)
- `evva swarm rm <ref>` — forget a space
- `evva swarm reset <ref>` — wipe ledger + clear context, same id
- `evva swarm add <ref> <member>` — hot-load a new member from `agents/sub/<member>/`

## Step 5 — Verify the team works

After the user opens the web UI:

1. **Pick a member** in the left roster → its console opens in the center pane.
2. **Send a message** to the leader with the first task (e.g. "build a hello-world CLI").
3. **Watch the board** — tasks move through pending → running → verifying → completed.
4. **Approve tool calls** if using `default` permission mode — the approval overlay pops when an agent wants to write files or run shell.
5. **Check the timeline** tab for the message flow between members.

## Reference: complete minimal example

A working 3-agent swarm (leader + builder + reviewer) in a folder called `my-swarm/`:

```
my-swarm/
├── evva-swarm.yml
└── agents/
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
- The swarm runtime auto-injects: system collaboration prompt, per-role tools (task_create/task_assign/task_verify for leader; my_tasks/task_get for workers; send_message/list_members for all), the skill tool, and `advertise_skills`.
- NEVER touch the `.vero/` directory — it's the swarm's internal SQLite ledger. Deleting it resets the space.
- The service binds `127.0.0.1:8888` by default — never expose it to a network without proper auth.
- If `evva swarm .` says "service unreachable", the user needs to run `evva service start` first.
