package session

import (
	"testing"
	"time"
)

func hdr(id, slug string, age time.Duration, pinned bool) Header {
	return Header{
		Meta:  Meta{SessionID: id, WorkdirSlug: slug, Pinned: pinned},
		MTime: time.Now().Add(-age).UnixNano(),
	}
}

func ids(hs []Header) map[string]bool {
	out := map[string]bool{}
	for _, h := range hs {
		out[h.SessionID] = true
	}
	return out
}

func TestSelectExpiredEmptyPolicyDeletesNothing(t *testing.T) {
	rows := []Header{hdr("a", "p", 400*24*time.Hour, false)}
	if got := SelectExpired(rows, Retention{}, nil, time.Now()); len(got) != 0 {
		t.Errorf("the shipped default (both caps 0) must never select a victim; got %v", ids(got))
	}
}

func TestSelectExpiredCountIsPerWorkdir(t *testing.T) {
	// Project "big" has four sessions, "small" has one. keep=2 must not
	// punish "small" for "big"'s volume.
	rows := []Header{
		hdr("b1", "big", 1*time.Hour, false),
		hdr("b2", "big", 2*time.Hour, false),
		hdr("b3", "big", 3*time.Hour, false),
		hdr("b4", "big", 4*time.Hour, false),
		hdr("s1", "small", 9*time.Hour, false),
	}
	got := ids(SelectExpired(rows, Retention{MaxCount: 2}, nil, time.Now()))
	if !got["b3"] || !got["b4"] {
		t.Errorf("the two oldest in 'big' should go; got %v", got)
	}
	if got["b1"] || got["b2"] {
		t.Errorf("the two newest in 'big' must survive; got %v", got)
	}
	if got["s1"] {
		t.Errorf("'small' has one session under keep=2 and must be untouched; got %v", got)
	}
}

// Pins are invisible to the policy, not merely exempt from deletion: they
// must not consume a keep slot either, or pinning would silently shorten
// the retained history.
func TestSelectExpiredPinsDoNotConsumeSlots(t *testing.T) {
	rows := []Header{
		hdr("pinned", "p", 1*time.Hour, true),
		hdr("n1", "p", 2*time.Hour, false),
		hdr("n2", "p", 3*time.Hour, false),
		hdr("n3", "p", 4*time.Hour, false),
	}
	got := ids(SelectExpired(rows, Retention{MaxCount: 2}, nil, time.Now()))
	if got["pinned"] {
		t.Error("a pinned session must never be selected")
	}
	if !got["n3"] {
		t.Errorf("n3 is the third unpinned session under keep=2 and should go; got %v", got)
	}
	if got["n1"] || got["n2"] {
		t.Errorf("two unpinned sessions must survive keep=2 alongside the pin; got %v", got)
	}
}

func TestSelectExpiredAgeLimit(t *testing.T) {
	rows := []Header{
		hdr("fresh", "p", 2*24*time.Hour, false),
		hdr("stale", "p", 40*24*time.Hour, false),
	}
	got := ids(SelectExpired(rows, Retention{MaxAge: 30 * 24 * time.Hour}, nil, time.Now()))
	if !got["stale"] || got["fresh"] {
		t.Errorf("age limit selected the wrong set: %v", got)
	}
}

func TestSelectExpiredKeepIDsProtectsLiveSession(t *testing.T) {
	rows := []Header{
		hdr("live", "p", 90*24*time.Hour, false),
		hdr("old", "p", 90*24*time.Hour, false),
	}
	got := ids(SelectExpired(rows, Retention{MaxAge: 24 * time.Hour}, map[string]bool{"live": true}, time.Now()))
	if got["live"] {
		t.Error("a session named in keepIDs must survive even when the policy would take it")
	}
	if !got["old"] {
		t.Errorf("the unprotected stale session should still go; got %v", got)
	}
}

func TestSetFlagsPreservesConversation(t *testing.T) {
	home := t.TempDir()
	snap := newSnapshot("s1", "-tmp", "hello there")
	if err := Save(home, snap); err != nil {
		t.Fatal(err)
	}

	title := "the refactor"
	pinned := true
	if err := SetFlags(home, "-tmp", "s1", &title, &pinned); err != nil {
		t.Fatalf("SetFlags: %v", err)
	}
	got, err := Load(home, "-tmp", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != title || !got.Pinned {
		t.Errorf("flags not persisted: title=%q pinned=%v", got.Title, got.Pinned)
	}
	if len(got.Session.Messages) != len(snap.Session.Messages) {
		t.Errorf("SetFlags disturbed the conversation: %d messages, want %d",
			len(got.Session.Messages), len(snap.Session.Messages))
	}

	// A nil pointer leaves that field alone — the picker toggles a pin
	// without knowing the title.
	off := false
	if err := SetFlags(home, "-tmp", "s1", nil, &off); err != nil {
		t.Fatal(err)
	}
	got, _ = Load(home, "-tmp", "s1")
	if got.Pinned {
		t.Error("pin should be off")
	}
	if got.Title != title {
		t.Errorf("title should be untouched by a pin-only update; got %q", got.Title)
	}
}

func TestDeleteAllReportsCount(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"a", "b"} {
		if err := Save(home, newSnapshot(id, "-tmp", id)); err != nil {
			t.Fatal(err)
		}
	}
	rows, _, err := List(home, "-tmp")
	if err != nil {
		t.Fatal(err)
	}
	n, err := DeleteAll(home, rows)
	if err != nil || n != 2 {
		t.Fatalf("DeleteAll: n=%d err=%v", n, err)
	}
	left, _, _ := List(home, "-tmp")
	if len(left) != 0 {
		t.Errorf("expected an empty directory; %d left", len(left))
	}
}
