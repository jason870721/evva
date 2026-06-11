package tools

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/skill"
)

// AC#1: role → tool set. Leader gets the writes; a Worker gets only the
// read-only task views plus the common send_message/list_members.
func TestToolNamesForRole(t *testing.T) {
	leader := toolNamesForRole(agentdef.RoleLeader)
	wantLeader := []string{toolSendMessage, toolListMembers, toolAlarmSet, toolAlarmClear, toolTaskCreate, toolTaskAssign, toolTaskUpdateStatus, toolTaskVerify, toolTaskList, toolScheduleSet, toolScheduleClear}
	if !reflect.DeepEqual(leader, wantLeader) {
		t.Fatalf("leader tools = %v\nwant %v", leader, wantLeader)
	}

	worker := toolNamesForRole(agentdef.RoleWorker)
	wantWorker := []string{toolSendMessage, toolListMembers, toolAlarmSet, toolAlarmClear, toolMyTasks, toolTaskGet}
	if !reflect.DeepEqual(worker, wantWorker) {
		t.Fatalf("worker tools = %v\nwant %v", worker, wantWorker)
	}

	for _, n := range worker {
		switch n {
		case toolTaskCreate, toolTaskAssign, toolTaskUpdateStatus, toolTaskVerify:
			t.Errorf("worker must not hold write tool %q", n)
		}
	}
}

func TestSetForReturnsOptionPerTool(t *testing.T) {
	if got := len(Set{}.For("leader", agentdef.RoleLeader, nil)); got != 11 {
		t.Errorf("leader options = %d, want 11", got)
	}
	if got := len(Set{}.For("w", agentdef.RoleWorker, nil)); got != 6 {
		t.Errorf("worker options = %d, want 6", got)
	}
}

// The swarm's coordination tools — including the Leader's task-ledger writes —
// auto-allow in a non-bypass mode so the leader runs its create→assign→verify
// loop without a human approval on every dispatch. The leader-only guard is the
// store's job (store.ErrNotLeader), not the permission gate; the real gated
// boundary is a Worker's file/shell writes, exercised elsewhere.
func TestPermissionClassification(t *testing.T) {
	decide := func(name string) permission.Behavior {
		return permission.Decide(permission.ToolCall{Name: name}, permission.ModeDefault, nil, permission.Hint{}, "", "").Behavior
	}
	autoAllow := []string{
		toolSendMessage, toolListMembers, toolTaskList, toolMyTasks, toolTaskGet,
		toolTaskCreate, toolTaskAssign, toolTaskUpdateStatus, toolTaskVerify,
		toolScheduleSet, toolScheduleClear,
		toolAlarmSet, toolAlarmClear,
	}
	for _, n := range autoAllow {
		if b := decide(n); b != permission.BehaviorAllow {
			t.Errorf("%s: behavior = %s, want allow (swarm coordination)", n, b)
		}
	}
	// A non-swarm write (no safelist entry) still asks — proves we widened the
	// swarm coordination set, not the gate itself.
	if b := decide("write"); b != permission.BehaviorAsk {
		t.Errorf("write: behavior = %s, want ask (worker file writes stay gated)", b)
	}
}

// The factory path: per-agent identity is read from Config, not a closure.
func TestFactoryReadsMemberContextFromConfig(t *testing.T) {
	sp := liteSpace(t)
	cfg := &config.Config{}
	swarm.BindMemberContext(cfg, leaderMC(sp))

	tool, err := factories[toolSendMessage](stubState{cfg: cfg})
	if err != nil || tool == nil {
		t.Fatalf("factory with context: tool=%v err=%v", tool, err)
	}
	if tool.Name() != toolSendMessage {
		t.Errorf("tool name = %q", tool.Name())
	}

	if _, err := factories[toolSendMessage](stubState{cfg: &config.Config{}}); err == nil {
		t.Error("factory without a bound member context should error")
	}
}

// --- send_message ----------------------------------------------------------

// AC#4: send_message persists a row with the baked sender.
func TestSendMessageBakesSender(t *testing.T) {
	sp := liteSpace(t)
	tool := newSendMessage(workerMC(sp, "worker-a"))

	res := exec(t, tool, `{"to":"leader","subject":"status","body":"done with the migration"}`)
	if res.IsError {
		t.Fatalf("send_message: %s", res.Content)
	}

	unread, _ := sp.Store.UnreadFor("leader")
	if len(unread) != 1 {
		t.Fatalf("leader unread = %d, want 1", len(unread))
	}
	m, _ := sp.Store.GetMessage(unread[0])
	if m.Sender != "worker-a" || m.Body != "done with the migration" {
		t.Fatalf("message = %+v, want sender=worker-a baked", m)
	}

	if r := exec(t, tool, `{"to":"leader"}`); !r.IsError {
		t.Error("send_message without body should error")
	}
}

