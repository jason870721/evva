package swarm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/bus"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/ui"
)

// fakeController is a scripted ui.Controller. The supervisor only ever calls
// Run; the embedded nil interface satisfies the rest of the (fat) surface.
type fakeController struct {
	ui.Controller
	runs     atomic.Int32
	inFlight atomic.Int32
	block    bool // Run blocks until ctx is cancelled (models a long run)
	doPanic  bool // Run panics every time (models a crashing agent)

	mu      sync.Mutex
	prompts []string
}

func (f *fakeController) Run(ctx context.Context, prompt string) (string, error) {
	f.runs.Add(1)
	f.inFlight.Add(1)
	defer f.inFlight.Add(-1)

	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()

	if f.doPanic {
		panic("fake controller boom")
	}
	if f.block {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return "ok", nil
}

func (f *fakeController) lastPrompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.prompts) == 0 {
		return ""
	}
	return f.prompts[len(f.prompts)-1]
}

// ctlSpace builds a minimal live space backed by a real store + bus and fake
// controllers — no agent.New, so the supervisor's logic is exercised directly.
func ctlSpace(t *testing.T, members map[string]agentdef.Role) (*SwarmSpace, map[string]*fakeController) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	sp := &SwarmSpace{
		ID:        "t",
		Store:     st,
		Roster:    newRoster(),
		schedules: map[string]agentdef.Schedule{},
	}
	sp.Bus = bus.New(st, sp.Roster)

	ctls := make(map[string]*fakeController, len(members))
	for name, role := range members {
		fc := &fakeController{}
		if err := sp.Roster.add(name, role, "", fc); err != nil {
			t.Fatalf("roster add %q: %v", name, err)
		}
		sp.Bus.Register(name)
		ctls[name] = fc
	}
	return sp, ctls
}

// startSup starts a supervisor with a fast tick under a cancel-on-cleanup ctx.
func startSup(t *testing.T, sp *SwarmSpace) *Supervisor {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sup := NewSupervisor(sp)
	sup.tickInterval = 5 * time.Millisecond
	sup.Start(ctx)
	return sup
}

func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s: %s", d, what)
}

func runStatusOf(sp *SwarmSpace, name string) RunStatus {
	for _, mv := range sp.Roster.Snapshot() {
		if mv.Name == name {
			return mv.Run
		}
	}
	return ""
}

// AC#1: an idle agent woken by a message runs once with a prompt carrying the
// sender + body, and the message is marked read (drain A).
func TestMessageWakeRunsAndMarksRead(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	startSup(t, sp)

	uuid, err := sp.Bus.Send(store.Message{Sender: "boss", Subject: "ship it", Recipient: "w", Body: "please do the thing"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}

	waitFor(t, time.Second, "w runs once", func() bool { return ctls["w"].runs.Load() >= 1 })

	if p := ctls["w"].lastPrompt(); !strings.Contains(p, "boss") || !strings.Contains(p, "please do the thing") {
		t.Fatalf("prompt missing sender/body: %q", p)
	}
	waitFor(t, time.Second, "message marked read", func() bool {
		m, err := sp.Store.GetMessage(uuid)
		return err == nil && m.ReadAt != nil
	})
}

// AC#2: a broadcast wakes every active member; a frozen member is not scheduled.
func TestBroadcastWakesActiveNotFrozen(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{
		"a": agentdef.RoleWorker, "b": agentdef.RoleWorker, "c": agentdef.RoleWorker,
	})
	sup := startSup(t, sp)
	if err := sup.Freeze("c"); err != nil {
		t.Fatalf("freeze: %v", err)
	}

	if _, err := sp.Bus.Send(store.Message{Sender: "boss", Recipient: store.RecipientAll, Body: "standup"}); err != nil {
		t.Fatalf("broadcast: %v", err)
	}

	waitFor(t, time.Second, "a and b wake", func() bool {
		return ctls["a"].runs.Load() >= 1 && ctls["b"].runs.Load() >= 1
	})
	time.Sleep(40 * time.Millisecond) // give a wrong wake a chance to show up
	if got := ctls["c"].runs.Load(); got != 0 {
		t.Fatalf("frozen member ran %d times, want 0", got)
	}
}

