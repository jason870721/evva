// This file is the DWF dispatch engine: the mechanical half of the task
// graph. The leader declares structure at task_create time (assignee,
// dependencies, verify policy); the engine executes it — flipping
// engine-managed tasks to running the moment their dependencies complete and
// delivering the same assignment mail a manual task_assign sends. Every
// trigger converges on one idempotent store sweep (store.SweepDispatchable),
// so the completion hook and the rescan tick can both fire without
// double-dispatch, and a crash between a completion and its dispatch heals on
// the next tick. The DB is the truth; the sweep makes it converge.

package swarm

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/event"
)

// EngineSender is the mail sender name for engine actions — the same
// non-member identity the ops watchdogs use (scheduler.notifyOps).
const EngineSender = "system"

// DWF engine event kinds — space-level synthetics flowing through the same
// pump the member sinks feed, so the live console and the durable event log
// both carry every engine action. The Kind vocabulary is open by design
// (pkg/event); each event reuses TextPayload for one greppable line.
const (
	KindTaskDispatched = event.Kind("task_dispatched")
	KindMemberSpawned  = event.Kind("member_spawned")
	KindMemberRetired  = event.Kind("member_retired")
)

// KindOpsAlert is a system alert promoted to a first-class space event
// (NTF-1): every notifyOps notice — stall, budget trip, stale task, stale
// mailbox — emits one alongside its durable mail, so the console timeline,
// the chatlog, and the outbound notifier see what was previously
// mailbox-only. TextPayload carries "subject\nbody"; AgentID names the
// member the alert is about. Dedup rides the sources (one per episode/stay/
// run), exactly like the mail.
const KindOpsAlert = event.Kind("ops_alert")

// emitEngineEvent pushes one space-level synthetic event into sp.out —
// engine actions have no member tool-call event to ride. Non-blocking: a
// space nobody drains (lite tests) drops the line, and an engine action must
// never stall on observability (the eventlog rule — the observer never slows
// the observed). AgentID carries the affected member so the WS agent filter
// and the console group it correctly.
func (sp *SwarmSpace) emitEngineEvent(kind event.Kind, agentID, line string) {
	if sp.out == nil {
		return
	}
	ev := SpacedEvent{SpaceID: sp.ID, Event: event.Event{
		Kind:    kind,
		AgentID: agentID,
		Time:    time.Now(),
		Text:    &event.TextPayload{Text: line},
	}}
	select {
	case sp.out <- ev:
	default:
	}
}

// AssignmentMail composes the wake message that starts a task — one body for
// both dispatch paths, so a worker cannot tell manual from auto apart except
// by the marker. auto adds the "(auto-dispatched)" note: it tells the worker
// no leader run preceded the dispatch, so spec ambiguity means asking the
// leader, not guessing (DWF open question #3).
func AssignmentMail(t store.Task, sender string, auto bool) store.Message {
	refID := t.ID
	subject := fmt.Sprintf("Task #%d assigned", t.ID)
	body := fmt.Sprintf("You are assigned task #%d: %s", t.ID, t.Title)
	if t.Spec != "" {
		body += "\n\n" + t.Spec
	}
	if auto {
		subject += " (auto-dispatched)"
		body += "\n\n(Auto-dispatched: this task's dependencies completed, so the engine started it " +
			"directly — no leader run preceded this. If the spec is ambiguous, ask the leader before guessing.)"
	}
	return store.Message{
		Sender:    sender,
		Recipient: t.Assignee,
		Subject:   subject,
		Body:      body,
		RefTask:   &refID,
	}
}

// DispatchReady runs one engine turn: sweep the ledger for engine-managed
// tasks whose time has come (store.SweepDispatchable marks them running
// atomically) and mail each assignee. Returns the dispatched tasks so callers
// can fold them into tool results and event lines. A mail failure after the
// flip mirrors manual task_assign's failure surface — the task IS running,
// the error is logged, and the RP-22 stale-task watchdog is the backstop; the
// persist-before-signal bus makes anything short of a store failure durable.
func (sp *SwarmSpace) DispatchReady() ([]store.Task, error) {
	ready, err := sp.Store.SweepDispatchable()
	if err != nil || len(ready) == 0 {
		return nil, err
	}
	for _, t := range ready {
		if _, err := sp.Bus.Send(AssignmentMail(t, EngineSender, true)); err != nil {
			slog.Warn("swarm dispatch: task set running but assignment mail failed",
				"task", t.ID, "assignee", t.Assignee, "err", err)
		}
		sp.emitEngineEvent(KindTaskDispatched, t.Assignee,
			fmt.Sprintf("task #%d %q auto-dispatched → %s", t.ID, t.Title, t.Assignee))
	}
	sp.metrics.countAutoDispatch(len(ready))
	return ready, nil
}
