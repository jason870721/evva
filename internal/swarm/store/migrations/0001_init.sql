-- Veronica per-space ledger (design §7.2).
--
-- tasks    : the shared task ledger. 5-state machine + Leader-only status
--            writes are enforced at the DAO (tasks.go), not in SQL. Push model:
--            assignee is set at creation (single writer = Leader, no claim race).
-- messages : durable message store. id is a UUID; the in-memory mailbox chan
--            carries only that id. read_at NULL = unread; drain stamps it.

CREATE TABLE tasks (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  title       TEXT    NOT NULL,
  spec        TEXT    NOT NULL,                  -- task description + acceptance criteria
  status      TEXT    NOT NULL DEFAULT 'pending',-- pending|running|suspended|verifying|completed
  assignee    TEXT    NOT NULL,                  -- push: assigned at creation
  created_by  TEXT    NOT NULL,                  -- usually the leader
  result      TEXT,                              -- worker output summary awaiting verification
  verify_note TEXT,                              -- leader's approve/reject rationale
  parent_id   INTEGER REFERENCES tasks(id),      -- optional task-decomposition tree
  created_at  INTEGER NOT NULL,                  -- unix millis
  updated_at  INTEGER NOT NULL
);

CREATE INDEX idx_tasks_status   ON tasks(status);
CREATE INDEX idx_tasks_assignee ON tasks(assignee);

CREATE TABLE messages (
  id         TEXT    PRIMARY KEY,                -- UUID; the chan carries only this
  sender     TEXT    NOT NULL,
  recipient  TEXT    NOT NULL,                   -- agent name | 'all'
  subject    TEXT,
  body       TEXT    NOT NULL,
  ref_task   INTEGER REFERENCES tasks(id),       -- optional task linkage
  read_at    INTEGER,                            -- NULL = unread; stamped on drain
  created_at INTEGER NOT NULL
);

-- Inbox scan: unread-for-recipient, oldest first (restart reload + drain A).
CREATE INDEX idx_msg_inbox ON messages(recipient, read_at, created_at);
