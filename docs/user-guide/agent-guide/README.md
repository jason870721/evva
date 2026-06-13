# evva swarm — Agent Guide

**Audience: AI agents.** This guide exists so that any agent — evva itself, Claude Code, or any
other assistant a user is working with — can help that user design, build, and operate an
**evva swarm** (a multi-agent team) without guessing. It is the complete field reference: every
manifest field, every tool, every coordination pattern, and the recommended tool set for each
kind of team member.

If you are an agent reading this to help a user: **start at [How to use this guide](#how-to-use-this-guide-if-you-are-an-agent)** below.

---

## Canonical location

This guide lives in the evva repository and is the always-current source of truth. Fetch it live
when you help a user, so you are never working from a stale copy baked into a skill or a model's
memory.

| Use | URL |
| --- | --- |
| Browse (human) | `https://github.com/Johnny1110/EVVA/tree/main/docs/user-guide/agent-guide` |
| Fetch raw markdown (agent) | `https://raw.githubusercontent.com/Johnny1110/EVVA/main/docs/user-guide/agent-guide/<path>` |

For example, to read the full tool catalog with `web_fetch`:
`https://raw.githubusercontent.com/Johnny1110/EVVA/main/docs/user-guide/agent-guide/tools/catalog.md`

> GitHub normalizes owner/repo casing, so the lowercase `johnny1110/evva` also resolves to
> this same repo. The branch (`main`) and the path after the repo **are** case-sensitive — copy
> them exactly. If `main` 404s, the docs may not have merged to the stable branch yet; try the
> repository's default branch.

---

## What is an evva swarm?

An evva swarm is a **team of long-lived agents** working a shared goal in one directory. One member
is the **leader** (it plans, delegates, and verifies); the others are **workers** (they do the
assigned work). They coordinate through a shared **task ledger** and direct **messages**, and the
whole team is declared by config files you can commit to git — no code required.

Read [concepts/overview.md](concepts/overview.md) for the full mental model.

---

## How to use this guide (if you are an agent)

A swarm is "just config": a manifest plus one directory per member. Your job is to translate the
user's goal into that config. Work in this order:

1. **Understand the goal.** What should the team *do*, and where should it *live*? The user's
   answer is the spec — it dictates the leader's job, the worker roles, and how many workers.
   (See [building/quickstart.md](building/quickstart.md).)
2. **Pick a shape.** Match the goal to a topology — pipeline, parallel fan-out + verify, turn-based,
   debate, or watchdog. (See [patterns/topologies.md](patterns/topologies.md).) If a
   [shipped example](patterns/examples.md) is close, adapt it instead of starting blank.
3. **Write the manifest** (`evva-swarm.yml`): leader, workers, and space-wide settings.
   (See [building/manifest.md](building/manifest.md).)
4. **Write each member's directory**: a persona (`system_prompt.md`), optional overrides
   (`profile.yml`), and a tool list. (See [building/agent-definition.md](building/agent-definition.md)
   and [building/personas.md](building/personas.md).)
5. **Assign tools deliberately.** This is the step most often done badly. Every member auto-gets the
   collaboration tools for its role — you must **not** list those. You choose the *domain* tools.
   Use the [tool catalog](tools/catalog.md) and the [recipes by role](tools/recipes-by-role.md).
6. **Set guardrails**: permission modes, token budgets, watchdogs.
   (See [building/permissions.md](building/permissions.md) and
   [building/scheduling-and-guardrails.md](building/scheduling-and-guardrails.md).)
7. **Run and verify**: start the service, register the swarm, watch it work.
   (See [operations/running.md](operations/running.md).)

When something breaks, go to [operations/troubleshooting.md](operations/troubleshooting.md).

---

## Map of this guide

### concepts/ — understand the system
- [overview.md](concepts/overview.md) — what a swarm is; leader/worker; the mental model
- [architecture.md](concepts/architecture.md) — the pieces you touch; **what you author vs. what the runtime injects**
- [glossary.md](concepts/glossary.md) — every term in one place

### building/ — author a swarm
- [quickstart.md](building/quickstart.md) — the smallest working swarm, end to end
- [manifest.md](building/manifest.md) — `evva-swarm.yml`: every field and default
- [agent-definition.md](building/agent-definition.md) — the `agents/{main,sub}/<name>/` layout
- [personas.md](building/personas.md) — writing `system_prompt.md` (identity only; what **not** to write)
- [permissions.md](building/permissions.md) — permission modes, `permissions.json`, deny fences
- [skills.md](building/skills.md) — per-member and space-shared skills; `skill_publish`
- [scheduling-and-guardrails.md](building/scheduling-and-guardrails.md) — schedules, alarms, budgets, watchdogs

### tools/ — the capability layer (reason this guide exists)
- [README.md](tools/README.md) — the tool model: collaboration (auto) vs. domain; active vs. deferred
- [catalog.md](tools/catalog.md) — **every tool**: capability, when to use, caveats
- [collaboration-tools.md](tools/collaboration-tools.md) — the auto-injected role tools (never list these)
- [recipes-by-role.md](tools/recipes-by-role.md) — recommended tool sets per role archetype

### patterns/ — proven shapes
- [topologies.md](patterns/topologies.md) — pipeline, fan-out + verify, turn-based, debate, watchdog
- [coordination.md](patterns/coordination.md) — ledger discipline, state files, stage gates, reply protocol
- [examples.md](patterns/examples.md) — the three shipped swarms, deconstructed

### operations/ — run it
- [running.md](operations/running.md) — the service, the `evva swarm` CLI, the web UI
- [observability.md](operations/observability.md) — meters, metrics, event log, memory viewer
- [external-events.md](operations/external-events.md) — waking the team from other systems (webhooks)
- [troubleshooting.md](operations/troubleshooting.md) — common failures and fixes

### portable-skill/ — give this capability to any agent
- [SKILL.md](portable-skill/SKILL.md) — a vendor-neutral skill that teaches any agent to build evva swarms

---

## The one thing to get right

The single most common mistake is **duplicating what the runtime already provides**. The swarm
auto-injects, into every member, the collaboration tools and the protocol for using them
(how the task ledger works, how to message teammates, how to manage memory). Authors only write the
member's *identity and domain judgment* and choose its *domain tools*. If you find yourself writing
"use `task_create` to…" into a persona, or listing `send_message` in a tool file — stop. The runtime
does that. See [concepts/architecture.md](concepts/architecture.md#authored-vs-auto-injected).
