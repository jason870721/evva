// Package alarm provides a one-shot, absolute-time wake scheduler and the
// alarm_create / alarm_list / alarm_cancel tools built on it.
//
// Unlike schedule_wakeup — a blocking, relative sleep capped at one hour — an
// alarm fires at an absolute wall-clock instant arbitrarily far in the future,
// survives process restarts (when durable), and never holds a goroutine: it
// registers a time.AfterFunc and returns immediately. On fire the scheduler
// invokes OnFire, which the agent wires to WakeupQueue.Enqueue plus a loop
// wake-up signal — reusing the exact delivery path schedule_wakeup and daemons
// already use (drainWakeupPrompts at the top of every loop iteration, and the
// signal-wake that starts a fresh run when the agent is idle).
package alarm

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/common"
)

// defaultMaxPending caps simultaneous pending alarms per scheduler so a runaway
// loop cannot accumulate unbounded timers and store growth.
const defaultMaxPending = 100

// Alarm is one scheduled one-shot wake.
//
// Target and Origin are only meaningful in a multi-agent (swarm) context: the
// OnFire wiring routes the wake to Target and labels it as coming from Origin.
// In single-agent use both are empty and the wake goes to the caller itself.
type Alarm struct {
	ID      string    `json:"id"`
	FireAt  time.Time `json:"fire_at"`
	Prompt  string    `json:"prompt"`
	Label   string    `json:"label,omitempty"`
	Durable bool      `json:"durable"`
	Created time.Time `json:"created"`

	// Target is the agent to wake (swarm). "" => the caller/self.
	Target string `json:"target,omitempty"`
	// Origin is the agent that set the alarm (swarm). "" in single-agent use.
	Origin string `json:"origin,omitempty"`
}

// Fired is the payload handed to OnFire when an alarm triggers. Late is true
// when the alarm's instant had already passed by the time it fired — the
// restart-recovery path (LoadAndRearm) fires past-due durable alarms once so a
// wake set before a downtime is not silently dropped.
type Fired struct {
	Alarm
	Late bool
}

// Message renders the user-message body injected into the conversation when
// this alarm fires: an ⏰ banner carrying the fire instant (offset-stamped, so
// the woken agent knows what time it is) plus label and an optional late
// marker, followed by the alarm's own prompt.
func (f Fired) Message() string {
	var b strings.Builder
	b.WriteString("⏰ Alarm fired")
	if f.Label != "" {
		b.WriteString(" [" + f.Label + "]")
	}
	switch {
	case f.Late:
		fmt.Fprintf(&b, " (late — was due %s)", common.Stamp(f.FireAt))
	case !f.FireAt.IsZero():
		fmt.Fprintf(&b, " — %s", common.Stamp(f.FireAt))
	}
	b.WriteByte('\n')
	b.WriteString(f.Prompt)
	return b.String()
}

// Config configures a Scheduler.
type Config struct {
	// OnFire is invoked on a background goroutine each time an alarm fires.
	// nil is allowed — fires become no-ops, useful in tests that only assert
	// scheduling state. The agent wires this to WakeupQueue.Enqueue + a wake
	// signal.
	OnFire func(Fired)

	// StorePath is the JSON file durable alarms persist to. "" disables
	// persistence entirely (every alarm becomes session-only regardless of its
	// Durable flag — typical for tests and config-less harnesses).
	StorePath string

	// Now injects the clock for tests. nil => time.Now.
	Now func() time.Time

	// MaxPending overrides defaultMaxPending when > 0.
	MaxPending int
}

type armed struct {
	alarm Alarm
	timer *time.Timer
}

// Scheduler owns the set of pending alarms and their timers. All methods are
// safe for concurrent use; fires run on time.AfterFunc goroutines.
type Scheduler struct {
	mu     sync.Mutex
	alarms map[string]*armed
	onFire func(Fired)
	path   string
	now    func() time.Time
	max    int
}

