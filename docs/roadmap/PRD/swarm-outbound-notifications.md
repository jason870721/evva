# PRD — Swarm Outbound Notifications — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed.
> **Target release:** TBD — wave-sized minor (`v1.11+` candidate). Per the
> checkpoint-rewind precedent, the CLAUDE.md wave → minor row is added only
> when the operator confirms the wave.
> **Roadmap source:** swarm design review 2026-07-04 — "gates, stalls, and
> budget freezes are visible only to an operator who happens to be watching
> the web console" surfaced as the top operational gap for unattended
> swarms. RP-15 shipped the *inbound* webhook half; this wave is the
> outbound half.
> **Evaluation provenance:** live-source audit at `dev@be2f949`
> (v1.8.5-beta.1), 2026-07-04/05. All file:line references verified against
> that commit.
> **Reference source:** none — evva-native (no ref/src analog).

---

## 1. TL;DR

A swarm member that raises an approval gate blocks until a human answers
(`KindApprovalNeeded`, pkg/event/event.go:90 → phase `waiting-approval`).
A stalled run, a tripped budget, a stale task — all of it lands in exactly
two places: the web console's attention strip
(web2/src/lib/events.ts:423) and durable mail to the literal recipient
`"user"` (`notifyOps`, internal/swarm/scheduler.go:299-303). Both require
the operator to be *looking at the console*. Close the laptop and a blocked
member waits forever — the stall watchdog deliberately exempts
`waiting-approval`/`waiting-input` (they're a human's wait, not a hang), so
nothing ever escalates.

The inverse direction already exists: RP-15 lets external systems POST
events *into* a space (`IngestEvent`, internal/swarm/service/service.go:1795;
route `POST /api/swarm/{id}/event`, webapi/api.go:534, authenticated by
`X-Evva-Webhook-Secret`, api.go:334). A swarm can be driven from outside but
cannot call out.

This wave adds the outbound half: a per-space **notifier** that taps the one
chokepoint every event already flows through (`Service.publish`,
service/service.go:870-892) and forwards attention-worthy moments to an
operator-configured **webhook URL** (plain JSON or Slack-compatible) or a
**local command** (desktop notify). Two prerequisites make it clean: system
alerts get promoted from mail-only to first-class events (a new
`ops_alert` kind emitted inside `notifyOps`), and delivery copies the event
log's non-blocking single-writer discipline (eventlog.go:64-69) — the
observer never slows the observed.

---

## 2. Goals / non-goals

### Goals

- An operator away from the console learns within seconds that a member is
  blocked on approval/question, errored, paused at iteration limit, stalled,
  budget-frozen, or sitting on a stale task/mailbox — via Slack, a generic
  webhook, or a local command.
- Zero behavior change for spaces that don't configure it; zero added
  latency for spaces that do (async, bounded queue, drop-and-count under
  pressure).
- System alerts (stall/budget/stale) become visible in the console timeline
  and durable chatlog too — a side benefit of promoting them to events.
- Noise-controlled by design: gate notifications fire once per gate, alert
  sources keep their existing one-per-episode dedup, and a per-space rate
  limit caps the blast.

### Non-goals (this wave)

- No delivery guarantees — this is best-effort observability, not a message
  queue. One retry, then drop and count (the eventlog contract,
  service/eventlog.go:26-30).
- No two-way actions (answering a gate from Slack). The notification carries
  the console URL; acting happens there. (A signed action link is a natural
  follow-on once this ships.)
- No email/SMS/PagerDuty-specific integrations — one JSON shape, one
  Slack-compatible shape, one exec hook. Anything else is a relay the
  operator writes.
