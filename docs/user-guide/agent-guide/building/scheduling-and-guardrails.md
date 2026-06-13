# Scheduling and guardrails

This page covers the time-based wakes (schedules, alarms) and the operational safety knobs (token
budgets, watchdogs). All of the budget/watchdog knobs live under `settings:` in the manifest; see
[manifest.md](manifest.md#settings) for the table — here we explain *when and why* to use each.

## Schedules — recurring wakes

A schedule makes a member wake on a cadence with a synthetic prompt. Declare it in the manifest
(preferred — the team's cadence reads in one file):

```yaml
workers:
  - agent: monitor
    schedule:
      cron: "*/30 * * * *"        # 5-field cron — every 30 min
      prompt: "scan system health; alert the leader on anything red"
  - agent: digest
    schedule:
      every: "24h"                 # Go duration — alternative to cron
      prompt: "compile and post the daily digest"
```

- Exactly one of `cron` / `every`.
- `prompt` is injected as a "user" message on each tick; write it so the member knows exactly what the
  wake is for.
- The leader can also create/adjust schedules at runtime with `schedule_set` / `schedule_clear`, and
  the web UI has a schedule editor. **Runtime schedules are durable across restarts, but
  `evva swarm .` resets them to the manifest baseline.**

Use a schedule for standing duties: patrols, periodic reviews, digests, polling.

## Alarms — one-shot wakes

An alarm fires **once**, at an absolute time. Every member can set one for itself; the leader can also
target a teammate. Alarms are a *tool*, not a manifest field — members use them during a run:

- `alarm_set { at: "2026-09-11 12:31:50", prompt: "re-check the deploy" }` — wake the caller once.
- Leader: `alarm_set { at, prompt, member: "qa" }` — wake a specific teammate once.
- `alarm_clear { id }` — cancel a pending alarm. Pending ⏰ alarms show in `list_members`.

**Schedule vs. alarm:** recurring cadence → schedule (leader's `schedule_set`); single future moment →
alarm (`alarm_set`). "Review the run in 30 minutes" is an alarm; "review every run" is a schedule.

## Token budgets — cost control

`settings.daily_budget_tokens` caps each member's input+output tokens per local day. A member that
crosses its cap **freezes** until the day rolls over.

```yaml
settings:
  daily_budget_tokens: 1000000     # 1M tokens/member/day; 0 = unlimited
  budget_stay_frozen: false        # auto-unfreeze at day rollover (true = require manual unfreeze)
workers:
  - agent: watchdog
    budget_tokens: 200000          # this member: tighter own cap
  - agent: lead
    budget_tokens: -1              # this member: exempt (unlimited) even though the space sets a default
```

- Member-level `budget_tokens`: `>0` = own cap, `-1` = exempt, `0`/omitted = inherit the space default.
- A high-frequency scheduled member (a watchdog waking every few minutes) is the classic budget
  risk — give it an explicit cap and watch its per-run cost in the metrics view.
- `budget_stay_frozen: true` is for "if it blew the budget, I want to look before it resumes."

## Watchdogs — liveness alerts

Three independent timers surface stuck work. Each raises a one-per-episode alert to the **operator and
the leader**, with a suggested action. They are alert-only by default (except the optional hard
timeout).

| Knob | Default | Watches |
| --- | --- | --- |
| `stall_threshold` | `10m` | **One hung run** — a member busy this long without waiting on a human. |
| `stall_hard_timeout` | off | Same, but **auto-cancels** the run (its mail is unclaimed and retries next wake). Turn on once thresholds are tuned. |
| `task_stale_threshold` | `24h` | **A parked task** — sitting in `running`/`verifying` too long. `suspended` is exempt. |
| `mailbox_stale_threshold` | `30m` | **Undrained mail** — a member's oldest unread message aging out. Should never fire normally; when it does, a member is frozen or the wake chain regressed. |

```yaml
settings:
  stall_threshold: 15m
  stall_hard_timeout: 2h       # auto-cancel runaway runs after 2h
  task_stale_threshold: 24h
  mailbox_stale_threshold: 30m
```

Tune these up if they page too often, down if you want earlier warning. Set any to `"0"` to disable.

## Retention — keeping the ledger lean

`settings.retention_days` (default `30`) runs a daily vacuum: read mail older than the window and
tasks completed at least that long ago are archived to `.vero/archive/` and deleted from the live
ledger. This keeps the web/API working set small on a 24/7 swarm while the archive keeps full history.
Run it on demand with `evva swarm vacuum <ref> [--days N] [--dry-run]`. Set `"0"` to never delete.

## Event log — forensics and cost

`settings.event_log` (default `true`) mirrors the space's events to `.vero/events/*.jsonl`. Every
`run_end` line carries that run's exact token usage — the raw data for "is my watchdog's per-run cost
creeping up?" See [../operations/observability.md](../operations/observability.md).

## A fully-guarded autonomous worker

Putting it together — an unattended watchdog that's fenced, budgeted, and scheduled:

```yaml
# evva-swarm.yml
settings:
  permission_mode: default
  daily_budget_tokens: 0
  stall_threshold: 10m
workers:
  - agent: watchdog
    permission_mode: bypass          # runs hands-off…
    budget_tokens: 300000            # …with a daily cost cap…
    schedule:                        # …on a cadence…
      every: "15m"
      prompt: "scan for regressions; alert the leader on anything red"
```

```json
// agents/sub/watchdog/permissions.json — …behind a deny fence
{ "permissions": { "deny": ["Bash(rm:*)", "Bash(git push:*)", "Write(/**)"] ,
                   "allow": ["Write(./alerts/**)"] } }
```

## See also

- The manifest field table: [manifest.md](manifest.md#settings).
- Permission modes and the deny fence in depth: [permissions.md](permissions.md).
- Reading the cost/liveness data the guardrails produce:
  [../operations/observability.md](../operations/observability.md).
