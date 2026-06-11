-- RP-23: worker task proposals — the bottom-up work inlet. A worker cannot
-- write the task ledger (single-writer invariant, design v1), but it CAN file
-- a proposal here; the leader turns an accepted one into a real task through
-- the normal create+assign flow. Three terminal states, no reopen: re-raising
-- means a new row, so the full decision history survives.
--
-- ref_task is deliberately NOT a FOREIGN KEY: it is an audit pointer from an
-- accepted proposal to the task it became. A real FK would entangle the
-- RP-16 vacuum's transitive pinning fixpoint and keep completed tasks alive
-- just because their origin proposal still exists (or vice versa); either row
-- may be archived independently.

CREATE TABLE proposals (
  id                 INTEGER PRIMARY KEY AUTOINCREMENT,
  proposer           TEXT    NOT NULL,
  title              TEXT    NOT NULL,
  spec               TEXT    NOT NULL DEFAULT '',
  suggested_assignee TEXT    NOT NULL DEFAULT '',
  status             TEXT    NOT NULL DEFAULT 'open',  -- open | accepted | declined
  decided_by         TEXT    NOT NULL DEFAULT '',
  decide_note        TEXT    NOT NULL DEFAULT '',
  ref_task           INTEGER,                          -- task an accepted proposal became (audit pointer)
  created_at         INTEGER NOT NULL,                 -- unix millis
  decided_at         INTEGER                           -- unix millis; NULL while open
);

CREATE INDEX idx_proposals_status ON proposals(status);
