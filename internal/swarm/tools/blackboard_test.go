package tools

import (
	"strings"
	"testing"
)

// BB-2: write→read round-trips through the tools; the result strings teach
// the economics (no wakes) and the read carries freshness + attribution.
func TestBlackboardToolsRoundTrip(t *testing.T) {
	sp := liteSpace(t, "leader", "w")
	sp.Workdir = t.TempDir()

	write := newBlackboardWrite(leaderMC(sp))
	read := newBlackboardRead(workerMC(sp, "w"))

	// Empty board reads as explicitly empty, not an error.
	if res := exec(t, read, `{}`); res.IsError || !strings.Contains(res.Content, "empty") {
		t.Fatalf("empty read = %+v", res)
	}

	res := exec(t, write, `{"content":"# Plan\nGoal: ship v2."}`)
	if res.IsError {
		t.Fatalf("write failed: %s", res.Content)
	}
	if !strings.Contains(res.Content, "next wake brief") || !strings.Contains(res.Content, "no one was") {
		t.Errorf("write result should teach the zero-wake economics: %q", res.Content)
	}

	res = exec(t, read, `{}`)
	if res.IsError || !strings.Contains(res.Content, "Goal: ship v2.") {
		t.Fatalf("read after write = %+v", res)
	}
	if !strings.Contains(res.Content, "updated just now by leader") {
		t.Errorf("read should carry freshness + attribution: %q", res.Content)
	}
}

// BB-2: the cap surfaces through the tool as a model-visible error (IsError,
// not a Go error), naming the knob so the leader knows to prune.
func TestBlackboardWriteToolCapError(t *testing.T) {
	sp := liteSpace(t, "leader")
	sp.Workdir = t.TempDir()

	write := newBlackboardWrite(leaderMC(sp))
	big := strings.Repeat("x", 5000) // over the 4096 default
	res := exec(t, write, `{"content":"`+big+`"}`)
	if !res.IsError {
		t.Fatal("oversize write must surface as a tool error")
	}
	if !strings.Contains(res.Content, "blackboard_max_bytes") || !strings.Contains(res.Content, "4096") {
		t.Errorf("cap error should name the knob and the cap: %q", res.Content)
	}
}

// BB-2: clearing via an empty write reports the cleared state.
func TestBlackboardWriteToolClear(t *testing.T) {
	sp := liteSpace(t, "leader")
	sp.Workdir = t.TempDir()

	write := newBlackboardWrite(leaderMC(sp))
	exec(t, write, `{"content":"something"}`)
	res := exec(t, write, `{"content":""}`)
	if res.IsError || !strings.Contains(res.Content, "cleared") {
		t.Fatalf("clear = %+v", res)
	}
}
