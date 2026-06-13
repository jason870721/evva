# Permissions: modes, rules, and the deny fence

A swarm's members write files and run shell commands. Permissions decide which of those side effects
run freely, which ask the operator first, and which are hard-blocked. There are two layers: the
coarse **permission mode** and the fine-grained **`permissions.json`** rules.

## Layer 1 — the permission mode (coarse stance)

Set space-wide with `settings.permission_mode`, overridable per member with a member-level
`permission_mode:` in the manifest.

| Mode | File writes | Shell commands | Use when |
| --- | --- | --- | --- |
| `default` | ask | ask | The safe default. Every write/shell pops an approval in the web UI. |
| `accept_edits` | auto-allow | ask | You trust edits but want to watch shell. |
| `plan` | blocked (read-only) | blocked | Analysis only; rarely what you want for a *working* member. |
| `bypass` | auto-allow | auto-allow | Trusted/throwaway folders, CI-like pipelines, unattended watchdogs. |

In `default` mode the operator clicks **Allow** (or **Always allow** to allow that tool for the rest
of the session) in an approval overlay. In `bypass` mode there is no overlay — the member runs
hands-off.

> **Coordination tools never ask.** The leader's task-ledger writes (`task_assign`, `task_verify`,
> `task_update_status`, …) and `skill_publish` are team coordination, not file/shell side effects, so
> they execute without approval in every mode. The real permission boundary is a worker's *file and
> shell* writes. `skill_publish` only ever writes inside the space's own `agents/skills/` dir.

### The common pattern

`default` for the team, plus `bypass` for one trusted high-frequency worker:

```yaml
settings:
  permission_mode: default     # the team asks before writing/running
workers:
  - agent: watchdog
    permission_mode: bypass     # this one runs unattended
```

## Layer 2 — `permissions.json` (fine-grained rules)

Drop a Claude-Code-compatible `permissions.json` next to a member's definition
(`agents/{main,sub}/<name>/permissions.json`) for rule-level control:

```json
{
  "permissions": {
    "allow": ["Bash(git diff:*)", "Bash(go test:*)"],
    "deny":  ["Bash(rm -rf:*)", "Write(/etc/**)", "Bash(curl:*)"],
    "ask":   ["Bash(git push:*)"]
  }
}
```

How the rules interact with the mode:

- **`allow` rules open holes in `default`** — listed actions run without an approval even though the
  mode would otherwise ask.
- **`deny` rules bind in EVERY mode — `bypass` included.** This is the key property: a denied action
  is hard-blocked no matter how permissive the mode.
- **`ask` rules deliberately do NOT fire in `bypass`** — an unattended member must never stall waiting
  for a human who isn't there.

## The deny fence (the supported autonomous-but-fenced pattern)

When a member must run unattended but you want hard limits, prefer **`bypass` + deny rules** over
writing cautionary prose into the persona. Prose is advisory and can be reasoned around; a deny rule
is enforced by the gate.

```yaml
# manifest
workers:
  - agent: trader
    permission_mode: bypass
```

```json
// agents/sub/trader/permissions.json — bypass, but fenced
{
  "permissions": {
    "deny": [
      "Bash(rm:*)",
      "Bash(git push:*)",
      "Write(/**)",
      "Edit(/**)"
    ],
    "allow": ["Write(./reports/**)", "Edit(./reports/**)"]
  }
}
```

That member runs hands-off but can only write under `./reports/` and can never delete or push.

## Built-in fences (always on)

Some boundaries hold regardless of your config:

- **Memory isolation** — a member can read any member's `memory/`, but writes outside its **own**
  memory directory are rejected, even in `bypass`.
- **`.vero/` is off-limits** to authoring — it's the runtime's ledger.

## Choosing a stance — a quick decision guide

- Touching a repo you care about, operator present → `default`.
- Throwaway/sandbox folder, or you want speed and will review the git diff → `bypass`.
- A member that only reads and reports (reviewer, researcher) → its *tools* are read-only anyway, so
  the mode barely matters; `bypass` is fine and avoids needless approvals.
- An unattended member with real power (watchdog, trader, ops) → `bypass` + a deny fence.

## See also

- Where these fields sit in the manifest: [manifest.md](manifest.md#settings).
- Token budgets and watchdogs (the *cost* and *liveness* guardrails):
  [scheduling-and-guardrails.md](scheduling-and-guardrails.md).
