package agentdef

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed timer-wake spec. Exactly one form is set: a 5-field
// cron expression (Cron) or a fixed interval (Every). It is a plain comparable
// value — the scheduler (SPRD-1-6) owns the tick loop and decides when an
// agent is due via Next.
//
// We carry the raw forms (not a pre-parsed cron object) so a Loaded built twice
// from the same dir compares equal (SPRD-1-3 re-callability), and so the
// scheduler can cache its own parsed form once.
type Schedule struct {
	Cron   string        // raw 5-field cron ("*/5 * * * *"); "" for the interval form
	Every  time.Duration // fixed interval; 0 for the cron form
	Prompt string        // custom wake text injected at fire time (RP-7); "" → standing-duty fallback
}

var errEmptySchedule = errors.New("schedule: neither cron nor every is set")

// parseSchedule builds a Schedule from the two profile.yml forms. Exactly one
// of cron / every may be set. A cron expression is validated here so a bad
// spec fails at load, not at the first tick.
func parseSchedule(cronExpr, every string) (Schedule, error) {
	cronExpr = strings.TrimSpace(cronExpr)
	every = strings.TrimSpace(every)

	switch {
	case cronExpr != "" && every != "":
		return Schedule{}, errors.New("schedule: set either cron or every, not both")
	case cronExpr != "":
		s := Schedule{Cron: cronExpr}
		if err := s.Validate(); err != nil {
			return Schedule{}, err
		}
		return s, nil
	case every != "":
		d, err := time.ParseDuration(every)
		if err != nil {
			return Schedule{}, fmt.Errorf("schedule: bad every %q: %w", every, err)
		}
		if d <= 0 {
			return Schedule{}, fmt.Errorf("schedule: every must be positive, got %q", every)
		}
		return Schedule{Every: d}, nil
	default:
		return Schedule{}, errEmptySchedule
	}
}

// Validate reports whether the schedule is well-formed (a parseable cron or a
// positive interval).
func (s Schedule) Validate() error {
	if s.Every > 0 {
		return nil
	}
	if s.Cron == "" {
		return errEmptySchedule
	}
	_, err := parseCron(s.Cron)
	return err
}

// Next returns the first activation strictly after `after`. For the interval
// form it is after+Every; for cron it is the next minute matching the spec.
func (s Schedule) Next(after time.Time) (time.Time, error) {
	if s.Every > 0 {
		return after.Add(s.Every), nil
	}
	if s.Cron == "" {
		return time.Time{}, errEmptySchedule
	}
	c, err := parseCron(s.Cron)
	if err != nil {
		return time.Time{}, err
	}
	return c.next(after)
}

// --- minimal standard 5-field cron (minute hour dom month dow) ---------------
//
// Supports `*`, `*/n`, `n`, `a-b`, `a-b/n`, and comma lists. Day-of-month and
// day-of-week follow standard cron OR semantics (when both are restricted, a
// day matches if EITHER matches). Self-contained so the swarm pulls in no cron
// dependency — the realistic Veronica specs are patrol/review cadences like
// "*/5 * * * *" or "0 17 * * *".

type cronExpr struct {
	min, hour, dom, month, dow uint64 // bitsets, bit v set means value v allowed
	domStar, dowStar           bool   // true when the field is a bare "*"
}