// New constructs a Scheduler from cfg. It does not read the durable store —
// call LoadAndRearm once at startup to reinstate persisted alarms.
func New(cfg Config) *Scheduler {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	max := cfg.MaxPending
	if max <= 0 {
		max = defaultMaxPending
	}
	return &Scheduler{
		alarms: make(map[string]*armed),
		onFire: cfg.OnFire,
		path:   cfg.StorePath,
		now:    now,
		max:    max,
	}
}

// Arm validates and schedules a one-shot alarm. ID and Created are assigned
// here when empty. The fire instant must be strictly in the future; arming a
// past time is an error (LoadAndRearm handles already-due alarms separately).
// Returns the stored alarm with its assigned ID.
func (s *Scheduler) Arm(a Alarm) (Alarm, error) {
	if strings.TrimSpace(a.Prompt) == "" {
		return Alarm{}, fmt.Errorf("prompt is required")
	}
	if a.FireAt.IsZero() {
		return Alarm{}, fmt.Errorf("fire time is required")
	}
	now := s.now()
	if !a.FireAt.After(now) {
		return Alarm{}, fmt.Errorf("fire time %s is not in the future (now %s)",
			common.Stamp(a.FireAt), common.Stamp(now))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.alarms) >= s.max {
		return Alarm{}, fmt.Errorf("too many pending alarms (max %d) — cancel one first", s.max)
	}
	if a.ID == "" {
		a.ID = newID()
	}
	if a.Created.IsZero() {
		a.Created = now
	}
	s.armLocked(a, now)
	if err := s.persistLocked(); err != nil {
		s.removeLocked(a.ID) // roll back so memory matches disk
		return Alarm{}, fmt.Errorf("persist: %w", err)
	}
	return a, nil
}

// Cancel removes a pending alarm by id, stopping its timer. Reports whether an
// alarm with that id existed.
func (s *Scheduler) Cancel(id string) bool {
	s.mu.Lock()
	ok := s.removeLocked(id)
	if ok {
		_ = s.persistLocked()
	}
	s.mu.Unlock()
	return ok
}

// List returns every pending alarm, sorted by fire time (soonest first).
func (s *Scheduler) List() []Alarm {
	s.mu.Lock()
	out := make([]Alarm, 0, len(s.alarms))
	for _, ar := range s.alarms {
		out = append(out, ar.alarm)
	}
	s.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].FireAt.Before(out[j].FireAt) })
	return out
}

// Pending reports the number of armed alarms.
func (s *Scheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.alarms)
}

// Rearm reinstates persisted alarms at startup: every future alarm gets a fresh
// timer, and every already-due alarm (its instant passed while the process was
// down) is returned — flagged late — for the caller to deliver, WITHOUT firing
// OnFire here. Past-due alarms are dropped from the store before returning.
//
// Splitting "re-arm" from "fire past-due" lets a host that must not start work
// before its UI is live (the solo TUI) deliver missed alarms on the next run
// instead of autonomously at boot; callers for whom immediate delivery is safe
// (durable bus mail in the swarm) use LoadAndRearm. No-op (nil) when no store
// path is configured or the file is absent.
func (s *Scheduler) Rearm() ([]Fired, error) {
	stored, err := s.loadStored()
	if err != nil {
		return nil, err
	}
	now := s.now()
	var due []Fired
	s.mu.Lock()
	for _, a := range stored {
		a.Durable = true // only durable alarms are ever persisted
		if a.FireAt.After(now) {
			s.armLocked(a, now)
		} else {
			due = append(due, Fired{Alarm: a, Late: true})
		}
	}
	_ = s.persistLocked() // drop the past-due ones from disk
	s.mu.Unlock()
	return due, nil
}

// LoadAndRearm is Rearm plus firing each past-due alarm through OnFire
// immediately. Use it where immediate delivery at startup is safe — e.g. the
// swarm, where a fire is a durable bus message the target drains when its loop
// starts.
func (s *Scheduler) LoadAndRearm() error {
	due, err := s.Rearm()
	if err != nil {
		return err
	}
	s.mu.Lock()
	onFire := s.onFire
	s.mu.Unlock()
	if onFire != nil {
		for _, f := range due {
			onFire(f)
		}
	}
	return nil
}

