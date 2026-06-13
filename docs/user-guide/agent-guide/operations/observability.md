# Observability: seeing what the team is doing

A swarm runs unattended for long stretches, so visibility matters. evva exposes four windows: the
roster meters, the metrics view, the event log, and the memory viewer.

## Roster meters (live cost + health)

The left roster in the web console shows, per member:

- **Status** — idle / running / verifying / frozen / suspended.
- **Context meter** — how full the member's context window is (a member near the top will compact
  soon, losing in-run scratch memory — another reason durable state belongs in its `memory/` or a
  state file).
- **Daily token spend vs. budget** — input+output tokens used today against its `budget_tokens` cap.
  A member at its cap is **frozen** until the day rolls over (or until you unfreeze it). See
  [../building/scheduling-and-guardrails.md](../building/scheduling-and-guardrails.md#token-budgets--cost-control).
- **Pending ⏰ alarms** — one-shot wakes scheduled for this member.

## Metrics view (per-member usage over time)

The space metrics view adds per-member **run-token histograms** — the shape of cost over time. This is
where you answer "is my watchdog's per-run cost creeping up?" or "which member is burning the budget?".

## Event log (forensics + exact usage)

With `settings.event_log: true` (the default), the space mirrors its event stream to
`.vero/events/*.jsonl` — one file per day. Every `run_end` line carries that run's **exact token
usage**. Use it for:

- Cost accounting beyond the live meters (sum usage per member per day).
- Forensics after an incident (what happened, in order, with timestamps).
- Feeding external dashboards (it's plain jsonl).

`.vero/events/` is **read-only to you** — read it, don't edit it. (`.vero/` as a whole is the runtime's;
never modify it by hand.)

## Memory viewer (what the team has learned)

Each member curates a private `memory/` directory (auto-created at first boot). The web console shows
each member's memory **read-only**: open a member → **Memory**. This is the transparency window onto
what the team has durably learned — decisions, durable facts, open leads.

- Members write their own memory; a deny fence stops one member writing another's, even in `bypass`.
- If something in a member's memory is wrong and must go, the operator edits it **on disk** (the viewer
  is read-only).

## Proposals tab (bottom-up work)

Workers raise trackable work with `task_propose`; the leader accepts (`proposal_accept`, which
atomically creates the task) or declines with a note. The **Proposals** tab is the operator's
read-only window onto that queue — useful for seeing what the team *wants* to do that the leader
hasn't actioned yet.

## Watchdog alerts (pushed to you)

The watchdog timers (see
[../building/scheduling-and-guardrails.md](../building/scheduling-and-guardrails.md#watchdogs--liveness-alerts))
mail the operator (and the leader) when work is stuck, each with a suggested action:

| Alert | Means | Typical fix |
| --- | --- | --- |
| **Stall** | one run has been busy too long | check the member's console; consider `stall_hard_timeout` |
| **Stale task** | a task is parked in `running`/`verifying` | nudge the leader; the assignee may be stuck |
| **Mailbox backlog** | a member isn't draining its mail | it's likely frozen/suspended — unfreeze or investigate |

If they page too often, tune the thresholds up in `settings:`.

## See also

- The knobs that produce this data: [../building/scheduling-and-guardrails.md](../building/scheduling-and-guardrails.md).
- Interpreting a stuck team: [troubleshooting.md](troubleshooting.md).
