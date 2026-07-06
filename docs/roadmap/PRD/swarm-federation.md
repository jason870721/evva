# PRD — Swarm Federation (remote members over the wire) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W14 / v1.24** per
> [../long-range.md](../long-range.md). Graduates explore spike **EX-2
> (remote persona)**. Assumes swarm worktree isolation (W2) shipped —
> remote checkouts lean on the same git fabric.
> **Roadmap source:** 2026-07-06 long-range planning pass + the EX-2
> hypothesis. Today's swarm is one process on one host (process-model A):
> the roster is capped by one machine's cores, RAM, and API-key identity;
> a beefy desktop, a homelab server, or a teammate's machine can
> contribute nothing.
> **Reference source:** none — evva-native. (The `agent_type`-is-a-string
> registry and the architecture doc's "a future nono web service registers
> as a remote agent endpoint" line anticipated exactly this seam.)

---

## 1. TL;DR

Federation lets a member of a swarm live in a **different evva process on
a different machine**: `evva swarm join wss://host:8888/spaces/<space>
--token <t> --as <member>` connects a remote worker to the leader's bus.
The space's service process remains the **single brain** — ledger, mail
store, scheduler, event log, and web console stay exactly where they are;
what changes is that a member's *runtime* (its agent loop, its tools, its
workdir) can execute elsewhere, with mail and events crossing a WebSocket.

The design is deliberately hub-and-spoke, not peer-to-peer: the shipped
single-writer ledger invariants, the DWF task graph, and the mail
semantics all survive untouched because there is still exactly one
authoritative store. A remote member is, from the store's point of view,
just a member whose wake executor is far away.

Work travels through **git, not the socket**: a remote member clones the
repo and pushes its task branches to the same remote the leader merges
from (the W2 worktree/merge-back fabric) — the bus carries only mail,
task state, and small artifacts. This keeps the protocol tiny and the
failure modes legible.

## 2. Goals / non-goals

### Goals

- **Wire protocol v1:** authenticated WS carrying (down) wake/mail
  deliveries, task assignments, blackboard/brief content, interject
  signals; (up) `task_done`, mail sends, tool-side events for the event
  log, heartbeats. Versioned envelope; JSON frames (the webapi already
  speaks JSON + auth from the RP-15 hardening — extend, don't invent).
- **Join/leave lifecycle:** per-member join tokens minted by the leader
  or operator (`swarm token issue <member>`); a joined member registers
  its capabilities (persona, tools, model access) against the manifest
  entry it fills; clean leave + crash detection via heartbeat timeout.
- **Presence model:** roster gains `local | remote(connected) |
  remote(offline)`; offline members' mail queues durably (it already
  does — mailboxes are store rows); the scheduler skips waking offline
  members and the leader is informed past a staleness threshold.
- **Workdir strategy:** remote members clone + branch per the W2
  conventions; task briefs carry the branch/remote contract; merge-back
  stays a leader-side operation on the hub.
- **Web console:** remote badges, connection state, last-heartbeat, and
  per-member lag in the roster; join-token management screen.
- **Security:** tokens are per-member + revocable; TLS required for
  non-loopback (`wss://`); the join surface is off by default
  (`federation: on` knob).

### Non-goals (this wave)

- Multi-leader / distributed ledger — one store, one brain, full stop.
- Cross-space federation or swarm-of-swarms (EX-9).
- NAT traversal, relays, or discovery — a reachable URL is the
  operator's problem (docs show ssh-tunnel and tailscale patterns).
- Remote *leaders* (the leader lives with the store).
- Artifact sync beyond git + small mail attachments.

## 3. Design sketch

- **The seam:** the audit pass must locate the exact boundary where the
  scheduler wakes a local member's loop and where member loops call back
  into store/bus APIs. Federation splits that boundary into an interface
  (`MemberRuntime`?) with the existing in-process implementation and a
  new proxy implementation (hub side) ↔ agent host (remote side). If the
  seam is tangled, untangling it is the wave's first deliverable — same
  philosophy as the ACP wave proving the UI seam.
- **Delivery semantics:** at-least-once with member-side dedup on
  monotonic delivery ids (mail is durable hub-side; a reconnecting
  member replays from its last-acked id). Interjects (STE-5) are
  best-effort — a missed interject falls back to being ordinary mail.
- **Heartbeat/backpressure:** ping every T seconds; miss-3 → offline;
  event-log floods from a remote member are rate-capped at the hub
  (protect the store from a chatty spoke).
- **Identity:** remote member runs under its host's own provider keys —
  a homelab box can contribute its own rate limits/subscriptions. This
  is a feature, and the cost-accounting integration (W1's PRD) must
  attribute per-member regardless of which key paid.

## 4. Work items

- **FED-1 — Runtime seam extraction.** Interface between scheduler/store
  and member execution; in-process impl refactored beneath it,
  behavior-identical. *Accept:* full existing swarm test suite green on
  the refactored seam (this is the riskiest, most valuable ticket).
- **FED-2 — Wire protocol + hub endpoint.** Envelope, versioning, WS
  endpoint on the existing webapi server, auth middleware reuse.
  *Accept:* golden-frame tests; unauthenticated/expired-token joins
  rejected loudly.
- **FED-3 — `evva swarm join` (spoke).** Remote agent host: connect,
  register, run loop against proxied mail/task APIs, local tool
  execution, reconnect-with-replay. *Accept:* a two-host (or
  two-process) fixture completes a task assigned to the remote member,
  including `task_done` and dependent auto-dispatch.
- **FED-4 — Presence + scheduler awareness.** Heartbeats, offline
  state, skip-wake, staleness notice to leader, durable queue-while-
  offline. *Accept:* killing the spoke mid-task → offline in roster,
  mail queues, reconnect replays exactly-once effects.
- **FED-5 — Token lifecycle.** Issue/revoke/list CLI + web screen,
  per-member binding, TLS enforcement. *Accept:* revoked token
  disconnects within a heartbeat window and cannot rejoin.
- **FED-6 — Workdir/git contract.** Brief templates carry branch
  contract; docs for remote clone setup; verify merge-back path with a
  remote-produced branch. *Accept:* fixture merge-back of a
  spoke-pushed branch via the standard leader flow.
- **FED-7 — Web console surfaces.** Badges, lag, token screen, remote
  event provenance in the log. *Accept:* console distinguishes the three
  presence states live.
- **FED-8 — Docs + changelog.** User-guide (en + zh-tw): topology,
  security model, tailscale/ssh patterns, failure-mode table.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Seam extraction (FED-1) destabilizes the shipped swarm | it lands alone, behavior-identical, gated by the full suite before any wire code exists |
| Partition ambiguity (member working while marked offline) | task lease semantics: hub owns task state; a `task_done` from a stale lease is flagged for leader review, not auto-applied |
| Token leakage = code execution on a spoke | tokens are join-authorization only; the spoke executes only what its operator's local config allows (its own permission gate applies — a spoke is a consenting evva, not a drone) |
| Protocol versioning debt | versioned envelope + explicit min-version handshake from day one |
| Clock skew corrupting schedules | all scheduling stays hub-side; spokes never interpret wall-clock semantics |

## 6. Open questions

1. Should a spoke host *multiple* members (one process, several roster
   slots) in v1, or one-member-per-join? Leaning one-per-join for
   legibility.
2. Blackboard/brief size over the wire — inline vs lazy fetch? (Depends
   on W2 blackboard sizes; likely inline given the existing size caps.)
3. Does `member_spawn` (DWF ephemeral clones) extend to spokes ("spawn 3
   clones on the homelab box")? Tempting; defer to a fast-follow after
   the presence model soaks.
