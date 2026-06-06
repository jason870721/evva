package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// RecipientAll is the broadcast recipient. Fan-out to individual mailboxes is
// the bus's job (SPRD-1-5); the store treats "all" as an opaque recipient
// value — UnreadFor matches the exact recipient string.
const RecipientAll = "all"

// Message sentinel errors (test with errors.Is).
var (
	ErrEmptyMessageID    = errors.New("store: message requires a non-empty id")
	ErrIncompleteMessage = errors.New("store: message requires sender, recipient, and body")
	ErrMessageNotFound   = errors.New("store: message not found")
)

// Message is one durable row in the message store. ReadAt is nil while unread.
// ClaimedAt is nil unless the message is currently folded into an in-flight run
// (the unread→claimed→read lifecycle; see migration 0002). Times are unix millis.
type Message struct {
	ID        string
	Sender    string
	Recipient string // agent name | RecipientAll
	Subject   string
	Body      string
	RefTask   *int64
	ReadAt    *int64
	ClaimedAt *int64
	CreatedAt int64
}

const msgCols = `id, sender, recipient, subject, body, ref_task, read_at, claimed_at, created_at`

// PutMessage durably stores a message. The caller supplies the id (a UUID; see
// pkg/common.GenUUID). CreatedAt defaults to now when zero.
func (s *Store) PutMessage(m Message) error {
	if strings.TrimSpace(m.ID) == "" {
		return ErrEmptyMessageID
	}
	if strings.TrimSpace(m.Sender) == "" || strings.TrimSpace(m.Recipient) == "" || strings.TrimSpace(m.Body) == "" {
		return ErrIncompleteMessage
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = time.Now().UnixMilli()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(
		`INSERT INTO messages (id, sender, recipient, subject, body, ref_task, read_at, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Sender, m.Recipient, nullableStr(m.Subject), m.Body,
		nullableInt(m.RefTask), nullableInt(m.ReadAt), m.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: put message %s: %w", m.ID, err)
	}
	slog.Debug("swarm message stored", "id", m.ID, "sender", m.Sender, "recipient", m.Recipient, "ref_task", m.RefTask)
	return nil
}

// ErrEmptyIdempotencyKey guards the dedup path (the keyless path is PutMessage).
var ErrEmptyIdempotencyKey = errors.New("store: PutMessageIfNew requires a non-empty idempotency key")

// PutMessageIfNew durably stores a message keyed by an idempotency key, collapsing
// a retried external event (RP-9) to a single row: it returns inserted=false plus
// the pre-existing row's id when the key was already seen. The check-then-insert
// runs under the store's write lock (the DAO serialises every write — single
// writer), so concurrent retries within the process can't both insert; the
// partial unique index on idempotency_key is the backstop. The key is write-only
// here — it is not surfaced on Message, so the read path is untouched.
func (s *Store) PutMessageIfNew(m Message, key string) (inserted bool, existingID string, err error) {
	if strings.TrimSpace(m.ID) == "" {
		return false, "", ErrEmptyMessageID
	}
	if strings.TrimSpace(m.Sender) == "" || strings.TrimSpace(m.Recipient) == "" || strings.TrimSpace(m.Body) == "" {
		return false, "", ErrIncompleteMessage
	}
	if strings.TrimSpace(key) == "" {
		return false, "", ErrEmptyIdempotencyKey
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = time.Now().UnixMilli()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var prior string
	switch err := s.db.QueryRow(`SELECT id FROM messages WHERE idempotency_key = ?`, key).Scan(&prior); {
	case err == nil:
		return false, prior, nil // already accepted under this key
	case !errors.Is(err, sql.ErrNoRows):
		return false, "", fmt.Errorf("store: idempotency lookup: %w", err)
	}

	if _, err := s.db.Exec(
		`INSERT INTO messages (id, sender, recipient, subject, body, ref_task, read_at, created_at, idempotency_key)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.Sender, m.Recipient, nullableStr(m.Subject), m.Body,
		nullableInt(m.RefTask), nullableInt(m.ReadAt), m.CreatedAt, key,
	); err != nil {
		return false, "", fmt.Errorf("store: put message (idempotent) %s: %w", m.ID, err)
	}
	slog.Debug("swarm external event stored", "id", m.ID, "sender", m.Sender, "recipient", m.Recipient, "key", key)
	return true, "", nil
}

// GetMessage returns one message by id, or ErrMessageNotFound.
func (s *Store) GetMessage(id string) (Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	m, err := scanMessage(s.db.QueryRow(`SELECT `+msgCols+` FROM messages WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Message{}, ErrMessageNotFound
	}
	if err != nil {
		return Message{}, fmt.Errorf("store: get message %s: %w", id, err)
	}
	return m, nil
}

// MarkRead stamps read_at on a still-unread message. It is idempotent — marking
// an already-read message is a no-op — but a missing id returns
// ErrMessageNotFound.
func (s *Store) MarkRead(id string) error {
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(`UPDATE messages SET read_at = ? WHERE id = ? AND read_at IS NULL`, now, id)
	if err != nil {
		return fmt.Errorf("store: mark read %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Either the id doesn't exist or it was already read — distinguish.
		var one int
		if err := s.db.QueryRow(`SELECT 1 FROM messages WHERE id = ?`, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return ErrMessageNotFound
		}
		return nil
	}
	slog.Debug("swarm message marked read", "id", id)
	return nil
}

// ListMessages returns every message in the space, oldest first (by
// created_at, then id) — the read-only snapshot the webapi /api/messages
// endpoint serves. A zero limit returns all rows; a positive limit caps the
// result to the most recent N (still returned oldest-first). Returns a non-nil
// empty slice when there are none.
func (s *Store) ListMessages(limit int) ([]Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	q := `SELECT ` + msgCols + ` FROM messages ORDER BY created_at, id`
	if limit > 0 {
		// Take the newest `limit` rows, then re-order ascending so the caller
		// always sees oldest-first regardless of the cap.
		q = `SELECT ` + msgCols + ` FROM (
			SELECT ` + msgCols + ` FROM messages ORDER BY created_at DESC, id DESC LIMIT ?
		) ORDER BY created_at, id`
	}

	var (
		rows *sql.Rows
		err  error
	)
	if limit > 0 {
		rows, err = s.db.Query(q, limit)
	} else {
		rows, err = s.db.Query(q)
	}
	if err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	defer rows.Close()

	out := make([]Message, 0)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan message: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// scanMessage reads one message row from a *sql.Row or *sql.Rows.
func scanMessage(sc rowScanner) (Message, error) {
	var (
		m         Message
		subject   sql.NullString
		refTask   sql.NullInt64
		readAt    sql.NullInt64
		claimedAt sql.NullInt64
	)
	if err := sc.Scan(&m.ID, &m.Sender, &m.Recipient, &subject, &m.Body, &refTask, &readAt, &claimedAt, &m.CreatedAt); err != nil {
		return Message{}, err
	}
	m.Subject = subject.String
	if refTask.Valid {
		v := refTask.Int64
		m.RefTask = &v
	}
	if readAt.Valid {
		v := readAt.Int64
		m.ReadAt = &v
	}
	if claimedAt.Valid {
		v := claimedAt.Int64
		m.ClaimedAt = &v
	}
	return m, nil
}

// UnreadFor returns the ids of unread messages for a recipient, oldest first
// (by created_at, then id). This is the restart-reload + drain-A scan. Returns
// a non-nil empty slice when there are none.
func (s *Store) UnreadFor(recipient string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT id FROM messages WHERE recipient = ? AND read_at IS NULL ORDER BY created_at, id`,
		recipient,
	)
	if err != nil {
		return nil, fmt.Errorf("store: unread for %s: %w", recipient, err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("store: scan unread id: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ClaimUnread atomically claims every currently unread+unclaimed message for a
// recipient (oldest first) and returns the full rows — the run-start batch
// (drain A). "Claimed" means folded into an in-flight run: read_at stays NULL
// (so a crash leaves it recoverable), claimed_at marks it in-flight (so drain B
// won't re-fold it). The SELECT and UPDATE are consistent because the store
// serialises all writes under mu — no PutMessage can insert between them — so the
// returned set is exactly the set claimed. A clean run later calls SettleClaimed;
// an aborted one calls UnclaimFor. Returns a non-nil empty slice when none.
func (s *Store) ClaimUnread(recipient string) ([]Message, error) {
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	rows, err := s.db.Query(
		`SELECT `+msgCols+` FROM messages WHERE recipient = ? AND read_at IS NULL AND claimed_at IS NULL ORDER BY created_at, id`,
		recipient,
	)
	if err != nil {
		return nil, fmt.Errorf("store: claim unread for %s: %w", recipient, err)
	}
	out := make([]Message, 0)
	for rows.Next() {
		m, err := scanMessage(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: scan claim: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(out) == 0 {
		return out, nil
	}

	if _, err := s.db.Exec(
		`UPDATE messages SET claimed_at = ? WHERE recipient = ? AND read_at IS NULL AND claimed_at IS NULL`,
		now, recipient,
	); err != nil {
		return nil, fmt.Errorf("store: claim unread update for %s: %w", recipient, err)
	}
	for i := range out {
		out[i].ClaimedAt = &now
	}
	return out, nil
}

// ClaimOne claims a single message by id for drain B (the mid-run fold), iff it
// is still unread AND unclaimed. ok=false means it was already folded (claimed by
// the start batch or an earlier drain-B poll), already read, or gone — the
// caller skips it, which is exactly the dedup that stops a message being folded
// twice. A claimed message settles to read on the run's clean end, or resets to
// unread on abort.
func (s *Store) ClaimOne(id string) (Message, bool, error) {
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.Exec(
		`UPDATE messages SET claimed_at = ? WHERE id = ? AND read_at IS NULL AND claimed_at IS NULL`,
		now, id,
	)
	if err != nil {
		return Message{}, false, fmt.Errorf("store: claim one %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Message{}, false, nil // not claimable — already folded/read/missing
	}
	m, err := scanMessage(s.db.QueryRow(`SELECT `+msgCols+` FROM messages WHERE id = ?`, id))
	if err != nil {
		return Message{}, false, fmt.Errorf("store: claim one fetch %s: %w", id, err)
	}
	return m, true, nil
}

// SettleClaimed stamps read_at on every message a recipient claimed during a run
// that has now finished cleanly — the start batch plus anything drain B folded.
// This is the single mark-read point (drain A and drain B both flow through it),
// so the two drains can never disagree about when a message becomes read.
func (s *Store) SettleClaimed(recipient string) error {
	now := time.Now().UnixMilli()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(
		`UPDATE messages SET read_at = ? WHERE recipient = ? AND claimed_at IS NOT NULL AND read_at IS NULL`,
		now, recipient,
	); err != nil {
		return fmt.Errorf("store: settle claimed for %s: %w", recipient, err)
	}
	return nil
}

// UnclaimFor resets a recipient's in-flight claims back to unread — called when a
// run aborts (suspend / cancel / error) so the mail retries on resume, and on
// restart so a claim left dangling by a crashed run is recovered. Read messages
// are untouched.
func (s *Store) UnclaimFor(recipient string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.Exec(
		`UPDATE messages SET claimed_at = NULL WHERE recipient = ? AND read_at IS NULL AND claimed_at IS NOT NULL`,
		recipient,
	); err != nil {
		return fmt.Errorf("store: unclaim for %s: %w", recipient, err)
	}
	return nil
}
