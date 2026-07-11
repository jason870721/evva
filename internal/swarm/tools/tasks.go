package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/store"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// memberActor is the store Actor for a member's ledger writes. Passing the
// member's real role lets the store's writer matrix rule (DWF §4): the leader
// holds every edge, the assignee holds running→verifying via task_done, and a
// member that somehow held someone else's write tool is rejected there —
// defense-in-depth, enforced at the data layer.
func memberActor(mc swarm.MemberContext) store.Actor {
	return store.Actor{Name: mc.Name, Role: store.Role(mc.Role)}
}

// transitionError maps a store rejection onto a model-visible tool error so the
// model can adjust rather than crash (AC#3 — surfaced, not panicked).
func transitionError(tool string, err error) pubtools.Result {
	switch {
	case errors.Is(err, store.ErrNotLeader):
		return errf("%s: this transition is not yours to write (the Leader holds every edge; a worker only reports its OWN task done via task_done)", tool)
	case errors.Is(err, store.ErrTaskNotFound):
		return errf("%s: task not found", tool)
	default: // ErrIllegalTransition and anything else
		return errf("%s: %v", tool, err)
	}
}

// idList renders task ids as "#2, #3" for messages.
func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("#%d", id)
	}
	return strings.Join(parts, ", ")
}

// formatTask renders one ledger row. staleAfter > 0 tags tasks parked in
// running/verifying beyond it with their age (RP-22) — the inline twin of the
// watchdog's reminder, so a leader re-reading the board sees the same signal.
func formatTask(t store.Task, staleAfter time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d [%s] %s (assignee: %s)", t.ID, t.Status, t.Title, t.Assignee)
	if len(t.DependsOn) > 0 {
		fmt.Fprintf(&b, " ⛓ deps: %s", idList(t.DependsOn))
	}
	switch t.VerifyPolicy {
	case store.VerifyAuto:
		b.WriteString(" [verify:auto]")
	case store.VerifyChecks:
		b.WriteString(" [verify:checks]")
	}
	if t.CheckOff {
		b.WriteString(" [check:off]")
	}
	if staleAfter > 0 && (t.Status == store.StatusRunning || t.Status == store.StatusVerifying) {
		if age := time.Since(time.UnixMilli(t.UpdatedAt)); age >= staleAfter {
			fmt.Fprintf(&b, " ⏳ stale %s", humanTaskAge(age))
		}
	}
	if t.Spec != "" {
		fmt.Fprintf(&b, "\n    spec: %s", t.Spec)
	}
	if t.Result != "" {
		fmt.Fprintf(&b, "\n    result: %s", t.Result)
	}
	if t.VerifyNote != "" {
		fmt.Fprintf(&b, "\n    note: %s", t.VerifyNote)
	}
	if t.Checks != nil {
		fmt.Fprintf(&b, "\n    checks: %s", t.Checks.Outcome())
	}
	return b.String()
}

// task_list paging: completed is monotonic, so an unbounded list would re-inject
// the whole history into the leader's context on every poll (RP-6). Default to a
// recent page; cap hard so even an explicit limit can't blow the context.
const (
	taskListDefaultLimit = 20
	taskListMaxLimit     = 50
)

// humanTaskAge renders a stale age board-style: days past 48h, else hours,
// else minutes. Mirrors the watchdog's humanAge wording (internal/swarm).
func humanTaskAge(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
}

// formatTasks renders a page of tasks. offset/total describe the window within
// the full match set: when the page IS the whole set (offset 0, total == len) it
// prints the plain "label (N)" header (unchanged for my_tasks); otherwise it
// shows "showing A–B of TOTAL" and, if more remain, the next offset to page with.
func formatTasks(label string, tasks []store.Task, offset, total int, staleAfter time.Duration) pubtools.Result {
	var b strings.Builder
	end := offset + len(tasks)
	if offset > 0 || total > len(tasks) {
		start := offset + 1
		if len(tasks) == 0 {
			start = offset
		}
		fmt.Fprintf(&b, "%s (showing %d-%d of %d):\n", label, start, end, total)
	} else {
		fmt.Fprintf(&b, "%s (%d):\n", label, len(tasks))
	}
	for _, t := range tasks {
		b.WriteString(formatTask(t, staleAfter))
		b.WriteByte('\n')
	}
	if end < total {
		fmt.Fprintf(&b, "\n%d more — pass offset=%d to see the next page.\n", total-end, end)
	}
	return pubtools.Result{Content: b.String(), Metadata: tasks}
}

