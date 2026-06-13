# The agent definition directory

Every member named in the manifest — leader and workers alike — has a directory with the same
structure. The leader/worker distinction is in the manifest, not here.

```
agents/
├── main/
│   └── <leader-name>/        # e.g. lead — the leader lives under main/
│       ├── system_prompt.md  # REQUIRED — the persona (identity + judgment)
│       ├── profile.yml       # optional — model, effort, when_to_use, memory, schedule
│       ├── tools/
│       │   ├── active.yml    # optional — domain tools exposed every turn
│       │   └── deferr.yml    # optional — domain tools fetched on demand via tool_search
│       ├── skills/           # optional — this member's private skills
│       └── permissions.json  # optional — fine-grained allow/deny rules
└── sub/
    ├── <worker-1-name>/      # same structure as the leader
    └── <worker-2-name>/
```

Only `system_prompt.md` is required. Everything else has a sensible empty/zero default.

---

## `system_prompt.md` (required)

The member's **persona**: its identity and domain judgment. The first line must be a markdown
heading naming the member (`# Builder`). Keep it to *who the member is and how it should think* — the
runtime appends all the operational scaffolding (communication rules, the role protocol, tool
mechanics, memory discipline).

```markdown
# Builder

You implement code changes: read the task spec, write the implementation, run the tests, and report
back when done. Favor simple, working solutions.
```

This deserves its own page — see [personas.md](personas.md) for what to write, what **not** to write,
and why the leader's persona is the swarm's skeleton.

## `profile.yml` (optional)

Per-member overrides. Every field is optional; absent means "inherit the service/space default."

```yaml
model: claude-sonnet-4-6        # override the LLM model for this member
effort: high                    # low | medium | high | ultra — thinking depth
when_to_use: "Backend implementation: APIs, DB schema, migrations."
inject_memory: true             # inject the user's EVVA.md / USER_PROFILE.md into the prompt
advertise_skills: true          # list this member's skills in its prompt
schedule:                       # a recurring wake (manifest schedule overrides this)
  every: "5m"
  prompt: "poll the queue"
```

| Field | Type | Notes |
| --- | --- | --- |
| `model` | string | LLM model id. A manifest `model:` for this member overrides it. |
| `effort` | `low`/`medium`/`high`/`ultra` | Thinking depth. Manifest `effort:` overrides it. |
| `when_to_use` | string | Shown to the leader for delegation decisions. Manifest `when_to_use:` overrides it. |
| `inject_memory` | bool | Inject the operator's personal memory files (`EVVA.md`/`USER_PROFILE.md`) into this member. Usually `false` for workers. |
| `advertise_skills` | bool | If `true`, list the member's skills in its prompt so it knows they exist. |
| `schedule` | object | A recurring wake. **The manifest schedule wins** if both are set — prefer declaring cadence in the manifest. |

> Note: `permission_mode` and `budget_tokens` are **manifest-only** — they are team-composition
> decisions and have no `profile.yml` field. See [manifest.md](manifest.md#member-entry).

## `tools/active.yml` and `tools/deferr.yml` (optional)

Flat YAML lists of **domain** tool names. This is where you decide what each member can *do*.

```yaml
# tools/active.yml — exposed to the model every turn
- read
- write
- edit
- glob
- grep
- bash
```

```yaml
# tools/deferr.yml — known by name, loaded on demand with tool_search
- web_search
- web_fetch
- repl
```

Three rules that trip people up:

1. **List only domain tools.** The swarm auto-injects every collaboration tool
   (`send_message`, `list_members`, the `task_*` set, `alarm_*`, and for the leader `schedule_*`,
   `proposal_*`, `skill_publish`). Listing those here is redundant and confusing — see
   [../tools/collaboration-tools.md](../tools/collaboration-tools.md).
2. **`active` vs. `deferr` is about turn-cost, not capability.** Active tools cost prompt space every
   turn; deferred tools cost nothing until the model fetches them. Put the everyday tools in `active`
   and the occasional ones in `deferr`. A member with any deferred tools automatically gets
   `tool_search` wired in.
3. **The `skill` tool is always available** — you don't list it.

For *which* tools a member should get, see [../tools/catalog.md](../tools/catalog.md) (every tool) and
[../tools/recipes-by-role.md](../tools/recipes-by-role.md) (recommended sets per role).

## `skills/` (optional)

Per-member skills live at `<member>/skills/<skill-name>/SKILL.md`. The first line is
`# <name> <description>`; the body is instructions. The `skill` tool is auto-injected;
`advertise_skills: true` in `profile.yml` lists them in the prompt. Full details: [skills.md](skills.md).

## `permissions.json` (optional)

Claude-Code-compatible fine-grained allow/deny rules for this member, layered under its
`permission_mode`. **Deny rules bind in every mode, including `bypass`.** This is the seam for the
"bypass + deny fence" pattern. Full details: [permissions.md](permissions.md).

## `memory/` (automatic — do not create)

At first boot the runtime creates `<member>/memory/` with a `MEMORY.md` index — the member's private
long-term memory. Don't scaffold or pre-fill it; members tend their own notes, and a deny fence stops
one member writing into another's memory even in `bypass` mode. The web UI shows each member's memory
read-only.

## A complete minimal member

```
agents/sub/reviewer/
├── system_prompt.md      # "# Reviewer\n\nYou review the builder's work…"
└── tools/
    └── active.yml        # - read / - glob / - grep / - bash
```

`profile.yml`, `deferr.yml`, and `skills/` omitted — all optional. That's a valid, complete worker.

## See also

- [personas.md](personas.md) — writing the persona well.
- [manifest.md](manifest.md) — how the manifest references this directory.
- [../tools/README.md](../tools/README.md) — the tool model.
