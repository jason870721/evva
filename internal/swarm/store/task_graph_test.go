package store

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"
)

func asWorker(name string) Actor { return Actor{Name: name, Role: RoleWorker} }
func asSystem() Actor            { return Actor{Name: "engine", Role: RoleSystem} }

// seedGraphTask creates a task with the requested dependency shape and verify
// policy, then drives it to `target` via leader edges. deps: "" (none),
// "incomplete" (one pending dep — task born blocked), "complete" (one
// completed dep — task born pending, still engine-managed).
func seedGraphTask(t *testing.T, st *Store, target Status, deps, policy string) int64 {
	t.Helper()
	tk := Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "leader", VerifyPolicy: policy}
	switch deps {
	case "incomplete":
		tk.DependsOn = []int64{taskInState(t, st, StatusPending)}
	case "complete":
		tk.DependsOn = []int64{taskInState(t, st, StatusCompleted)}
	}
	id, err := st.CreateTask(tk)
	if err != nil {
		t.Fatalf("seedGraphTask create: %v", err)
	}

	got, err := st.GetTask(id)
	if err != nil {
		t.Fatalf("seedGraphTask get: %v", err)
	}
	steps := map[Status]map[Status][]Status{
		StatusBlocked: {
			StatusBlocked:   {},
			StatusPending:   {StatusPending},
			StatusRunning:   {StatusPending, StatusRunning},
			StatusVerifying: {StatusPending, StatusRunning, StatusVerifying},
		},
		StatusPending: {
			StatusPending:   {},
			StatusRunning:   {StatusRunning},
			StatusSuspended: {StatusRunning, StatusSuspended},
			StatusVerifying: {StatusRunning, StatusVerifying},
		},
	}
	path, ok := steps[got.Status][target]
	if !ok {
		t.Fatalf("seedGraphTask: no path %s -> %s", got.Status, target)
	}
	for _, step := range path {
		if err := st.TransitionTask(id, step, leader(), ""); err != nil {
			t.Fatalf("seedGraphTask: step ->%s: %v", step, err)
		}
	}
	return id
}

// TestWriterMatrix pins the DWF §4 writer matrix cell-by-cell for the two
// mechanical writers (the leader's all-edge power is TestTaskStateMachine's
// subject). A refused writer gets ErrNotLeader and the row is unmutated.
func TestWriterMatrix(t *testing.T) {
	cells := []struct {
		name     string
		from, to Status
		by       Actor
		deps     string
		policy   string
		want     bool
	}{
		// Worker: exactly one edge, own task only.
		{"worker_done_own_task", StatusRunning, StatusVerifying, asWorker("w"), "", "", true},
		{"worker_done_foreign_task", StatusRunning, StatusVerifying, asWorker("intruder"), "", "", false},
		{"worker_assign", StatusPending, StatusRunning, asWorker("w"), "", "", false},
		{"worker_force_unblock", StatusBlocked, StatusPending, asWorker("w"), "incomplete", "", false},
		{"worker_verify", StatusVerifying, StatusCompleted, asWorker("w"), "", "", false},
		{"worker_reject", StatusVerifying, StatusRunning, asWorker("w"), "", "", false},
		{"worker_suspend", StatusRunning, StatusSuspended, asWorker("w"), "", "", false},

		// System: three mechanical edges, each condition-gated.
		{"system_unblock", StatusBlocked, StatusPending, asSystem(), "incomplete", "", true},
		{"system_dispatch_dep_task", StatusPending, StatusRunning, asSystem(), "complete", "", true},
		{"system_dispatch_depless", StatusPending, StatusRunning, asSystem(), "", "", false},
		{"system_autoverify_auto_policy", StatusVerifying, StatusCompleted, asSystem(), "", VerifyAuto, true},
		{"system_autoverify_leader_policy", StatusVerifying, StatusCompleted, asSystem(), "", VerifyLeader, false},
		{"system_reject", StatusVerifying, StatusRunning, asSystem(), "", "", false},
		{"system_suspend", StatusRunning, StatusSuspended, asSystem(), "", "", false},
		{"system_resume", StatusSuspended, StatusRunning, asSystem(), "", "", false},

		// Leader on the new edges (the old ones are matrix-tested already).
		{"leader_force_unblock", StatusBlocked, StatusPending, leader(), "incomplete", "", true},
		{"leader_settles_auto_policy_too", StatusVerifying, StatusCompleted, leader(), "", VerifyAuto, true},
	}

	for _, c := range cells {
		t.Run(c.name, func(t *testing.T) {
			st := openTemp(t)
			var id int64
			if c.from == StatusSuspended {
				id = taskInState(t, st, StatusSuspended)
			} else {
				id = seedGraphTask(t, st, c.from, c.deps, c.policy)
			}

			err := st.TransitionTask(id, c.to, c.by, "")
			got, gerr := st.GetTask(id)
			if gerr != nil {
				t.Fatalf("GetTask: %v", gerr)
			}
			if c.want {
				if err != nil {
					t.Fatalf("%s->%s by %s(%s): %v, want allowed", c.from, c.to, c.by.Name, c.by.Role, err)
				}
				if got.Status != c.to {
					t.Fatalf("status = %s, want %s", got.Status, c.to)
				}
			} else {
				if !errors.Is(err, ErrNotLeader) {
					t.Fatalf("%s->%s by %s(%s): err = %v, want ErrNotLeader", c.from, c.to, c.by.Name, c.by.Role, err)
				}
				if got.Status != c.from {
					t.Fatalf("refused write mutated status to %s (want %s)", got.Status, c.from)
				}
			}
		})
	}
}

