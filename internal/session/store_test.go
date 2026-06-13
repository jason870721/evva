package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/llm"
)

func newSnapshot(id, slug, prompt string) *Snapshot {
	return &Snapshot{
		Version:         SnapshotVersion,
		SessionID:       id,
		Workdir:         "/tmp/proj",
		WorkdirSlug:     slug,
		Profile:         "evva",
		Provider:        "anthropic",
		Model:           "claude-opus-4-8",
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
		FirstUserPrompt: prompt,
		Session: SessionState{
			Messages: []llm.Message{{Role: llm.RoleUser, Content: prompt}},
		},
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	home := t.TempDir()
	want := newSnapshot("abc123", "-tmp-proj", "first user prompt")
	if err := Save(home, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(home, want.WorkdirSlug, want.SessionID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SessionID != want.SessionID || got.FirstUserPrompt != want.FirstUserPrompt {
		t.Errorf("round-trip mismatch: got=%+v want=%+v", got, want)
	}
	if len(got.Session.Messages) != 1 || got.Session.Messages[0].Content != want.FirstUserPrompt {
		t.Errorf("session messages diverged: %+v", got.Session.Messages)
	}
}

func TestListSortsMTimeDesc(t *testing.T) {
	home := t.TempDir()
	older := newSnapshot("aaa", "-tmp", "older prompt")
	newer := newSnapshot("bbb", "-tmp", "newer prompt")
	if err := Save(home, older); err != nil {
		t.Fatalf("save older: %v", err)
	}
	// Bump the older file's mtime backwards so we can guarantee ordering
	// regardless of how fast the test runs.
	olderPath := SessionFilePath(home, older.WorkdirSlug, older.SessionID)
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(olderPath, past, past); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	if err := Save(home, newer); err != nil {
		t.Fatalf("save newer: %v", err)
	}
	entries, warnings, err := List(home, "-tmp")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("expected no warnings; got %v", warnings)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries; got %d", len(entries))
	}
	if entries[0].Snapshot.SessionID != "bbb" {
		t.Errorf("expected newest first; got %q then %q",
			entries[0].Snapshot.SessionID, entries[1].Snapshot.SessionID)
	}
}

func TestListSkipsCorruptFiles(t *testing.T) {
	home := t.TempDir()
	good := newSnapshot("good", "-tmp", "good prompt")
	if err := Save(home, good); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Drop a junk file alongside the good one.
	junkPath := filepath.Join(SessionsDir(home, "-tmp"), "garbage.json")
	if err := os.WriteFile(junkPath, []byte("{not-json"), 0o644); err != nil {
		t.Fatalf("write junk: %v", err)
	}
	entries, warnings, err := List(home, "-tmp")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 || entries[0].Snapshot.SessionID != "good" {
		t.Errorf("good entry missing or polluted: %+v", entries)
	}
	if len(warnings) == 0 || !strings.Contains(warnings[0], "garbage.json") {
		t.Errorf("expected a warning naming the junk file; got %v", warnings)
	}
}

func TestListEmptyDirReturnsNoError(t *testing.T) {
	home := t.TempDir()
	entries, warnings, err := List(home, "-never-existed")
	if err != nil {
		t.Fatalf("List on missing dir should succeed; got %v", err)
	}
	if len(entries) != 0 || len(warnings) != 0 {
		t.Errorf("expected zero entries/warnings; got %v / %v", entries, warnings)
	}
}

func TestDeleteIsIdempotent(t *testing.T) {
	home := t.TempDir()
	s := newSnapshot("x", "-tmp", "x")
	if err := Save(home, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := Delete(home, s.WorkdirSlug, s.SessionID); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if err := Delete(home, s.WorkdirSlug, s.SessionID); err != nil {
		t.Errorf("second delete should be a no-op; got %v", err)
	}
}

func TestSaveRejectsBadEnvelope(t *testing.T) {
	home := t.TempDir()
	if err := Save(home, nil); err == nil {
		t.Error("Save(nil) should error")
	}
	if err := Save(home, &Snapshot{Version: SnapshotVersion}); err == nil {
		t.Error("Save with empty slug/id should error")
	}
}
