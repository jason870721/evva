package agentdef

import (
	"testing"
	"time"
)

func TestParseScheduleForms(t *testing.T) {
	t.Run("cron", func(t *testing.T) {
		s, err := parseSchedule("*/5 * * * *", "")
		if err != nil {
			t.Fatal(err)
		}
		if s.Cron != "*/5 * * * *" || s.Every != 0 {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("every", func(t *testing.T) {
		s, err := parseSchedule("", "30s")
		if err != nil {
			t.Fatal(err)
		}
		if s.Every != 30*time.Second || s.Cron != "" {
			t.Fatalf("got %+v", s)
		}
	})
	t.Run("rejects both", func(t *testing.T) {
		if _, err := parseSchedule("* * * * *", "30s"); err == nil {
			t.Fatal("want error when both cron and every are set")
		}
	})
	t.Run("rejects neither", func(t *testing.T) {
		if _, err := parseSchedule("", ""); err == nil {
			t.Fatal("want error when neither is set")
		}
	})
	t.Run("rejects bad cron", func(t *testing.T) {
		if _, err := parseSchedule("not a cron", ""); err == nil {
			t.Fatal("want error for malformed cron")
		}
	})
	t.Run("rejects bad every", func(t *testing.T) {
		if _, err := parseSchedule("", "frequently"); err == nil {
			t.Fatal("want error for malformed duration")
		}
	})
}

func TestScheduleNextEvery(t *testing.T) {
	base := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	s := Schedule{Every: 30 * time.Second}
	got, err := s.Next(base)
	if err != nil {
		t.Fatal(err)
	}
	if want := base.Add(30 * time.Second); !got.Equal(want) {
		t.Fatalf("Next = %v, want %v", got, want)
	}
}

func mustNext(t *testing.T, expr string, after time.Time) time.Time {
	t.Helper()
	n, err := Schedule{Cron: expr}.Next(after)
	if err != nil {
		t.Fatalf("Next(%q): %v", expr, err)
	}
	return n
}

func TestScheduleNextCronExact(t *testing.T) {
	// 2026-06-03 is a Wednesday.
	base := time.Date(2026, 6, 3, 12, 2, 30, 0, time.UTC)

	cases := []struct {
		expr string
		want time.Time
	}{
		{"*/5 * * * *", time.Date(2026, 6, 3, 12, 5, 0, 0, time.UTC)},
		{"0 17 * * *", time.Date(2026, 6, 3, 17, 0, 0, 0, time.UTC)},
		{"0 0 1 * *", time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"15,45 * * * *", time.Date(2026, 6, 3, 12, 15, 0, 0, time.UTC)},
		{"0 9-17 * * *", time.Date(2026, 6, 3, 13, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := mustNext(t, c.expr, base); !got.Equal(c.want) {
			t.Errorf("Next(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}

func TestScheduleNextStrictlyAfter(t *testing.T) {
	// `after` is exactly on a matching minute — Next must return the FOLLOWING
	// activation, not the same instant.
	onMatch := time.Date(2026, 6, 3, 9, 30, 0, 0, time.UTC)
	got := mustNext(t, "30 9 * * *", onMatch)
	if want := time.Date(2026, 6, 4, 9, 30, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("Next = %v, want %v (strictly after)", got, want)
	}
}

func TestScheduleNextDayOfWeek(t *testing.T) {
	base := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC) // Wednesday
	got := mustNext(t, "0 9 * * 1", base)                // Mondays at 09:00
	if got.Weekday() != time.Monday || got.Hour() != 9 || got.Minute() != 0 {
		t.Fatalf("Next = %v, want a Monday 09:00", got)
	}
	if !got.After(base) || got.Sub(base) > 7*24*time.Hour {
		t.Fatalf("Next = %v, want within a week after %v", got, base)
	}
	// Sunday as 0 and as 7 must mean the same day.
	if a, b := mustNext(t, "0 0 * * 0", base), mustNext(t, "0 0 * * 7", base); !a.Equal(b) {
		t.Fatalf("dow 0 (%v) and 7 (%v) should both be Sunday", a, b)
	}
}

func TestScheduleNextDomDowOr(t *testing.T) {
	// When both day-of-month and day-of-week are restricted, a day matches if
	// EITHER does (standard cron OR semantics).
	base := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	got := mustNext(t, "0 0 13 * 5", base) // the 13th OR any Friday, at midnight
	if got.Hour() != 0 || got.Minute() != 0 {
		t.Fatalf("Next = %v, want midnight", got)
	}
	if got.Day() != 13 && got.Weekday() != time.Friday {
		t.Fatalf("Next = %v, want the 13th or a Friday", got)
	}
}

func TestParseCronErrors(t *testing.T) {
	bad := []string{
		"* * * *",     // too few fields
		"* * * * * *", // too many
		"60 * * * *",  // minute out of range
		"* 24 * * *",  // hour out of range
		"* * 0 * *",   // dom out of range (min 1)
		"* * 32 * *",  // dom out of range
		"* * * 13 *",  // month out of range
		"* * * * 8",   // dow out of range (max 7)
		"a * * * *",   // non-numeric
		"*/0 * * * *", // zero step
	}
	for _, expr := range bad {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("parseCron(%q) = nil error, want failure", expr)
		}
	}
}
