-- CHK-1 (swarm-verify-checks): machine evidence lands on the task row.
--
-- checks holds the LATEST check run's evidence as JSON ({command, exit,
-- timedOut, durationMs, startedAt, workdir, tail, truncated, pass});
-- NULL = never ran. Latest-run-only by design — column overwrite, not
-- append: the row carries the current fact, the durable event log carries
-- the history (the .vero archive philosophy).
--
-- check_off is the ONE lever agents hold over checks (PRD §4 trust model):
-- a task created with check:"off" (docs-only, discussion) never enqueues a
-- check. The command text itself is operator-authored manifest config —
-- no task field ever executes.
--
-- verify_policy needs no change for its new 'checks' value: 0006 stored it
-- as free-form TEXT for exactly this wave.

ALTER TABLE tasks ADD COLUMN checks TEXT;
ALTER TABLE tasks ADD COLUMN check_off INTEGER NOT NULL DEFAULT 0;
