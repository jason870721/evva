package swarm

import (
	"testing"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/event"
)

// TestPhaseDeriverTracksSubPhases mirrors the TUI status state_test: events drive
// the phase through the running → thinking → texting → executing → running
// sub-phases, plus the swarm-only waiting phases.
func TestPhaseDeriverTracksSubPhases(t *testing.T) {
	d := newPhaseDeriver()
	if d.phase != PhaseReady {
		t.Fatalf("initial phase = %q, want ready", d.phase)
	}

	steps := []struct {
		name string
		ev   event.Event
		want RunPhase
		tool string
	}{
		{"run start", event.Event{Kind: event.KindRunStart, RunStart: &event.RunStartPayload{Prompt: "x"}}, PhaseRunning, ""},
		{"thinking", event.Event{Kind: event.KindThinking, Thinking: &event.TextPayload{Text: "..."}}, PhaseThinking, ""},
		{"text", event.Event{Kind: event.KindText, Text: &event.TextPayload{Text: "hi"}}, PhaseTexting, ""},
		{"tool start", event.Event{Kind: event.KindToolUseStart, ToolUseStart: &event.ToolUseStartPayload{Name: "bash"}}, PhaseExecuting, "bash"},
		{"approval", event.Event{Kind: event.KindApprovalNeeded, ApprovalNeeded: &event.ApprovalNeededPayload{ToolName: "bash"}}, PhaseWaitingApproval, "bash"},
		{"tool result", event.Event{Kind: event.KindToolUseResult, ToolUseResult: &event.ToolUseResultPayload{}}, PhaseRunning, ""},
		{"question", event.Event{Kind: event.KindQuestionNeeded, QuestionNeeded: &event.QuestionNeededPayload{}}, PhaseWaitingInput, ""},
		{"compacting", event.Event{Kind: event.KindCompacting, Compacting: &event.CompactingPayload{}}, PhaseCompacting, ""},
		{"compact end", event.Event{Kind: event.KindCompactingEnd, CompactingEnd: &event.CompactingEndPayload{OK: true}}, PhaseRunning, ""},
		{"run end", event.Event{Kind: event.KindRunEnd, RunEnd: &event.RunEndPayload{}}, PhaseReady, ""},
	}
	for _, s := range steps {
		p, tool, _ := d.apply(s.ev)
		if p != s.want || tool != s.tool {
			t.Errorf("after %s: phase=%q tool=%q, want %q/%q", s.name, p, tool, s.want, s.tool)
		}
	}
}

// TestPhaseDeriverChangedFlag: an event that doesn't move the phase reports
// changed=false, so streaming chunks don't thrash the roster lock.
func TestPhaseDeriverChangedFlag(t *testing.T) {
	d := newPhaseDeriver()
	if _, _, changed := d.apply(event.Event{Kind: event.KindThinking, Thinking: &event.TextPayload{Text: "a"}}); !changed {
		t.Fatal("first thinking event should be a change")
	}
	if _, _, changed := d.apply(event.Event{Kind: event.KindThinkingChunk, Thinking: &event.TextPayload{Text: "b"}}); changed {
		t.Fatal("a second thinking chunk should NOT report a change")
	}
	// An unhandled event kind (usage) leaves the phase put.
	if _, _, changed := d.apply(event.Event{Kind: event.KindUsage}); changed {
		t.Fatal("an unrelated event must not change the phase")
	}
}

// TestDisplayPhaseComposition: the coarse run status and fine phase compose into
// one label — suspended wins, the tool name is appended for executing/waiting,
// and an empty phase falls back to the coarse status.
func TestDisplayPhaseComposition(t *testing.T) {
	cases := []struct {
		run  RunStatus
		ph   RunPhase
		tool string
		want string
	}{
		{RunBusy, PhaseExecuting, "bash", "executing:bash"},
		{RunBusy, PhaseWaitingApproval, "bash", "waiting-approval:bash"},
		{RunBusy, PhaseThinking, "", "thinking"},
		{RunIdle, PhaseReady, "", "ready"},
		{RunSuspended, PhaseReady, "", "suspended"}, // coarse suspended wins over a moved-on phase
		{RunBusy, "", "", "busy"},                   // empty phase falls back to coarse
	}
	for _, c := range cases {
		got := MemberView{Run: c.run, Phase: c.ph, Tool: c.tool}.DisplayPhase()
		if got != c.want {
			t.Errorf("DisplayPhase(run=%s phase=%s tool=%s) = %q, want %q", c.run, c.ph, c.tool, got, c.want)
		}
	}
}

// TestSinkUpdatesRosterPhase: a member's sink derives the phase from its event
// stream and writes it to the roster (the single source of truth for both
// list_members and the web), while still forwarding every event downstream.
func TestSinkUpdatesRosterPhase(t *testing.T) {
	r := newRoster()
	if err := r.add("w", agentdef.RoleWorker, "", nil); err != nil {
		t.Fatal(err)
	}
	out := make(chan SpacedEvent, 16)
	sink := &spaceSink{spaceID: "s", name: "w", roster: r, deriver: newPhaseDeriver(), out: out}

	phaseOf := func() (RunPhase, string) {
		for _, mv := range r.Snapshot() {
			if mv.Name == "w" {
				return mv.Phase, mv.Tool
			}
		}
		return "", ""
	}

	sink.Emit(event.Event{Kind: event.KindToolUseStart, ToolUseStart: &event.ToolUseStartPayload{Name: "bash"}})
	if p, tool := phaseOf(); p != PhaseExecuting || tool != "bash" {
		t.Fatalf("after tool start: %s/%s, want executing/bash", p, tool)
	}
	sink.Emit(event.Event{Kind: event.KindApprovalNeeded, ApprovalNeeded: &event.ApprovalNeededPayload{ToolName: "bash"}})
	if p, _ := phaseOf(); p != PhaseWaitingApproval {
		t.Fatalf("after approval: %s, want waiting-approval", p)
	}
	if len(out) != 2 {
		t.Fatalf("sink forwarded %d events downstream, want 2", len(out))
	}
}
