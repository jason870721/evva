package service

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johnny1110/evva/pkg/common"
)

// eventLog mirrors one space's event stream into daily append-only files,
// <workdir>/.vero/events/YYYY-MM-DD.jsonl (RP-17) — the durable answer to
// "what happened at 03:00 last night". Each line is
//
//	{"ts":"2026-06-10 03:00:12 +08:00","event":<wireEvent JSON>}
//
// — the exact payload the WebSocket carried, framed with an offset-stamped
// wall clock (pkg/common zone discipline) so the file is greppable by time.
// Token-level stream chunks stay out; the pump's turnCoalescer folds them into
// one whole-turn text/thinking line instead (chatlog.go), so the file also
// carries the full conversation text the /chatlog replay rebuilds the web
// console from.
//
// The cardinal rule is inherited from the pump it taps (RP-2 §3.5): the
// observer must NEVER slow the observed. Offer is a non-blocking handoff to
// one writer goroutine; when the buffer is full the line is dropped and
// counted (events_dropped in the metrics endpoint) — losing a few lines under
// burst is acceptable, freezing the pump is not.
type eventLog struct {
	dir       string // <workdir>/.vero/events
	retention int    // days; <= 0 keeps every file forever

	ch      chan []byte
	logged  atomic.Int64
	dropped atomic.Int64
	done    chan struct{}

	now func() time.Time // injected by rotation tests; time.Now in production
}

// eventLogBuffer absorbs publish bursts between writer wake-ups. Lines are
// small (one marshaled event); 1024 in flight is a few hundred KB at worst.
const eventLogBuffer = 1024

// newEventLog starts the writer goroutine for one space. retentionDays
// follows the space's RP-16 window: rotation prunes day files older than it.
func newEventLog(workdir string, retentionDays int) *eventLog {
	l := &eventLog{
		dir:       filepath.Join(workdir, ".vero", "events"),
		retention: retentionDays,
		ch:        make(chan []byte, eventLogBuffer),
		done:      make(chan struct{}),
		now:       time.Now,
	}
	go l.run()
	return l
}

// Offer hands one already-marshaled wireEvent to the writer without ever
// blocking the caller. Call only before Close (the service teardown waits for
// the pump to exit first — see teardownSpace).
func (l *eventLog) Offer(payload []byte) {
	select {
	case l.ch <- payload:
	default:
		l.dropped.Add(1)
	}
}

// Close drains whatever is already buffered, closes the day file, and stops
// the writer.
func (l *eventLog) Close() {
	close(l.ch)
	<-l.done
}

// Logged / Dropped report the lifetime line counters (metrics endpoint).
func (l *eventLog) Logged() int64  { return l.logged.Load() }
func (l *eventLog) Dropped() int64 { return l.dropped.Load() }

// run is the single writer: it owns the current day file, rotates (and
// prunes) when the local day flips, and frames each payload as one ts-stamped
// JSON line. A write or open failure counts the line as dropped and moves on —
// the log is observability, never load-bearing.
func (l *eventLog) run() {
	defer close(l.done)
	var (
		day string
		f   *os.File
	)
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	for payload := range l.ch {
		now := l.now()
		if d := now.Local().Format("2006-01-02"); f == nil || d != day {
			if f != nil {
				_ = f.Close()
				f = nil
			}
			day = d
			nf, err := l.openDay(day)
			if err != nil {
				l.dropped.Add(1)
				continue
			}
			f = nf
			l.prune(now)
		}

		line := make([]byte, 0, len(payload)+48)
		line = append(line, `{"ts":"`...)
		line = append(line, common.Stamp(now)...)
		line = append(line, `","event":`...)
		line = append(line, payload...)
		line = append(line, '}', '\n')
		if _, err := f.Write(line); err != nil {
			l.dropped.Add(1)
			continue
		}
		l.logged.Add(1)
	}
}

func (l *eventLog) openDay(day string) (*os.File, error) {
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(l.dir, day+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

// prune removes day files older than the retention window — the event log's
// rotation rides the space's RP-16 retention_days (0 = keep forever). Day
// names are zero-padded ISO dates, so a string compare is a date compare.
func (l *eventLog) prune(now time.Time) {
	if l.retention <= 0 {
		return
	}
	cutoff := now.AddDate(0, 0, -l.retention).Local().Format("2006-01-02")
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".jsonl") || len(name) != len("2006-01-02.jsonl") {
			continue
		}
		if day := strings.TrimSuffix(name, ".jsonl"); day < cutoff {
			_ = os.Remove(filepath.Join(l.dir, name))
		}
	}
}