// TestCreateTaskGraph covers dep validation, birth states, dedupe, and the
// verify-policy gate.
func TestCreateTaskGraph(t *testing.T) {
	t.Run("missing_dep", func(t *testing.T) {
		st := openTemp(t)
		_, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "l", DependsOn: []int64{999}})
		if !errors.Is(err, ErrDepNotFound) {
			t.Fatalf("err = %v, want ErrDepNotFound", err)
		}
	})

	t.Run("incomplete_dep_born_blocked", func(t *testing.T) {
		st := openTemp(t)
		dep := taskInState(t, st, StatusRunning)
		id, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "l", DependsOn: []int64{dep}})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		got, _ := st.GetTask(id)
		if got.Status != StatusBlocked {
			t.Fatalf("status = %s, want blocked", got.Status)
		}
		if !got.EngineManaged() {
			t.Fatal("dep task should be engine-managed")
		}
	})

	t.Run("completed_dep_born_pending", func(t *testing.T) {
		st := openTemp(t)
		dep := taskInState(t, st, StatusCompleted)
		id, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "l", DependsOn: []int64{dep}})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		got, _ := st.GetTask(id)
		if got.Status != StatusPending {
			t.Fatalf("status = %s, want pending (dep already complete)", got.Status)
		}
		if !got.EngineManaged() {
			t.Fatal("dep task should stay engine-managed even when born pending")
		}
	})

	t.Run("mixed_deps_born_blocked", func(t *testing.T) {
		st := openTemp(t)
		done := taskInState(t, st, StatusCompleted)
		open := taskInState(t, st, StatusPending)
		id, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "l", DependsOn: []int64{done, open}})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		got, _ := st.GetTask(id)
		if got.Status != StatusBlocked {
			t.Fatalf("status = %s, want blocked (one dep open)", got.Status)
		}
	})

	t.Run("duplicate_deps_collapse", func(t *testing.T) {
		st := openTemp(t)
		dep := taskInState(t, st, StatusPending)
		id, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "l", DependsOn: []int64{dep, dep, dep}})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		got, _ := st.GetTask(id)
		if !reflect.DeepEqual(got.DependsOn, []int64{dep}) {
			t.Fatalf("DependsOn = %v, want [%d]", got.DependsOn, dep)
		}
	})

	t.Run("verify_policy", func(t *testing.T) {
		st := openTemp(t)
		if _, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "l", VerifyPolicy: "checks"}); !errors.Is(err, ErrBadVerifyPolicy) {
			t.Fatalf("unknown policy: err = %v, want ErrBadVerifyPolicy", err)
		}
		id, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "l"})
		if err != nil {
			t.Fatalf("CreateTask: %v", err)
		}
		got, _ := st.GetTask(id)
		if got.VerifyPolicy != VerifyLeader {
			t.Fatalf("default policy = %q, want %q", got.VerifyPolicy, VerifyLeader)
		}
	})
}

