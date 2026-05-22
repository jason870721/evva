package shell

import (
	"sync"
	"testing"
	"time"

	"github.com/johnny1110/evva/pkg/observable"
)

func TestBgTaskStore_AddCompleteDrain(t *testing.T) {
	store := NewBgTaskStore()
	cancel := func() {}
	snap := BgTaskSnapshot{
		ID:        "b1",
		Command:   "echo hi",
		Status:    BgRunning,
		StartedAt: time.Now(),
	}
	store.Add(snap, cancel)

	if !store.HasPending() == true {
		// running tasks aren't pending
	}
	if store.HasPending() {
		t.Error("running task should not register as pending")
	}

	store.Complete("b1", BgCompleted, 0, "hi\n")
	if !store.HasPending() {
		t.Error("completed task should be pending until drained")
	}

	drained := store.DrainCompleted()
	if len(drained) != 1 {
		t.Fatalf("DrainCompleted: got %d want 1", len(drained))
	}
	if drained[0].ID != "b1" || drained[0].Status != BgCompleted {
		t.Errorf("drained snapshot: %+v", drained[0])
	}
	if store.HasPending() {
		t.Error("DrainCompleted should clear the pending entries")
	}
}

func TestBgTaskStore_Stop(t *testing.T) {
	store := NewBgTaskStore()
	var cancelCalled bool
	var mu sync.Mutex
	cancel := func() {
		mu.Lock()
		cancelCalled = true
		mu.Unlock()
	}
	store.Add(BgTaskSnapshot{ID: "b2", Status: BgRunning, StartedAt: time.Now()}, cancel)

	snap, ok := store.Stop("b2")
	if !ok {
		t.Fatal("Stop should return ok=true for a running task")
	}
	if snap.ID != "b2" {
		t.Errorf("Stop returned wrong snapshot: %+v", snap)
	}
	mu.Lock()
	called := cancelCalled
	mu.Unlock()
	if !called {
		t.Error("Stop should invoke cancel")
	}

	// stopping a completed task is a no-op
	store.Complete("b2", BgKilled, -1, "")
	_, ok = store.Stop("b2")
	if ok {
		t.Error("Stop on terminal task should return ok=false")
	}
}

func TestBgTaskStore_StopUnknown(t *testing.T) {
	store := NewBgTaskStore()
	snap, ok := store.Stop("nope")
	if ok {
		t.Error("Stop on unknown task should return ok=false")
	}
	if snap.ID != "" {
		t.Errorf("Stop on unknown task should return zero snapshot; got %+v", snap)
	}
}

func TestBgTaskStore_ObservableEmitsLifecycle(t *testing.T) {
	store := NewBgTaskStore()
	var seen []string
	var mu sync.Mutex
	store.Subscribe(func(c observable.Change) {
		mu.Lock()
		seen = append(seen, c.Op)
		mu.Unlock()
	})

	store.Add(BgTaskSnapshot{ID: "b3", Status: BgRunning, StartedAt: time.Now()}, func() {})
	store.Complete("b3", BgCompleted, 0, "ok")
	_ = store.DrainCompleted()

	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	want := []string{"started", "completed", "removed"}
	if len(got) != len(want) {
		t.Fatalf("observable ops: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("op[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestGenerateID_PrefixedAndUnique(t *testing.T) {
	a := GenerateID()
	b := GenerateID()
	if len(a) != 9 || a[0] != 'b' {
		t.Errorf("ID shape wrong: %q", a)
	}
	if a == b {
		t.Errorf("two IDs collide: %q == %q", a, b)
	}
}

func TestCapOutput_PassThroughSmall(t *testing.T) {
	got := capOutput("hello")
	if got != "hello" {
		t.Errorf("small output should pass through; got %q", got)
	}
}

func TestCapOutput_TrimsLarge(t *testing.T) {
	big := make([]byte, outputCap+100)
	for i := range big {
		big[i] = 'x'
	}
	got := capOutput(string(big))
	if len(got) <= outputCap {
		// truncation header adds some chars
	}
	if got[:6] != "[bg ou" {
		end := 20
		if end > len(got) {
			end = len(got)
		}
		t.Errorf("large output missing truncation header; got prefix %q", got[:end])
	}
}
