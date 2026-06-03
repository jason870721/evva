# SPRD-1-5 — Message bus & mailboxes (per-space, chan-of-uuid, broadcast)

> Milestone: M2 ｜ Status: TODO ｜ Owner: (unassigned) ｜ Depends on: 1-2
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

- [ ] Per-agent mailbox `chan` of UUIDs; `Send`/`Inbox`/`Register`/`Requeue`.
- [ ] Persist-before-signal ordering proven by test (the §6.2 invariant).
- [ ] Broadcast respects active membership; non-blocking send.
- [ ] `-race` clean; per-space isolation; no `internal/agent` import (invariants #1, #2).
