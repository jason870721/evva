package swarm

import (
	"strings"
	"testing"
)

func permModeOf(t *testing.T, sp *SwarmSpace, name string) string {
	t.Helper()
	for _, mv := range sp.Roster.Snapshot() {
		if mv.Name == name {
			return mv.PermissionMode
		}
	}
	t.Fatalf("member %q not on roster", name)
	return ""
}

// TestSetMemberPermissionMode_RuntimeSwitch: the web's per-member switch lands
// on the live agent AND the roster cache, rejects bad input, and survives a
// kill-9 restart as a runtime override — while untouched members keep their
// construction-time stance (manifest authority). A fresh register discards it.
func TestSetMemberPermissionMode_RuntimeSwitch(t *testing.T) {
	cfg := stubConfig(t)

	// --- first life ---------------------------------------------------------
	sp1, err := NewSpace("s1", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #1: %v", err)
	}
	sup1 := NewSupervisor(sp1)

	// The test manifest runs the whole space bypass (Settings.PermissionMode),
	// so the override direction here is bypass → default: dial ONE member's
	// autonomy down while its teammates keep the space-wide stance.
	if err := sup1.SetMemberPermissionMode("worker-a", "default"); err != nil {
		t.Fatalf("SetMemberPermissionMode: %v", err)
	}
	if got := permModeOf(t, sp1, "worker-a"); got != "default" {
		t.Errorf("roster mode after switch: got %q, want default", got)
	}
	if got := sp1.agents["worker-a"].PermissionModeName(); got != "default" {
		t.Errorf("live agent mode after switch: got %q, want default", got)
	}

	// Bad input errors (→ 400 at the web layer); unknown member names the
	// 404-mapped keyword.
	if err := sup1.SetMemberPermissionMode("worker-a", "yolo"); err == nil {
		t.Error("invalid mode accepted")
	}
	if err := sup1.SetMemberPermissionMode("ghost", "default"); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown member: got %v, want an 'unknown' error", err)
	}

	sp1.Shutdown() // simulate the process dying

	// --- restart: the override is reapplied over the construction seed ------
	sp2, err := NewSpace("s1", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #2: %v", err)
	}
	sp2.Reload()
	defer sp2.Shutdown()

	if got := sp2.agents["worker-a"].PermissionModeName(); got != "default" {
		t.Errorf("agent mode after restart: got %q, want default (override lost)", got)
	}
	if got := permModeOf(t, sp2, "worker-a"); got != "default" {
		t.Errorf("roster mode after restart: got %q, want default", got)
	}
	// Untouched members keep their construction-time stance (space bypass).
	if got := permModeOf(t, sp2, "worker-b"); got != "bypass" {
		t.Errorf("worker-b lost its manifest stance: got %q, want bypass", got)
	}

	// Fresh register's discard: manifest is authoritative again.
	sp2.DiscardRuntimePermModes()
	if rt := loadRuntime(sp2.Workdir); rt.PermModes != nil {
		t.Errorf("PermModes still in runtime.json after discard: %+v", rt.PermModes)
	}
}
