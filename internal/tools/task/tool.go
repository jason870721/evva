package task

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/johnny1110/evva/internal/tools"
)

// The task tools are stateful: all six (Create, Get, List, Update, Output,
// Stop) share one *TaskGroup per agent. The profile builder constructs one
// TaskGroup and threads it through each NewXxx constructor.
//
// Output and Stop are reserved for background-process tasks (Bash
// run_in_background, future Monitor); until those land they return a clear
// "not implemented" error so the model can route around them.

// --- TaskCreate -----------------------------------------------------------

type CreateTool struct {
	store *TaskGroup
}

func NewCreate(s *TaskGroup) *CreateTool { return &CreateTool{store: s} }

func (t *CreateTool) Name() string { return string(tools.TASK_CREATE) }

func (t *CreateTool) Description() string {
	return "Create a structured task in the session's task list. " +
		"Use for complex multi-step work (3+ steps), plan mode, or when the user provides multiple tasks. " +
		"Skip for single trivial tasks. All tasks are created with status `pending`."
}

func (t *CreateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["subject","description"],
		"properties":{
			"subject":{"type":"string","description":"A brief title for the task (imperative form, e.g., \"Fix authentication bug\")"},
			"description":{"type":"string","description":"What needs to be done"},
			"activeForm":{"type":"string","description":"Present continuous form shown in spinner when in_progress (e.g., \"Running tests\")"},
			"metadata":{"type":"object","additionalProperties":{},"propertyNames":{"type":"string"},"description":"Arbitrary metadata to attach to the task"}
		}
	}`)
}

type createInput struct {
	Subject     string         `json:"subject"`
	Description string         `json:"description"`
	ActiveForm  string         `json:"activeForm"`
	Metadata    map[string]any `json:"metadata"`
}

func (t *CreateTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in createInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_create: decode: %v", err)}, nil
	}
	if strings.TrimSpace(in.Subject) == "" {
		return tools.Result{IsError: true, Content: "task_create: subject is required"}, nil
	}
	created := t.store.Create(Task{
		Subject:     in.Subject,
		Description: in.Description,
		ActiveForm:  in.ActiveForm,
		Metadata:    in.Metadata,
	})
	return tools.Result{Content: fmt.Sprintf("created task, ID: %s, status: pending, subject: %s", created.ID, created.Subject)}, nil
}

// --- TaskGet --------------------------------------------------------------

type GetTool struct {
	store *TaskGroup
}

func NewGet(s *TaskGroup) *GetTool { return &GetTool{store: s} }

func (t *GetTool) Name() string { return string(tools.TASK_GET) }

func (t *GetTool) Description() string {
	return "Retrieve full task details by ID — subject, description, status, blocks, blockedBy. " +
		"Verify blockedBy is empty before starting work on a task."
}

func (t *GetTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["taskId"],
		"properties":{
			"taskId":{"type":"string","description":"The ID of the task to retrieve"}
		}
	}`)
}

type getInput struct {
	TaskID string `json:"taskId"`
}

func (t *GetTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in getInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_get: decode: %v", err)}, nil
	}
	task, ok := t.store.Get(in.TaskID)
	if !ok {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_get: no task with id %q", in.TaskID)}, nil
	}
	out, _ := json.MarshalIndent(task, "", "  ")
	return tools.Result{Content: string(out)}, nil
}

// --- TaskList -------------------------------------------------------------

type ListTool struct {
	store *TaskGroup
}

func NewList(s *TaskGroup) *ListTool { return &ListTool{store: s} }

func (t *ListTool) Name() string { return string(tools.TASK_LIST) }

func (t *ListTool) Description() string {
	return "List all tasks in summary form (id, subject, status, owner, blockedBy). " +
		"Prefer working on tasks in ID order (lowest first). Use TaskGet for full details."
}

