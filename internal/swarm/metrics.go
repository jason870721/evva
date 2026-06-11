package swarm

import (
	"sync"
	"time"
)

// MemberMetrics counts one member's scheduler lifecycle (RP-17): wakes by
// source, runs, non-clean exits, and a coarse run-duration histogram. Exported
// as a snapshot value through SwarmSpace.MetricsSnapshot.
type MemberMetrics struct {
	WakesMessage int64
	WakesTimer   int64
	Runs         int64
	Aborts       int64
	// RunSeconds buckets completed runs by wall-clock duration:
	// [0] <10s, [1] <1m, [2] <10m, [3] ≥10m.
	RunSeconds [4]int64
	// RunTokens buckets runs by token cost (input+output, the same per-run
	// delta the RP-13 daily counter folds — one source, no double books):
	// [0] <1k, [1] <10k, [2] <50k, [3] ≥50k. A run whose provider reported
	// no usage lands in [0] with zero cost — the bucket count still equals
	// Runs, so "every run metered" stays checkable (RP-28).
	RunTokens [4]int64
}

// spaceMetrics aggregates per-member counters. Mutex-guarded plain ints —
// increments happen once per wake/run, never per token, so contention is nil.
// Every method is nil-receiver-safe: hand-built test spaces simply skip
// metrics, the same stance the meter and schedules take.
type spaceMetrics struct {
	mu      sync.Mutex
	started time.Time
	members map[string]*MemberMetrics

	// RP-22 workflow-watchdog tallies, space-level: one increment per stale
	// reminder / backlog alert actually sent (not per sweep pass).
	tasksStale   int64
	mailboxStale int64
}

func newSpaceMetrics() *spaceMetrics {
	return &spaceMetrics{started: time.Now(), members: map[string]*MemberMetrics{}}
}

func (m *spaceMetrics) memberLocked(name string) *MemberMetrics {
	mm, ok := m.members[name]
	if !ok {
		mm = &MemberMetrics{}
		m.members[name] = mm
	}
	return mm
}

// countWake tallies one serve of an active member, by wake source.
func (m *spaceMetrics) countWake(name string, r wakeReason) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mm := m.memberLocked(name)
	if r == wakeTimer {
		mm.WakesTimer++
	} else {
		mm.WakesMessage++
	}
}

// countRun tallies one finished run: total, abort-or-clean, duration bucket.
func (m *spaceMetrics) countRun(name string, d time.Duration, clean bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mm := m.memberLocked(name)
	mm.Runs++
	if !clean {
		mm.Aborts++
	}
	switch {
	case d < 10*time.Second:
		mm.RunSeconds[0]++
	case d < time.Minute:
		mm.RunSeconds[1]++
	case d < 10*time.Minute:
		mm.RunSeconds[2]++
	default:
		mm.RunSeconds[3]++
	}
}

// countRunTokens buckets one finished run's token cost (RP-28). Fed from
// meterRun with the SAME delta that feeds the RP-13 daily counter, so the
// histogram and the budget gauge can never disagree about a run's cost.
func (m *spaceMetrics) countRunTokens(name string, total int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mm := m.memberLocked(name)
	switch {
	case total < 1_000:
		mm.RunTokens[0]++
	case total < 10_000:
		mm.RunTokens[1]++
	case total < 50_000:
		mm.RunTokens[2]++
	default:
		mm.RunTokens[3]++
	}
}

// countTaskStale / countMailboxStale tally one workflow-watchdog notification
// each (RP-22).
func (m *spaceMetrics) countTaskStale() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.tasksStale++
	m.mu.Unlock()
}

func (m *spaceMetrics) countMailboxStale() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.mailboxStale++
	m.mu.Unlock()
}

// WorkflowStaleCounts reports the space's RP-22 watchdog tallies (stale-task
// reminders, mailbox-backlog alerts). Exported for the metrics endpoint.
func (sp *SwarmSpace) WorkflowStaleCounts() (tasksStale, mailboxStale int64) {
	m := sp.metrics
	if m == nil {
		return 0, 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tasksStale, m.mailboxStale
}

// MetricsSnapshot copies every member's counters plus the space's start time
// (the uptime anchor). Exported for the service's metrics endpoint (RP-17).
func (sp *SwarmSpace) MetricsSnapshot() (map[string]MemberMetrics, time.Time) {
	m := sp.metrics
	if m == nil {
		return map[string]MemberMetrics{}, time.Time{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]MemberMetrics, len(m.members))
	for k, v := range m.members {
		out[k] = *v
	}
	return out, m.started
}
