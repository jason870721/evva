package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
)

// boardSpace is the minimal space the blackboard needs: a workdir. No store,
// no roster — the board is a file, deliberately independent of the ledger.
func boardSpace(t *testing.T) *SwarmSpace {
	t.Helper()
	return &SwarmSpace{Workdir: t.TempDir()}
}

// BB-1: write→read round-trip; absent = dormant everywhere; freshness +
// attribution ride along.
func TestBlackboardWriteReadRoundTrip(t *testing.T) {
	sp := boardSpace(t)

	if c, mt, by := sp.Blackboard(); c != "" || !mt.IsZero() || by != "" {
		t.Fatalf("fresh space should be dormant, got (%q, %v, %q)", c, mt, by)
	}
	if s := sp.blackboardWakeSection(time.Now()); s != "" {
		t.Fatalf("dormant board must add zero bytes to the brief, got %q", s)
	}

	doc := "# Plan\nGoal: ship v2.\n- qa owns the regression sweep"
	n, err := sp.WriteBlackboard("lead", doc)
	if err != nil || n != len(doc) {
		t.Fatalf("WriteBlackboard = (%d, %v), want (%d, nil)", n, err, len(doc))
	}
	c, mt, by := sp.Blackboard()
	if c != doc || by != "lead" || mt.IsZero() {
		t.Fatalf("Blackboard = (%q, %v, %q)", c, mt, by)
	}

	sec := sp.blackboardWakeSection(mt.Add(3 * time.Minute))
	if !strings.HasPrefix(sec, "## Team blackboard (updated 3m ago by lead)\n") || !strings.HasSuffix(sec, doc) {
		t.Fatalf("wake section = %q", sec)
	}

	// The file is the truth on disk, operator-readable beside the ledger.
	b, err := os.ReadFile(filepath.Join(sp.Workdir, ".vero", "blackboard.md"))
	if err != nil || string(b) != doc {
		t.Fatalf("on-disk board = (%q, %v)", b, err)
	}
}