func (t *ListTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{}
	}`)
}

type taskSummary struct {
	ID        string   `json:"id"`
	Subject   string   `json:"subject"`
	Status    Status   `json:"status"`
	Owner     string   `json:"owner,omitempty"`
	BlockedBy []string `json:"blockedBy,omitempty"`
}

func (t *ListTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	all := t.store.List()
	out := make([]taskSummary, 0, len(all))
	for _, task := range all {
		out = append(out, taskSummary{
			ID:        task.ID,
			Subject:   task.Subject,
			Status:    task.Status,
			Owner:     task.Owner,
			BlockedBy: task.BlockedBy,
		})
	}
	// Sort by numeric suffix of ID for stable display ("t1", "t2", ..., "t10").
	sort.Slice(out, func(i, j int) bool { return idLess(out[i].ID, out[j].ID) })
	body, _ := json.MarshalIndent(out, "", "  ")
	return tools.Result{Content: string(body)}, nil
}

// --- TaskUpdate -----------------------------------------------------------

type UpdateTool struct {
	store *TaskGroup
}

func NewUpdate(s *TaskGroup) *UpdateTool { return &UpdateTool{store: s} }

func (t *UpdateTool) Name() string { return string(tools.TASK_UPDATE) }

func (t *UpdateTool) Description() string {
	return "Update an existing task — status, subject, description, owner, dependencies. " +
		"Status flow: pending → in_progress → completed (use `deleted` to permanently remove). " +
		"Only mark completed when work is fully done."
}

func (t *UpdateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["taskId"],
		"properties":{
			"taskId":{"type":"string","description":"The ID of the task to update"},
			"status":{"type":"string","enum":["pending","in_progress","completed","deleted"],"description":"New status for the task"},
			"subject":{"type":"string","description":"New subject for the task"},
			"description":{"type":"string","description":"New description for the task"},
			"activeForm":{"type":"string","description":"Present continuous form shown in spinner when in_progress"},
			"owner":{"type":"string","description":"New owner for the task"},
			"addBlocks":{"type":"array","items":{"type":"string"},"description":"Task IDs that this task blocks"},
			"addBlockedBy":{"type":"array","items":{"type":"string"},"description":"Task IDs that block this task"},
			"metadata":{"type":"object","additionalProperties":{},"propertyNames":{"type":"string"},"description":"Metadata keys to merge into the task. Set a key to null to delete it."}
		}
	}`)
}

type updateInput struct {
	TaskID       string         `json:"taskId"`
	Status       *Status        `json:"status,omitempty"`
	Subject      *string        `json:"subject,omitempty"`
	Description  *string        `json:"description,omitempty"`
	ActiveForm   *string        `json:"activeForm,omitempty"`
	Owner        *string        `json:"owner,omitempty"`
	AddBlocks    []string       `json:"addBlocks,omitempty"`
	AddBlockedBy []string       `json:"addBlockedBy,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

func (t *UpdateTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in updateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_update: decode: %v", err)}, nil
	}
	if in.TaskID == "" {
		return tools.Result{IsError: true, Content: "task_update: taskId is required"}, nil
	}

	patch := UpdatePatch{
		Status:       in.Status,
		Subject:      in.Subject,
		Description:  in.Description,
		ActiveForm:   in.ActiveForm,
		Owner:        in.Owner,
		AddBlocks:    in.AddBlocks,
		AddBlockedBy: in.AddBlockedBy,
		Metadata:     in.Metadata,
	}
	updated, ok, err := t.store.Update(in.TaskID, patch)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_update: %v", err)}, nil
	}
	if !ok {
		return tools.Result{IsError: true, Content: fmt.Sprintf("task_update: no task with id %q", in.TaskID)}, nil
	}
	return tools.Result{Content: fmt.Sprintf("updated task %s (status=%s): %s", updated.ID, updated.Status, updated.Subject)}, nil
}

// --- TaskOutput -----------------------------------------------------------

type OutputTool struct {
	store *TaskGroup
}

func NewOutput(s *TaskGroup) *OutputTool { return &OutputTool{store: s} }

func (t *OutputTool) Name() string { return string(tools.TASK_OUTPUT) }

func (t *OutputTool) Description() string {
	return "Retrieve stdout/stderr output from a background task (Bash run_in_background, Monitor). " +
		"Not yet implemented — background execution is reserved for a future phase."
}

func (t *OutputTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["task_id"],
		"properties":{
			"task_id":{"type":"string","description":"The task ID to get output from"},
			"block":{"type":"boolean","default":true,"description":"Whether to wait for completion"},
			"timeout":{"type":"number","default":30000,"minimum":0,"maximum":600000,"description":"Max wait time in ms"}
		}
	}`)
}

func (t *OutputTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return tools.Result{
		IsError: true,
		Content: "task_output: background tasks are not implemented yet",
	}, nil
}

// --- TaskStop -------------------------------------------------------------

type StopTool struct {
	store *TaskGroup
}

func NewStop(s *TaskGroup) *StopTool { return &StopTool{store: s} }

func (t *StopTool) Name() string { return string(tools.TASK_STOP) }

func (t *StopTool) Description() string {
	return "Stop a running background task by ID. " +
		"Not yet implemented — background execution is reserved for a future phase."
}

func (t *StopTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{
			"task_id":{"type":"string","description":"The ID of the background task to stop"}
		}
	}`)
}

func (t *StopTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return tools.Result{
		IsError: true,
		Content: "task_stop: background tasks are not implemented yet",
	}, nil
}

// --- helpers --------------------------------------------------------------

// idLess sorts task IDs by their numeric suffix ("t1" < "t2" < "t10").
// Falls back to lexicographic comparison if either ID doesn't match the
// "t<int>" shape.
func idLess(a, b string) bool {
	ai, aok := parseID(a)
	bi, bok := parseID(b)
	if aok && bok {
		return ai < bi
	}
	return a < b
}

func parseID(s string) (int, bool) {
	if len(s) < 2 || s[0] != 't' {
		return 0, false
	}
	var n int
	for i := 1; i < len(s); i++ {
		c := s[i]
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
