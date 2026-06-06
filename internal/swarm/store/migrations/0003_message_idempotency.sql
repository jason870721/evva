-- RP-9: external-event webhook idempotency. An external app may deliver the same
-- event more than once (network retry, at-least-once semantics); a caller-supplied
-- idempotency_key lets the ingest path collapse retries to a single delivery so a
-- jittery engine can't trigger the leader twice for one signal.
--
-- The unique index is PARTIAL (only non-NULL keys), so ordinary messages — which
-- carry no key — are entirely unaffected: any number of NULL-key rows coexist.
ALTER TABLE messages ADD COLUMN idempotency_key TEXT;

CREATE UNIQUE INDEX idx_messages_idempotency
  ON messages(idempotency_key) WHERE idempotency_key IS NOT NULL;
