package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/tools"
)

// Names lists every tool name this package contributes, for profile
// composition alongside the other package Names() helpers.
func Names() []tools.ToolName {
	return []tools.ToolName{
		tools.WF_TASK_CREATE, tools.WF_TASK_UPDATE, tools.WF_TASK_VERIFY,
		tools.WF_TASK_LIST, tools.WF_TASK_GET,
	}
}

// Dispatcher is the engine seam: after a readiness-changing mutation the
// tools poke it so ready tasks dispatch on the same tool call that made
// them ready. Late-bound through a lookup because the engine wires up
// after the toolset is built; a nil Dispatcher (tests, embedders with
// their own loop) leaves the board fully functional minus auto-dispatch.
type Dispatcher interface {
	Sweep()
}

// DispatcherLookup resolves the current Dispatcher, or nil.
type DispatcherLookup func() Dispatcher

func sweep(lookup DispatcherLookup) {
	if lookup == nil {
		return
	}
	if d := lookup(); d != nil {
		d.Sweep()
	}
}

// AgentTypesLookup resolves the live subagent-type list for worker
// validation at create time, or nil when unknown (validation then defers
// to dispatch).
type AgentTypesLookup func() []string

func errResult(format string, a ...any) (tools.Result, error) {
	return tools.Result{IsError: true, Content: fmt.Sprintf(format, a...)}, nil
}

// ---- wf_task_create ----

type CreateTool struct {
	store      *Store
	dispatch   DispatcherLookup
	agentTypes AgentTypesLookup
}

func NewCreate(s *Store, dispatch DispatcherLookup, agentTypes AgentTypesLookup) *CreateTool {
	return &CreateTool{store: s, dispatch: dispatch, agentTypes: agentTypes}
}

func (t *CreateTool) Name() string        { return string(tools.WF_TASK_CREATE) }
func (t *CreateTool) Description() string { return createDescription }