// AC#3: a timer-scheduled agent wakes at each due tick; an unscheduled idle
// agent with no mail never runs (idle burns no tokens).
func TestTimerWakeAndIdleBurnsNoTokens(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{
		"patrol": agentdef.RoleWorker, "lazy": agentdef.RoleWorker,
	})
	sp.schedules["patrol"] = agentdef.Schedule{Every: 20 * time.Millisecond}
	startSup(t, sp)

	waitFor(t, 2*time.Second, "patrol wakes repeatedly", func() bool { return ctls["patrol"].runs.Load() >= 3 })
	if got := ctls["lazy"].runs.Load(); got != 0 {
		t.Fatalf("idle unscheduled agent ran %d times, want 0", got)
	}
	if p := ctls["patrol"].lastPrompt(); !strings.Contains(p, "Scheduled duty") {
		t.Errorf("timer prompt = %q, want a standing-duty prompt", p)
	}
}

// AC#4: Suspend cancels an in-flight run; Resume starts a new one; HaltAll
// cancels everything in flight.
func TestSuspendResumeHaltAll(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	ctls["w"].block = true
	sup := startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "boss", Recipient: "w", Body: "long task"}); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, time.Second, "w is running", func() bool { return ctls["w"].inFlight.Load() == 1 })
	waitFor(t, time.Second, "roster shows busy", func() bool { return runStatusOf(sp, "w") == RunBusy })

	if err := sup.Suspend("w"); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	waitFor(t, time.Second, "in-flight run cancelled", func() bool { return ctls["w"].inFlight.Load() == 0 })
	if got := runStatusOf(sp, "w"); got != RunSuspended {
		t.Fatalf("run status = %s, want suspended", got)
	}

	// Resume re-runs the still-unread work (the suspended run never marked it read).
	if err := sup.Resume("w"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	waitFor(t, time.Second, "resume starts a new run", func() bool { return ctls["w"].runs.Load() >= 2 })
	waitFor(t, time.Second, "new run is in flight", func() bool { return ctls["w"].inFlight.Load() == 1 })

	if err := sup.HaltAll(); err != nil {
		t.Fatalf("halt: %v", err)
	}
	waitFor(t, time.Second, "halt cancels in-flight run", func() bool { return ctls["w"].inFlight.Load() == 0 })
}

// AC#5: a panicking run is contained — the agent's own loop survives to handle
// the next message, and a sibling agent is unaffected (process lives).
func TestPanicContainment(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{
		"boom": agentdef.RoleWorker, "ok": agentdef.RoleWorker,
	})
	ctls["boom"].doPanic = true
	startSup(t, sp)

	if _, err := sp.Bus.Send(store.Message{Sender: "x", Recipient: "boom", Body: "1"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "boom panics once (recovered)", func() bool { return ctls["boom"].runs.Load() >= 1 })

	// A second message proves boom's loop survived its own panic.
	if _, err := sp.Bus.Send(store.Message{Sender: "x", Recipient: "boom", Body: "2"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "boom's loop survives to run again", func() bool { return ctls["boom"].runs.Load() >= 2 })

	// The sibling is unaffected.
	if _, err := sp.Bus.Send(store.Message{Sender: "x", Recipient: "ok", Body: "hi"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, "sibling unaffected by the panic", func() bool { return ctls["ok"].runs.Load() >= 1 })
}

// AC#6: AddMember hot-loads a new agent (roster + mailbox + run loop) with no
// restart, and it is immediately addressable. Uses the real construction path
// (the stub LLM provider registered in space_test.go).
func TestAddMemberHotLoad(t *testing.T) {
	cfg := stubConfig(t)

	dir := filepath.Join(cfg.WorkDir, "agents", "sub", "newbie")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "system_prompt.md"), []byte("You are newbie."), 0o644); err != nil {
		t.Fatal(err)
	}

	sp, err := NewSpace("hot", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	defer sp.Shutdown()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sup := NewSupervisor(sp)
	sup.Start(ctx)

	if err := sup.AddMember("newbie"); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	if _, ok := sp.Roster.Controller("newbie"); !ok {
		t.Fatal("newbie not addressable in the roster")
	}
	if sp.Bus.Inbox("newbie") == nil {
		t.Fatal("newbie has no mailbox")
	}

	// It wakes on a message like any other member (the stub agent runs, drain A
	// marks the message read).
	uuid, err := sp.Bus.Send(store.Message{Sender: "leader", Recipient: "newbie", Body: "welcome"})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	waitFor(t, 2*time.Second, "hot-loaded member processes mail", func() bool {
		m, err := sp.Store.GetMessage(uuid)
		return err == nil && m.ReadAt != nil
	})
}