// --- Leader writes ---------------------------------------------------------

// dispatchNote runs the DWF engine after a completion and renders what it
// started as a tool-result suffix — the leader sees the cascade inside the
// same run, no extra wake. A sweep error never fails the completing tool
// (the task IS completed); the rescan tick retries the dispatch.
func dispatchNote(sp *swarm.SwarmSpace) string {
	ready, err := sp.DispatchReady()
	if err != nil {
		return fmt.Sprintf(" (dependency dispatch sweep failed: %v — the rescan tick will retry)", err)
	}
	if len(ready) == 0 {
		return ""
	}
	parts := make([]string, len(ready))
	for i, t := range ready {
		parts[i] = fmt.Sprintf("#%d→%s", t.ID, t.Assignee)
	}
	return " Auto-dispatched: " + strings.Join(parts, ", ") + "."
}

// newTaskCreate pushes a new task (assignee required). Depless tasks land
// pending for a manual task_assign; tasks with depends_on are engine-managed —
// born blocked (or dispatched immediately when every dep is already complete)
// and started by the engine, never by hand.
func newTaskCreate(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskCreate,
		desc: "Create a new task and assign it to a worker (push model). Without depends_on it starts " +
			"'pending' — use task_assign to dispatch it. With depends_on it is engine-managed: it waits in " +
			"'blocked' and auto-dispatches (set running + assignee notified) the moment every dependency " +
			"completes — plan whole chains up front and do NOT task_assign them. Only the Leader creates tasks.",
		schema: `{"type":"object","properties":{` +
			`"title":{"type":"string","description":"Short task title."},` +
			`"spec":{"type":"string","description":"Full task specification / acceptance criteria."},` +
			`"assignee":{"type":"string","description":"Member name to own this task (see list_members)."},` +
			`"depends_on":{"type":"array","items":{"type":"integer"},"description":"Existing task ids this task waits for (AND-join, immutable). The engine auto-dispatches it when all complete."},` +
			`"verify":{"type":"string","enum":["leader","auto","checks"],"description":"Who settles verifying: 'leader' (default — you inspect, then task_verify), 'auto' (completes the instant the worker reports task_done; reserve for mechanical, low-risk steps), or 'checks' (the space's configured check command gates it — green auto-completes, red escalates to you with evidence; needs settings.verify_checks)."},` +
			`"check":{"type":"string","enum":["on","off"],"description":"Verify-time check opt-out: 'off' skips the space check for this task (docs-only, discussion). Default 'on' when the space configures checks."},` +
			`"parent_task":{"type":"integer","description":"Optional parent task id for a subtask."}` +
			`},"required":["title","assignee"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Title      string  `json:"title"`
				Spec       string  `json:"spec"`
				Assignee   string  `json:"assignee"`
				DependsOn  []int64 `json:"depends_on"`
				Verify     string  `json:"verify"`
				Check      string  `json:"check"`
				ParentTask *int64  `json:"parent_task"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("task_create: invalid input: %v", err), nil
			}
			switch in.Check {
			case "", "on", "off":
			default:
				return errf(`task_create: check must be "on" or "off" (got %q)`, in.Check), nil
			}
			// verify:"checks" is only meaningful where a check can actually
			// run — fail at create time, not deep inside the first task_done.
			if in.Verify == store.VerifyChecks {
				if mc.Space == nil || !mc.Space.ChecksConfigured() {
					return errf(`task_create: verify:"checks" needs settings.verify_checks configured on this space — use "leader" or "auto", or ask the operator to add the check command to the manifest`), nil
				}
				if in.Check == "off" {
					return errf(`task_create: verify:"checks" with check:"off" is contradictory — the policy gates on the check it would skip`), nil
				}
			}
			// Validate the assignee against the live roster: assigning to a
			// non-member would dead-letter the dispatch (task_assign notifies a
			// mailbox nobody drains). Empty assignee falls through to CreateTask's
			// ErrEmptyAssignee. Same guard as send_message (see rosterHas).
			if strings.TrimSpace(in.Assignee) != "" {
				if ok, names := rosterHas(mc.Space, in.Assignee); !ok {
					return errf("task_create: no swarm member named %q to assign. Valid assignees: %s. "+
						"Run list_members for exact names.", in.Assignee, strings.Join(names, ", ")), nil
				}
			}
			id, err := mc.Space.Store.CreateTask(store.Task{
				Title:        in.Title,
				Spec:         in.Spec,
				Assignee:     in.Assignee,
				CreatedBy:    mc.Name,
				DependsOn:    in.DependsOn,
				VerifyPolicy: in.Verify,
				CheckOff:     in.Check == "off",
				ParentID:     in.ParentTask,
			})
			if err != nil {
				switch {
				case errors.Is(err, store.ErrEmptyAssignee):
					return errf("task_create: an assignee is required (push model)"), nil
				case errors.Is(err, store.ErrDepNotFound):
					return errf("task_create: %v — depends_on may only reference already-created task ids", err), nil
				case errors.Is(err, store.ErrBadVerifyPolicy):
					return errf("task_create: %v", err), nil
				}
				return errf("task_create: %v", err), nil
			}
			if len(in.DependsOn) == 0 {
				return okf("Created task #%d %q assigned to %s (pending). Use task_assign to start it.", id, in.Title, in.Assignee), nil
			}
			if unmet, err := mc.Space.Store.UnmetDeps(id); err == nil && len(unmet) > 0 {
				return okf("Created task #%d %q assigned to %s — blocked on %s. The engine auto-dispatches it "+
					"when every dependency completes; do not task_assign it.", id, in.Title, in.Assignee, idList(unmet)), nil
			}
			// Every dependency is already complete: the task is born ready and the
			// engine starts it in this same tool call.
			return okf("Created task #%d %q assigned to %s — dependencies already complete.%s",
				id, in.Title, in.Assignee, dispatchNote(mc.Space)), nil
		},
	}
}