func (t *CreateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["subject"],
		"properties":{
			"subject":{"type":"string","minLength":1,"description":"Brief, actionable title in imperative form (e.g., \"Implement the /health endpoint\")"},
			"description":{"type":"string","description":"What needs to be done. For a worker task this IS the worker's briefing — write it so another agent can execute it without your conversation context (goals, files, constraints, how to verify)."},
			"active_form":{"type":"string","description":"Present continuous form shown in the board spinner while running (e.g., \"Implementing /health endpoint\")"},
			"depends_on":{"type":"array","items":{"type":"string"},"description":"IDs of existing tasks that must complete first (AND-join). Edges are immutable after creation — extend the graph by creating more tasks."},
			"verify":{"type":"string","enum":["leader","auto"],"description":"Verification policy. \"leader\" (default): the worker's result comes back to you for judgment before the task completes. \"auto\": the engine completes it mechanically and cascades dependents without waking you — reserve for mechanical, low-blast-radius steps."},
			"worker":{
				"type":"object",
				"additionalProperties":false,
				"required":["agent_type"],
				"description":"Spawn spec making this an engine-managed task: the engine dispatches an ephemeral subagent for it the moment it is unblocked. Omit for a self-task you will do yourself.",
				"properties":{
					"agent_type":{"type":"string","description":"Subagent kind to dispatch (as the agent tool's subagent_type, e.g. \"explore\", \"general-purpose\")"},
					"isolation":{"type":"string","enum":["worktree"],"description":"Run the worker in an isolated git worktree. Use for file-writing work that runs in parallel with other workers."},
					"level":{"type":"integer","minimum":1,"maximum":2,"description":"Model capability tier (1 = general, 2 = thinking). Omit for the default."}
				}
			}
		}
	}`)
}

type createInput struct {
	Subject     string   `json:"subject"`
	Description string   `json:"description"`
	ActiveForm  string   `json:"active_form"`
	DependsOn   []string `json:"depends_on"`
	Verify      Verify   `json:"verify"`
	Worker      *struct {
		AgentType string `json:"agent_type"`
		Isolation string `json:"isolation"`
		Level     int    `json:"level"`
	} `json:"worker"`
}

func (t *CreateTool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in createInput
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult("wf_task_create: decode: %v", err)
	}
	nt := Task{
		Subject:     in.Subject,
		Description: in.Description,
		ActiveForm:  in.ActiveForm,
		DependsOn:   in.DependsOn,
		Verify:      in.Verify,
	}
	if in.Worker != nil {
		if in.Worker.Isolation != "" && in.Worker.Isolation != "worktree" {
			return errResult("wf_task_create: worker.isolation must be \"worktree\" or omitted, got %q", in.Worker.Isolation)
		}
		if t.agentTypes != nil {
			if known := t.agentTypes(); len(known) > 0 && !slices.Contains(known, in.Worker.AgentType) {
				return errResult("wf_task_create: unknown worker.agent_type %q (available: %s)",
					in.Worker.AgentType, strings.Join(known, ", "))
			}
		}
		nt.Worker = &WorkerSpec{
			AgentType: in.Worker.AgentType,
			Isolation: in.Worker.Isolation,
			Level:     in.Worker.Level,
		}
	}
	created, err := t.store.Create(nt)
	if err != nil {
		return errResult("wf_task_create: %v", err)
	}
	logger.Debug("workflow.create", "id", created.ID, "status", created.Status, "managed", created.EngineManaged())

	var b strings.Builder
	fmt.Fprintf(&b, "created task #%s [%s] %s", created.ID, created.Status, created.Subject)
	switch {
	case created.Status == StatusBlocked:
		fmt.Fprintf(&b, "\nwaiting on: #%s", strings.Join(t.store.UnmetDeps(created.ID), ", #"))
		if created.EngineManaged() {
			b.WriteString(" — the engine dispatches it when they complete")
		}
	case created.EngineManaged():
		b.WriteString("\nready — the engine is dispatching a worker for it now")
	}
	if created.EngineManaged() {
		sweep(t.dispatch)
	}
	return tools.Result{Content: b.String()}, nil
}

// ---- wf_task_update ----

type UpdateTool struct {
	store    *Store
	dispatch DispatcherLookup
}

func NewUpdate(s *Store, dispatch DispatcherLookup) *UpdateTool {
	return &UpdateTool{store: s, dispatch: dispatch}
}

func (t *UpdateTool) Name() string        { return string(tools.WF_TASK_UPDATE) }
func (t *UpdateTool) Description() string { return updateDescription }

func (t *UpdateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["task_id"],
		"properties":{
			"task_id":{"type":"string","minLength":1,"description":"The task to update"},
			"subject":{"type":"string","description":"New title (imperative form)"},
			"description":{"type":"string","description":"New description. Update this BEFORE rejecting a worker task so the rework instruction reaches the next worker."},
			"active_form":{"type":"string","description":"New spinner text"},
			"status":{"type":"string","enum":["pending","running","completed","deleted"],"description":"New status. Self-tasks walk pending → running → completed. \"pending\" on a blocked task is the force-unblock override. \"deleted\" permanently removes a task created in error (refused while running or depended-on). Worker tasks are otherwise engine-driven — verify them with wf_task_verify instead."},
			"note":{"type":"string","description":"Audit note appended to the task's comments (why the change was made)"}
		}
	}`)
}

type updateInput struct {
	TaskID      string  `json:"task_id"`
	Subject     *string `json:"subject"`
	Description *string `json:"description"`
	ActiveForm  *string `json:"active_form"`
	Status      string  `json:"status"`
	Note        string  `json:"note"`
}

func (t *UpdateTool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in updateInput
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult("wf_task_update: decode: %v", err)
	}
	if in.Status == "deleted" {
		if err := t.store.Delete(in.TaskID); err != nil {
			return errResult("wf_task_update: %v", err)
		}
		logger.Debug("workflow.delete", "id", in.TaskID)
		return tools.Result{Content: fmt.Sprintf("deleted task #%s", in.TaskID)}, nil
	}

	edited := false
	if in.Subject != nil || in.Description != nil || in.ActiveForm != nil || (in.Note != "" && in.Status == "") {
		patch := Patch{Subject: in.Subject, Description: in.Description, ActiveForm: in.ActiveForm}
		if in.Status == "" {
			patch.Note = in.Note
		}
		if _, err := t.store.Update(in.TaskID, patch); err != nil {
			return errResult("wf_task_update: %v", err)
		}
		edited = true
	}

	if in.Status == "" {
		if !edited {
			return errResult("wf_task_update: nothing to do — provide fields to edit, a status, or a note")
		}
		task, _ := t.store.Get(in.TaskID)
		return tools.Result{Content: fmt.Sprintf("updated task #%s [%s] %s", task.ID, task.Status, task.Subject)}, nil
	}

	to := Status(in.Status)
	updated, err := t.store.Transition(in.TaskID, to, ActorRoot, in.Note)
	if err != nil {
		return errResult("wf_task_update: %v", err)
	}
	logger.Debug("workflow.transition", "id", updated.ID, "to", updated.Status)

	var b strings.Builder
	fmt.Fprintf(&b, "task #%s → %s", updated.ID, updated.Status)
	if to == StatusCompleted {
		reportUnblocked(&b, t.store, updated.ID)
	}
	// A force-unblock or a completion can make engine-managed tasks ready.
	if to == StatusCompleted || to == StatusPending {
		sweep(t.dispatch)
	}
	return tools.Result{Content: b.String()}, nil
}

