# SPRD-1-2 — `.vero` store: persistence layer, task state machine, message DAO

> Milestone: M1 (tasks) / M2 (messages) ｜ Status: IN REVIEW ｜ Owner: (unassigned) ｜ Depends on: 1-1
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 1) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §7

## 1. Goal

The **single per-space data layer**: open `<workdir>/.vero/vero.db`, run
migrations, and expose a concurrency-safe DAO for the **task ledger (with its
5-state machine)** and the **message store**. This is the spine the bus,
scheduler, tools, and restart-resume all build on.

## 2. Scope

**In:**
- DB bootstrap: `*sql.DB` (modernc/mattn sqlite — pick one, document why),
  **WAL mode + `busy_timeout`**, foreign keys on.
- Migrations runner (embedded `.sql` files; forward-only for v1).
- `tasks` + `messages` schema per design §7.2 (note `messages.id TEXT` UUID).
- `sync.RWMutex`-wrapped DAO: writes hold `Lock()`, reads hold `RLock()`.
- **Task state machine**: enforce legal transitions; **status writes are
  Leader-only at the API** (the DAO takes a `by` actor and rejects non-leader
  status writes — caller passes role).
- Message DAO: `PutMessage`, `GetMessage`, `MarkRead`, `UnreadFor`.

**Out:** the bus/chan (1-5), the tools that call this (1-7), per-agent domain
tables for Phase 2 (trading) — only the convention/helper for namespaced tables
is in scope, not trading tables.

## 3. Dependencies & what this unblocks

- Depends on: 1-1.
- Unblocks: 1-5 (bus uses message DAO), 1-6 (scheduler reads tasks/assignments),
  1-7 (tools), 1-11 (restart reload).

## 4. Technical design

Package `internal/swarm/store`.

```go
type Status string // pending|running|suspended|verifying|completed
type Store struct { db *sql.DB; mu sync.RWMutex }

func Open(workdir string) (*Store, error)         // .vero/vero.db + WAL + migrate
func (s *Store) Close() error

// tasks (Leader-only status writes)
func (s *Store) CreateTask(t Task) (int64, error)             // status=pending, assignee required
func (s *Store) TransitionTask(id int64, to Status, by Actor, note string) error
func (s *Store) GetTask(id int64) (Task, error)
func (s *Store) ListTasks(f TaskFilter) ([]Task, error)       // by space-implicit (one db per space)

// messages (UUID id)
func (s *Store) PutMessage(m Message) error
func (s *Store) GetMessage(id string) (Message, error)
func (s *Store) MarkRead(id string) error
func (s *Store) UnreadFor(recipient string) ([]string, error) // ordered by created_at; restart reload
```

- **Legal transition table** (reject everything else, return typed error):
  `pending→running`, `running→{suspended,verifying}`, `suspended→running`,
  `verifying→{completed,running}`. `by` must be the leader for any status write.
- Files: `store.go`, `tasks.go` (state machine), `messages.go`, `migrations/*.sql`,
  `*_test.go`.

## 5. Acceptance criteria

1. `Open` creates `.vero/vero.db`, applies migrations, sets WAL + busy_timeout
   (assert `PRAGMA journal_mode` == wal).
2. Every legal transition succeeds; every illegal transition returns the typed
   error and **does not mutate** the row.
3. A non-leader `Actor` calling `TransitionTask` is rejected.
4. `CreateTask` requires a non-empty `assignee` (push model).
5. Message round-trips: `PutMessage`→`GetMessage`; `MarkRead` sets `read_at`;
   `UnreadFor` returns only unread, oldest-first, and excludes read ones.
6. Concurrent readers + a writer do not race (`go test -race`).

## 6. Verification

- Unit tests: state-machine matrix (all legal + a sample of illegal), leader-only
  guard, create-validation, message CRUD + unread ordering, WAL pragma check.
- `go test -race ./internal/swarm/store/...` clean.
- Table-driven transition test is the centerpiece.

## 7. Definition of Done

- [x] `Open` + forward-only migration runner (`schema_migrations`) + WAL/busy_timeout/foreign_keys; one db file per workdir. Verified: `journal_mode==wal`, `busy_timeout==5000`, `foreign_keys==1`, and a dangling `ref_task` is rejected (FK actually enforced).
- [x] Task state machine with leader-only writes; illegal transitions inert. Verified by the full 5×5 `TestTaskStateMachine` matrix (6 legal moves succeed; the other 19 — incl. self-transitions and any move out of `completed` — return `ErrIllegalTransition` and leave the row unchanged). Non-leader actor → `ErrNotLeader`, no mutation.
- [x] Message DAO incl. `UnreadFor` (unread-only, oldest-first, recipient-isolated; restart-reload ready). `MarkRead` is idempotent; missing id → `ErrMessageNotFound`.
- [x] `-race` clean; transition matrix + message tests + an 8-reader/3-writer concurrency test green.
- [x] No `internal/agent` import (dep-check green); one `*sql.DB` per workdir = per-space (invariant #2).

### Implementation notes / decisions

- **Driver: `modernc.org/sqlite` (pure Go, no cgo).** `mattn/go-sqlite3` (cgo) would break `release.yml`, which cross-compiles darwin/linux × amd64/arm64 with a plain `CGO_ENABLED=0 go build`. Documented at the top of `store.go`. Cost: a sizeable indirect dependency tree (modernc libc et al.) in `go.sum` — runtime links only libc/mathutil/memory.
- **Pragmas via DSN** (`_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)`) so every pooled connection gets the connection-scoped ones; WAL is also re-asserted via `Exec` (file-level). A test reads each pragma back to prove the DSN took.
- **Concurrency:** `sync.RWMutex` wraps the DAO (writes `Lock`, reads `RLock`), per design §7.3. Tasks are single-writer (Leader) by design; the mutex's real job is the multi-writer `messages` table. Default connection pool + WAL gives real read concurrency.
- **State writes are Leader-only at the DAO** (`TransitionTask(by Actor)`), checked before any read — defence-in-depth under the tool-layer gate (SPRD-1-7). `CreateTask` always inserts `pending` and requires a non-empty assignee (push model).
- **Broadcast (`recipient="all"`) fan-out is the bus's job (SPRD-1-5);** the store treats `"all"` as an opaque recipient — `UnreadFor` matches the exact string.