// newTaskAssign moves a task to running and wakes the assignee with a message.
// Works from pending (initial dispatch) and suspended (resume).
func newTaskAssign(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskAssign,
		desc: "Dispatch a task: set it to 'running' and notify the assignee so they start work. " +
			"Use this to kick off a pending task or to resume a suspended one. Only the Leader assigns.",
		schema: `{"type":"object","properties":{` +
			`"task_id":{"type":"integer","description":"Id of the task to assign/start."}` +
			`},"required":["task_id"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				TaskID int64 `json:"task_id"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("task_assign: invalid input: %v", err), nil
			}
			t, err := mc.Space.Store.GetTask(in.TaskID)
			if err != nil {
				return transitionError("task_assign", err), nil
			}
			if t.Status == store.StatusBlocked {
				unmet, _ := mc.Space.Store.UnmetDeps(in.TaskID)
				return errf("task_assign: task #%d is blocked by %s — the engine auto-dispatches it when they "+
					"complete. If a dependency was abandoned, force-unblock with task_update_status "+
					`{"task_id":%d,"status":"pending"} and the engine takes it from there.`,
					in.TaskID, idList(unmet), in.TaskID), nil
			}
			if err := mc.Space.Store.TransitionTask(in.TaskID, store.StatusRunning, memberActor(mc), ""); err != nil {
				return transitionError("task_assign", err), nil
			}
			// Wake the assignee (the task wake source = a message, §5.5/§7.1) —
			// the same mail body the DWF engine sends, minus the auto marker.
			if _, err := mc.Space.Bus.Send(swarm.AssignmentMail(t, mc.Name, false)); err != nil {
				return errf("task_assign: task #%d set running but notifying %s failed: %v", t.ID, t.Assignee, err), nil
			}
			return okf("Task #%d assigned to %s and set running.", t.ID, t.Assignee), nil
		},
	}
}

