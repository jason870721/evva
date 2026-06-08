package swarm

import (
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
)

// RP-7: a leader-set schedule takes effect on the RUNNING loop immediately (not
// only at startMemberLoop), the wake carries the custom prompt + current time,
// and ClearSchedule stops the wakes.
func TestSetScheduleLiveAndClear(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"worker": agentdef.RoleWorker})
	sup := startSup(t, sp)

	// worker starts unscheduled — idle, no runs.
	time.Sleep(30 * time.Millisecond)
	if got := ctls["worker"].runs.Load(); got != 0 {
		t.Fatalf("unscheduled worker ran %d times, want 0", got)
	}

	if err := sup.SetSchedule("worker", agentdef.Schedule{Every: 20 * time.Millisecond, Prompt: "do the patrol"}); err != nil {
		t.Fatalf("SetSchedule: %v", err)
	}
	waitFor(t, 2*time.Second, "worker wakes on the live schedule", func() bool { return ctls["worker"].runs.Load() >= 2 })

	if p := ctls["worker"].lastPrompt(); !strings.Contains(p, "<system-reminder>currenttime: ") || !strings.Contains(p, "do the patrol") {
		t.Errorf("wake prompt = %q, want currenttime + the custom prompt", p)
	}

	// Clear: the wakes must stop. Allow at most one already in flight at clear.
	if err := sup.ClearSchedule("worker"); err != nil {
		t.Fatalf("ClearSchedule: %v", err)
	}
	n := ctls["worker"].runs.Load()
	time.Sleep(120 * time.Millisecond) // several Every windows
	if got := ctls["worker"].runs.Load(); got > n+1 {
		t.Errorf("worker ran %d more times after clear, want it stopped (was %d)", got-n, n)
	}
	if _, ok := sp.ScheduleFor("worker"); ok {
		t.Errorf("ScheduleFor(worker) still set after ClearSchedule")
	}
}

// RP-7 §3.6: a timer tick that lands while the member is already running is
// SKIPPED, not queued — no catch-up run piles up behind a long task.
func TestScheduledWakeSkippedWhileBusy(t *testing.T) {
	sp, ctls := ctlSpace(t, map[string]agentdef.Role{"busy": agentdef.RoleWorker})
	ctls["busy"].block = true // first run blocks until teardown cancels the ctx
	sup := startSup(t, sp)

	if err := sup.SetSchedule("busy", agentdef.Schedule{Every: 10 * time.Millisecond}); err != nil {
		t.Fatalf("SetSchedule: %v", err)
	}
	waitFor(t, time.Second, "first scheduled wake starts and blocks", func() bool { return ctls["busy"].inFlight.Load() == 1 })

	// Many ticks pass while the member is busy; none may queue a second run.
	time.Sleep(100 * time.Millisecond)
	if got := ctls["busy"].runs.Load(); got != 1 {
		t.Errorf("busy member ran %d times, want exactly 1 (later ticks skipped, not queued)", got)
	}
}

// RP-7 AC#6: a leader-set schedule survives a service restart (runtime.json).
func TestScheduleSetSurvivesRestart(t *testing.T) {
	cfg := stubConfig(t)

	sp1, err := NewSpace("sched-set", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #1: %v", err)
	}
	sup1 := NewSupervisor(sp1) // wires sp1.super; no Start needed to persist
	sch := agentdef.Schedule{Cron: "0 9 * * 1", Prompt: "weekly report"}
	if err := sup1.SetSchedule("worker-a", sch); err != nil {
		t.Fatalf("SetSchedule: %v", err)
	}
	sp1.Shutdown()

	sp2, err := NewSpace("sched-set", testManifest(), testLoaded(), nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #2: %v", err)
	}
	sp2.Reload()
	defer sp2.Shutdown()

	got, ok := sp2.ScheduleFor("worker-a")
	if !ok || got.Cron != sch.Cron || got.Prompt != sch.Prompt {
		t.Errorf("restored schedule = %+v ok=%v, want %+v", got, ok, sch)
	}
}

// RP-7 AC#5: a leader CLEAR survives restart and beats a schedule that the
// manifest/profile still declares — the cleared crontab must not resurrect.
func TestScheduleClearSurvivesRestart(t *testing.T) {
	cfg := stubConfig(t)

	loaded := testLoaded()
	// worker-a is declared (as if by manifest/profile) — NewSpace seeds it.
	loaded[1].Schedule = &agentdef.Schedule{Cron: "*/5 * * * *", Prompt: "patrol"}

	sp1, err := NewSpace("sched-clear", testManifest(), loaded, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #1: %v", err)
	}
	if _, ok := sp1.ScheduleFor("worker-a"); !ok {
		t.Fatal("worker-a should start scheduled from its declaration")
	}
	sup1 := NewSupervisor(sp1)
	if err := sup1.ClearSchedule("worker-a"); err != nil {
		t.Fatalf("ClearSchedule: %v", err)
	}
	sp1.Shutdown()

	// Restart with the SAME declaration still present.
	sp2, err := NewSpace("sched-clear", testManifest(), loaded, nil, cfg)
	if err != nil {
		t.Fatalf("NewSpace #2: %v", err)
	}
	sp2.Reload()
	defer sp2.Shutdown()

	if got, ok := sp2.ScheduleFor("worker-a"); ok {
		t.Errorf("worker-a schedule = %+v, want cleared (persisted clear must beat the re-declared schedule)", got)
	}
}
