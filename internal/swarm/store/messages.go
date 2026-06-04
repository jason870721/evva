package store

import (
	"database/sql"
	"errors"
	"fmt"
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
// Times are unix millis.
type Message struct {
	ID        string
	Sender    string
	Recipient string // agent name | RecipientAll
	Subject   string
	Body      string
	RefTask   *int64
	ReadAt    *int64
	CreatedAt int64
}

const msgCols = `id, sender, recipient, subject, body, ref_task, read_at, created_at`

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
	return nil
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
	}
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
		m       Message
		subject sql.NullString
		refTask sql.NullInt64
		readAt  sql.NullInt64
	)
	if err := sc.Scan(&m.ID, &m.Sender, &m.Recipient, &subject, &m.Body, &refTask, &readAt, &m.CreatedAt); err != nil {
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
