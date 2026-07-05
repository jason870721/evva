-- DWF-1: the task ledger becomes a dependency graph the engine can execute.
--
-- task_deps holds the edges: task_id depends on depends_on_id. Deps may only
-- reference tasks that already exist and are immutable after creation, so the
-- graph is acyclic BY CONSTRUCTION — no cycle detection anywhere. A task with
-- at least one edge is "engine-managed": the engine (system actor) dispatches
-- it the moment its dependencies complete; a task with no edges stays
-- leader-managed exactly as before.
--
-- depends_on_id is a real FK (unlike proposals.ref_task): an edge to a
-- vanished task would wedge its dependent in `blocked` forever, so the ledger
-- must refuse it. The RP-16 vacuum already pins tasks transitively before
-- archiving; task_deps rows ride along with their task_id row.
--
-- verify_policy is who settles `verifying`: 'leader' (default — human-judgment
-- flow, unchanged) or 'auto' (the system actor completes it on the worker's
-- task_done, letting declared-mechanical chains flow with zero leader wakes).
-- Stored as TEXT so a future machine-evidence wave can add 'checks' without
-- another migration.

CREATE TABLE task_deps (
  task_id       INTEGER NOT NULL REFERENCES tasks(id),
  depends_on_id INTEGER NOT NULL REFERENCES tasks(id),
  PRIMARY KEY (task_id, depends_on_id)
);

CREATE INDEX idx_task_deps_on ON task_deps(depends_on_id);

ALTER TABLE tasks ADD COLUMN verify_policy TEXT NOT NULL DEFAULT 'leader';
