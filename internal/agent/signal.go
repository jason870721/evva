package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools/monitor"
	"github.com/johnny1110/evva/pkg/tools/shell"
)

// SignalKind tags an AgentSignal. The two values share one chan so the
// pump goroutine acquires the run flag once per signal regardless of
// source — bg bash and Monitor have parallel idle-wake semantics.
type SignalKind string

const (
	SignalBgResult     SignalKind = "bg_result"
	SignalMonitorEvent SignalKind = "monitor_event"
)

// AgentSignal is the unit the agent's signal channel carries. Exactly
// one of the typed pointer fields is non-nil per signal, matched to
// Kind. The producer (Bash bg goroutine, Monitor goroutine) populates
// the store BEFORE sending the signal — that ordering is what lets the
// drain path read consistent state when CAS loses.
type AgentSignal struct {
	Kind         SignalKind
	BgResult     *shell.BgTaskSnapshot
	MonitorEvent *monitor.MonitorEvent
}

// signalChanCap is the buffered capacity of a.signalCh. Sized so a
// burst of monitor events doesn't block the producing goroutine; if
// the buffer fills (~64 in-flight signals) we drop the wake-up signal
// and rely on the next-iter drain — the store / queue is the durable
// backstop, the chan is only the wake-up vehicle.
const signalChanCap = 64

// SendSignal pushes one signal on a.signalCh. Non-blocking — if the
// chan is full the signal is dropped and we log; the loop's drain path
// at iter start still picks up the result because the producer wrote
// the store BEFORE calling SendSignal.
//
// Safe to call from any goroutine; the chan does its own
// synchronisation.
func (a *Agent) SendSignal(sig AgentSignal) {
	if a.signalCh == nil {
		return
	}
	select {
	case a.signalCh <- sig:
	default:
		a.logger.Warn("signal.dropped", "kind", sig.Kind, "reason", "chan_full")
	}
}

// signalPump is the per-agent goroutine started in agent.New that
// listens for AgentSignals and either wakes an idle agent (CAS on
// a.running, spawn a runLoop) or relies on the running loop's
// iteration-boundary drain.
//
// The pump exits cleanly when rootCtx is cancelled (agent.Shutdown
// closes the chan via rootCancel).
func (a *Agent) signalPump() {
	for {
		select {
		case <-a.rootCtx.Done():
			a.logger.Debug("signal.pump.exit", "reason", "root_ctx_done")
			return
		case sig, ok := <-a.signalCh:
			if !ok {
				a.logger.Debug("signal.pump.exit", "reason", "chan_closed")
				return
			}
			a.handleSignal(sig)
		}
	}
}

// handleSignal does two things per signal:
//
//  1. Always emit the wire event for the TUI (KindBgResult /
//     KindMonitorEvent) so the strip + transcript can react regardless
//     of whether the loop is idle or busy.
//
//  2. If the loop is idle, try to acquire it via CAS on a.running and
//     start a fresh runLoop seeded with a system-reminder prompt. The
//     store / queue mutation already happened on the producer side, so
//     the new runLoop's drain at iter start picks it up.
//
// Subagents never wake on signals — only the root agent. Subagent
// bg results bubble up through event.BubbleUp to the parent's TUI
// strip; their conversation context is rebuilt only by the parent's
// next dispatch.
func (a *Agent) handleSignal(sig AgentSignal) {
	a.emitSignalEvent(sig)

	if a.IsSubagent() {
		return
	}
	if !a.running.CompareAndSwap(false, true) {
		// Busy path: the live runLoop's next iteration drain pulls the
		// terminal store entry / queue event into the conversation. No
		// further action needed here.
		return
	}
	// Idle path: we own the run flag. Spawn the run on a fresh goroutine
	// so the pump stays free for follow-up signals; clear the flag in
	// defer.
	go a.runFromSignal()
}

// emitSignalEvent fires KindBgResult or KindMonitorEvent for one signal
// regardless of idle/busy state. The payload is the wire-friendly
// snapshot, so a subagent's bubbled event reaches the parent TUI with
// the right AgentID + content.
func (a *Agent) emitSignalEvent(sig AgentSignal) {
	switch sig.Kind {
	case SignalBgResult:
		if sig.BgResult == nil {
			return
		}
		snap := sig.BgResult
		a.emit(event.KindBgResult, func(e *event.Event) {
			e.BgResult = &event.BgResultPayload{
				TaskID:   snap.ID,
				Status:   string(snap.Status),
				ExitCode: snap.ExitCode,
				Output:   snap.Output,
				AgentID:  snap.AgentID,
			}
		})
	case SignalMonitorEvent:
		if sig.MonitorEvent == nil {
			return
		}
		ev := sig.MonitorEvent
		a.emit(event.KindMonitorEvent, func(e *event.Event) {
			e.MonitorEvent = &event.MonitorEventPayload{
				MonitorID: ev.MonitorID,
				Line:      ev.Line,
				Closing:   ev.Closing,
				AgentID:   a.ID,
			}
		})
	}
}

// runFromSignal is the idle-wake entry. We already CAS-acquired
// a.running on the pump side; defer-release it on the way out. The
// drain at the top of runLoop folds the queued result / event into
// the session, so we don't seed a user message here ourselves.
func (a *Agent) runFromSignal() {
	defer a.running.Store(false)
	a.logger.Debug("run.signal_wake")
	if _, err := a.runLoop(a.rootCtx); err != nil {
		a.logger.Warn("run.signal_wake.err", "err", err)
	}
}

// composeBgReminder builds the <system-reminder> body for one drained
// background-task snapshot. Used by drainBackgroundTaskResults to
// concatenate every drained terminal task into a single RoleUser
// message.
func composeBgReminder(snap shell.BgTaskSnapshot) string {
	verb := string(snap.Status)
	output := strings.TrimRight(snap.Output, "\n")
	if output == "" {
		output = "(no output)"
	}
	return fmt.Sprintf("<system-reminder>background task %s %s, exit code %d\n%s\n</system-reminder>",
		snap.ID, verb, snap.ExitCode, output)
}

// composeMonitorReminder builds the body for one drained monitor
// event. Closing events get a distinct phrasing so the model knows the
// monitor is finished and shouldn't expect more updates.
func composeMonitorReminder(ev monitor.MonitorEvent) string {
	if ev.Closing {
		return fmt.Sprintf("<system-reminder>monitor %s closed at %s</system-reminder>",
			ev.MonitorID, ev.At.Format(time.RFC3339))
	}
	return fmt.Sprintf("<system-reminder>monitor %s event: %s</system-reminder>",
		ev.MonitorID, ev.Line)
}

// signalReminderMessage assembles one RoleUser message body from the
// list of drained reminders, joining with newlines. Empty input
// returns the zero-length llm.Message (caller should short-circuit
// before appending).
func signalReminderMessage(parts []string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: strings.Join(parts, "\n")}
}