// AC#4: to:"all" broadcasts to active members.
func TestSendMessageBroadcast(t *testing.T) {
	sp := liteSpace(t, "worker-a", "worker-b")
	tool := newSendMessage(leaderMC(sp))

	if res := exec(t, tool, `{"to":"all","body":"standup at 3pm"}`); res.IsError {
		t.Fatalf("broadcast: %s", res.Content)
	}
	for _, name := range []string{"worker-a", "worker-b"} {
		unread, _ := sp.Store.UnreadFor(name)
		if len(unread) != 1 {
			t.Fatalf("%s broadcast unread = %d, want 1", name, len(unread))
		}
	}
}

// An unknown recipient must surface a correctable error, not silently
// dead-letter. Regression for the role-vs-name slip: a worker addressing the
// leader as "lead" (or "leader") when the member has a different name — the
// message was durably stored to a mailbox nobody drained, so the leader never
// woke and never replied.
func TestSendMessageRejectsUnknownRecipient(t *testing.T) {
	sp := realSpace(t) // members: leader, worker-a, worker-b
	tool := newSendMessage(workerMC(sp, "worker-a"))

	res := exec(t, tool, `{"to":"lead","body":"task #2 done"}`)
	if !res.IsError {
		t.Fatalf("send to unknown recipient should error, got ok: %s", res.Content)
	}
	if !strings.Contains(res.Content, "leader") || !strings.Contains(res.Content, "worker-b") {
		t.Errorf("error should list valid member names, got: %s", res.Content)
	}
	if unread, _ := sp.Store.UnreadFor("lead"); len(unread) != 0 {
		t.Errorf("dead-letter: %d message(s) stored for unknown recipient %q", len(unread), "lead")
	}

	// The correct name still delivers.
	if res := exec(t, tool, `{"to":"leader","body":"task #2 done"}`); res.IsError {
		t.Fatalf("send to a valid member should succeed: %s", res.Content)
	}
	if unread, _ := sp.Store.UnreadFor("leader"); len(unread) != 1 {
		t.Fatalf("valid send: leader unread = %d, want 1", len(unread))
	}
}

// AC#5: list_members returns the live roster snapshot.
func TestListMembers(t *testing.T) {
	sp := realSpace(t)
	res := exec(t, newListMembers(leaderMC(sp)), `{}`)
	if res.IsError {
		t.Fatalf("list_members: %s", res.Content)
	}
	for _, name := range []string{"leader", "worker-a", "worker-b"} {
		if !strings.Contains(res.Content, name) {
			t.Errorf("list_members missing %q in: %s", name, res.Content)
		}
	}
}

// --- task ledger -----------------------------------------------------------

func TestTaskCreate(t *testing.T) {
	sp := liteSpace(t)
	tool := newTaskCreate(leaderMC(sp))

	if r := exec(t, tool, `{"title":"no assignee"}`); !r.IsError {
		t.Error("task_create without assignee should error")
	}

	res := exec(t, tool, `{"title":"build the API","spec":"REST + tests","assignee":"worker-a"}`)
	if res.IsError {
		t.Fatalf("task_create: %s", res.Content)
	}
	tasks, _ := sp.Store.ListTasks(store.TaskFilter{})
	if len(tasks) != 1 || tasks[0].Assignee != "worker-a" || tasks[0].Status != store.StatusPending || tasks[0].CreatedBy != "leader" {
		t.Fatalf("task = %+v, want pending/worker-a/created-by-leader", tasks[0])
	}
}

