package common

import (
	"regexp"
	"testing"
	"time"
)

// withLocal pins the process-local zone for the duration of a test so stamp
// rendering is deterministic regardless of the host's TZ.
func withLocal(t *testing.T, loc *time.Location) {
	t.Helper()
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}

func TestStamp(t *testing.T) {
	withLocal(t, time.FixedZone("HKT", 8*3600))
	in := time.Date(2026, 6, 10, 12, 25, 0, 0, time.UTC)
	if got, want := Stamp(in), "2026-06-10 20:25:00 +08:00"; got != want {
		t.Errorf("Stamp = %q, want %q", got, want)
	}
}

func TestStampWithUTC(t *testing.T) {
	withLocal(t, time.FixedZone("HKT", 8*3600))
	in := time.Date(2026, 6, 10, 12, 25, 0, 0, time.UTC)
	want := "2026-06-10 20:25:00 +08:00 (= 2026-06-10 12:25 UTC)"
	if got := StampWithUTC(in); got != want {
		t.Errorf("StampWithUTC = %q, want %q", got, want)
	}
}

func TestStampWithUTC_dateRollsAcrossZones(t *testing.T) {
	withLocal(t, time.FixedZone("HKT", 8*3600))
	in := time.Date(2026, 6, 10, 20, 0, 0, 0, time.UTC) // next day local
	want := "2026-06-11 04:00:00 +08:00 (= 2026-06-10 20:00 UTC)"
	if got := StampWithUTC(in); got != want {
		t.Errorf("StampWithUTC = %q, want %q", got, want)
	}
}

func TestZoneLabel(t *testing.T) {
	withLocal(t, time.FixedZone("HKT", 8*3600))
	if got, want := ZoneLabel(), "HKT (UTC+08:00)"; got != want {
		t.Errorf("ZoneLabel = %q, want %q", got, want)
	}
}

func TestZoneLabel_negativeAndHalfHour(t *testing.T) {
	withLocal(t, time.FixedZone("NST", -(3*3600 + 30*60)))
	if got, want := ZoneLabel(), "NST (UTC-03:30)"; got != want {
		t.Errorf("ZoneLabel = %q, want %q", got, want)
	}
}

func TestStampCarriesOffset(t *testing.T) {
	// Whatever the zone, a stamp must end in an explicit ±HH:MM offset.
	if !regexp.MustCompile(`[+-]\d{2}:\d{2}$`).MatchString(Stamp(time.Now())) {
		t.Errorf("Stamp(now) = %q lacks a UTC offset", Stamp(time.Now()))
	}
}
