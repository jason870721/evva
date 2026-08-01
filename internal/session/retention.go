package session

import (
	"sort"
	"time"
)

// Retention bounds how many / how old persisted sessions a workdir keeps.
// A zero field means "no limit on this axis"; both zero means unbounded,
// which is what evva ships (see SelectExpired's note on why).
//
// Deliberately the same shape as checkpoint.Retention — the two stores GC
// the same way even though only one of them does it automatically.
type Retention struct {
	MaxCount int           // keep at most this many newest sessions per workdir (0 = unlimited)
	MaxAge   time.Duration // drop sessions older than this (0 = no age limit)
}

// Empty reports whether the policy would delete nothing under any input.
func (r Retention) Empty() bool { return r.MaxCount <= 0 && r.MaxAge <= 0 }

// SelectExpired returns the headers a prune would delete under ret, newest
// first within each workdir. Pure — it decides, it never touches disk, so
// the dry run and the real run are provably the same selection.
//
// Two exemptions, in order:
//
//   - Pinned sessions are invisible to the policy. They are not deleted and
//     they do not consume a MaxCount slot, so pinning three sessions under
//     `-keep 10` leaves ten unpinned ones alive, not seven.
//   - `keepIDs` protects sessions that must not vanish underneath a running
//     process — the live session, and (when pruning machine-wide) any
//     session a caller names.
//
// MaxCount is applied per workdir slug, not globally: a project with two
// sessions should not lose them because a different project has fifty.
//
// Unlike checkpoints — derived before-images that evva prunes on its own —
// a session transcript is the operator's own writing, so nothing calls this
// automatically. `evva sessions prune` is the only caller, and it dry-runs
// by default.
func SelectExpired(headers []Header, ret Retention, keepIDs map[string]bool, now time.Time) []Header {
	if ret.Empty() || len(headers) == 0 {
		return nil
	}

	bySlug := map[string][]Header{}
	for _, h := range headers {
		if h.Pinned || keepIDs[h.SessionID] {
			continue
		}
		bySlug[h.WorkdirSlug] = append(bySlug[h.WorkdirSlug], h)
	}

	var victims []Header
	for _, rows := range bySlug {
		sort.Slice(rows, func(i, j int) bool { return rows[i].MTime > rows[j].MTime })
		for i, h := range rows {
			switch {
			case ret.MaxCount > 0 && i >= ret.MaxCount:
				victims = append(victims, h)
			case ret.MaxAge > 0 && now.Sub(time.Unix(0, h.MTime)) > ret.MaxAge:
				victims = append(victims, h)
			}
		}
	}
	sort.Slice(victims, func(i, j int) bool { return victims[i].MTime > victims[j].MTime })
	return victims
}

// DeleteAll removes every listed session's snapshot file, returning how
// many went and the first error. Best-effort: one failed unlink does not
// abandon the rest, because a half-finished prune the operator has to
// re-run is worse than one that reports a straggler.
func DeleteAll(appHome string, headers []Header) (int, error) {
	var (
		n        int
		firstErr error
	)
	for _, h := range headers {
		if err := Delete(appHome, h.WorkdirSlug, h.SessionID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		n++
	}
	return n, firstErr
}

// SetFlags rewrites a persisted session's title / pin without disturbing
// its conversation. Load-modify-save rather than an in-place patch: the
// snapshot is one JSON document and there is no cheaper honest way to
// change a field in it.
//
// A nil pointer leaves that field alone, so callers can set one without
// knowing the other.
func SetFlags(appHome, workdirSlug, sessionID string, title *string, pinned *bool) error {
	snap, err := Load(appHome, workdirSlug, sessionID)
	if err != nil {
		return err
	}
	if title != nil {
		snap.Title = *title
	}
	if pinned != nil {
		snap.Pinned = *pinned
	}
	return Save(appHome, snap)
}