// loadStored reads and parses the durable store. Returns nil (no error) when no
// store path is configured or the file does not exist.
func (s *Scheduler) loadStored() ([]Alarm, error) {
	if s.path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("alarm: read store: %w", err)
	}
	var stored []Alarm
	if len(strings.TrimSpace(string(data))) > 0 {
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("alarm: parse store: %w", err)
		}
	}
	return stored, nil
}

// Stop halts every pending timer so no alarm fires during shutdown. Durable
// alarms remain on disk and are reinstated by LoadAndRearm on the next start;
// session-only alarms are simply lost. Safe to call multiple times.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	for _, ar := range s.alarms {
		ar.timer.Stop()
	}
	s.mu.Unlock()
}

// armLocked registers a timer for a and records it. Caller holds s.mu.
func (s *Scheduler) armLocked(a Alarm, now time.Time) {
	d := max(a.FireAt.Sub(now), 0)
	id := a.ID
	t := time.AfterFunc(d, func() { s.fire(id) })
	s.alarms[id] = &armed{alarm: a, timer: t}
}

// removeLocked stops and forgets an alarm. Caller holds s.mu. Reports whether
// it existed.
func (s *Scheduler) removeLocked(id string) bool {
	ar, ok := s.alarms[id]
	if !ok {
		return false
	}
	ar.timer.Stop()
	delete(s.alarms, id)
	return true
}

// fire is the timer callback: drop the alarm, persist the smaller set, then
// invoke OnFire outside the lock (it enqueues + signals, which take their own
// locks / channel sends).
func (s *Scheduler) fire(id string) {
	s.mu.Lock()
	ar, ok := s.alarms[id]
	if !ok {
		s.mu.Unlock()
		return
	}
	delete(s.alarms, id)
	a := ar.alarm
	_ = s.persistLocked() // a fired alarm must not survive a restart
	onFire := s.onFire
	s.mu.Unlock()

	if onFire != nil {
		onFire(Fired{Alarm: a})
	}
}

// persistLocked atomically rewrites the durable store with the current set of
// durable alarms. No-op when no store path is configured. Caller holds s.mu.
func (s *Scheduler) persistLocked() error {
	if s.path == "" {
		return nil
	}
	durable := make([]Alarm, 0, len(s.alarms))
	for _, ar := range s.alarms {
		if ar.alarm.Durable {
			durable = append(durable, ar.alarm)
		}
	}
	sort.Slice(durable, func(i, j int) bool { return durable[i].FireAt.Before(durable[j].FireAt) })

	if len(durable) == 0 {
		// Nothing durable left — remove the file so a stale store doesn't linger.
		if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(durable, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// newID returns a wire-stable alarm id: "alm_" + 8 base-36 chars. 36^8 ≈ 2.8
// trillion combinations — ample for the lifetime of any session.
func newID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	out := make([]byte, 0, 12)
	out = append(out, 'a', 'l', 'm', '_')
	for _, b := range buf {
		out = append(out, idAlphabet[int(b)%len(idAlphabet)])
	}
	return string(out)
}

// ParseFireTime parses a user-supplied timestamp into an absolute instant.
// Accepted forms, tried in order: RFC3339 (explicit offset honored), then the
// local layouts "2006-01-02 15:04:05" / "...T..." and their minute-only
// variants (seconds default to :00), interpreted in loc.
func ParseFireTime(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if loc == nil {
		loc = time.Local
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02T15:04",
	} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	zone, _ := time.Now().In(loc).Zone()
	return time.Time{}, fmt.Errorf("unrecognized time %q — use \"2006-01-02 15:04:05\" (local %s) or RFC3339 with an explicit offset", s, zone)
}
