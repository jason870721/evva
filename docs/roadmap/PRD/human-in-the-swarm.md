# PRD — Human-in-the-Swarm (people as roster members) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W32**, suggested horizon H4
> per [../long-range.md](../long-range.md) §3b. Depends on
> outbound-notifications (W1) for reach; composes with federation
> (W14) but does not require it.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> The swarm's design already says it out loud: "judgment stays
> human-shaped" (DWF), verify policies default to a *person-like*
> leader, and the operator approves plans on the web console. But the
> human is architecturally a *spectator with veto power* — not
> addressable, not assignable, not part of the graph. Real teams have
> tasks only a human can do: review this design, provide the API key,
> test on the physical device, make the call marketing won't let an
> agent make.
> **Reference source:** none — evva-native. (The classic
> human-in-the-loop pattern, but placed *inside* the roster rather
> than above it.)

---

## 1. TL;DR

A manifest member may be a person:

```yaml
members:
  - name: johnny
    kind: human                    # ← the new axis
    contact: [web, push]           # console inbox + notification channel
    duties: "design review, credentials, anything requiring judgment or a wallet"
    sla: { respond: 4h, escalate: leader }
```

Human members occupy the same primitives as agent members — mailbox,
task assignment, `task_done`, ledger presence — with one substitution:
where an agent member gets a *wake*, a human member gets a
**notification + a task inbox card** on the web console (title, brief,
the asking member, due state). The human completes tasks through a
deliberately tiny surface: **done (with a text/file result), decline
(with reason), question (mail back)** — three buttons, no YAML.

The payoff is graph-level: `depends_on` edges can now run *through*
people. "Draft the migration (agent) → approve the plan (johnny) →
execute it (agents, auto-dispatched the moment johnny taps done)" is
one task graph, with the human step tracked, nagged, and escalated
exactly like any other blocked dependency — instead of living as an
untracked Slack message that stalls the swarm invisibly.

## 2. Goals / non-goals

### Goals

- `kind: human` in the manifest/roster: no agent session, no model, no
  budget — but a real mailbox, real ledger participation, and roster
  presence (web roster renders human members distinctly).
- Task inbox on the web console: assigned-to-me cards with brief,
  attachments, and the three actions; mobile-usable (the console is
  already web — this is layout care, not new infra).
- Notification integration: assignment/nag/escalation ride the
  outbound-notifications channels (W1); links deep-link to the card.
- Scheduler semantics for humans: no wakes; SLA timers instead —
  gentle nag at fraction-of-SLA, leader notification at breach
  (escalation is *to the leader*, who decides — reassign, do without,
  or wait; the machine never overrides the graph).
- Writer-matrix extension: the human actor gets exactly the worker's
  edge (`task_done` on own tasks, mail send) — enforced in the store
  like every other cell; results from the inbox flow as `task_done
  {result}` verbatim.
- Leader protocol teaching: briefs for humans must be self-contained
  (the person won't grep the chatlog); the team protocol gains a
  "writing for humans" section and the leader knows human tasks are
  slow — batch them, front-load them, never put one on the critical
  path without an SLA.

### Non-goals (this wave)

- Humans as *leaders* (the operator already is one, above the swarm;
  modeling it adds nothing).
- Multi-human identity/auth beyond the webapi's existing auth (one
  operator-grade auth realm; per-human accounts on the console are a
  fast-follow if teams materialize).
- Chat-platform delivery of tasks (CHB/W35 can later render the inbox
  in Telegram — the seam is the inbox contract, not this wave).
- Payroll-grade time tracking, workload analytics (the ledger's
  existing metrics suffice).
- Agents *evaluating* human work (verify policy for human tasks is
  `leader` or `auto`, never a model judging a person unprompted).

## 3. Design sketch

- **The substitution point:** the audit finds where the scheduler
  dispatches a wake for an assigned task; `kind: human` branches to
  notification + inbox-card creation instead. Everything upstream
  (assignment mail, ledger transition to `running`) and downstream
  (`task_done` → verifying/complete → dependent dispatch) is
  untouched — that symmetry is the design's whole argument.
- **Inbox card contract:** `{task_id, brief, attachments, asked_by,
  assigned_at, sla_state}` rendered from existing ledger + mail rows —
  the inbox is a *view*, not a second store (single-writer invariants
  stay pristine).
- **Result fidelity:** a human's "done" text (and optional file
  attachments, stored in the space) becomes the task result consumed
  by dependents — same field agent workers fill. Files flow through
  the space's artifact conventions; images become vision-ingestible
  where relevant (W10).
- **SLA timers:** ride the existing alarm/scheduler infrastructure —
  a human task is an alarm with escalation payload, cancelled on
  completion. No new timing machinery.
- **Presence honesty:** humans are `away` by default; the roster
  never pretends a person is "online" — SLA state, not presence, is
  the operative signal.

## 4. Work items

- **HUM-1 — Manifest + roster + store.** `kind: human`, validation
  (no model/tools fields), roster rendering, writer-matrix human
  actor cells (table-tested). *Accept:* a manifest with a human
  member loads; the matrix permits exactly the worker edge; agent-only
  operations (spawn/clone) reject human targets cleanly.
- **HUM-2 — Dispatch substitution.** Scheduler branch, inbox-card
  view, assignment notification with deep link. *Accept:* assigning a
  task to the fixture human creates a card + notification and a
  `running` ledger state, with zero agent-session artifacts.
- **HUM-3 — Inbox UI + the three actions.** Console inbox, done/
  decline/question flows, attachment upload, result → `task_done`.
  *Accept:* completing a card from the console cascades dependent
  auto-dispatch (DWF fixture); decline routes to the leader with
  reason; question lands as mail to the asking member.
- **HUM-4 — SLA + escalation.** Timers on the alarm infra, nag,
  leader-notify on breach, cancellation on completion. *Accept:*
  fixture SLA breach notifies the leader exactly once; completion
  before breach cancels cleanly.
- **HUM-5 — Protocol + leader teaching.** Team-protocol section,
  brief-quality guidance, planning heuristics for human latency.
  *Accept:* prompt-review sign-off; a fixture leader plans a graph
  that front-loads the human dependency (scripted assertion).
- **HUM-6 — Docs + example.** User-guide (en + zh-tw); an
  `examples/evva-swarm/` team with a human design-review gate wired
  through a real graph.
- **HUM-7 — Events + metrics.** `human_task_assigned/completed/
  sla_breached` events; metrics for human-task latency. *Accept:*
  events appear in the durable log + live WS; metrics render on the
  console.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Human tasks silently stall the graph | SLA timers + nag + leader escalation are mandatory fields with defaults — an SLA-less human task is a lint warning at manifest load |
| Notification fatigue → ignored inbox | assignment + one nag + one escalation, period; batching guidance in the protocol; the inbox badge carries the standing count |
| The three-action surface proves too small (humans want to partially answer) | "question" covers dialogue; result text is freeform; resist workflow-builder creep until real usage demands it |
| Leader assigns agents' drudgework to humans | duties field + protocol guidance; declines-with-reason teach the leader; metrics expose misuse to the operator |

## 6. Open questions

1. Should human members be assignable by *workers* (peer asks) or
   leader-only in v1? Leaning leader-only — matches the mail etiquette
   already in the protocol.
2. Vacation/away state that pre-declines with a return date —
   worth v1 or fast-follow?
3. Multiple humans with duty-based routing ("any human who can
   approve billing") — needs real-team evidence; park as the natural
   v2.
