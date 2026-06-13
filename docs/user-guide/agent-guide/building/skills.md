# Skills in a swarm

A **skill** is a Markdown file of reusable instructions a member can invoke by name — a runbook, a
checklist, a report format. The `skill` tool is auto-injected into every member, so you never list it
in a tool file. Skills come in three flavors: per-member, space-shared, and leader-published.

## The file format

A skill is one `SKILL.md` whose **first line** is `# <name> <description>`; the rest is the body
(instructions, examples, whatever the member should follow when it invokes the skill).

```markdown
# standup Run a daily standup and summarize blockers.

Ask each active member for status, collect blockers, and post a short summary to the operator.
Order: leader first, then workers in roster order. Keep it to five lines.
```

The name is the token the member invokes; the description tells it when to reach for the skill.

## Per-member skills

Live under a member's own directory:

```
agents/sub/qa/skills/
└── regression-checklist/
    └── SKILL.md
```

Only that member sees them. Set `advertise_skills: true` in the member's `profile.yml` to have its
skills listed in its prompt (so it knows they exist without searching).

## Space-shared skills

Live at the workdir root and load into **every** member:

```
agents/skills/
├── house-style/
│   └── SKILL.md
└── output-format/
    └── SKILL.md
```

Use shared skills for team-wide know-how: coding conventions, the report format, a deployment runbook.
One copy, every member. **On a name clash, a member's private skill shadows the shared one** (local
overrides global) — the shadowing is surfaced as a load warning.

> Prefer a shared skill over copy-pasting the same procedure into several personas. The persona says
> *who the member is*; a shared skill carries *how the team does X*.

## Leader-published skills (runtime)

The leader can institutionalize a recurring procedure at runtime with its `skill_publish` tool:

```
skill_publish { name: "incident-report", description: "...", body: "..." }
```

This writes a new shared skill into `agents/skills/` and hot-reloads it into every member's catalog —
permanently, unlike a message that's forgotten at the next context compaction. The leader updates one
with `overwrite: true` when the procedure evolves.

Guidance for the leader (and for you when you write the leader's persona): **publish sparingly.** A
handful of well-named skills the team actually uses beats a dump of every passing thought. Good
candidates: a report format the leader keeps re-explaining, a review checklist, a how-to the team
repeats.

## Runtime management

The web console can add or remove skills while the swarm runs:

- Per-member: open a member in the roster → **skills** → add/remove. Hot-reloads that member.
- Space-shared: add/delete shared skills from the web UI → hot-reloads every member.

## When to use a skill vs. a persona vs. the manifest

| Put it in… | When it's… |
| --- | --- |
| The **persona** (`system_prompt.md`) | *Who* a member is and how it judges — stable identity. |
| A **shared skill** (`agents/skills/`) | *How the team does X* — a procedure several members follow. |
| A **per-member skill** | A procedure only one member needs, kept out of its persona for focus. |
| The **manifest** | Team composition and guardrails — not instructions. |

## See also

- The auto-injected `skill` tool and the rest of the tool model: [../tools/README.md](../tools/README.md).
- The leader's role, including `skill_publish`: [../tools/collaboration-tools.md](../tools/collaboration-tools.md#leader-only-tools).
