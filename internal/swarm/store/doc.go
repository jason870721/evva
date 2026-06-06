// Package store is the single per-space data layer: it opens
// <workdir>/.vero/vero.db (WAL + busy_timeout, foreign keys on), runs
// migrations, and exposes a sync.RWMutex-wrapped DAO for the task ledger and
// the message store.
//
// The task ledger is a 5-state machine (pending -> running ->
// {suspended,verifying} -> completed) with Leader-only status writes — the DAO
// takes a `by` actor and rejects non-leader transitions and illegal moves. The
// `messages` table is the multi-writer hot spot the RWMutex really protects;
// tasks are single-writer (the Leader) by design, so there is no claim race.
//
// This is NOT evva's built-in `todo` store (which is private, ephemeral, and
// auto-collapses): the Veronica ledger is cross-agent, durable, and gated by a
// verification step.
//
// TODO(SPRD-1-2): implement Open, migrations, the task state machine
// (tasks.go), and the message DAO incl. UnreadFor (messages.go).
package store