- No per-member notification routing; config is per space.
- No digest/batching windows (open question #3).

---

## 3. Verified current state

### 3.1 Attention exists only as pull

- Gate kinds: `KindApprovalNeeded` (pkg/event/event.go:90),
  `KindQuestionNeeded` (:96); failure kinds: `KindError` (:108),
  `KindIterLimit` (:49). The FE reduces these to roster *phases* and ranks
  them (`attentionKind`, events.ts:381: `waiting-approval`/`waiting-input`
  → act; `error`/`paused` → warn; `attentionItems`, :423 adds time-based
  "stalled" warns). Nothing pushes.
- System alerts are durable mail, not events: `notifyOps`
  (scheduler.go:299-303) sends sender `"system"` to the leader **and** to
  recipient `"user"` — from the stall sweep (`sweepStalls`,
  scheduler.go:315; one-per-run flag `stallNotified` :349-351), the budget
  breaker (`tripBudget`, :276-292), and the workflow watchdog
  (`notifyStaleTask` workflow_watch.go:91, key-suppressed :77-79;
  `notifyStaleMailbox` :148, episode-suppressed :138). The web shows these
  in the mailbox; no event line exists for them.
- The one chokepoint: every space event passes `Service.publish`
  (service/service.go:870) — gate lifecycle is already observed there
  (`pending.observe(e.Event)`, :871-873) before the WS fan-out (:892) and
  the event-log offer (:888-890). A notifier tapping here sees everything
  the console sees.

### 3.2 Inbound exists; outbound does not

`IngestEvent` (service.go:1795) + constant-time secret check (:1806-1812)
is shipped and tested. A repo-wide search finds no outbound HTTP sender in
the swarm layer — confirmed absent.

### 3.3 Already built — reuse, do not redo

| Piece | Where | What it gives this wave |
|---|---|---|
| The publish chokepoint | service.go:870-892 | The single tap point; gate lifecycle already observed there |
| Non-blocking single-writer + drop counter | eventLog (`Offer`, eventlog.go:64-69; buffer :45; doc :26-30) | The delivery queue's exact discipline |
| Alert sources with dedup built in | scheduler.go:349-351, :267-292; workflow_watch.go:77-79, :138 | `ops_alert` inherits one-per-episode semantics for free |
| Shared HTTP client shape | `httpReqClient` (pkg/tools/web/http.go:20-26, 30 s timeout); update checker (pkg/update/github.go:75) | The outbound POST plumbing pattern |
| Secret header convention | `X-Evva-Webhook-Secret` (api.go:334) | The outbound request reuses the same header name for symmetry |
| Settings parsing | `WebhookSecret` trim (manifest.go:348), `parseStallDuration` (:245) | The `notify:` block's validation style |
| Synthetic event lines | scheduleChangeEvent et al. (service.go:1408-1609) | Not needed here — `ops_alert` is a real space event, but the naming style carries |

---

## 4. The shape decision: promote alerts to events, tap one chokepoint

Three candidate architectures for "what does the notifier listen to":

1. **Tap the Bus for mail to `"user"`** — rejected. The Bus is space-layer
   and message-shaped; the notifier would need a second tap for gate events
   anyway, and mail already fans to the leader, forcing cross-recipient
   dedup.
2. **Mirror the FE's phase reduction server-side** — rejected. Phases are a
   UI reduction (events.ts:381 switches on phase strings); reimplementing
   `phaseFor` in Go duplicates semantics that would drift. The FE's
   time-based "stalled" warns are also redundant server-side — the RP-14
   stall sweep already covers hangs with a configurable threshold.
3. **Promote system alerts to a first-class `ops_alert` event and tap
   `publish` for five kinds** — chosen. `notifyOps` gains one line: emit
   `Kind: "ops_alert"` (payload: about-member, subject, body) through the
   space's event stream alongside the durable mail. Now
   `{approval_needed, question_needed, error, iter_limit, ops_alert}` at the
   publish chokepoint is the complete attention surface, each already
   deduped at its source — and the console timeline + durable chatlog gain
   alert lines as a side effect (today they're mailbox-only).

The `event.Kind` vocabulary is explicitly open ("New kinds are added by
extending this list", pkg/event/event.go:27-29).

---

## 5. Design

### 5.1 D1 — Config

```yaml
settings:
  notify:
    url: "https://hooks.slack.com/services/T.../B.../x"   # or any endpoint
    format: slack            # "json" (default) | "slack"
    secret: "s3cret"         # optional; sent as X-Evva-Webhook-Secret
    events: [gates, errors, alerts]   # default: all three groups
    command: ""              # alternative/additional: local exec, JSON on stdin
    rate_limit: 12           # max sends per minute per space; default 12
```

`Settings.Notify *NotifySpec`, absent = off. Fail-fast at `LoadManifest`:
`url` xor/and `command` (at least one), `format` enum, `rate_limit ≥ 1`,
event-group names from `{gates, errors, alerts}` (gates =
approval+question; errors = error+iter_limit; alerts = ops_alert).
`WriteManifest` round-trips.

### 5.2 D2 — The notifier

One `notifier` per configured space, owned by the service entry beside the
event log: bounded channel (256), single sender goroutine, non-blocking
`Offer` with a `dropped` counter — a byte-for-byte copy of the eventlog
discipline (eventlog.go:45-69). Sender behavior per item: build payload →
POST with a 10 s context (dedicated `http.Client`, the update-checker
pattern, pkg/update/github.go:75) → on network error, one retry after 5 s →
drop and count. `command` mode: `proc.Shell()` exec with the JSON on stdin,
15 s timeout, tree-killed (the verify-checks runner discipline if that wave
lands first; plain `exec.CommandContext` + `proc.KillTree` regardless).

Payload (format `json`):

```json
{"space":"web-team","spaceId":"…","agent":"qa","kind":"approval_needed",
 "title":"qa is waiting for approval","body":"bash: rm -rf dist/ …",
 "at":"2026-07-05T09:31:02Z","console":"http://127.0.0.1:8888/?space=…"}
```

Format `slack`: the same content folded into `{"text": "…"}` with a
two-line summary — deliberately the lowest common denominator (works for
Slack, Discord-compatible relays, and most chat webhooks).

### 5.3 D3 — Triggering + noise control

In `publish` (service.go:870), after `pending.observe`:

- `approval_needed`/`question_needed`: offer only on the gate's **first
  sighting** — keyed `(agentID, requestID)` against the same pending-gate
  observation that already tracks lifecycle (:871-873). Re-broadcasts and
  reconnect re-sends never re-notify.
- `error`/`iter_limit`: offer as-is (their sources are already run-scoped).
- `ops_alert`: offer as-is (deduped at source, §4).
- Rate limiter: token bucket per space (`rate_limit`/min); past the limit,
  drop, count, and — once per quiet period — send a final "rate limit hit,
  N suppressed" notice so silence is never ambiguous.

The `events:` filter applies before the queue, so an alerts-only config
costs nothing on gate-heavy days.

### 5.4 D4 — Failure semantics

The notifier can never wedge a space: offers are non-blocking, the sender
holds no locks, and teardown closes it after the pump exits (the eventlog
ordering in `teardownSpace`, service.go:344 area). A dead webhook shows up
as `notifsDropped` climbing in metrics and one WARN log per failure burst —
never as backpressure.

### 5.5 D5 — Observability

`spaceMetrics` (metrics.go:31) gains `NotifsSent`, `NotifsDropped`,
`NotifsSuppressed`; surfaced in `GET /api/swarm/{id}/metrics` (api.go:630)
and the healthz aggregate. The `ops_alert` promotion also lands alert lines
in the console timeline and `/chatlog` replay for free.

---

## 6. Work items

**NTF-1 — `ops_alert` event promotion.**
Emit the new kind inside `notifyOps` (scheduler.go:299) with
about/subject/body payload; FE reducer renders it as a timeline line
(fallback-safe for old logs).
*Accept:* stall, budget-trip, stale-task, stale-mailbox each produce exactly
one `ops_alert` event alongside their existing mail; chatlog replay shows
them; no double-fire on the deduped paths.

**NTF-2 — Notifier core.**
The 5.2 component: bounded queue, single sender, POST + retry + drop
counters, `json`/`slack` formats, `command` exec mode, teardown ordering.
*Accept:* unit tests with a local `httptest` sink — delivery, retry-once,
drop-on-dead-endpoint (space unaffected), stdin payload for command mode,
tree-kill on hung command; race-clean.

**NTF-3 — Config knob.**
`Settings.Notify` parse/validate/round-trip (5.1).
*Accept:* fail-fast on bad format/rate/empty targets; absent block
constructs no notifier.

**NTF-4 — Trigger wiring + noise control.**
The publish-tap (5.3): first-sighting gate keying, event-group filter,
token-bucket limiter + suppression notice.
*Accept:* integration — a gate raised, re-broadcast, and answered notifies
exactly once; a 50-event burst at `rate_limit: 5` sends ≤5 + one
suppression notice; filters drop before enqueue.

**NTF-5 — Metrics + docs.**
Counters (5.5); user guide (en, zh-tw) "getting paged by your swarm" —
Slack setup, generic webhook, desktop-notify command recipe, the
best-effort contract; CHANGELOG.
*Accept:* metrics move under test; docs in both languages.

Sequencing: `NTF-1 ∥ NTF-2 → NTF-3 → NTF-4 → NTF-5`.

---

## 7. CI plan summary

| Stage | Change | Cost |
|---|---|---|
| NTF-1 | scheduler/watchdog test extensions assert the event alongside the mail | seconds |
| NTF-2 | httptest-backed unit suite; command-mode fixtures POSIX-written | seconds, both CI OSes |
| NTF-4 | service-level integration on temp spaces | seconds |
| all | no new dependencies (stdlib http, existing proc seam) | — |

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Webhook endpoint slow/dead wedges the pump | Non-blocking offer + bounded queue + drop counter — the pump never waits (the eventlog contract) |
| Notification storm (wide swarm, gate-heavy run) | Source-level dedup + first-sighting gate keying + per-space token bucket + group filter |
| Secrets in manifests get committed | Same exposure class as the existing `webhook_secret` (manifest.go:85) — docs recommend env-substitution at authoring time and repo hygiene; the payload never echoes the secret |
| Payload leaks sensitive tool input to a third-party channel | Body is capped (title + 500-char body tail) and documented; `events:` lets the operator send titles-only groups (open question #2) |
| `command` mode is arbitrary exec | Operator-authored manifest config — the same trust class as `permission_mode: bypass` and the verify-checks command (its PRD §4 states the shared rule) |
| Duplicate notifications after service restart | Gate re-hydration re-observes pending gates; first-sighting keys rebuild from `PendingGates`, so a still-open gate re-notifies once per service lifetime — accepted (better than silence after a restart) and documented |
| Local command hangs | 15 s timeout + `proc.KillTree` |

---

## 9. Open questions

1. **Signed action links (approve/deny from the notification)?** Recommend
   defer — it's a security surface (token-bearing URLs) worth its own
   design; this wave carries the console link only.
2. **Titles-only privacy mode?** Recommend yes as a boolean
   (`notify.redact_bodies`) if review flags it — one line of code, big
   comfort for shared channels.
3. **Digest window (batch N events per minute into one message)?**
   Recommend defer; the rate limiter's suppression notice covers the
   pathological case.
4. **Service-level (not per-space) fallback config?** Recommend defer until
   multi-space operators ask — per-space keeps ownership with the manifest.

---

## 10. Rollout

1. NTF-1..NTF-5 via `feature/swarm-notify` → `dev`.
2. `pre-release feature` cuts the wave's first beta under the minor the
   operator assigns at confirmation.
3. Beta validation: a real Slack webhook + a desktop `command` recipe on a
   gate-heavy swarm; a dead-endpoint soak proving drops-without-drag.
4. `release` promotes.
