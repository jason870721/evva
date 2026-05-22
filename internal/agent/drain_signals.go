package agent

import (
	"github.com/johnny1110/evva/pkg/event"
)

// drainBackgroundTaskResults pulls every terminal background-task
// snapshot off the BgTaskStore and folds them into the session as one
// concatenated <system-reminder> user message. Mirror of
// drainWakeupPrompts (state_machine.go:116) and drainAsyncSubagents.
//
// Returns true when at least one snapshot was drained — callers can
// use this to drive iteration re-entry (the end-of-loop pending check
// in runLoop).
//
// Runs at the top of every iteration. Subagents currently never own a
// bg-task store but the lazy-allocation check makes the call free for
// agents that never spawned a bg task.
func (a *Agent) drainBackgroundTaskResults() bool {
	if !a.toolState.HasBgTaskStore() {
		return false
	}
	drained := a.toolState.BgTaskStore().DrainCompleted()
	if len(drained) == 0 {
		return false
	}
	parts := make([]string, 0, len(drained))
	ids := make([]string, 0, len(drained))
	for _, snap := range drained {
		parts = append(parts, composeBgReminder(snap))
		ids = append(ids, snap.ID)
	}
	a.session.Append(signalReminderMessage(parts))
	a.emit(event.KindDrainBackgroundTask, func(e *event.Event) {
		e.DrainBackgroundTask = &event.DrainBackgroundTaskPayload{TaskIDs: ids}
	})
	a.logger.Debug("bg_tasks.drained", "count", len(drained))
	return true
}

// drainMonitorEvents pulls every queued monitor event off the per-agent
// queue and folds them into the session as one concatenated
// <system-reminder> user message. Same shape as drainBackgroundTaskResults.
//
// Closing events get a distinct phrasing inside composeMonitorReminder
// so the model knows the monitor is finished and should not expect
// further updates from that id.
func (a *Agent) drainMonitorEvents() bool {
	if !a.toolState.HasMonitorEventQueue() {
		return false
	}
	events := a.toolState.MonitorEventQueue().Drain()
	if len(events) == 0 {
		return false
	}
	parts := make([]string, 0, len(events))
	monitorSet := map[string]struct{}{}
	monitorIDs := make([]string, 0)
	for _, ev := range events {
		parts = append(parts, composeMonitorReminder(ev))
		if _, seen := monitorSet[ev.MonitorID]; !seen {
			monitorSet[ev.MonitorID] = struct{}{}
			monitorIDs = append(monitorIDs, ev.MonitorID)
		}
	}
	a.session.Append(signalReminderMessage(parts))
	a.emit(event.KindDrainMonitorEvents, func(e *event.Event) {
		e.DrainMonitorEvents = &event.DrainMonitorEventsPayload{
			EventCount: len(events),
			MonitorIDs: monitorIDs,
		}
	})
	a.logger.Debug("monitor_events.drained", "count", len(events), "monitors", len(monitorIDs))
	return true
}

// hasPendingSignals reports whether either signal-backed queue/store
// carries undrained terminal entries. The agent loop calls this at the
// terminal turn (no tool_calls) to decide whether to release the run
// flag or loop one more iteration so the model sees the result before
// returning to idle.
func (a *Agent) hasPendingSignals() bool {
	if a.toolState.HasBgTaskStore() && a.toolState.BgTaskStore().HasPending() {
		return true
	}
	if a.toolState.HasMonitorEventQueue() && a.toolState.MonitorEventQueue().HasPending() {
		return true
	}
	return false
}

