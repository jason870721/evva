# `evva-swarm.yml` — the manifest reference

The manifest declares the team and the space-wide guardrails. It lives at the root of the workdir.
The service reads it on `evva swarm .`, validates it, and builds the team. **Re-registering applies a
changed manifest and resets runtime schedules to its baseline.**

A validation error rejects the *whole* manifest at register time (a typo in `effort` or a bad cron
fails fast), so you never get a half-built space.

## Full shape

```yaml
name: my-eng-team          # optional — the service assigns a handle if omitted
workdir: .                 # always "." — means "this directory"

leader:
  agent: lead              # must match a folder under agents/main/

workers:
  - agent: builder         # must match a folder under agents/sub/
  - agent: reviewer
    permission_mode: bypass     # optional per-member override
    budget_tokens: 500000       # optional per-member daily token cap
    model: claude-sonnet-4-6    # optional per-member model
    effort: high                # optional per-member thinking effort
    when_to_use: "Adversarial review of findings."
    schedule:                   # optional recurring wake
      cron: "*/30 * * * *"
      prompt: "scan the latest build for regressions"

settings:
  permission_mode: default        # space-wide trust stance
  max_iterations: 40              # per-run iteration cap before the agent pauses
  daily_budget_tokens: 0          # per-member daily token cap (0 = unlimited)
  budget_stay_frozen: false       # keep a budget-frozen member frozen across the day rollover
  stall_threshold: 10m            # one run busy this long → stall alert
  stall_hard_timeout: 1h          # one run busy this long → auto-cancel (default off)
  task_stale_threshold: 24h       # a task parked in running/verifying this long → alert
  mailbox_stale_threshold: 30m    # unread mail aging this long → alert
  retention_days: 30              # archive+delete old read mail / done tasks daily
  event_log: true                 # mirror events to .vero/events/*.jsonl
  webhook_secret: ""              # require X-Evva-Webhook-Secret on external event POSTs
```

Everything except `leader.agent` (or `leader.persona`) is optional.

---

## Top-level fields

| Field | Type | Default | Notes |
| --- | --- | --- | --- |
| `name` | string | service-assigned | The space name. Docker-style: omit it and the service generates a handle, or pass `evva swarm . --name <n>`. Used as a `<ref>` in CLI commands. |
| `workdir` | string | `.` | Always `.` — the swarm lives in the directory containing the manifest. |
| `leader` | member | — | **Required.** Exactly one leader. |
| `workers` | list of members | `[]` | Zero or more workers. A leader-only swarm is legal (rare). |
| `settings` | object | see below | Space-wide guardrails. All optional. |

## Member entry

Each `leader:` and `workers[]:` entry is a *member*. A member is named in exactly **one** of two ways:

