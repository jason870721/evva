package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// RP-17 DoD#2: Offer never blocks the caller — with no writer draining, a
// full buffer drops and counts instead of freezing.
func TestEventLogOfferNeverBlocks(t *testing.T) {
	l := &eventLog{ch: make(chan []byte, 2)} // writer deliberately not running
	done := make(chan struct{})
	go func() {
		for i := 0; i < 10; i++ {
			l.Offer([]byte(`{"k":1}`))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Offer blocked on a full buffer")
	}
	if got := l.Dropped(); got != 8 {
		t.Fatalf("dropped = %d, want 8 (10 offers, buffer 2)", got)
	}
}

// The writer stamps + frames lines, rotates on the local day flip, and prunes
// day files older than the retention window at rotation time.
func TestEventLogRotateAndPrune(t *testing.T) {
	workdir := t.TempDir()
	dir := filepath.Join(workdir, ".vero", "events")

	// A stale file well past the 30-day window must vanish on the first rotation.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(dir, "2020-01-01.jsonl")
	if err := os.WriteFile(stale, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	day1 := time.Date(2026, 6, 10, 23, 59, 0, 0, time.Local)
	day2 := day1.Add(2 * time.Minute) // crosses midnight into 2026-06-11
	var clock atomic.Pointer[time.Time]
	clock.Store(&day1)

	l := &eventLog{
		dir:       dir,
		retention: 30,
		ch:        make(chan []byte, 16),
		done:      make(chan struct{}),
		now:       func() time.Time { return *clock.Load() },
	}
	go l.run()

	l.Offer([]byte(`{"spaceId":"x","event":{"kind":"run_start"}}`))
	waitForCond(t, func() bool { return l.Logged() == 1 })
	clock.Store(&day2)
	l.Offer([]byte(`{"spaceId":"x","event":{"kind":"run_end"}}`))
	waitForCond(t, func() bool { return l.Logged() == 2 })
	l.Close()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale day file survived the rotation prune")
	}
	b1, err := os.ReadFile(filepath.Join(dir, "2026-06-10.jsonl"))
	if err != nil {
		t.Fatalf("day1 file: %v", err)
	}
	b2, err := os.ReadFile(filepath.Join(dir, "2026-06-11.jsonl"))
	if err != nil {
		t.Fatalf("day2 file: %v", err)
	}
	if !strings.Contains(string(b1), "run_start") || !strings.Contains(string(b2), "run_end") {
		t.Fatalf("rotation split wrong: day1=%q day2=%q", b1, b2)
	}

	// Each line is one JSON object: an offset-stamped ts + the verbatim event.
	var line struct {
		TS    string          `json:"ts"`
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(b1, &line); err != nil || line.TS == "" || len(line.Event) == 0 {
		t.Fatalf("line shape = %q (err %v), want {ts, event}", b1, err)
	}
	if !strings.Contains(line.TS, "2026-06-10") {
		t.Fatalf("ts = %q, want the injected wall clock", line.TS)
	}
}

func waitForCond(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never became true")
}

// Integration: a registered space with event_log on mirrors its lifecycle into
// .vero/events/<today>.jsonl; the stub default (Go zero value = off) does no IO.
func TestEventLogIntegration(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()

	// Toggle ON: stub manifest with EventLog set.
	m := stubManifest()
	m.Settings.EventLog = true
	cfg := stubConfig(t)
	idOn, err := svc.register("sp-evlog-on", "evlog-on", m, stubLoaded(), cfg, false)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := svc.SendUserMessage(idOn, "leader", "", "hello"); err != nil {
		t.Fatalf("send: %v", err)
	}
	evDir := filepath.Join(cfg.WorkDir, ".vero", "events")
	var content []byte
	waitForCond(t, func() bool {
		entries, err := os.ReadDir(evDir)
		if err != nil || len(entries) == 0 {
			return false
		}
		content, _ = os.ReadFile(filepath.Join(evDir, entries[0].Name()))
		return strings.Contains(string(content), `"turn_start"`) &&
			strings.Contains(string(content), `"run_end"`)
	})
	if !strings.Contains(string(content), `"spaceId":"sp-evlog-on"`) {
		t.Fatalf("event line lacks the wire envelope: %q", content)
	}
	// RP-28 acceptance: the run_end line in the FILE carries the run's own
	// token cost (the stub reports 120 in / 30 out per turn), so one day of
	// event log reconstructs any member's per-run series with jq alone.
	if !strings.Contains(string(content), `"InputTokens":120`) || !strings.Contains(string(content), `"OutputTokens":30`) {
		t.Fatalf("run_end line lacks per-run usage: %q", content)
	}

	// Toggle OFF (the plain stub): same activity, no events dir.
	cfgOff := stubConfig(t)
	idOff, err := svc.register("sp-evlog-off", "evlog-off", stubManifest(), stubLoaded(), cfgOff, false)
	if err != nil {
		t.Fatalf("register off: %v", err)
	}
	if _, err := svc.SendUserMessage(idOff, "leader", "", "hello"); err != nil {
		t.Fatalf("send off: %v", err)
	}
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(cfgOff.WorkDir, ".vero", "events")); err == nil {
			t.Fatal("event_log off still wrote .vero/events")
		}
		time.Sleep(25 * time.Millisecond)
	}
}
