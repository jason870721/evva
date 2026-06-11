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

// leaderActor is the store Actor for a member's ledger writes. The store
// enforces the leader-only guard; passing the member's real role gives
// defense-in-depth (a Worker that somehow held a write tool is rejected).
func leaderActor(mc swarm.MemberContext) store.Actor {
	return store.Actor{Name: mc.Name, Role: store.Role(mc.Role)}
}

// transitionError maps a store rejection onto a model-visible tool error so the
// model can adjust rather than crash (AC#3 — surfaced, not panicked).
func transitionError(tool string, err error) pubtools.Result {
	switch {
	case errors.Is(err, store.ErrNotLeader):
		return errf("%s: only the Leader may write task status", tool)
	case errors.Is(err, store.ErrTaskNotFound):
		return errf("%s: task not found", tool)
	default: // ErrIllegalTransition and anything else
		return errf("%s: %v", tool, err)
	}
}

// formatTask renders one ledger row. staleAfter > 0 tags tasks parked in
// running/verifying beyond it with their age (RP-22) — the inline twin of the
// watchdog's reminder, so a leader re-reading the board sees the same signal.
func formatTask(t store.Task, staleAfter time.Duration) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#%d [%s] %s (assignee: %s)", t.ID, t.Status, t.Title, t.Assignee)
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

// newTaskCreate pushes a new task in the pending state (assignee required).
func newTaskCreate(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolTaskCreate,
		desc: "Create a new task and assign it to a worker (push model). The task starts in 'pending'; " +
			"use task_assign to dispatch and start it. Only the Leader creates tasks.",
		schema: `{"type":"object","properties":{` +
			`"title":{"type":"string","description":"Short task title."},` +
			`"spec":{"type":"string","description":"Full task specification / acceptance criteria."},` +
			`"assignee":{"type":"string","description":"Member name to own this task (see list_members)."},` +
			`"parent_task":{"type":"integer","description":"Optional parent task id for a subtask."}` +
			`},"required":["title","assignee"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Title      string `json:"title"`
				Spec       string `json:"spec"`
				Assignee   string `json:"assignee"`
				ParentTask *int64 `json:"parent_task"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("task_create: invalid input: %v", err), nil
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
				Title:     in.Title,
				Spec:      in.Spec,
				Assignee:  in.Assignee,
				CreatedBy: mc.Name,
				ParentID:  in.ParentTask,
			})
			if err != nil {
				if errors.Is(err, store.ErrEmptyAssignee) {
					return errf("task_create: an assignee is required (push model)"), nil
				}
				return errf("task_create: %v", err), nil
			}
			return okf("Created task #%d %q assigned to %s (pending). Use task_assign to start it.", id, in.Title, in.Assignee), nil
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
			if err := mc.Space.Store.TransitionTask(in.TaskID, store.StatusRunning, leaderActor(mc), ""); err != nil {
				return transitionError("task_assign", err), nil
			}
			// Wake the assignee (the task wake source = a message, §5.5/§7.1).
			refID := t.ID
			body := fmt.Sprintf("You are assigned task #%d: %s", t.ID, t.Title)
			if t.Spec != "" {
				body += "\n\n" + t.Spec
			}
			if _, err := mc.Space.Bus.Send(store.Message{
				Sender:    mc.Name,
				Recipient: t.Assignee,
				Subject:   fmt.Sprintf("Task #%d assigned", t.ID),
				Body:      body,
				RefTask:   &refID,
			}); err != nil {
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
			"(pending→running→{suspended,verifying}, verifying→{completed,running}). " +
			"Use task_assign for →running and task_verify for verifying decisions; this is the general writer " +
			"for moves like running→suspended or running→verifying. Only the Leader writes status.",
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
			if err := mc.Space.Store.TransitionTask(in.TaskID, store.Status(in.Status), leaderActor(mc), in.Note); err != nil {
				return transitionError("task_update_status", err), nil
			}
			return okf("Task #%d → %s.", in.TaskID, in.Status), nil
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
			if err := mc.Space.Store.TransitionTask(in.TaskID, to, leaderActor(mc), in.Note); err != nil {
				return transitionError("task_verify", err), nil
			}
			if in.Approve {
				return okf("Task #%d verified and completed.", in.TaskID), nil
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
			`"status":{"type":"string","enum":["pending","running","suspended","verifying","completed"],"description":"Optional status filter."},` +
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
			return formatTasks("Tasks", tasks, offset, total, mc.Space.TaskStaleThreshold()), nil
		},
	}
}

// --- Worker reads ----------------------------------------------------------

// newMyTasks lists the calling worker's own tasks (assignee baked).
func newMyTasks(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name:   toolMyTasks,
		desc:   "List the tasks assigned to you. Read-only.",
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
