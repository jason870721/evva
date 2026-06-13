# Troubleshooting

Symptom → likely cause → fix. Ordered roughly by how often each comes up.

## Registration and startup

**`evva swarm .` says "service unreachable."**
The daemon isn't running. → `evva service start`, then retry. Check with `evva service status`.

**The manifest is rejected at register time.**
A validation error fails the *whole* manifest fast (by design). Read the message — common causes:
- Duplicate member name (including leader vs. a worker sharing a name). Names must be unique.
- `agent: <name>` doesn't match a folder under `agents/main/` (leader) or `agents/sub/` (worker).
- Invalid `effort` (must be `low`/`medium`/`high`/`ultra`) or `permission_mode` (must be
  `default`/`accept_edits`/`plan`/`bypass`).
- A malformed schedule `cron`/`every`, or both set at once.
- Missing `leader.agent` (or `leader.persona`).

**A member fails to build:** `system_prompt.md` is missing or empty — it's the one required file in a
member directory.

## Members and messaging

**A worker says "done" but the leader never reacts.**
The worker reported via its **output text** (which only the operator sees) instead of `send_message`
(the teammate channel). → The runtime injects this rule, so usually it's a transient model slip; if it
recurs, check the persona isn't *contradicting* the injected protocol, and that the worker's persona
reinforces "report to the leader with `send_message` when done." See
[../concepts/overview.md](../concepts/overview.md#two-ways-members-talk).

**A message dead-letters (nobody responds, no error).**
It was addressed to a name that isn't a member — e.g. `to: "leader"` when the leader's actual name is
`lead`. → Members should resolve names with `list_members` (the leader is "the member whose role is
leader"). The runtime guards obvious slips, but persona text that hard-codes a wrong name will miss.

**A member won't wake / its mailbox is backing up.**
- It's **frozen** (hit its `budget_tokens` cap) → wait for the day rollover, or unfreeze it; if
  `budget_stay_frozen: true`, it requires a manual unfreeze.
- It's **suspended** (a task was parked) → resume it (leader `task_update_status`).
- If neither and the mailbox-stale alert fired, the wake chain may have regressed — `evva swarm stop`
  then `run` the space, or `reset` if it persists.

## Tools and capabilities

**A member can't find a tool it "should" have.**
- The tool is **deferred** (in `tools/deferr.yml`): the model must `tool_search` to load its schema
  first — that's expected. The runtime wires `tool_search` in automatically for members with deferred
  tools.
- You listed a **collaboration tool** (`send_message`, `task_*`, …) in `active.yml`: remove it — those
  are injected by role, not declared. See [../tools/collaboration-tools.md](../tools/collaboration-tools.md).
- You tried to assign a **single-session tool** (`config`, `enter_plan_mode`, `cron_*`, …): these
  aren't for swarm members. Use the swarm-native equivalent. See
  [../tools/catalog.md](../tools/catalog.md#single-session-tools--usually-not-for-swarm-members).

**A worker can't write files (or every write asks for approval).**
- The member has no `write`/`edit` tool — add it to `tools/active.yml` if it genuinely needs to write.
- The space/member is in `default` mode → writes ask for approval; the operator must be present to
  click *Allow*, or switch the member to `accept_edits`/`bypass`.
- A `permissions.json` **deny** rule is blocking it — deny binds in *every* mode, including `bypass`.
  Check the member's rules. See [../building/permissions.md](../building/permissions.md).

**An unattended (`bypass`) member stalls instead of running.**
It hit an `ask` rule — but `ask` rules don't fire in `bypass` (an unattended member must never wait on
a human). If it truly stalled, look for a `default`-mode member upstream, or a tool that genuinely
errored.

## Configuration changes not taking effect

**Editing `profile.yml` (model/effort) does nothing.**
A member's profile is fixed when the space is **created**. → `evva swarm reset <ref>` (or re-register)
to rebuild members with the new profile. Note `reset` wipes the ledger; if you need to keep history,
plan accordingly.

**Schedule changes vanished.**
You edited a schedule at runtime (leader `schedule_set` or the web editor), then ran `evva swarm .` —
re-registering **resets every runtime schedule to the manifest baseline**. → Put the cadence you want
to keep in the manifest's `schedule:` blocks.

**A manifest edit didn't apply.**
You must re-register after editing: `evva swarm .`. For a *new member*, also create its
`agents/sub/<name>/` directory, then `evva swarm add <ref> <name>` (or re-register).

## Persona quality smells

**Members behave erratically around coordination.**
The persona is probably **duplicating injected content** (explaining `task_create`, the channels, or
memory rules) and drifting from it. → Strip operational how-tos from the persona; keep identity +
domain judgment (+ the leader's coordination *policy*). See [../building/personas.md](../building/personas.md).

**The leader loses the plot on long runs.**
No state file, or one with vague update rules. → Give the leader a state file with **action-bound**
triggers ("write it *before* every dispatch"). See
[../patterns/coordination.md](../patterns/coordination.md#4-the-state-file-is-the-leaders-reliable-memory).

**One silent member deadlocks the team.**
No downgrade rule. → Add "re-ask once, then proceed and mark it uncovered" to the leader's persona.

## The nuclear options

- **`evva swarm reset <ref>`** — same space id, fresh ledger + cleared context. Use after profile
  changes or when state is corrupted.
- **`evva swarm rm <ref>`** then **`evva swarm .`** — forget and rebuild from scratch.
- **Never delete `.vero/` by hand** — it's the live ledger; removing it resets the space and loses
  history that retention would otherwise have archived.

## See also

- [../building/manifest.md](../building/manifest.md), [../building/permissions.md](../building/permissions.md),
  [../building/personas.md](../building/personas.md) — the three things most worth getting right.
- [observability.md](observability.md) — the data that tells you *which* failure you're hitting.