func parseCron(expr string) (cronExpr, error) {
	// Name the unsupported dialects up front (RP-18) — a Vixie-cron habit
	// should fail with "that syntax doesn't exist here", not "bad value".
	if strings.HasPrefix(strings.TrimSpace(expr), "@") {
		return cronExpr{}, fmt.Errorf("cron %q: @-aliases (@daily, @hourly, @every …) are not supported — write the 5-field form (e.g. %q)", expr, "0 0 * * *")
	}
	fields := strings.Fields(expr)
	if len(fields) > 0 && strings.HasPrefix(fields[0], "TZ=") {
		return cronExpr{}, fmt.Errorf("cron %q: a TZ= prefix is not supported — schedules always match the system's LOCAL wall clock", expr)
	}
	if len(fields) != 5 {
		hint := ""
		if len(fields) == 6 {
			hint = " — a seconds field is not supported; minute resolution only"
		}
		return cronExpr{}, fmt.Errorf("cron %q: want 5 fields (minute hour day-of-month month day-of-week), got %d%s", expr, len(fields), hint)
	}
	var c cronExpr
	var err error
	if c.min, _, err = parseField(fields[0], 0, 59); err != nil {
		return cronExpr{}, fmt.Errorf("cron %q minute: %w", expr, err)
	}
	if c.hour, _, err = parseField(fields[1], 0, 23); err != nil {
		return cronExpr{}, fmt.Errorf("cron %q hour: %w", expr, err)
	}
	if c.dom, c.domStar, err = parseField(fields[2], 1, 31); err != nil {
		return cronExpr{}, fmt.Errorf("cron %q day-of-month: %w", expr, err)
	}
	if c.month, _, err = parseField(fields[3], 1, 12); err != nil {
		return cronExpr{}, fmt.Errorf("cron %q month: %w", expr, err)
	}
	if c.dow, c.dowStar, err = parseField(fields[4], 0, 7); err != nil {
		return cronExpr{}, fmt.Errorf("cron %q day-of-week: %w", expr, err)
	}
	// 7 and 0 both mean Sunday; fold 7 into 0 so matching can use Weekday() (0-6).
	if c.dow&(1<<7) != 0 {
		c.dow |= 1 << 0
		c.dow &^= 1 << 7
	}
	return c, nil
}

// parseField parses one comma-separated cron field into a bitset and reports
// whether it was a bare "*".
func parseField(spec string, lo, hi int) (uint64, bool, error) {
	var mask uint64
	star := false
	for _, part := range strings.Split(spec, ",") {
		if strings.ContainsAny(part, "LW#?") {
			return 0, false, fmt.Errorf("%q: L/W/#/? specials are not supported — only plain values, ranges (a-b), steps (*/n, a-b/n) and comma lists", part)
		}
		base, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			s, err := strconv.Atoi(stepStr)
			if err != nil || s <= 0 {
				return 0, false, fmt.Errorf("bad step in %q", part)
			}
			step = s
		}

		var from, to int
		switch {
		case base == "*":
			from, to = lo, hi
			if !hasStep {
				star = true
			}
		case strings.ContainsRune(base, '-'):
			a, b, _ := strings.Cut(base, "-")
			x, err1 := strconv.Atoi(a)
			y, err2 := strconv.Atoi(b)
			if err1 != nil || err2 != nil {
				return 0, false, fmt.Errorf("bad range %q", part)
			}
			from, to = x, y
		default:
			x, err := strconv.Atoi(base)
			if err != nil {
				return 0, false, fmt.Errorf("bad value %q", part)
			}
			from, to = x, x
		}

		if from < lo || to > hi || from > to {
			return 0, false, fmt.Errorf("%q out of range [%d,%d]", part, lo, hi)
		}
		for v := from; v <= to; v += step {
			mask |= 1 << uint(v)
		}
	}
	return mask, star, nil
}

func bitSet(mask uint64, v int) bool { return mask&(1<<uint(v)) != 0 }

func (c cronExpr) matchDay(t time.Time) bool {
	domMatch := bitSet(c.dom, t.Day())
	dowMatch := bitSet(c.dow, int(t.Weekday())) // Sunday == 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dowMatch
	case c.dowStar:
		return domMatch
	default:
		return domMatch || dowMatch // standard cron OR semantics
	}
}

// next steps minute-by-minute from the minute after `after`, bounded to ~5
// years so an impossible spec (e.g. Feb 30) returns an error instead of
// looping forever.
func (c cronExpr) next(after time.Time) (time.Time, error) {
	t := after.Truncate(time.Minute).Add(time.Minute)
	const maxMinutes = 5 * 366 * 24 * 60
	for i := 0; i < maxMinutes; i++ {
		if bitSet(c.month, int(t.Month())) && c.matchDay(t) &&
			bitSet(c.hour, t.Hour()) && bitSet(c.min, t.Minute()) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron: no activation within 5 years")
}