// reportUnblocked appends the freshly-unblocked dependents of id (they
// flipped to pending inside the completing transition).
func reportUnblocked(b *strings.Builder, s *Store, id string) {
	var freed []string
	for _, dep := range s.Dependents(id) {
		if d, ok := s.Get(dep); ok && d.Status == StatusPending {
			freed = append(freed, "#"+d.ID)
		}
	}
	if len(freed) > 0 {
		fmt.Fprintf(b, "\nunblocked: %s", strings.Join(freed, ", "))
	}
}

// ---- wf_task_verify ----

type VerifyTool struct {
	store    *Store
	dispatch DispatcherLookup
}

func NewVerify(s *Store, dispatch DispatcherLookup) *VerifyTool {
	return &VerifyTool{store: s, dispatch: dispatch}
}

func (t *VerifyTool) Name() string        { return string(tools.WF_TASK_VERIFY) }
func (t *VerifyTool) Description() string { return verifyDescription }

func (t *VerifyTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["task_id","approve"],
		"properties":{
			"task_id":{"type":"string","minLength":1,"description":"The verifying task to judge"},
			"approve":{"type":"boolean","description":"true: the work is verified — task completes and dependents unblock. false: reject — the task re-queues and the engine dispatches a FRESH worker (update the description first so the rework instruction travels)."},
			"note":{"type":"string","description":"Verification verdict or rework instruction, appended to the task's audit comments"}
		}
	}`)
}

type verifyInput struct {
	TaskID  string `json:"task_id"`
	Approve *bool  `json:"approve"`
	Note    string `json:"note"`
}

func (t *VerifyTool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in verifyInput
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult("wf_task_verify: decode: %v", err)
	}
	if in.Approve == nil {
		return errResult("wf_task_verify: approve is required")
	}
	task, ok := t.store.Get(in.TaskID)
	if !ok {
		return errResult("wf_task_verify: task not found: %q", in.TaskID)
	}
	if task.Status != StatusVerifying {
		return errResult("wf_task_verify: task #%s is %s, not verifying — only a reported result can be judged", task.ID, task.Status)
	}

	if *in.Approve {
		updated, err := t.store.Transition(in.TaskID, StatusCompleted, ActorRoot, in.Note)
		if err != nil {
			return errResult("wf_task_verify: %v", err)
		}
		logger.Debug("workflow.verify", "id", updated.ID, "approve", true)
		var b strings.Builder
		fmt.Fprintf(&b, "task #%s verified → completed", updated.ID)
		reportUnblocked(&b, t.store, updated.ID)
		sweep(t.dispatch)
		return tools.Result{Content: b.String()}, nil
	}

	updated, err := t.store.Transition(in.TaskID, StatusPending, ActorRoot, in.Note)
	if err != nil {
		return errResult("wf_task_verify: %v", err)
	}
	logger.Debug("workflow.verify", "id", updated.ID, "approve", false)
	msg := fmt.Sprintf("task #%s rejected → re-queued", updated.ID)
	if updated.EngineManaged() {
		msg += " (the engine will dispatch a fresh worker — its briefing is the task description, so make sure the rework instruction is in it)"
		sweep(t.dispatch)
	}
	return tools.Result{Content: msg}, nil
}

// ---- wf_task_list ----

type ListTool struct {
	store *Store
}

func NewList(s *Store) *ListTool { return &ListTool{store: s} }

func (t *ListTool) Name() string        { return string(tools.WF_TASK_LIST) }
func (t *ListTool) Description() string { return listDescription }

func (t *ListTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"properties":{
			"status":{"type":"string","enum":["blocked","pending","running","verifying","completed"],"description":"Only list tasks in this status"}
		}
	}`)
}

