-- STE-5 (steering-v2): mail can ask to be read NOW.
--
-- urgency is the wire word from the send_message schema: '' / 'normal' keeps
-- the historical behaviour (the recipient folds the message at its next
-- iteration boundary, or on its next wake), 'interject' additionally cuts
-- the recipient's in-flight LLM call or tool batch short so the message
-- lands at once.
--
-- Stored as free-form TEXT with a '' default rather than a CHECK constraint:
-- every existing row reads as normal without a backfill, and a future third
-- level does not need a second migration to widen an enum. The delivery path
-- treats anything it does not recognise as normal, which is the safe
-- direction — an unknown word must never be MORE disruptive than the
-- default.
--
-- Deliberately not indexed. Urgency is read on the row already being
-- delivered, never used as a query predicate.

ALTER TABLE messages ADD COLUMN urgency TEXT NOT NULL DEFAULT '';
