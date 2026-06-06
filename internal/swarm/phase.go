package swarm

import "github.com/johnny1110/evva/pkg/event"

// phaseDeriver maps a member's event stream onto the fine RunPhase shown in the
// roster (RP-3). It is a port of evva's TUI run-state machine
// (pkg/ui/bubbletea/components/status.State.Apply) — the swarm already receives
// the same events over its sink, so it derives the same vocabulary the TUI shows
// rather than collapsing a whole run into a flat "busy" — extended with the two
// swarm-only blocked phases (WAITING_APPROVAL / WAITING_INPUT) that make a stuck
// member's reason visible at a glance.
//
// One deriver per member, owned by that member's sink; apply runs on the agent's
// emit goroutine. It returns changed=false when the phase is unchanged so the
// sink only writes the roster on an actual transition (streaming chunks don't
// thrash the lock — the first chunk moves to thinking/texting, the rest are
// no-ops).
type phaseDeriver struct {
	phase RunPhase
	tool  string
}

func newPhaseDeriver() *phaseDeriver { return &phaseDeriver{phase: PhaseReady} }

// apply folds one event into the phase and reports whether it changed.
func (d *phaseDeriver) apply(e event.Event) (RunPhase, string, bool) {
	prevPhase, prevTool := d.phase, d.tool

	switch e.Kind {
	case event.KindRunStart, event.KindRunResume, event.KindTurnStart, event.KindTurnEnd:
		d.set(PhaseRunning, "")
	case event.KindRunEnd, event.KindIdle, event.KindRunCancelled:
		// Run finished/torn down — back to ready. A suspended member still reads
		// "suspended" because display composition lets the coarse status win.
		d.set(PhaseReady, "")
	case event.KindThinking, event.KindThinkingChunk:
		d.set(PhaseThinking, "")
	case event.KindText, event.KindTextChunk:
		d.set(PhaseTexting, "")
	case event.KindToolUseStart:
		tool := ""
		if e.ToolUseStart != nil {
			tool = e.ToolUseStart.Name
		}
		d.set(PhaseExecuting, tool)
	case event.KindToolUseResult:
		// Tool finished — back to generic running; the next sub-phase moves on.
		d.set(PhaseRunning, "")
	case event.KindApprovalNeeded:
		// The blocked tool now parks in the permission broker until answered — the
		// single most useful state to surface (it is how RP-2's hang looked as a
		// flat "busy"). Stays here, emitting nothing, until the reply lands.
		tool := ""
		if e.ApprovalNeeded != nil {
			tool = e.ApprovalNeeded.ToolName
		}
		d.set(PhaseWaitingApproval, tool)
	case event.KindQuestionNeeded:
		d.set(PhaseWaitingInput, "")
	case event.KindDrainingInfo, event.KindDrainInbox, event.KindDrainBackgroundTask, event.KindDrainMonitorEvents:
		d.set(PhaseDraining, "")
	case event.KindCompacting:
		d.set(PhaseCompacting, "")
	case event.KindCompactingEnd:
		d.set(PhaseRunning, "")
	case event.KindIterLimit:
		d.set(PhasePaused, "")
	case event.KindError:
		d.set(PhaseError, "")
	}

	return d.phase, d.tool, d.phase != prevPhase || d.tool != prevTool
}

func (d *phaseDeriver) set(p RunPhase, tool string) { d.phase, d.tool = p, tool }