// TestCompleteWork covers the worker's single write: result + running ->
// verifying in one call, ownership-checked.
func TestCompleteWork(t *testing.T) {
	t.Run("assignee_completes", func(t *testing.T) {
		st := openTemp(t)
		id := taskInState(t, st, StatusRunning)
		if err := st.CompleteWork(id, asWorker("w"), "shipped in pkg/x"); err != nil {
			t.Fatalf("CompleteWork: %v", err)
		}
		got, _ := st.GetTask(id)
		if got.Status != StatusVerifying || got.Result != "shipped in pkg/x" {
			t.Fatalf("got status=%s result=%q, want verifying + result", got.Status, got.Result)
		}
	})

	t.Run("foreign_worker_refused", func(t *testing.T) {
		st := openTemp(t)
		id := taskInState(t, st, StatusRunning)
		if err := st.CompleteWork(id, asWorker("intruder"), "x"); !errors.Is(err, ErrNotLeader) {
			t.Fatalf("err = %v, want ErrNotLeader", err)
		}
		got, _ := st.GetTask(id)
		if got.Status != StatusRunning || got.Result != "" {
			t.Fatalf("refused CompleteWork mutated the row: %+v", got)
		}
	})

	t.Run("system_refused", func(t *testing.T) {
		st := openTemp(t)
		id := taskInState(t, st, StatusRunning)
		if err := st.CompleteWork(id, asSystem(), "x"); !errors.Is(err, ErrNotLeader) {
			t.Fatalf("err = %v, want ErrNotLeader", err)
		}
	})

	t.Run("leader_may_complete_work", func(t *testing.T) {
		st := openTemp(t)
		id := taskInState(t, st, StatusRunning)
		if err := st.CompleteWork(id, leader(), "done for w"); err != nil {
			t.Fatalf("CompleteWork by leader: %v", err)
		}
	})

	t.Run("not_running", func(t *testing.T) {
		st := openTemp(t)
		id := taskInState(t, st, StatusPending)
		if err := st.CompleteWork(id, asWorker("w"), "x"); !errors.Is(err, ErrIllegalTransition) {
			t.Fatalf("err = %v, want ErrIllegalTransition", err)
		}
	})

	t.Run("unknown_task", func(t *testing.T) {
		st := openTemp(t)
		if err := st.CompleteWork(99999, asWorker("w"), "x"); !errors.Is(err, ErrTaskNotFound) {
			t.Fatalf("err = %v, want ErrTaskNotFound", err)
		}
	})
}

// TestSweepDispatchable walks a chain end-to-end: completions make dependents
// sweepable exactly once (idempotent), force-unblocked and born-pending dep
// tasks dispatch too, and depless pending tasks are never touched.
func TestSweepDispatchable(t *testing.T) {
	st := openTemp(t)

	t1, err := st.CreateTask(Task{Title: "t1", Spec: "s", Assignee: "a", CreatedBy: "l"})
	if err != nil {
		t.Fatalf("t1: %v", err)
	}
	t2, err := st.CreateTask(Task{Title: "t2", Spec: "s", Assignee: "b", CreatedBy: "l", DependsOn: []int64{t1}})
	if err != nil {
		t.Fatalf("t2: %v", err)
	}
	t3, err := st.CreateTask(Task{Title: "t3", Spec: "s", Assignee: "c", CreatedBy: "l", DependsOn: []int64{t2}})
	if err != nil {
		t.Fatalf("t3: %v", err)
	}

	// Nothing ready: t1 is depless (leader-managed), t2/t3 have unmet deps.
	if ready, err := st.SweepDispatchable(); err != nil || len(ready) != 0 {
		t.Fatalf("initial sweep = %v, %v; want empty", ready, err)
	}

	// Complete t1 the manual way; t2 becomes sweepable.
	for _, step := range []Status{StatusRunning, StatusVerifying, StatusCompleted} {
		if err := st.TransitionTask(t1, step, leader(), ""); err != nil {
			t.Fatalf("drive t1 ->%s: %v", step, err)
		}
	}
	ready, err := st.SweepDispatchable()
	if err != nil || len(ready) != 1 || ready[0].ID != t2 {
		t.Fatalf("sweep after t1 done = %+v, %v; want [t2]", ready, err)
	}
	if ready[0].Status != StatusRunning || !reflect.DeepEqual(ready[0].DependsOn, []int64{t1}) {
		t.Fatalf("dispatched t2 = %+v, want running with deps [t1]", ready[0])
	}
	if got, _ := st.GetTask(t3); got.Status != StatusBlocked {
		t.Fatalf("t3 = %s, want still blocked", got.Status)
	}

	// Idempotent: the flip moved t2 out of the query's reach.
	if again, err := st.SweepDispatchable(); err != nil || len(again) != 0 {
		t.Fatalf("second sweep = %v, %v; want empty", again, err)
	}

	// task_done + leader verify on t2; t3 becomes sweepable.
	if err := st.CompleteWork(t2, asWorker("b"), "done"); err != nil {
		t.Fatalf("t2 done: %v", err)
	}
	if err := st.TransitionTask(t2, StatusCompleted, leader(), "lgtm"); err != nil {
		t.Fatalf("t2 verify: %v", err)
	}
	if ready, err := st.SweepDispatchable(); err != nil || len(ready) != 1 || ready[0].ID != t3 {
		t.Fatalf("sweep after t2 done = %+v, %v; want [t3]", ready, err)
	}

	// Force-unblock dispatches even with an unmet dep (leader abandoned it).
	t4 := taskInState(t, st, StatusPending)
	t5, err := st.CreateTask(Task{Title: "t5", Spec: "s", Assignee: "d", CreatedBy: "l", DependsOn: []int64{t4}})
	if err != nil {
		t.Fatalf("t5: %v", err)
	}
	if err := st.TransitionTask(t5, StatusPending, leader(), "t4 abandoned"); err != nil {
		t.Fatalf("force-unblock t5: %v", err)
	}
	ready, err = st.SweepDispatchable()
	if err != nil || len(ready) != 1 || ready[0].ID != t5 {
		t.Fatalf("sweep after force-unblock = %+v, %v; want [t5]", ready, err)
	}

	// Born pending with an already-complete dep: engine-managed, dispatches.
	done := taskInState(t, st, StatusCompleted)
	t6, err := st.CreateTask(Task{Title: "t6", Spec: "s", Assignee: "e", CreatedBy: "l", DependsOn: []int64{done}})
	if err != nil {
		t.Fatalf("t6: %v", err)
	}
	if ready, err := st.SweepDispatchable(); err != nil || len(ready) != 1 || ready[0].ID != t6 {
		t.Fatalf("sweep for born-pending t6 = %+v, %v; want [t6]", ready, err)
	}
}

