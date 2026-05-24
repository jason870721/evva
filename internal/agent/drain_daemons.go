package agent

import (
	"fmt"
	"strings"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/tools/daemon"
)

// drainDaemonSignals pulls every queued daemon signal off the agent's
// DaemonState and folds them into the session as one concatenated
// <system-reminder> user message. Replaces the per-kind helpers
// (drainBackgroundTaskResults, drainMonitorEvents) — every daemon kind
// (bash, monitor, agent, future) flows through this single path.
//
// Returns true when at least one signal was drained — callers use this to
// drive iteration re-entry (the end-of-loop pending check in runLoop).
//
// Runs at the top of every iteration. Subagents do not own a DaemonState
// by default but the lazy-allocation check keeps the call free for agents
// that never registered a daemon.
func (a *Agent) drainDaemonSignals() bool {
	if !a.toolState.HasDaemonState() {
		return false
	}
	state := a.toolState.DaemonState()
	signals := state.DrainSignals()
	if len(signals) == 0 {
		return false
	}

	// Bucket events by daemon id so a chatty monitor renders as one
	// system-reminder block per monitor rather than one per line.
	// Lifecycles get their own per-daemon reminders.
	eventBuckets := map[string][]daemon.Signal{}
	eventOrder := []string{}
	lifecycles := make([]daemon.Signal, 0)

	for _, sig := range signals {
		switch {
		case sig.IsLifecycle():
			lifecycles = append(lifecycles, sig)
		case sig.IsEvent():
			if _, seen := eventBuckets[sig.DaemonID]; !seen {
				eventOrder = append(eventOrder, sig.DaemonID)
			}
			eventBuckets[sig.DaemonID] = append(eventBuckets[sig.DaemonID], sig)
		}
	}

	parts := make([]string, 0, len(eventOrder)+len(lifecycles))

	// Events first (the stream chronologically precedes the closing
	// lifecycle for a given daemon).
	for _, id := range eventOrder {
		parts = append(parts, composeDaemonEvents(id, eventBuckets[id]))
	}

	// Lifecycles, with terminal daemons evicted as we render them.
	for _, sig := range lifecycles {
		parts = append(parts, composeDaemonLifecycle(sig))
		if daemon.IsTerminal(sig.Lifecycle.Status) {
			state.Evict(sig.DaemonID)
		}
	}

	if len(parts) == 0 {
		return false
	}

	a.session.Append(signalReminderMessage(parts))

	daemonIDs := make([]string, 0, len(eventOrder)+len(lifecycles))
	for _, id := range eventOrder {
		daemonIDs = append(daemonIDs, id)
	}
	for _, sig := range lifecycles {
		daemonIDs = append(daemonIDs, sig.DaemonID)
	}
	a.emit(event.KindDrainBackgroundTask, func(e *event.Event) {
		e.DrainBackgroundTask = &event.DrainBackgroundTaskPayload{TaskIDs: daemonIDs}
	})
	a.logger.Debug("daemon_signals.drained",
		"signals", len(signals),
		"events", len(eventOrder),
		"lifecycles", len(lifecycles))
	return true
}

// composeDaemonEvents renders one <system-reminder> block grouping every
// streamed event for a single daemon. Closing markers are folded into a
// trailing "(stream closed)" line so the model knows no more events are
// coming from this id.
func composeDaemonEvents(id string, evs []daemon.Signal) string {
	if len(evs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "<system-reminder>daemon %s [%s] events:\n", id, evs[0].Kind)
	closing := false
	for _, sig := range evs {
		if sig.Event == nil {
			continue
		}
		if sig.Event.Closing {
			closing = true
			continue
		}
		b.WriteString(sig.Event.Line)
		b.WriteString("\n")
	}
	if closing {
		b.WriteString("(stream closed)\n")
	}
	b.WriteString("</system-reminder>")
	return b.String()
}

// composeDaemonLifecycle renders one <system-reminder> block for a
// terminal lifecycle transition. Kind-specific metadata (bash exit code +
// output, agent summary, monitor event count) is appended verbatim so
// the model has the result without needing a follow-up daemon_output call.
func composeDaemonLifecycle(sig daemon.Signal) string {
	snap := sig.Snapshot
	status := sig.Lifecycle.Status

	var detail string
	switch m := snap.Metadata.(type) {
	case daemon.LocalBashMeta:
		exit := 0
		if m.ExitCode != nil {
			exit = *m.ExitCode
		}
		output := strings.TrimRight(m.Output, "\n")
		if output == "" {
			output = "(no output)"
		}
		detail = fmt.Sprintf(", exit code %d\n%s", exit, output)
	case daemon.LocalAgentMeta:
		body := strings.TrimSpace(m.Summary)
		if m.Err != "" {
			if body != "" {
				body += "\n"
			}
			body += "error: " + m.Err
		}
		if body == "" {
			body = "(no summary)"
		}
		detail = "\n" + body
	case daemon.MonitorMeta:
		detail = fmt.Sprintf(", events=%d", m.EventCount)
	case daemon.LSPMeta:
		detail = fmt.Sprintf(", server=%s, state=%s, restarts=%d/%d",
			m.ServerName, m.State, m.RestartCount, m.MaxRestarts)
	}

	return fmt.Sprintf("<system-reminder>daemon %s [%s] %s%s</system-reminder>",
		sig.DaemonID, sig.Kind, status, detail)
}
