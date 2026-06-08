# SPRD-1-5 — Message bus & mailboxes (per-space, chan-of-uuid, broadcast)

> Milestone: M2 ｜ Status: IN REVIEW ｜ Owner: (unassigned) ｜ Depends on: 1-2
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 2) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §6, §6.2

## 1. Goal

The **per-space inter-agent transport**: one in-memory mailbox `chan` per agent that
carries only message **UUIDs**, backed by the durable `messages` table (1-2). Delivery
writes the row **first**, then signals the channel — so the DB is the single source of
truth and the channel is just an ordered "you have mail" notifier that survives restart
by reloading unread UUIDs.

## 2. Scope

**In:**
- `Bus` (per `SwarmSpace`): one `chan string` (msg-UUID) per registered agent name.
- `Send(m)` — persist the row then enqueue its UUID to the recipient's inbox;
  `to == "all"` broadcasts to every **active** member (membership from the roster).
- `Inbox(name) <-chan string` — the scheduler (1-6) selects on this.
- `Register(name)` / `Deregister(name)` — mailbox lifecycle as members join / freeze.
- **Ordering invariant**: the row is persisted (`store.PutMessage`) *before* its UUID is
  pushed, so any drain that reads a UUID always finds the row (§6.2).
- Restart helper: `Requeue(name, uuids)` to re-push unread UUIDs on boot (1-11 calls it).
- Bounded, non-blocking semantics: a full/slow inbox must never deadlock the sender.

**Out:** the drain itself (UUID → prompt → mark-read) — that is the scheduler's job
(1-6, drain A) and the loop seam (1-12, drain B). The `send_message` tool that calls
`Send` is 1-7. Cross-space delivery (never — §3.1).

## 3. Dependencies & what this unblocks

- Depends on: 1-2 (message DAO: `PutMessage`/`GetMessage`/`MarkRead`/`UnreadFor`).
- Unblocks: 1-6 (scheduler selects on `Inbox`), 1-7 (`send_message` → `Bus.Send`),
  1-11 (restart `Requeue` of unread).

## 4. Technical design

Package `internal/swarm/bus`.

```go
type Bus struct {
    mu       sync.RWMutex
    inboxes  map[string]chan string
    store    *store.Store
    roster   Membership // read-only view to expand "all"
}

func New(st *store.Store, roster Membership) *Bus
func (b *Bus) Register(name string)             // make an inbox (idempotent)
func (b *Bus) Deregister(name string)
func (b *Bus) Send(m store.Message) (uuid string, err error) // PutMessage → push UUID
func (b *Bus) Inbox(name string) <-chan string  // scheduler selects here
func (b *Bus) Requeue(name string, uuids []string) // restart reload (1-11)

// Membership is the minimal roster view the bus needs (defined here to avoid a
// cycle: the swarm Roster implements it; the bus never imports package swarm).
type Membership interface { ActiveMembers() []string }
```

- The UUID is generated in the store layer (1-2) — one source of identity.
- Per-space: a `Bus` belongs to one `SwarmSpace`; recipients resolve only within that
  space (invariant #2 — no cross-space). `to == "all"` expands via `roster.ActiveMembers()`.
- Channel buffer has a sane default; `Send` never blocks a calling agent's tool
  execution — if a buffer is full, the row is still persisted and the reader catches it
  via the unread set on its next cycle (DB is truth, chan is a hint).

## 5. Acceptance criteria

1. `Send` persists the `messages` row **before** the UUID appears on the inbox chan
   (assert via a store hook / by reading the row the instant a UUID arrives).
2. `Inbox(b)` receives the UUID after `Send(to=b)`; `GetMessage(uuid)` returns the full
   row with the correct `sender`/`recipient`.
3. `to == "all"` delivers one UUID to every active member and **none** to frozen members.
4. `Requeue` re-pushes a list of unread UUIDs in `created_at` order.
5. A full inbox does not deadlock `Send`; the row is still persisted (recoverable).
6. Two spaces' buses with the same agent names never cross-deliver (isolation).

## 6. Verification

- Unit tests: persist-before-signal ordering, single + broadcast delivery, frozen
  excluded from broadcast, requeue ordering, non-blocking send under a full buffer.
- Two-space isolation test (same names, no cross-talk), mirroring 1-4's.
- `go test -race ./internal/swarm/bus/...` clean.

## 7. Definition of Done

- [x] Per-agent mailbox `chan` of UUIDs; `Send`/`Inbox`/`Register`/`Deregister`/`Requeue`.
- [x] Persist-before-signal ordering proven by test (the §6.2 invariant).
- [x] Broadcast respects active membership; non-blocking send.
- [x] `-race` clean; per-space isolation; no `internal/agent` import (invariants #1, #2).

### Implementation design / decisions

- **Broadcast fans out into one durable row per active peer** (each its own
  UUID + `read_at`), *not* a single `recipient="all"` row. This is forced by
  §6.2's restart query (`WHERE recipient=? AND read_at IS NULL` per agent name):
  a shared `"all"` row would be invisible to every member's `UnreadFor`, so it
  could neither restart-resume nor track per-recipient read state. Per-recipient
  rows make a broadcast behave exactly like a unicast for both. (`store.RecipientAll`
  stays a valid opaque value the store can hold; the *bus* never persists it.)
- **The sender is skipped on broadcast** — a broadcast goes to peers, so an
  agent never mails (and thus never self-wakes) on its own broadcast. AC #3's
  "every active member" is read as the membership filter it demonstrates (frozen
  excluded), not as self-inclusion.
- **Persist-before-signal** lives in one private `deliver()`: `PutMessage`
  returns (row committed) *before* `signal()` pushes the UUID. The ordering test
  asserts `GetMessage` succeeds the instant the UUID arrives — if signal could
  outrun the write it would be `ErrMessageNotFound`.
- **`signal()` is always non-blocking** (`select { case ch <- uuid: default: }`).
  A missing inbox (unregistered / deregistered-on-freeze) *and* a full buffer
  both drop only the **hint**; the row is durable, so the reader recovers it via
  `store.UnreadFor` next cycle (DB is truth, chan is a hint). Proven by the
  full-buffer test (no deadlock; all rows persisted; chan holds exactly the cap).
- **The bus owns message identity**: `Send` always mints a fresh
  `common.GenUUID()` per delivered row (any `ID` on the passed `Message` is
  ignored), so it is the single source of identity (§6.2). Return value = the
  delivered row's UUID for a single recipient; `""` for a broadcast (no single
  identity — N rows).
- **`Deregister` deletes, never closes** the channel: closing would make a
  scheduler's `select` spin on the zero value and risk a send-on-closed panic
  under a racing `Send`. Undrained UUIDs stay durable and reload via `Requeue`.
  **`Register` is idempotent** — it keeps the existing channel + its queued mail.
- **`Membership` interface is declared in package `bus`** (`ActiveMembers() []string`)
  so the bus never imports package `swarm`; the swarm `Roster` gained
  `ActiveMembers()` and satisfies it structurally (compile-time `var _ bus.Membership`
  assertion in `roster_test.go`). Dependency stays one-directional: `swarm → bus`.
- **`inboxBuffer = 256`** — UUID-only hints are cheap; overflow is recoverable.
- **Out of scope (per ticket), deferred:** the drain (UUID → prompt → mark-read)
  is the scheduler's job (1-6 drain A / 1-12 drain B); the `send_message` tool
  that calls `Send` is 1-7; wiring the `Bus` into `SwarmSpace`/supervisor is 1-6.
  This ticket ships the transport + tests only.
