package shell

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/tools"
)

// TaskNames lists the companion task tools this package contributes.
// Composed into a profile's DeferredTools (the Main profile in
// internal/agent/profiles.go) — the model discovers them through
// tool_search after spawning its first background task.
func TaskNames() []tools.ToolName {
	return []tools.ToolName{tools.TASK_LIST, tools.TASK_OUTPUT, tools.TASK_STOP}
}

// --- task_list ----------------------------------------------------------

// TaskListTool enumerates every background task in the agent's
// BgTaskStore. Pure read; safe in any permission mode. Mirrors ref's
// TaskListTool.
type TaskListTool struct{ host BgTaskHost }

// NewTaskList constructs the tool. host may be nil — Execute reports a
// clear error in that case so the model gets a useful message instead
// of a nil panic.
func NewTaskList(host BgTaskHost) *TaskListTool { return &TaskListTool{host: host} }

func (t *TaskListTool) Name() string { return string(tools.TASK_LIST) }

func (t *TaskListTool) Description() string {
	return "List every background task started by `bash run_in_background:true` in this session. " +
		"Returns each task's id, status (running/completed/failed/killed), command, started-at, " +
		"and exit code when terminal. Use this to discover tasks you spawned earlier in the session, " +
		"or to confirm whether a task you fired-and-forgot has finished. " +
		"Pairs with task_output (read captured stdout/stderr) and task_stop (terminate a runner)."
}

func (t *TaskListTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
}

func (t *TaskListTool) Execute(_ context.Context, logger *slog.Logger, _ json.RawMessage) (tools.Result, error) {
	if t.host == nil || t.host.BgTaskStore() == nil {
		return tools.Result{IsError: true, Content: "task_list: no background-task host available"}, nil
	}
	snaps := t.host.BgTaskStore().Snapshot()
	if len(snaps) == 0 {
		return tools.Result{Content: "no background tasks"}, nil
	}
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].StartedAt.Before(snaps[j].StartedAt) })
	var b strings.Builder
	fmt.Fprintf(&b, "%d background task(s):\n", len(snaps))
	for _, s := range snaps {
		exit := ""
		if s.Status != BgRunning {
			exit = fmt.Sprintf(" exit=%d", s.ExitCode)
		}
		desc := s.Description
		if desc == "" {
			desc = truncate(s.Command, 80)
		}
		fmt.Fprintf(&b, "- %s [%s]%s started=%s | %s\n",
			s.ID, s.Status, exit, s.StartedAt.Format(time.RFC3339), desc)
	}
	logger.Debug("task_list.ok", "count", len(snaps))
	return tools.Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// --- task_output --------------------------------------------------------

// TaskOutputTool returns the captured stdout+stderr of one task. Works
// for running and terminal tasks (running tasks return whatever has
// been buffered so far at snapshot time — Phase 16's store captures
// output only at Complete time, so running tasks read empty).
type TaskOutputTool struct{ host BgTaskHost }

func NewTaskOutput(host BgTaskHost) *TaskOutputTool { return &TaskOutputTool{host: host} }

func (t *TaskOutputTool) Name() string { return string(tools.TASK_OUTPUT) }

func (t *TaskOutputTool) Description() string {
	return "Return the captured stdout+stderr of one background task. " +
		"Use after task_list to identify the task id you want to inspect. " +
		"Output for a still-running task may be empty; output for a completed task " +
		"is the full capture (capped at ~64 KiB tail with a truncation header). " +
		"Use the optional `tail` parameter to limit the result to the last N lines."
}

func (t *TaskOutputTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["task_id"],
		"properties":{
			"task_id":{"type":"string","description":"The task id returned by bash run_in_background:true."},
			"tail":{"type":"number","minimum":1,"description":"Return only the last N lines."}
		}
	}`)
}

type taskOutputInput struct {
	TaskID string `json:"task_id"`
	Tail   *int   `json:"tail"`
}

func (t *TaskOutputTool) Execute(_ context.Context, logger *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	if t.host == nil || t.host.BgTaskStore() == nil {
		return tools.Result{IsError: true, Content: "task_output: no background-task host available"}, nil
	}
	var in taskOutputInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_output: decode: %v", err)}, nil
	}
	id := strings.TrimSpace(in.TaskID)
	if id == "" {
		return tools.Result{IsError: true, Content: "task_output: task_id is required"}, nil
	}
	snap, ok := t.host.BgTaskStore().Get(id)
	if !ok {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_output: %q not found", id)}, nil
	}
	output := snap.Output
	if in.Tail != nil && *in.Tail > 0 {
		output = tailLines(output, *in.Tail)
	}
	header := fmt.Sprintf("task %s [%s]", snap.ID, snap.Status)
	if snap.Status != BgRunning {
		header = fmt.Sprintf("%s exit=%d", header, snap.ExitCode)
	}
	logger.Debug("task_output.ok", "id", id, "bytes", len(output))
	if output == "" {
		return tools.Result{Content: fmt.Sprintf("%s (no output yet)", header)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("%s\n---\n%s", header, output)}, nil
}

// --- task_stop ----------------------------------------------------------

// TaskStopTool terminates a running task. Idempotent on tasks that
// have already finished (returns a no-op message). The store's Stop
// method cancels the task's per-process ctx; the bg goroutine then
// calls Complete with Status=BgKilled and the agent's signal pump
// delivers the killed snapshot like any other terminal result.
type TaskStopTool struct{ host BgTaskHost }

func NewTaskStop(host BgTaskHost) *TaskStopTool { return &TaskStopTool{host: host} }

func (t *TaskStopTool) Name() string { return string(tools.TASK_STOP) }

func (t *TaskStopTool) Description() string {
	return "Terminate a running background task. The task transitions to status=killed and " +
		"its captured output up to that point is preserved. No-op on tasks that have already " +
		"completed/failed/killed. Use after task_list to identify the task id."
}

func (t *TaskStopTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["task_id"],
		"properties":{
			"task_id":{"type":"string","description":"The task id returned by bash run_in_background:true."}
		}
	}`)
}

type taskStopInput struct {
	TaskID string `json:"task_id"`
}

func (t *TaskStopTool) Execute(_ context.Context, logger *slog.Logger, raw json.RawMessage) (tools.Result, error) {
	if t.host == nil || t.host.BgTaskStore() == nil {
		return tools.Result{IsError: true, Content: "task_stop: no background-task host available"}, nil
	}
	var in taskStopInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_stop: decode: %v", err)}, nil
	}
	id := strings.TrimSpace(in.TaskID)
	if id == "" {
		return tools.Result{IsError: true, Content: "task_stop: task_id is required"}, nil
	}
	snap, ok := t.host.BgTaskStore().Stop(id)
	if !ok {
		// either unknown or already terminal — disambiguate for the model
		if snap.ID == "" {
			return tools.Result{IsError: true, Content: fmt.Sprintf("task_stop: %q not found", id)}, nil
		}
		return tools.Result{Content: fmt.Sprintf("task_stop: %s already %s (no-op)", id, snap.Status)}, nil
	}
	logger.Info("task_stop.ok", "id", id)
	return tools.Result{Content: fmt.Sprintf("task_stop: %s terminating; you will receive a killed notification when the process exits", id)}, nil
}

// tailLines returns the last n lines of s, joined by "\n". When s has
// fewer than n lines, returns s unchanged.
func tailLines(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// truncate trims s to n bytes with an ellipsis when it overflows.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