// BB-1: a write over the cap fails loudly, names the cap and the overage, and
// leaves the previous board intact. Hand-built spaces get the default cap.
func TestBlackboardCapRejectsOversize(t *testing.T) {
	sp := boardSpace(t)
	if _, err := sp.WriteBlackboard("lead", "keep me"); err != nil {
		t.Fatal(err)
	}

	sp.settings.BlackboardMaxBytes = 64
	_, err := sp.WriteBlackboard("lead", strings.Repeat("x", 65))
	if err == nil {
		t.Fatal("oversize write must fail")
	}
	for _, want := range []string{"64-byte cap", "1 over", "blackboard_max_bytes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
	if c, _, _ := sp.Blackboard(); c != "keep me" {
		t.Errorf("rejected write must leave the board intact, got %q", c)
	}
	// Exactly at the cap is fine.
	if _, err := sp.WriteBlackboard("lead", strings.Repeat("y", 64)); err != nil {
		t.Errorf("at-cap write should pass: %v", err)
	}

	// Settings zero value (no manifest ran) = the default cap, not unwritable.
	sp.settings.BlackboardMaxBytes = 0
	if _, err := sp.WriteBlackboard("lead", strings.Repeat("z", agentdef.DefaultBlackboardMaxBytes+1)); err == nil {
		t.Error("default cap should gate a hand-built space too")
	} else if !strings.Contains(err.Error(), "4096-byte cap") {
		t.Errorf("default-cap error should name 4096, got %v", err)
	}
}

// BB-1: writing empty content clears the board back to dormant.
func TestBlackboardClear(t *testing.T) {
	sp := boardSpace(t)
	if _, err := sp.WriteBlackboard("lead", "something"); err != nil {
		t.Fatal(err)
	}
	n, err := sp.WriteBlackboard("lead", "")
	if err != nil || n != 0 {
		t.Fatalf("clear = (%d, %v)", n, err)
	}
	if c, mt, by := sp.Blackboard(); c != "" || !mt.IsZero() || by != "" {
		t.Errorf("cleared board should read dormant, got (%q, %v, %q)", c, mt, by)
	}
	if s := sp.blackboardWakeSection(time.Now()); s != "" {
		t.Errorf("cleared board must vanish from the brief, got %q", s)
	}
}

// BB-1/§5.4: an operator disk edit is live at the next read (disk is the
// truth) and drops the now-wrong tool-writer attribution — the freshness line
// carries no author rather than a stale one.
func TestBlackboardDiskEditDropsAttribution(t *testing.T) {
	sp := boardSpace(t)
	if _, err := sp.WriteBlackboard("lead", "tool version"); err != nil {
		t.Fatal(err)
	}
	if _, _, by := sp.Blackboard(); by != "lead" {
		t.Fatalf("attribution after tool write = %q, want lead", by)
	}

	path := filepath.Join(sp.Workdir, ".vero", "blackboard.md")
	if err := os.WriteFile(path, []byte("operator version"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force a distinct mtime — same-second edits are below some filesystems'
	// stamp resolution, and the test must not depend on write latency.
	edited := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, edited, edited); err != nil {
		t.Fatal(err)
	}

	c, _, by := sp.Blackboard()
	if c != "operator version" {
		t.Errorf("disk edit should be live at the next read, got %q", c)
	}
	if by != "" {
		t.Errorf("disk edit must drop the tool-writer attribution, got %q", by)
	}
	if sec := sp.blackboardWakeSection(edited.Add(time.Minute)); !strings.HasPrefix(sec, "## Team blackboard (updated 1m ago)\n") {
		t.Errorf("post-edit section should carry freshness without an author: %q", sec)
	}
}

// BB §5.4: each write self-audits as one blackboard_updated engine event —
// writer in AgentID, size (or the clear) in the line.
func TestBlackboardWriteEmitsEvent(t *testing.T) {
	sp := boardSpace(t)
	sp.out = make(chan SpacedEvent, 4)

	if _, err := sp.WriteBlackboard("lead", "hello board"); err != nil {
		t.Fatal(err)
	}
	ev := <-sp.out
	if ev.Event.Kind != KindBlackboardUpdated || ev.Event.AgentID != "lead" {
		t.Fatalf("event = %+v", ev.Event)
	}
	if txt := ev.Event.Text.Text; !strings.Contains(txt, "updated by lead") || !strings.Contains(txt, fmt.Sprintf("(%d bytes)", len("hello board"))) {
		t.Errorf("event line = %q", txt)
	}

	if _, err := sp.WriteBlackboard("lead", ""); err != nil {
		t.Fatal(err)
	}
	if txt := (<-sp.out).Event.Text.Text; !strings.Contains(txt, "cleared by lead") {
		t.Errorf("clear line = %q", txt)
	}
}

// BB-1: a hand-edited file past the hard ceiling is truncated on the read
// side — the write-side cap can't gate an editor, and one runaway file must
// not flood every member's wake.
func TestBlackboardReadTruncatesHandEditedOversize(t *testing.T) {
	sp := boardSpace(t)
	path := filepath.Join(sp.Workdir, ".vero", "blackboard.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", agentdef.MaxBlackboardMaxBytes+4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	c, _, _ := sp.Blackboard()
	if len(c) > agentdef.MaxBlackboardMaxBytes+128 {
		t.Errorf("read must truncate at the ceiling, got %d bytes", len(c))
	}
	if !strings.Contains(c, "truncated at the blackboard ceiling") {
		t.Error("truncation must be visible to the reader")
	}
}

// BB §4: one writer role × whole-file atomic rename — readers never see a
// torn document even under racing writes, and no temp litter survives.
func TestBlackboardConcurrentWritesNeverTear(t *testing.T) {
	sp := boardSpace(t)
	docs := make([]string, 8)
	for i := range docs {
		docs[i] = fmt.Sprintf("version %d\n%s", i, strings.Repeat(fmt.Sprintf("line-%d ", i), 50))
	}
	var wg sync.WaitGroup
	for _, d := range docs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := sp.WriteBlackboard("lead", d); err != nil {
				t.Errorf("concurrent write: %v", err)
			}
		}()
	}
	wg.Wait()

	c, _, _ := sp.Blackboard()
	found := false
	for _, d := range docs {
		if c == strings.TrimSpace(d) { // Blackboard() trims the document edges
			found = true
			break
		}
	}
	if !found {
		t.Errorf("final board must be exactly one write's document, got %q", c[:min(len(c), 80)])
	}
	entries, err := os.ReadDir(filepath.Join(sp.Workdir, ".vero"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// BB-3 integration: a board write is visible in the NEXT wake brief of BOTH
// wake kinds — message and timer — and the write itself wakes no one (the
// zero-wake economics the whole design rides on).
func TestBlackboardVisibleInNextWakeBriefs(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{
		"w": agentdef.RoleWorker, "patrol": agentdef.RoleWorker,
	})
	sp.schedules["patrol"] = agentdef.Schedule{Every: 20 * time.Millisecond}
	startSup(t, sp)

	if _, err := sp.WriteBlackboard("lead", "Goal: ship v2. qa owns the sweep."); err != nil {
		t.Fatal(err)
	}
	// The write pokes nobody: a mail-less member stays idle through it.
	time.Sleep(40 * time.Millisecond)
	if got := ctls["w"].runs.Load(); got != 0 {
		t.Fatalf("board write woke an idle member (%d runs)", got)
	}

	if _, err := sp.Bus.Send(store.Message{Sender: "boss", Recipient: "w", Body: "go"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "w wakes on mail", func() bool { return ctls["w"].runs.Load() >= 1 })
	if p := ctls["w"].lastPrompt(); !strings.Contains(p, "## Team blackboard (updated just now by lead)") ||
		!strings.Contains(p, "Goal: ship v2.") {
		t.Errorf("mail wake missing the board:\n%s", p)
	}

	waitFor(t, 2*time.Second, "patrol timer wake carries the board", func() bool {
		p := ctls["patrol"].lastPrompt()
		return strings.Contains(p, "## Team blackboard") && strings.Contains(p, "Goal: ship v2.")
	})
}

// The freshness vocabulary is coarse on purpose: current vs stale at a
// glance, no stopwatch.
func TestAgeString(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{-5 * time.Second, "just now"},
		{30 * time.Second, "just now"},
		{3 * time.Minute, "3m ago"},
		{59 * time.Minute, "59m ago"},
		{5 * time.Hour, "5h ago"},
		{48 * time.Hour, "2d ago"},
	}
	for _, c := range cases {
		if got := AgeString(c.d); got != c.want {
			t.Errorf("AgeString(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