// task_create must reject an unknown assignee — assigning to a non-member would
// dead-letter the dispatch (same class as the send_message recipient bug).
func TestTaskCreateRejectsUnknownAssignee(t *testing.T) {
	sp := realSpace(t) // members: leader, worker-a, worker-b
	tool := newTaskCreate(leaderMC(sp))

	res := exec(t, tool, `{"title":"build it","assignee":"bilder"}`) // typo'd worker name
	if !res.IsError {
		t.Fatalf("task_create with unknown assignee should error, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "worker-a") || !strings.Contains(res.Content, "worker-b") {
		t.Errorf("error should list valid assignees, got: %s", res.Content)
	}
	if tasks, _ := sp.Store.ListTasks(store.TaskFilter{}); len(tasks) != 0 {
		t.Errorf("unknown-assignee task should not be created; got %d", len(tasks))
	}

	// A valid assignee still creates the task.
	if res := exec(t, tool, `{"title":"build it","assignee":"worker-a"}`); res.IsError {
		t.Fatalf("task_create with valid assignee should succeed: %s", res.Content)
	}
}

// AC#2: task_assign flips to running AND delivers a wake message to the assignee.
func TestTaskAssignRunsAndNotifies(t *testing.T) {
	sp := liteSpace(t, "worker-a")
	id, _ := sp.Store.CreateTask(store.Task{Title: "do x", Spec: "the spec", Assignee: "worker-a", CreatedBy: "leader"})

	res := exec(t, newTaskAssign(leaderMC(sp)), fmt.Sprintf(`{"task_id":%d}`, id))
	if res.IsError {
		t.Fatalf("task_assign: %s", res.Content)
	}

	tk, _ := sp.Store.GetTask(id)
	if tk.Status != store.StatusRunning {
		t.Fatalf("status = %s, want running", tk.Status)
	}
	unread, _ := sp.Store.UnreadFor("worker-a")
	if len(unread) != 1 {
		t.Fatalf("assignee wake messages = %d, want 1", len(unread))
	}
	m, _ := sp.Store.GetMessage(unread[0])
	if m.Sender != "leader" || m.RefTask == nil || *m.RefTask != id {
		t.Fatalf("wake message = %+v, want from leader ref_task=%d", m, id)
	}
}

// AC#3: a worker invoking a status-write tool is rejected by the store's
// leader-only guard, surfaced as a tool error (not a panic).
func TestWorkerStatusWriteRejected(t *testing.T) {
	sp := liteSpace(t)
	id, _ := sp.Store.CreateTask(store.Task{Title: "x", Assignee: "worker-a", CreatedBy: "leader"})

	res := exec(t, newTaskUpdateStatus(workerMC(sp, "worker-a")), fmt.Sprintf(`{"task_id":%d,"status":"running"}`, id))
	if !res.IsError {
		t.Fatal("worker status write should be rejected")
	}
	if !strings.Contains(res.Content, "Leader") {
		t.Errorf("rejection = %q, want a leader-only message", res.Content)
	}
	tk, _ := sp.Store.GetTask(id)
	if tk.Status != store.StatusPending {
		t.Fatalf("status moved to %s despite rejection", tk.Status)
	}
}

func TestTaskUpdateStatusIllegalTransition(t *testing.T) {
	sp := liteSpace(t)
	id, _ := sp.Store.CreateTask(store.Task{Title: "x", Assignee: "worker-a", CreatedBy: "leader"})
	// pending → verifying is illegal (must go through running).
	res := exec(t, newTaskUpdateStatus(leaderMC(sp)), fmt.Sprintf(`{"task_id":%d,"status":"verifying"}`, id))
	if !res.IsError || !strings.Contains(res.Content, "task_update_status") {
		t.Fatalf("want illegal-transition error, got %+v", res)
	}
}

func TestTaskVerifyApproveAndReject(t *testing.T) {
	sp := liteSpace(t)
	leaderAct := store.Actor{Name: "leader", Role: store.RoleLeader}

	toVerifying := func() int64 {
		id, _ := sp.Store.CreateTask(store.Task{Title: "x", Assignee: "worker-a", CreatedBy: "leader"})
		_ = sp.Store.TransitionTask(id, store.StatusRunning, leaderAct, "")
		_ = sp.Store.TransitionTask(id, store.StatusVerifying, leaderAct, "")
		return id
	}

	approve := toVerifying()
	if res := exec(t, newTaskVerify(leaderMC(sp)), fmt.Sprintf(`{"task_id":%d,"approve":true}`, approve)); res.IsError {
		t.Fatalf("approve: %s", res.Content)
	}
	if tk, _ := sp.Store.GetTask(approve); tk.Status != store.StatusCompleted {
		t.Fatalf("approved task status = %s, want completed", tk.Status)
	}

	reject := toVerifying()
	if res := exec(t, newTaskVerify(leaderMC(sp)), fmt.Sprintf(`{"task_id":%d,"approve":false,"note":"fix tests"}`, reject)); res.IsError {
		t.Fatalf("reject: %s", res.Content)
	}
	tk, _ := sp.Store.GetTask(reject)
	if tk.Status != store.StatusRunning || tk.VerifyNote != "fix tests" {
		t.Fatalf("rejected task = %+v, want running with note", tk)
	}
}

func TestTaskListMyTasksAndGet(t *testing.T) {
	sp := liteSpace(t)
	a, _ := sp.Store.CreateTask(store.Task{Title: "task-a", Assignee: "worker-a", CreatedBy: "leader"})
	_, _ = sp.Store.CreateTask(store.Task{Title: "task-b", Assignee: "worker-b", CreatedBy: "leader"})
	c, _ := sp.Store.CreateTask(store.Task{Title: "task-c", Assignee: "worker-a", CreatedBy: "leader"})

	// task_list (leader) sees all on the default page.
	all := exec(t, newTaskList(leaderMC(sp)), `{}`)
	if !strings.Contains(all.Content, "task-a") || !strings.Contains(all.Content, "task-b") {
		t.Errorf("task_list missing tasks: %s", all.Content)
	}

	// RP-6 paging: a small explicit limit returns one window + a next-offset hint.
	pg1 := exec(t, newTaskList(leaderMC(sp)), `{"limit":2}`)
	if !strings.Contains(pg1.Content, "showing 1-2 of 3") || !strings.Contains(pg1.Content, "offset=2") {
		t.Errorf("task_list page1 missing window/next-offset hint: %s", pg1.Content)
	}
	if tasks, ok := pg1.Metadata.([]store.Task); !ok || len(tasks) != 2 {
		t.Errorf("task_list page1 should carry 2 tasks in Metadata, got %#v", pg1.Metadata)
	}
	pg2 := exec(t, newTaskList(leaderMC(sp)), `{"limit":2,"offset":2}`)
	if !strings.Contains(pg2.Content, "showing 3-3 of 3") || strings.Contains(pg2.Content, "pass offset") {
		t.Errorf("task_list page2 wrong window or stray next-offset hint: %s", pg2.Content)
	}

	// Completed is newest-first: complete a then c → c (higher id) leads.
	completeTask(t, sp.Store, a)
	completeTask(t, sp.Store, c)
	done := exec(t, newTaskList(leaderMC(sp)), `{"status":"completed"}`)
	ic, ia := strings.Index(done.Content, "task-c"), strings.Index(done.Content, "task-a")
	if ic < 0 || ia < 0 || ic > ia {
		t.Errorf("completed should be newest-first (task-c before task-a): %s", done.Content)
	}

	// my_tasks (worker-a) sees only its own, and stays UNPAGED (plain header, no hints).
	mine := exec(t, newMyTasks(workerMC(sp, "worker-a")), `{}`)
	if !strings.Contains(mine.Content, "task-a") || strings.Contains(mine.Content, "task-b") {
		t.Errorf("my_tasks scoping wrong: %s", mine.Content)
	}
	if strings.Contains(mine.Content, "showing") || strings.Contains(mine.Content, "offset=") {
		t.Errorf("my_tasks must stay unpaged (no paging hints): %s", mine.Content)
	}

	// task_get reads one; unknown id errors.
	if got := exec(t, newTaskGet(workerMC(sp, "worker-a")), fmt.Sprintf(`{"task_id":%d}`, a)); !strings.Contains(got.Content, "task-a") {
		t.Errorf("task_get = %s", got.Content)
	}
	if miss := exec(t, newTaskGet(workerMC(sp, "worker-a")), `{"task_id":99999}`); !miss.IsError {
		t.Error("task_get of an unknown id should error")
	}
}

// completeTask drives a task pending→running→verifying→completed via leader writes.
func completeTask(t *testing.T, st *store.Store, id int64) {
	t.Helper()
	ld := store.Actor{Name: "leader", Role: store.RoleLeader}
	for _, s := range []store.Status{store.StatusRunning, store.StatusVerifying, store.StatusCompleted} {
		if err := st.TransitionTask(id, s, ld, ""); err != nil {
			t.Fatalf("complete task %d ->%s: %v", id, s, err)
		}
	}
}

// End-to-end: building a space WITH the swarm tool set exercises the real path —
// constructMember binds the MemberContext, agent.New's toolset.Build calls each
// factory, and the factory reads that context off the per-agent Config. A broken
// binding or factory (the crux of the design) would fail agent.New here.
func TestToolsAttachThroughNewSpace(t *testing.T) {
	loaded := []agentdef.Loaded{
		{Def: agent.AgentDefinition{Name: "leader", SystemPrompt: "You are leader.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleLeader},
		{Def: agent.AgentDefinition{Name: "worker-a", SystemPrompt: "You are worker-a.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleWorker},
	}
	m := agentdef.Manifest{Name: "team", Settings: agentdef.Settings{PermissionMode: "bypass", MaxIterations: 3}}
	sp, err := swarm.NewSpace("attach", m, loaded, Set{}, stubCfg(t))
	if err != nil {
		t.Fatalf("NewSpace with Set{} (member-context binding broken?): %v", err)
	}
	sp.Shutdown()
}

// --- schedule_set / schedule_clear (RP-7) ----------------------------------

// schedule_set/clear apply through the space→supervisor seam. NewSupervisor wires
// sp.super (no Start needed: with no run loops, SetSchedule just updates the
// declared schedule map, which is what ScheduleFor reads).
func TestScheduleSetAndClearTool(t *testing.T) {
	sp := realSpace(t)
	_ = swarm.NewSupervisor(sp) // wire sp.super

	set := newScheduleSet(leaderMC(sp))
	if r := exec(t, set, `{"member":"worker-a","cron":"*/30 * * * *","prompt":"patrol the API"}`); r.IsError {
		t.Fatalf("schedule_set: %s", r.Content)
	}
	got, ok := sp.ScheduleFor("worker-a")
	if !ok || got.Cron != "*/30 * * * *" || got.Prompt != "patrol the API" {
		t.Fatalf("ScheduleFor(worker-a) = %+v ok=%v, want the set schedule", got, ok)
	}

	clear := newScheduleClear(leaderMC(sp))
	if r := exec(t, clear, `{"member":"worker-a"}`); r.IsError {
		t.Fatalf("schedule_clear: %s", r.Content)
	}
	if _, ok := sp.ScheduleFor("worker-a"); ok {
		t.Error("ScheduleFor(worker-a) still set after schedule_clear")
	}
}

// The leader cannot schedule (or clear) ITSELF — that cadence is the operator's
// (RP-7 §3.3). The error points at the web, and nothing is written.
func TestScheduleToolSelfGuard(t *testing.T) {
	sp := realSpace(t)
	_ = swarm.NewSupervisor(sp)

	set := newScheduleSet(leaderMC(sp)) // leaderMC.Name == "leader"
	r := exec(t, set, `{"member":"leader","cron":"* * * * *","prompt":"x"}`)
	if !r.IsError || !strings.Contains(r.Content, "web") {
		t.Errorf("schedule_set on self = %+v, want an error pointing at the web", r)
	}
	if _, ok := sp.ScheduleFor("leader"); ok {
		t.Error("self schedule_set should not have written a schedule")
	}

	clear := newScheduleClear(leaderMC(sp))
	if r := exec(t, clear, `{"member":"leader"}`); !r.IsError {
		t.Errorf("schedule_clear on self = %+v, want an error", r)
	}
}

// schedule_set rejects an unknown member (correctable, with valid names) and an
// invalid cron (at call time, not at the first tick — AC#7); neither persists.
func TestScheduleSetRejectsUnknownMemberAndBadCron(t *testing.T) {
	sp := realSpace(t)
	_ = swarm.NewSupervisor(sp)
	set := newScheduleSet(leaderMC(sp))

	r := exec(t, set, `{"member":"ghost","cron":"* * * * *","prompt":"x"}`)
	if !r.IsError || !strings.Contains(r.Content, "worker-a") {
		t.Errorf("schedule_set unknown member = %+v, want correctable error listing members", r)
	}

	r = exec(t, set, `{"member":"worker-a","cron":"nonsense","prompt":"x"}`)
	if !r.IsError {
		t.Errorf("schedule_set bad cron = %+v, want a validation error", r)
	}
	if _, ok := sp.ScheduleFor("worker-a"); ok {
		t.Error("a rejected schedule_set must not write a schedule")
	}
}

// list_members surfaces each member's crontab inline (RP-7 §3.5) — pinned to a
// re-queryable place so a compacted leader never loses who it scheduled — and
// tags its origin (RP-20 §2.5): a runtime-set cadence reads "(runtime, set
// <date>)" so leader and operator can tell it from a manifest seed at a glance.
func TestListMembersShowsCrontab(t *testing.T) {
	sp := realSpace(t)
	_ = swarm.NewSupervisor(sp)
	if err := sp.SetMemberSchedule("worker-a", agentdef.Schedule{Cron: "*/15 * * * *", Prompt: "health check"}); err != nil {
		t.Fatalf("SetMemberSchedule: %v", err)
	}

	res := exec(t, newListMembers(leaderMC(sp)), `{}`)
	if res.IsError {
		t.Fatalf("list_members: %s", res.Content)
	}
	if !strings.Contains(res.Content, `⏰ cron "*/15 * * * *": "health check" (runtime, set `) {
		t.Errorf("list_members missing worker-a's runtime-tagged crontab line, got:\n%s", res.Content)
	}
}

// A manifest-seeded schedule is tagged "(manifest)" in list_members (RP-20).
func TestFormatScheduleOrigin(t *testing.T) {
	if got := formatScheduleOrigin(swarm.ScheduleOrigin{}); got != "(manifest)" {
		t.Errorf("manifest origin = %q", got)
	}
	if got := formatScheduleOrigin(swarm.ScheduleOrigin{Runtime: true}); got != "(runtime)" {
		t.Errorf("runtime origin without instant = %q", got)
	}
	got := formatScheduleOrigin(swarm.ScheduleOrigin{Runtime: true, SetAt: 1765429200000})
	if !strings.HasPrefix(got, "(runtime, set 20") || !strings.HasSuffix(got, ")") {
		t.Errorf("runtime origin = %q, want a (runtime, set YYYY-MM-DD) tag", got)
	}
}

func TestFmtTokens(t *testing.T) {
	cases := map[int]string{
		0:          "0",
		950:        "950",
		1_500:      "1.5k",
		12_340:     "12k",
		999_999:    "1000k",
		1_234_000:  "1.2M",
		12_345_678: "12M",
	}
	for in, want := range cases {
		if got := fmtTokens(in); got != want {
			t.Errorf("fmtTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

// RP-22: task_list tags tasks parked in running/verifying beyond the space's
// task_stale_threshold — the inline twin of the watchdog reminder.
func TestTaskListMarksStaleTasks(t *testing.T) {
	loaded := []agentdef.Loaded{
		{Def: agent.AgentDefinition{Name: "leader", SystemPrompt: "You are leader.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleLeader},
		{Def: agent.AgentDefinition{Name: "worker-a", SystemPrompt: "You are worker-a.", Model: stubModel}, Skills: skill.NewRegistry(), Role: agentdef.RoleWorker},
	}
	m := agentdef.Manifest{Name: "team", Settings: agentdef.Settings{
		PermissionMode: "bypass", MaxIterations: 5, TaskStaleThreshold: 10 * time.Millisecond,
	}}
	sp, err := swarm.NewSpace("t-stale", m, loaded, nil, stubCfg(t))
	if err != nil {
		t.Fatalf("NewSpace: %v", err)
	}
	t.Cleanup(sp.Shutdown)

	id, err := sp.Store.CreateTask(store.Task{Title: "slow work", Spec: "s", Assignee: "worker-a", CreatedBy: "leader"})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := sp.Store.TransitionTask(id, store.StatusRunning, store.Actor{Name: "leader", Role: store.RoleLeader}, ""); err != nil {
		t.Fatalf("TransitionTask: %v", err)
	}
	time.Sleep(25 * time.Millisecond)

	res := exec(t, newTaskList(leaderMC(sp)), `{}`)
	if res.IsError {
		t.Fatalf("task_list: %s", res.Content)
	}
	if !strings.Contains(res.Content, "⏳ stale") {
		t.Errorf("task_list missing the stale tag:\n%s", res.Content)
	}

	// A pending task — even an old one — carries no tag (only running/verifying age).
	if _, err := sp.Store.CreateTask(store.Task{Title: "queued", Spec: "s", Assignee: "worker-a", CreatedBy: "leader"}); err != nil {
		t.Fatalf("CreateTask #2: %v", err)
	}
	time.Sleep(25 * time.Millisecond)
	res = exec(t, newTaskList(leaderMC(sp)), `{"status":"pending"}`)
	if strings.Contains(res.Content, "⏳ stale") {
		t.Errorf("pending tasks must not be tagged:\n%s", res.Content)
	}
}