// newTaskUpdateStatus is the generic state-machine writer (suspend a running
// task, move it to verifying when the worker reports done, etc.).
func newTaskUpdateStatus(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskUpdateStatus,
		desc: "Move a task to a new status, enforcing the task state machine " +
			"(blocked→pending, pending→running→{suspended,verifying}, verifying→{completed,running}). " +
			"Use task_assign for →running and task_verify for verifying decisions; this is the general writer " +
			"for moves like running→suspended, or blocked→pending to force-unblock a task whose dependency " +
			"was abandoned (the engine then dispatches it). Only the Leader writes status.",
		schema: `{"type":"object","properties":{` +
			`"task_id":{"type":"integer","description":"Id of the task."},` +
			`"status":{"type":"string","enum":["pending","running","suspended","verifying","completed"],"description":"Target status."},` +
			`"note":{"type":"string","description":"Optional note recorded on the task."}` +
			`},"required":["task_id","status"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				TaskID int64  `json:"task_id"`
				Status string `json:"status"`
				Note   string `json:"note"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("task_update_status: invalid input: %v", err), nil
			}
			if err := mc.Space.Store.TransitionTask(in.TaskID, store.Status(in.Status), memberActor(mc), in.Note); err != nil {
				return transitionError("task_update_status", err), nil
			}
			var cascade string
			switch store.Status(in.Status) {
			case store.StatusCompleted:
				cascade = dispatchNote(mc.Space)
			case store.StatusVerifying:
				// Verifying-entry trigger (CHK): the space check runs against
				// this task; evidence lands on the row and in the mailbox.
				if mc.Space.EnqueueCheck(in.TaskID) {
					cascade = " Check queued — evidence will follow as mail."
				}
			}
			return okf("Task #%d → %s.%s", in.TaskID, in.Status, cascade), nil
		},
	}
}

// newTaskVerify approves (verifying→completed) or rejects (verifying→running) a
// task that a worker reported finished.
func newTaskVerify(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskVerify,
		desc: "Verify a task that is in 'verifying': approve to complete it, or reject to send it back to " +
			"'running' for rework (the note explains what to fix). Tip: spawn a general subagent first to " +
			"objectively check the work before approving. Only the Leader verifies.",
		schema: `{"type":"object","properties":{` +
			`"task_id":{"type":"integer","description":"Id of the task in 'verifying'."},` +
			`"approve":{"type":"boolean","description":"true to complete, false to reject for rework."},` +
			`"note":{"type":"string","description":"Verification note / rework instructions."}` +
			`},"required":["task_id","approve"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				TaskID  int64  `json:"task_id"`
				Approve bool   `json:"approve"`
				Note    string `json:"note"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("task_verify: invalid input: %v", err), nil
			}
			to := store.StatusRunning
			if in.Approve {
				to = store.StatusCompleted
			}
			if err := mc.Space.Store.TransitionTask(in.TaskID, to, memberActor(mc), in.Note); err != nil {
				return transitionError("task_verify", err), nil
			}
			if in.Approve {
				return okf("Task #%d verified and completed.%s", in.TaskID, dispatchNote(mc.Space)), nil
			}
			return okf("Task #%d rejected — back to running for rework.", in.TaskID), nil
		},
	}
}