func (t *ListTool) Execute(_ context.Context, _ *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in struct {
		Status Status `json:"status"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult("wf_task_list: decode: %v", err)
	}
	all := t.store.List()
	if len(all) == 0 {
		return tools.Result{Content: "the board is empty — plan a graph with wf_task_create"}, nil
	}

	var b strings.Builder
	counts := map[Status]int{}
	shown := 0
	for _, task := range all {
		counts[task.Status]++
		if in.Status != "" && task.Status != in.Status {
			continue
		}
		shown++
		fmt.Fprintf(&b, "#%s [%s] %s", task.ID, task.Status, task.Subject)
		var extras []string
		if task.EngineManaged() {
			w := task.Worker.AgentType
			if task.Worker.Isolation != "" {
				w += "+" + task.Worker.Isolation
			}
			extras = append(extras, "worker: "+w)
		}
		if task.Verify == VerifyAuto {
			extras = append(extras, "verify: auto")
		}
		if unmet := t.store.UnmetDeps(task.ID); len(unmet) > 0 {
			extras = append(extras, "waiting on: #"+strings.Join(unmet, ", #"))
		}
		if task.WorkerFailed {
			extras = append(extras, "WORKER FAILED")
		}
		if len(extras) > 0 {
			fmt.Fprintf(&b, "  (%s)", strings.Join(extras, "; "))
		}
		b.WriteString("\n")
	}
	if shown == 0 {
		fmt.Fprintf(&b, "no tasks in status %q\n", in.Status)
	}
	fmt.Fprintf(&b, "---\n%d total: %d blocked, %d pending, %d running, %d verifying, %d completed",
		len(all), counts[StatusBlocked], counts[StatusPending], counts[StatusRunning],
		counts[StatusVerifying], counts[StatusCompleted])
	return tools.Result{Content: b.String()}, nil
}

// ---- wf_task_get ----

type GetTool struct {
	store *Store
}

func NewGet(s *Store) *GetTool { return &GetTool{store: s} }

func (t *GetTool) Name() string        { return string(tools.WF_TASK_GET) }
func (t *GetTool) Description() string { return getDescription }

func (t *GetTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["task_id"],
		"properties":{
			"task_id":{"type":"string","minLength":1,"description":"The task to fetch"}
		}
	}`)
}

func (t *GetTool) Execute(_ context.Context, _ *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return errResult("wf_task_get: decode: %v", err)
	}
	task, ok := t.store.Get(in.TaskID)
	if !ok {
		return errResult("wf_task_get: task not found: %q", in.TaskID)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "#%s [%s] %s\n", task.ID, task.Status, task.Subject)
	fmt.Fprintf(&b, "verify: %s", task.Verify)
	if task.EngineManaged() {
		fmt.Fprintf(&b, " | worker: %s", task.Worker.AgentType)
		if task.Worker.Isolation != "" {
			fmt.Fprintf(&b, " (isolation: %s)", task.Worker.Isolation)
		}
		if task.Worker.Level > 0 {
			fmt.Fprintf(&b, " (level %d)", task.Worker.Level)
		}
	} else {
		b.WriteString(" | self-task")
	}
	b.WriteString("\n")
	if len(task.DependsOn) > 0 {
		fmt.Fprintf(&b, "depends on: #%s", strings.Join(task.DependsOn, ", #"))
		if unmet := t.store.UnmetDeps(task.ID); len(unmet) > 0 {
			fmt.Fprintf(&b, " (waiting on: #%s)", strings.Join(unmet, ", #"))
		}
		b.WriteString("\n")
	}
	if deps := t.store.Dependents(task.ID); len(deps) > 0 {
		fmt.Fprintf(&b, "blocks: #%s\n", strings.Join(deps, ", #"))
	}
	if task.Owner != "" {
		fmt.Fprintf(&b, "worker daemon: %s\n", task.Owner)
	}
	if task.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", task.Description)
	}
	if task.Result != "" {
		failed := ""
		if task.WorkerFailed {
			failed = " (WORKER FAILED)"
		}
		fmt.Fprintf(&b, "\n## Result%s\n%s\n", failed, task.Result)
	}
	if len(task.Comments) > 0 {
		b.WriteString("\n## Audit\n")
		for _, c := range task.Comments {
			fmt.Fprintf(&b, "- [%s] %s: %s\n", common.Stamp(c.At), c.By, c.Note)
		}
	}
	return tools.Result{Content: strings.TrimRight(b.String(), "\n")}, nil
}

