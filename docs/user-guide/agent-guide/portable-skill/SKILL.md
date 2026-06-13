# build-evva-swarm Build a multi-agent evva swarm from a user's goal — scaffold the manifest, member personas, and per-member tool sets, consulting the live evva agent-guide.

> **Portable skill.** This file teaches *any* agent (evva, Claude Code, Cursor, Codex, Gemini, or a
> custom assistant) how to help a user build an **evva swarm** — a team of collaborating agents
> declared entirely in config. It is *hybrid*: the procedure and a working example are embedded here
> so it works offline, and it points at the always-current agent-guide for the complete reference.
> See **[Installing this skill](#installing-this-skill-into-your-agent)** at the bottom for the
> one-line tweak each platform needs.

## When to use this skill

Use it when a user wants to **create or scaffold a multi-agent evva swarm**: "set up an evva swarm",
"build me a team of agents", "scaffold a swarm to do X", "I need a leader + workers for Y". Do **not**
use it for runtime operations on an already-registered space (`evva swarm run/stop/rm/reset`) — those
are CLI commands, not authoring.

## The live reference (fetch this — it's the source of truth)

An evva swarm changes over time; this skill is a condensed snapshot. **Before and during a build, use
your web-fetch tool to read the live agent-guide** so you work from the current manifest fields, the
complete tool catalog, and the latest patterns.

| Use | URL |
| --- | --- |
| Browse (human) | `https://github.com/Johnny1110/EVVA/tree/main/docs/user-guide/agent-guide` |
| Fetch raw markdown (you) | `https://raw.githubusercontent.com/Johnny1110/EVVA/main/docs/user-guide/agent-guide/<path>` |

Start by fetching the index: `…/docs/user-guide/agent-guide/README.md`. Then fetch the specific pages
you need (paths below). GitHub normalizes owner/repo casing; the branch (`main`) and path are
case-sensitive.

**What to fetch, when:**

| You're deciding… | Fetch |
| --- | --- |
| the team shape | `patterns/topologies.md`, `patterns/examples.md` |
| the manifest fields | `building/manifest.md` |
| how to write personas | `building/personas.md` |
| **which tools each member gets** | `tools/catalog.md`, `tools/recipes-by-role.md` |
| what tools are auto-injected (don't list) | `tools/collaboration-tools.md` |
| permissions / budgets / schedules | `building/permissions.md`, `building/scheduling-and-guardrails.md` |
| running & debugging | `operations/running.md`, `operations/troubleshooting.md` |

If you have no web access, the embedded procedure and example below are enough for a basic swarm; say
so to the user and recommend they review the live guide for advanced features.

## Mental model (the 30-second version)

An evva swarm is a **team of long-lived agents in one directory**, declared by config — no code. One
**leader** plans, delegates, and verifies; **workers** do assigned work. They coordinate through a
shared **task ledger** and direct **messages**. Two facts drive everything you author:

1. **The runtime auto-injects all coordination.** Every member gets, by role, the collaboration tools
   (`send_message`, `list_members`, the `task_*` ledger, `alarm_*`; the leader also gets
   `schedule_*`, `proposal_*`, `skill_publish`) **and** the protocol for using them, the memory
   discipline, and the two-channel communication rule. **You never list these tools and never explain
   them in a persona.**
2. **You author three things:** the **manifest** (who's on the team + guardrails), each member's
   **persona** (`system_prompt.md` — identity and judgment only), and each member's **domain tools**
   (the file/shell/web/etc. tools it actually needs).

> Two communication channels, easy to get wrong: a member's **output text** goes to the human
> operator; the **`send_message` tool** goes to teammates. A worker that finishes must `send_message`
> the leader — printing "done" talks to the operator, and the work stalls.

## The build procedure

1. **Get the spec from the user.** Ask two questions: *(a) What should the team DO?* (this sets the
   leader's job, the worker roles, and the count) and *(b) Where should it live?* (a git-tracked or
   persistent folder — the runtime stores its `.vero/` ledger there). The user's answer is the spec;
   don't invent members they didn't ask for.
2. **Pick a shape** (fetch `patterns/topologies.md`): pipeline, parallel fan-out + verify, turn-based,
   debate, or watchdog. If a shipped example (`patterns/examples.md`) is close, **copy and adapt it**
   instead of starting blank.
3. **Write `evva-swarm.yml`** (fetch `building/manifest.md` for every field): `name`, `workdir: .`,
   `leader.agent`, `workers[].agent`, and `settings` (permission mode, budgets, watchdogs).
4. **Write each member directory** under `agents/main/<leader>/` and `agents/sub/<worker>/`:
   `system_prompt.md` (required) + optional `profile.yml` + `tools/active.yml` (+ `tools/deferr.yml`).
5. **Write personas** (fetch `building/personas.md`): identity + domain judgment only. The **leader's**
   persona is the skeleton — give it the team map, the coordination discipline (consult order, stage
   gates), a state-file format with action-bound update triggers, and a reply/downgrade protocol.
   Workers are short.
6. **Assign domain tools per member** (fetch `tools/catalog.md` + `tools/recipes-by-role.md`): give the
   *least power that does the job*. A reviewer with no `write`/`edit` literally can't edit. **List only
   domain tools — collaboration tools are injected.**
7. **Set guardrails** (fetch `building/permissions.md`, `building/scheduling-and-guardrails.md`):
   `default` mode when the operator is present; `bypass` + a `permissions.json` **deny fence** for an
   unattended member; `budget_tokens` caps; `schedule:` blocks for standing duties.
8. **Tell the user how to run it** (these are *their* terminal commands):
   `evva service start` → `cd <workdir> && evva swarm .` → open the printed `http://127.0.0.1:8888/?space=<id>`
   URL → send the leader the first goal → watch the task board. Re-run `evva swarm .` after any edit.

## Embedded minimal example (works without web access)

A complete 3-member swarm — leader + builder + reviewer:

```
my-swarm/
├── evva-swarm.yml
└── agents/
    ├── main/lead/{system_prompt.md, tools/active.yml}
    └── sub/{builder,reviewer}/{system_prompt.md, tools/active.yml}
```

`evva-swarm.yml`:
```yaml
name: my-swarm
workdir: .
leader:
  agent: lead
workers:
  - agent: builder
  - agent: reviewer
settings:
  permission_mode: default     # every file write / shell asks in the web UI; use bypass for hands-off
  max_iterations: 40
```

`agents/main/lead/system_prompt.md`:
```markdown
# Lead

You orchestrate a build-and-review team. Break the user's goal into concrete tasks and delegate:
implementation to `builder`, review to `reviewer`. Verify every result yourself (read the output;
a verbal "done" is not verification) before reporting to the operator. You plan and verify; you do
not write code. End each task you assign with: "When done, reply to me with send_message and include
the file paths." If a member goes silent, re-ask once, then proceed and note the gap.
```
`agents/main/lead/tools/active.yml`:
```yaml
- read
- bash
```

`agents/sub/builder/system_prompt.md`:
```markdown
# Builder

You implement code changes: read the task spec, write the implementation, run the tests, and report
back to the leader with send_message when it's green. Favor simple, working solutions. Report once,
then stop and wait.
```
`agents/sub/builder/tools/active.yml`:
```yaml
- read
- write
- edit
- glob
- grep
- bash
```

`agents/sub/reviewer/system_prompt.md`:
```markdown
# Reviewer

You review the builder's work for correctness and clarity. Read the changed files, check them against
the task spec, run the tests, and report findings to the leader with send_message — file:line for each
issue. You suggest changes; you do not make them.
```
`agents/sub/reviewer/tools/active.yml`:
```yaml
- read
- glob
- grep
- bash
```

Notice: **no `send_message`/`task_*` in any tool file** (injected), and the reviewer has no
`write`/`edit` (read-only by construction). Adapt names, personas, and tools to the user's real goal.

## Hard rules (most common mistakes)

- ❌ Never list collaboration tools (`send_message`, `task_create`, `list_members`, …) in a tool file —
  they're auto-injected by role.
- ❌ Never explain tool mechanics, the message channels, or memory rules in a persona — auto-injected.
- ✅ List only **domain** tools; give the least power that does the job.
- ✅ The `agent:` name in the manifest must match the member's folder under `agents/main|sub/`.
- ✅ Member names are unique within a space; exactly one leader.
- ✅ Re-run `evva swarm .` after any manifest/agent edit. A `profile.yml` model/effort change needs
  `evva swarm reset <ref>`.
- ⚠️ Never edit or delete `.vero/` — it's the runtime's ledger; deleting it resets the space.

## Installing this skill into your agent

This file uses evva's skill format (first line `# <name> <description>`). To use it elsewhere:

- **evva** — drop this folder into a skills directory (per-member `agents/{main,sub}/<m>/skills/` or
  space-shared `agents/skills/`), or your user skills dir. No change needed.
- **Claude Code / Claude Agent SDK** — prepend YAML frontmatter and save as `SKILL.md` in your skills
  directory (e.g. `~/.claude/skills/build-evva-swarm/SKILL.md`):
  ```yaml
  ---
  name: build-evva-swarm
  description: Build a multi-agent evva swarm from a user's goal — scaffold the manifest, member personas, and per-member tool sets, consulting the live evva agent-guide. Use when the user wants to create/scaffold an evva swarm or a team of agents.
  ---
  ```
  The existing `# build-evva-swarm …` line then acts as a normal H1 — harmless.
- **Any other agent** — paste this file's body into the agent's system prompt or a tool-instruction
  block, and ensure it has a **web-fetch** capability so it can read the live agent-guide.

Whatever the platform, the agent needs a way to **fetch URLs** to get the full, current reference.