// TestUnmetDeps: only incomplete dependencies are listed.
func TestUnmetDeps(t *testing.T) {
	st := openTemp(t)
	done := taskInState(t, st, StatusCompleted)
	open1 := taskInState(t, st, StatusPending)
	open2 := taskInState(t, st, StatusRunning)
	id, err := st.CreateTask(Task{Title: "t", Spec: "s", Assignee: "w", CreatedBy: "l", DependsOn: []int64{done, open1, open2}})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	unmet, err := st.UnmetDeps(id)
	if err != nil {
		t.Fatalf("UnmetDeps: %v", err)
	}
	want := []int64{open1, open2}
	slices.Sort(unmet)
	if !reflect.DeepEqual(unmet, want) {
		t.Fatalf("UnmetDeps = %v, want %v", unmet, want)
	}
}

// TestListTasksFillsDeps: the batch dep load joins the right edges to the
// right rows.
func TestListTasksFillsDeps(t *testing.T) {
	st := openTemp(t)
	a := taskInState(t, st, StatusPending)
	b := taskInState(t, st, StatusPending)
	c, err := st.CreateTask(Task{Title: "c", Spec: "s", Assignee: "w", CreatedBy: "l", DependsOn: []int64{a, b}})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	all, err := st.ListTasks(TaskFilter{})
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	byID := map[int64]Task{}
	for _, tk := range all {
		byID[tk.ID] = tk
	}
	if got := byID[c].DependsOn; !reflect.DeepEqual(got, []int64{a, b}) {
		t.Fatalf("c.DependsOn = %v, want [%d %d]", got, a, b)
	}
	if len(byID[a].DependsOn) != 0 || len(byID[b].DependsOn) != 0 {
		t.Fatalf("depless tasks grew deps: a=%v b=%v", byID[a].DependsOn, byID[b].DependsOn)
	}
}

// TestMigration0006AppliesToPopulatedLedger builds a real pre-DWF ledger
// (migrations 1..5 only, rows inserted with the old column set), then opens it
// through the normal path: 0006 must apply over the populated table, old rows
// must read back with the defaulted policy and no deps, and the graph must
// work post-migration.
func TestMigration0006AppliesToPopulatedLedger(t *testing.T) {
	dir := t.TempDir()
	vero := filepath.Join(dir, ".vero")
	if err := os.MkdirAll(vero, 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(vero, "vero.db")+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open legacy db: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		v, err := migrationVersion(name)
		if err != nil {
			t.Fatalf("version of %s: %v", name, err)
		}
		if v > 5 {
			continue
		}
		body, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(string(body)); err != nil {
			t.Fatalf("apply legacy %s: %v", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 1)`, v); err != nil {
			t.Fatalf("record legacy %s: %v", name, err)
		}
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (title, spec, status, assignee, created_by, created_at, updated_at)
		 VALUES ('old-row', 's', 'running', 'w', 'leader', 1, 1)`); err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("Open over legacy ledger: %v", err)
	}
	defer st.Close()

	got, err := st.GetTask(1)
	if err != nil {
		t.Fatalf("GetTask(legacy row): %v", err)
	}
	if got.Status != StatusRunning || got.VerifyPolicy != VerifyLeader || len(got.DependsOn) != 0 {
		t.Fatalf("legacy row after 0006 = %+v, want running/leader/no-deps", got)
	}

	id, err := st.CreateTask(Task{Title: "new", Spec: "s", Assignee: "w", CreatedBy: "l", DependsOn: []int64{1}})
	if err != nil {
		t.Fatalf("graph create post-migration: %v", err)
	}
	if tk, _ := st.GetTask(id); tk.Status != StatusBlocked {
		t.Fatalf("post-migration dep task = %s, want blocked (dep running)", tk.Status)
	}
}
