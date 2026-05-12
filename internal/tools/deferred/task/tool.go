package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/johnny1110/evva/internal/tools"
)

// The task tools are stateful: all six (Create, Get, List, Update, Output,
// Stop) share one *Store per agent. The profile builder constructs one Store
// and threads it through each NewXxx constructor.
//
// Descriptions and schemas match the docs in
// docs/claude-tool/deferred-tools/task-process-management.md.
// Execute on each tool is currently a stub returning "not implemented".

// --- TaskCreate -----------------------------------------------------------

type CreateTool struct {
	store *Store
}

func NewCreate(s *Store) *CreateTool { return &CreateTool{store: s} }

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

func (t *CreateTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return notImplemented(tools.TASK_CREATE)
}

// --- TaskGet --------------------------------------------------------------

type GetTool struct {
	store *Store
}

func NewGet(s *Store) *GetTool { return &GetTool{store: s} }

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

func (t *GetTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return notImplemented(tools.TASK_GET)
}

// --- TaskList -------------------------------------------------------------

type ListTool struct {
	store *Store
}

func NewList(s *Store) *ListTool { return &ListTool{store: s} }

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

func (t *ListTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return notImplemented(tools.TASK_LIST)
}

// --- TaskUpdate -----------------------------------------------------------

type UpdateTool struct {
	store *Store
}

func NewUpdate(s *Store) *UpdateTool { return &UpdateTool{store: s} }

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
			"status":{"description":"New status for the task","anyOf":[{"type":"string","enum":["pending","in_progress","completed"]},{"type":"string","const":"deleted"}]},
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

func (t *UpdateTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return notImplemented(tools.TASK_UPDATE)
}

// --- TaskOutput -----------------------------------------------------------

type OutputTool struct {
	store *Store
}

func NewOutput(s *Store) *OutputTool { return &OutputTool{store: s} }

func (t *OutputTool) Name() string { return string(tools.TASK_OUTPUT) }

func (t *OutputTool) Description() string {
	return "Retrieve stdout/stderr output from a running or completed task " +
		"(background shell, agent, or remote session). " +
		"DEPRECATED for many cases — prefer reading the task's output file path directly with Read " +
		"for bash/remote_agent tasks. Local_agent tasks: use the Agent result, never Read the .output file " +
		"(transcript will overflow context)."
}

func (t *OutputTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["task_id","block","timeout"],
		"properties":{
			"task_id":{"type":"string","description":"The task ID to get output from"},
			"block":{"type":"boolean","default":true,"description":"Whether to wait for completion"},
			"timeout":{"type":"number","default":30000,"minimum":0,"maximum":600000,"description":"Max wait time in ms"}
		}
	}`)
}

func (t *OutputTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return notImplemented(tools.TASK_OUTPUT)
}

// --- TaskStop -------------------------------------------------------------

type StopTool struct {
	store *Store
}

func NewStop(s *Store) *StopTool { return &StopTool{store: s} }

func (t *StopTool) Name() string { return string(tools.TASK_STOP) }

func (t *StopTool) Description() string {
	return "Stop a running background task by ID. Returns success or failure status."
}

func (t *StopTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{
			"task_id":{"type":"string","description":"The ID of the background task to stop"},
			"shell_id":{"type":"string","description":"Deprecated: use task_id instead"}
		}
	}`)
}

func (t *StopTool) Execute(_ context.Context, _ json.RawMessage) (tools.Result, error) {
	return notImplemented(tools.TASK_STOP)
}

// --- shared helper --------------------------------------------------------

func notImplemented(name tools.ToolName) (tools.Result, error) {
	return tools.Result{
		IsError: true,
		Content: fmt.Sprintf("tool %q is not implemented yet", name),
	}, nil
}