// ---- descriptions ----
// Adapted from ref/src/tools/Task{Create,Update,List,Get}Tool/prompt.ts
// (the teammate variants) with the SDW divergences: workers are ephemeral
// engine-dispatched subagents, the root is the single judgment writer,
// and dependencies drive auto-dispatch (the DWF execution model).

const createDescription = `Create a task on the workflow board. The board is a dependency graph the engine executes: plan the WHOLE graph up front, then let the engine dispatch.

## When to Use This Tool

Use this tool proactively for:
- Complex multi-step work — 3 or more distinct steps, or anything worth delegating
- Parallelizable work — independent pieces the engine can run as concurrent workers
- Plan mode — capture the plan as a task graph
- After receiving new instructions — record the requirements as tasks

Skip it for a single, trivial step — just do that directly.

## Task Kinds

- **Worker task** (has "worker"): the engine spawns an ephemeral subagent for it the moment it is unblocked — you do NOT dispatch it yourself. The description is the worker's entire briefing: it has none of your conversation context, so include goals, file paths, constraints, and how to check its work. Results of completed dependencies are appended to its prompt automatically.
- **Self-task** (no "worker"): a step you do yourself, tracked on the same graph (walk it pending → running → completed with wf_task_update).

## Dependencies

"depends_on" lists tasks that must complete first (AND-join). A task with incomplete dependencies is born blocked and auto-dispatches when the last one completes. Edges may only reference EXISTING tasks and are immutable — extend the graph by creating more tasks, never by editing edges.

## Verify Policy

- "leader" (default): the worker's result comes back to you; judge it with wf_task_verify before it completes.
- "auto": the engine completes the task the moment its worker reports and cascades dependents — zero interruptions. Reserve for mechanical, low-blast-radius steps; NEVER combine with isolation:"worktree" (auto-completing unmerged work loses it).

## Tips

- Check wf_task_list first to avoid duplicates
- New tasks are created pending (or blocked); ownership is stamped by the engine at dispatch`

const updateDescription = `Update a task on the workflow board: edit its fields, walk a self-task's status, force-unblock, or delete.

## When to Use This Tool

- **Self-task progress**: mark your own tasks running when you start and completed when done (completion unblocks dependents and triggers dispatch).
- **Rework briefing**: before rejecting a worker task with wf_task_verify, update its description so the fresh worker gets the corrected instruction.
- **Force-unblock**: status "pending" on a blocked task overrides its remaining dependencies (with a note) — the escape hatch for an abandoned branch.
- **Delete**: status "deleted" permanently removes a task created in error. Refused while the task is running or while other tasks depend on it.

Worker tasks are otherwise engine-driven: the engine starts them, records their results, and you judge them with wf_task_verify — do not hand-walk their statuses.

- ONLY mark a self-task completed when FULLY accomplished; if blocked, create a task describing what must be resolved
- Never mark completed with failing tests, partial implementation, or unresolved errors`

const verifyDescription = `Judge a worker task whose result is awaiting verification (status "verifying").

The worker's result is on the task (wf_task_get). Approve when the work is verified — the task completes, dependents unblock, and the engine dispatches them. Reject to re-queue: the engine spawns a FRESH worker (the old one is gone), whose only briefing is the task description — update the description with the rework instruction BEFORE rejecting.

A task whose worker crashed arrives here with WORKER FAILED marked; it never auto-completes, whatever its verify policy. Approve only if the partial work is genuinely acceptable; otherwise reject (or delete the task and replan).

Verify against evidence, not the worker's claims: read what it changed, run the checks it says pass.`

const listDescription = `List the workflow board: every task with status, worker spec, verify policy, and what it is waiting on.

## When to Use This Tool

- At the start of a workflow turn, to see the live board state (running workers, results awaiting verification, blocked chains)
- After a worker report, before verifying — to see what else moved
- To check for duplicates before creating tasks
- To find stuck chains (blocked tasks whose dependencies are not progressing)

Use wf_task_get for one task's full detail including its result and audit trail.`

const getDescription = `Fetch one workflow task's full detail: description, dependencies (and what is still unmet), the worker's recorded result, and the audit trail (force-unblocks, verdicts, worker-lost resets).

## When to Use This Tool

- Before wf_task_verify — the result recorded here is what you are judging
- Before rejecting — to see the last attempt before writing the rework briefing
- To understand a chain: "depends on" and "blocks" show the task's place in the graph`
