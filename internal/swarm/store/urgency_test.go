package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestMigration0008AppliesToPopulatedMailbox: a real pre-STE ledger
// (migrations 1..7, messages already in the old column set) opens through the
// normal path, and its existing rows read back as normal urgency without a
// backfill. That is the whole reason the column defaults to an empty string rather than
// carrying a CHECK constraint — an operator upgrading mid-conversation must
// not have their mailbox rewritten, or refused.
func TestMigration0008AppliesToPopulatedMailbox(t *testing.T) {
	dir := t.TempDir()
	vero := filepath.Join(dir, ".vero")
	if err := os.MkdirAll(vero, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(vero, "vero.db")+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		v, err := migrationVersion(name)
		if err != nil {
			t.Fatalf("version of %s: %v", name, err)
		}
		if v > 7 {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply legacy %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 1)`, v); err != nil {
			t.Fatalf("record legacy %s: %v", name, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO messages (id, sender, recipient, subject, body, created_at)
		 VALUES ('legacy-1', 'lead', 'worker', 'hi', 'old body', 1)`); err != nil {
		t.Fatalf("insert legacy message: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open over legacy ledger: %v", err)
	}
	defer st.Close()

	got, err := st.GetMessage("legacy-1")
	if err != nil {
		t.Fatalf("GetMessage(legacy row): %v", err)
	}
	if got.Urgency != "" {
		t.Errorf("legacy urgency = %q, want empty (reads as normal)", got.Urgency)
	}
	if got.Body != "old body" {
		t.Errorf("legacy body = %q — the migration disturbed existing rows", got.Body)
	}

	// And a fresh urgent row writes and reads back post-migration.
	if err := st.PutMessage(Message{
		ID: "new-1", Sender: "lead", Recipient: "worker", Body: "stop", Urgency: UrgencyInterject,
	}); err != nil {
		t.Fatalf("put urgent message post-migration: %v", err)
	}
	back, err := st.GetMessage("new-1")
	if err != nil {
		t.Fatalf("GetMessage(new): %v", err)
	}
	if back.Urgency != UrgencyInterject {
		t.Errorf("urgency readback = %q, want %q", back.Urgency, UrgencyInterject)
	}
}

// TestUrgencySurvivesEveryReadPath: the column is on msgCols, so every query
// that uses it has to scan it. A path that silently dropped urgency would make
// the web mailbox view disagree with what the recipient actually experienced.
func TestUrgencySurvivesEveryReadPath(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.PutMessage(Message{
		ID: "m1", Sender: "lead", Recipient: "worker", Body: "stop", Urgency: UrgencyInterject,
	}); err != nil {
		t.Fatal(err)
	}

	listed, err := st.ListMessages(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Urgency != UrgencyInterject {
		t.Errorf("ListMessages urgency = %+v", listed)
	}

	claimed, err := st.ClaimUnread("worker")
	if err != nil {
		t.Fatal(err)
	}
	if len(claimed) != 1 || claimed[0].Urgency != UrgencyInterject {
		t.Errorf("ClaimUnread urgency = %+v", claimed)
	}
}

// TestPutMessageIfNewCarriesUrgency covers the idempotent insert path used by
// the external-event webhook, which has its own INSERT statement.
func TestPutMessageIfNewCarriesUrgency(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	inserted, _, err := st.PutMessageIfNew(Message{
		ID: "x", Sender: "hook", Recipient: "worker", Body: "halt", Urgency: UrgencyInterject,
	}, "key-1")
	if err != nil || !inserted {
		t.Fatalf("PutMessageIfNew = %v, %v", inserted, err)
	}
	got, err := st.GetMessage("x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Urgency != UrgencyInterject {
		t.Errorf("urgency = %q, want %q", got.Urgency, UrgencyInterject)
	}
}
