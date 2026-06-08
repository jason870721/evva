package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/session"
)

func TestResetWorkdirSessions(t *testing.T) {
	appHome := t.TempDir()
	workdir := t.TempDir()
	dir := session.SessionsDir(appHome, memdir.ProjectKey(workdir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s1.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ResetWorkdirSessions(appHome, workdir); err != nil {
		t.Fatalf("ResetWorkdirSessions: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("sessions dir still present after reset (err=%v)", err)
	}

	// Idempotent: a second call (dir already gone) is a no-op, not an error.
	if err := ResetWorkdirSessions(appHome, workdir); err != nil {
		t.Errorf("second reset should be a no-op, got: %v", err)
	}
	// Empty inputs are no-ops.
	if err := ResetWorkdirSessions("", workdir); err != nil {
		t.Errorf("empty appHome should no-op, got: %v", err)
	}
}