// newTaskList is the Leader's ledger view, optionally filtered.
func newTaskList(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskList,
		desc: "List tasks in the ledger, optionally filtered by status and/or assignee. Returns one page " +
			"(default 20, max 50) plus the total count; completed tasks are newest-first — page back through " +
			"older ones with offset (the result tells you the next offset). Read-only.",
		schema: `{"type":"object","properties":{` +
			`"status":{"type":"string","enum":["pending","blocked","running","suspended","verifying","completed"],"description":"Optional status filter."},` +
			`"assignee":{"type":"string","description":"Optional assignee filter."},` +
			`"limit":{"type":"integer","description":"Max tasks to return (default 20, max 50)."},` +
			`"offset":{"type":"integer","description":"Skip this many matches for paging; the result reports the next offset when more remain."}` +
			`}}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Status   string `json:"status"`
				Assignee string `json:"assignee"`
				Limit    int    `json:"limit"`
				Offset   int    `json:"offset"`
			}
			_ = json.Unmarshal(input, &in) // all fields optional; ignore parse noise
			limit := in.Limit
			if limit <= 0 {
				limit = taskListDefaultLimit
			}
			if limit > taskListMaxLimit {
				limit = taskListMaxLimit
			}
			offset := in.Offset
			if offset < 0 {
				offset = 0
			}
			// Completed is the monotonic terminal pile — the leader almost always
			// wants the most recent, so default it newest-first; active states keep
			// the board's oldest-first reading order.
			newest := in.Status == string(store.StatusCompleted)
			match := store.TaskFilter{Status: store.Status(in.Status), Assignee: in.Assignee}
			page := match
			page.Limit, page.Offset, page.Newest = limit, offset, newest
			tasks, err := mc.Space.Store.ListTasks(page)
			if err != nil {
				return errf("task_list: %v", err), nil
			}
			total, err := mc.Space.Store.CountTasks(match)
			if err != nil {
				return errf("task_list: %v", err), nil
			}
			res := formatTasks("Tasks", tasks, offset, total, mc.Space.TaskStaleThreshold())
			// Surface waiting bottom-up work (RP-23) right where the leader
			// already looks — re-queryable, so it survives context compaction.
			if open, err := mc.Space.Store.CountProposals(store.ProposalOpen); err == nil && open > 0 {
				res.Content += fmt.Sprintf("\nOpen proposals: %d — review with proposal_list.\n", open)
			}
			return res, nil
		},
	}
}

// --- Worker reads ----------------------------------------------------------

// newMyTasks lists the calling worker's own tasks (assignee baked).
func newMyTasks(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolMyTasks,
		desc: "List the tasks assigned to you. A 'blocked' task is waiting on its dependencies — " +
			"the engine starts it for you when they complete; do nothing until then. Read-only.",
		schema: `{"type":"object","properties":{}}`,
		exec: func(_ context.Context, _ json.RawMessage) (pubtools.Result, error) {
			tasks, err := mc.Space.Store.ListTasks(store.TaskFilter{Assignee: mc.Name})
			if err != nil {
				return errf("my_tasks: %v", err), nil
			}
			// my_tasks stays unpaged (a worker's own set is small and called
			// on-demand, not polled — RP-6 scopes paging to the leader's task_list):
			// offset 0 + total == len prints the plain "Your tasks (N)" header.
			return formatTasks("Your tasks", tasks, 0, len(tasks), mc.Space.TaskStaleThreshold()), nil
		},
	}
}

// newTaskGet reads one task by id (read-only).
func newTaskGet(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskGet,
		desc: "Read one task by id: its title, spec, status, assignee, and notes. Read-only.",
		schema: `{"type":"object","properties":{` +
			`"task_id":{"type":"integer","description":"Id of the task to read."}` +
			`},"required":["task_id"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				TaskID int64 `json:"task_id"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("task_get: invalid input: %v", err), nil
			}
			t, err := mc.Space.Store.GetTask(in.TaskID)
			if err != nil {
				if errors.Is(err, store.ErrTaskNotFound) {
					return errf("task_get: task #%d not found", in.TaskID), nil
				}
				return errf("task_get: %v", err), nil
			}
			return pubtools.Result{Content: formatTask(t, mc.Space.TaskStaleThreshold()), Metadata: t}, nil
		},
	}
}

// --- Worker write (DWF §4: one edge, own task only) --------------------------

// newTaskDone is the worker's single ledger write: report your own task
// finished — result recorded + running→verifying in one store transaction
// (the writer matrix enforces ownership). What happens next is the task's
// verify policy: 'leader' mails the leader to inspect, so its wake starts at
// judgment instead of bookkeeping; 'auto' lets the engine complete the task
// immediately and dispatch whatever depended on it — zero leader wakes.
func newTaskDone(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskDone,
		desc: "Report YOUR assigned task finished: records the result and moves it running→verifying in one " +
			"step — use this instead of messaging the leader that you are done. On a verify:'leader' task the " +
			"leader is notified to inspect; on verify:'auto' the task completes instantly and dependent tasks " +
			"dispatch. Only the task's assignee may call this.",
		schema: `{"type":"object","properties":{` +
			`"task_id":{"type":"integer","description":"Id of your running task."},` +
			`"result":{"type":"string","description":"What you produced and where (files, commits, findings) — this is what verification judges."}` +
			`},"required":["task_id","result"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				TaskID int64  `json:"task_id"`
				Result string `json:"result"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("task_done: invalid input: %v", err), nil
			}
			t, err := mc.Space.Store.GetTask(in.TaskID)
			if err != nil {
				return transitionError("task_done", err), nil
			}
			if t.Assignee != mc.Name {
				return errf("task_done: task #%d is assigned to %s, not you", in.TaskID, t.Assignee), nil
			}
			if err := mc.Space.Store.CompleteWork(in.TaskID, memberActor(mc), in.Result); err != nil {
				return transitionError("task_done", err), nil
			}

			if t.VerifyPolicy == store.VerifyAuto {
				// Declared-mechanical: the engine settles it and cascades. No
				// leader mail — silence is the feature; the ledger, board, and
				// event log carry the record (DWF §5.3).
				sys := store.Actor{Name: swarm.EngineSender, Role: store.RoleSystem}
				if err := mc.Space.Store.TransitionTask(in.TaskID, store.StatusCompleted, sys, "auto-verified (policy: auto)"); err != nil {
					return okf("Task #%d done → verifying, but auto-complete failed (%v) — the leader will settle it.", in.TaskID, err), nil
				}
				return okf("Task #%d done and auto-completed (verify policy: auto).%s", in.TaskID, dispatchNote(mc.Space)), nil
			}

			// Verifying-entry trigger (CHK): queue the space check before the
			// leader mail composes, so evidence lands before (or with) the
			// leader's verify wake.
			checkQueued := mc.Space.EnqueueCheck(in.TaskID)

			if t.VerifyPolicy == store.VerifyChecks {
				if checkQueued {
					// The check gates settlement: green auto-completes with
					// zero leader wakes, red mails the leader the evidence.
					// No done-mail here — the check outcome IS the message.
					return okf("Task #%d done → verifying; the space check is running — green auto-completes the task (and dispatches dependents), red escalates to the leader with evidence.", in.TaskID), nil
				}
				// The policy asks for a check this space can no longer run
				// (verify_checks removed since create) — degrade to the
				// leader-mail flow below so the task is never stranded.
			}

			// Leader-verified: one durable mail starts the inspection.
			leaderName := "leader"
			if mc.Space.Roster != nil {
				leaderName = mc.Space.Roster.LeaderName()
			}
			refID := t.ID
			if _, err := mc.Space.Bus.Send(store.Message{
				Sender:    mc.Name,
				Recipient: leaderName,
				Subject:   fmt.Sprintf("Task #%d done by %s", t.ID, mc.Name),
				Body: fmt.Sprintf("Task #%d %q is done and in verifying.\n\nResult: %s\n\nInspect and task_verify {approve:true|false}.",
					t.ID, t.Title, in.Result),
				RefTask: &refID,
			}); err != nil {
				return okf("Task #%d done → verifying, but notifying the leader failed: %v (the stale-task watchdog is the backstop).", in.TaskID, err), nil
			}
			if checkQueued {
				return okf("Task #%d done → verifying; result recorded, the leader notified, and the space check is running (evidence mail follows).", in.TaskID), nil
			}
			return okf("Task #%d done → verifying; result recorded and the leader notified.", in.TaskID), nil
		},
	}
}