| Field | Meaning |
| --- | --- |
| `agent: <name>` | A **directory member** — its definition is at `agents/main/<name>/` (leader) or `agents/sub/<name>/` (worker). The usual case. |
| `persona: <name>` | A **persona member** — references a registry main-tier persona instead of a workdir directory (see [Persona members](#persona-members)). |

Setting both, or neither, rejects the manifest.

Optional per-member fields (all override the corresponding space/profile value):

| Field | Type | Values | Notes |
| --- | --- | --- | --- |
| `model` | string | a model id | Overrides the member's `profile.yml` model. For a persona member this is the only place to pin the model. |
| `effort` | string | `low` \| `medium` \| `high` \| `ultra` | Thinking depth. Invalid value rejects the manifest. |
| `when_to_use` | string | — | One line shown to the leader so it knows when to delegate to this member. |
| `schedule` | object | `cron`/`every` + `prompt` | A recurring wake — see [Schedules](#schedules). |
| `budget_tokens` | int | `>0` own cap · `-1` exempt · `0` inherit | Per-member daily token cap; overrides `settings.daily_budget_tokens`. `-1` exempts this member even when the space sets a default. |
| `permission_mode` | string | `default`/`accept_edits`/`plan`/`bypass` · `""` inherit | Per-member trust stance; overrides `settings.permission_mode`. |

> **Why `permission_mode`, `budget_tokens`, `model`, `effort`, and `when_to_use` can sit in the
> manifest:** trust tiering and team composition are decisions about the *whole roster*, so they read
> best in one version-controlled file. A manifest value is authoritative over the member's own
> `profile.yml`. (`permission_mode` and `budget_tokens` are manifest-only — they have no
> `profile.yml` equivalent.)

## Settings

All optional; sensible defaults. `"0"` disables a duration/day knob.

| Field | Type | Default | What it does |
| --- | --- | --- | --- |
| `permission_mode` | string | `default` | Space-wide trust stance. See [permissions.md](permissions.md). |
| `max_iterations` | int | runtime default | Per-run loop cap before a member pauses and yields. Raise for tool-heavy work (the code-review example uses `80`). |
| `daily_budget_tokens` | int | `0` (unlimited) | Per-member daily cap on input+output tokens (local day). A member that crosses it freezes until the day rolls over. Negative normalizes to `0`. |
| `budget_stay_frozen` | bool | `false` | If `true`, a budget-frozen member stays frozen past the day rollover until you unfreeze it manually. |
| `stall_threshold` | duration | `10m` | One run busy longer than this (and not waiting on a human) raises a one-per-run stall alert to the operator + leader. `"0"` disables. |
| `stall_hard_timeout` | duration | off | If set, auto-cancels a run busy longer than this; the run's mail is unclaimed so it retries on the next wake. Leave off until thresholds are tuned. |
| `task_stale_threshold` | duration | `24h` | A task parked in `running`/`verifying` longer than this raises one reminder. `suspended` is exempt (deliberate parking). `"0"` disables. |
| `mailbox_stale_threshold` | duration | `30m` | A member whose oldest *unread* message exceeds this age raises an alert — normally never fires, so when it does it means a member is frozen/forgotten. `"0"` disables. |
| `retention_days` | int (days) | `30` | Daily vacuum: read mail older than this and tasks completed at least this long ago are archived to `.vero/archive/` and deleted. `"0"` disables (never deletes history). |
| `event_log` | bool | `true` | Mirror the event stream to `.vero/events/*.jsonl` (forensics + per-run cost). `false` turns it off. |
| `webhook_secret` | string | none | If set, every external-event POST must carry it as the `X-Evva-Webhook-Secret` header. Without a secret, only same-machine callers are accepted. See [../operations/external-events.md](../operations/external-events.md). |

Durations use Go syntax: `30s`, `10m`, `1h`, `24h`.

## Schedules

Attach a `schedule:` block to any member to make it wake on a timer. Exactly one of `cron`/`every`:

```yaml
workers:
  - agent: qa
    schedule:
      cron: "*/30 * * * *"      # standard 5-field cron — every 30 minutes
      prompt: "scan the latest build for regressions"
  - agent: monitor
    schedule:
      every: "60s"               # Go duration string — alternative to cron
      prompt: "check system health"
```

`prompt` is the synthetic "user" message injected on each wake. A manifest schedule is authoritative
over the agent's own `profile.yml` schedule — the team's cadence lives in one file.

> Schedules changed at *runtime* (the leader's `schedule_set` tool, or the web schedule editor) are
> **durable** across a service restart. But **re-registering with `evva swarm .` resets every runtime
> schedule back to the manifest's baseline.** Treat the manifest as the source of truth for cadence.

For one-shot (non-recurring) wakes, use alarms instead — see
[scheduling-and-guardrails.md](scheduling-and-guardrails.md).

## Persona members

A persona member references one of evva's registry main-tier personas (the kind you pick with
`/profile`) instead of a workdir directory:

```yaml
workers:
  - persona: nono          # a registry persona (e.g. a financial manager) joins the team
    when_to_use: "Costing and budget questions."
    model: claude-sonnet-4-6
    effort: high
```

A persona member has **no** `agents/sub/<name>/` directory — the space resolves and composes the
persona's definition at assembly time. Because there's no `profile.yml`, the manifest fields
(`model`, `effort`, `when_to_use`, `schedule`, `budget_tokens`, `permission_mode`) are the *only*
place to pin its config. Use this to drop a cross-domain specialist (the `evva → nono` pattern) into
a team without re-authoring it. Most teams use directory members; reach for persona members when you
want a whole existing persona, with its own tools and skills, as a teammate.

## Editing the manifest later

- After any manifest change, re-run `evva swarm .` to apply it (and reset runtime schedules).
- The web UI's add/remove member writes back to the manifest, so dynamic membership survives a
  restart — but re-emitting the file drops hand-written comments/formatting.

## Validation rules (enforced at register)

- Exactly one leader; `leader.agent` (or `leader.persona`) is required.
- Every member name is non-empty and **unique** within the space — no replicas, no two members
  sharing a name (including the leader).
- The `agent:` name must match a folder under `agents/main/` (leader) or `agents/sub/` (worker).
- `effort`, `permission_mode`, and the schedule `cron`/`every` are validated; a bad value rejects the
  whole manifest.

## See also

- The member directory each `agent:` points at: [agent-definition.md](agent-definition.md).
- Guardrails in depth: [permissions.md](permissions.md),
  [scheduling-and-guardrails.md](scheduling-and-guardrails.md).
