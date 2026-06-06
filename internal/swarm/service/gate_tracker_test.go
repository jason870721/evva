package service

import (
	"testing"

	"github.com/johnny1110/evva/pkg/event"
)

// TestGateTrackerLifecycle: gates are recorded from the event stream, dropped
// when answered, and cleared when their run ends — so the reconnect-replay set
// (RP-2 §3.3) holds exactly the gates a member is still blocked on.
func TestGateTrackerLifecycle(t *testing.T) {
	g := newGateTracker()

	g.observe(event.Event{Kind: event.KindApprovalNeeded, AgentID: "a1",
		ApprovalNeeded: &event.ApprovalNeededPayload{RequestID: "r1", ToolName: "bash"}})
	g.observe(event.Event{Kind: event.KindQuestionNeeded, AgentID: "a2",
		QuestionNeeded: &event.QuestionNeededPayload{RequestID: "q1"}})
	if got := len(g.snapshot()); got != 2 {
		t.Fatalf("snapshot = %d, want 2", got)
	}

	// Answering a gate drops it from the replay set.
	g.remove("r1")
	if got := len(g.snapshot()); got != 1 {
		t.Fatalf("after remove r1: %d, want 1", got)
	}

	// A run-terminal event clears any gate still pending for that agent (a member
	// suspended mid-approval leaves a dead gate nobody will answer).
	g.observe(event.Event{Kind: event.KindRunCancelled, AgentID: "a2"})
	if got := len(g.snapshot()); got != 0 {
		t.Fatalf("after a2 run cancelled: %d, want 0", got)
	}

	// A gate event with no request id is ignored (nothing to key on).
	g.observe(event.Event{Kind: event.KindApprovalNeeded, AgentID: "a3", ApprovalNeeded: &event.ApprovalNeededPayload{}})
	if got := len(g.snapshot()); got != 0 {
		t.Fatalf("empty-reqID gate should not be tracked: %d", got)
	}
}
