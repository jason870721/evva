# PRD — Swarm Leader Health & Takeover — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W15 / v1.25** per
> [../long-range.md](../long-range.md). Graduates explore spike **EX-3
> (leader takeover)**. Sequenced after federation (W14) so the protocol
> is designed against the remote-member reality.
> **Roadmap source:** 2026-07-06 long-range planning pass + the EX-3
> hypothesis. The v1.5 hardening wave (RP-14) gave the swarm a stall
> *watchdog*; DWF (v1.10) gave it leaderless *mechanical* dispatch. But
> every judgment call — verification, replanning, priorities — still
> funnels through one leader session with no understudy: a wedged,
> compacted-into-confusion, or budget-exhausted leader means the swarm
> coasts on auto-dispatch until the graph runs dry, then stops.
> **Reference source:** none — evva-native.

---

## 1. TL;DR

Make the leader role **transferable at runtime** — deliberately, or
automatically on failure:

- **Leader health signal.** Extend the RP-14 watchdog vocabulary with
  leader-specific probes: mailbox drain latency (verifying tasks piling
  up), wake responsiveness, budget state, and an explicit self-reported
  health from the leader's own loop. Health is visible on the web
  console and in `swarm-doctor`'s checks.
- **Deputy.** The manifest may name a `deputy: <member>` (a persona
  member with leader-grade context — RP-29 made personas full members;
  a deputy is typically one). The deputy receives a compact,
  continuously-maintained **succession brief** (current plan snapshot,
  open verifications, standing decisions — the blackboard plus a
  leader-maintained addendum), so takeover doesn't start from zero.
- **Takeover protocol.** Triggered by operator command (`swarm leader
  transfer <member>`), by the leader itself (graceful handoff), or by
  the watchdog after N sustained health failures (auto mode is opt-in).
  The store performs an atomic role swap: mail routing (`to: leader`),
  ledger authority (verify transitions, task creation rights), team
  protocol identity, and event-log annotation all flip together. The
  old leader becomes an ordinary member (or is quarantined if wedged).
- **Handback** is the same transition in reverse, once a human or the
  recovered leader asks for it.

One invariant above all: **at every instant exactly one session holds
leader authority** — the store enforces the swap atomically; there is no
window with two writers of leader-only transitions.

## 2. Goals / non-goals

### Goals

- Health probe suite + `leader_health` state in the store, surfaced on
  console/doctor/metrics; explicit `health_report` from the leader loop
  (self-assessment is a signal, not the arbiter).
- Succession brief: leader-maintained via a light protocol duty
  (blackboard addendum section or sibling doc, size-capped); staleness
  tracked and nagged (a stale brief downgrades auto-takeover to
  operator-approval-required).
- Atomic role transfer in the store: single transaction flipping role,
  mail routing, and authority checks; event-logged with cause
  (`operator | graceful | watchdog`).
- Deputy wake-on-takeover with a purpose-built assumption prompt (brief
  + roster + open-verification digest), not a cold "you are now leader".
- Operator surfaces: transfer/handback commands (CLI + web), takeover
  history, auto-mode configuration (off by default).
- Old-leader quarantine option: wedged sessions get frozen (no wakes, no
  writes) for post-mortem rather than silently demoted.

### Non-goals (this wave)

- Election/quorum among peers — the deputy is *named*, not elected; with
  one authoritative store there is no distributed-consensus problem to
  solve, and inventing one would be theater.
- Split-brain handling beyond the store's atomic swap (federation spokes
  never hold leader authority — W14 invariant — so partition cannot
  create two leaders).
- Automatic *diagnosis* of why the leader wedged (that's swarm-doctor's
  lane; this wave is continuity, not root-causing).
- Multi-deputy chains (one deputy; a second-order failure is an operator
  page via outbound notifications, W1).

## 3. Design sketch

- **Authority enforcement point:** the audit must confirm that all
  leader-only transitions already pass through centralized store checks
  (the DWF writer-matrix work strongly suggests yes — it table-tested
  actor×transition cells). Takeover then reduces to swapping which
  member id the matrix's `leader` row binds to, inside one store
  transaction. If any leader privilege is enforced *outside* the store,
  centralizing it is ticket zero.
- **Watchdog integration:** reuse RP-14's stall-detection plumbing;
  leader health is a composite (probes + self-report + verifying-queue
  depth vs configured SLA). Auto-takeover requires M consecutive
  failures *and* a fresh-enough succession brief *and* auto mode on —
  three independent gates against flapping.
- **Assumption prompt:** built from durable artifacts only (blackboard,
  brief, ledger digest, roster) — never from the old leader's session
  history (which may be exactly what's poisoned). This is the
  architectural bet: durable shared state, not session state, is the
  swarm's continuity.
- **Mail continuity:** in-flight `to: leader` mail already targets a
  role alias or a member id (audit to confirm which); if id-targeted,
  add the role alias first so routing flips atomically with the swap.

## 4. Work items

- **RES-1 — Authority centralization audit + fix.** Confirm/centralize
  every leader-only check into the store's matrix. *Accept:* a
  table-test enumerating leader privileges passes with the leader id as
  a variable, not a constant.
- **RES-2 — Leader health.** Probes, composite state, console/doctor/
  metrics surfaces. *Accept:* an artificially wedged leader (paused
  loop in a fixture) degrades to `unhealthy` within the configured
  window; healthy operation never flaps.
- **RES-3 — Succession brief.** Protocol duty, size cap, staleness
  tracking + nag, brief section on the web. *Accept:* fixture leader
  maintains the brief across task churn; staleness beyond threshold is
  visible and gates auto mode.
- **RES-4 — Atomic transfer + handback.** Store transaction, mail-alias
  flip, event-log causes, quarantine option. *Accept:* transfer under
  concurrent member `task_done` traffic never yields a dual-authority
  window (hammer test); handback restores exactly.
- **RES-5 — Deputy assumption flow.** Wake prompt from durable
  artifacts, first-actions checklist (drain verifying queue, confirm
  roster, announce). *Accept:* in a fixture with three open
  verifications, the promoted deputy processes them without re-asking
  completed questions.
- **RES-6 — Operator surfaces + docs.** CLI + web transfer/handback,
  auto-mode config, takeover history; user-guide (en + zh-tw) incl. the
  "when to use auto" guidance and the quarantine post-mortem flow.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Auto-takeover flaps on transient slowness | three-gate rule (sustained failures + fresh brief + opt-in); hysteresis on health recovery |
| Deputy assumes with poisoned/stale context | assumption prompt built from durable artifacts only; stale brief blocks auto mode |
| Hidden leader privilege outside the store survives the swap | RES-1 is ticket zero precisely because of this |
| Old leader's queued tool effects land post-demotion | demotion freezes its loop at an iteration boundary; in-flight tool results write as an ordinary member (harmless) or are quarantined with it |
| Operators over-trust auto mode | off by default; docs frame it as "continuity for overnight runs", not hands-off management |

## 6. Open questions

1. Should the deputy *shadow-verify* a sample of tasks in steady state
   (cold-standby vs warm-standby trade: context freshness vs cost)?
   Leaning cold + good brief; revisit with real takeover data.
2. Is `deputy` per-manifest static, or leader-appointable at runtime
   (`member_promote`-style tool)? Leaning manifest + operator override.
3. Budget interaction: does the promoted deputy inherit the leader's
   budget envelope (RTE/W9) or bring its own? Operator call at pickup.
