package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

type fakeDispatcher struct{ sweeps int }

func (f *fakeDispatcher) Sweep() { f.sweeps++ }

func testKit() (*Store, *fakeDispatcher, DispatcherLookup) {
	s := NewStore()
	d := &fakeDispatcher{}
	return s, d, func() Dispatcher { return d }
}

type executor interface {
	Execute(ctx context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error)
}

func run(t *testing.T, e executor, input string) tools.Result {
	t.Helper()
	res, err := e.Execute(context.Background(), slog.Default(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return res
}

func TestCreateToolHappyAndBlocked(t *testing.T) {
	s, d, lookup := testKit()
	create := NewCreate(s, lookup, func() []string { return []string{"explore", "general-purpose"} })

	res := run(t, create, `{"subject":"build API","worker":{"agent_type":"general-purpose","isolation":"worktree"}}`)
	if res.IsError {
		t.Fatalf("create: %s", res.Content)
	}
	if !strings.Contains(res.Content, "#1 [pending]") || !strings.Contains(res.Content, "dispatching") {
		t.Errorf("create content: %q", res.Content)
	}
	if d.sweeps != 1 {
		t.Errorf("ready worker task should sweep once, got %d", d.sweeps)
	}

	res = run(t, create, `{"subject":"test API","depends_on":["1"],"worker":{"agent_type":"explore"},"verify":"auto"}`)
	if res.IsError || !strings.Contains(res.Content, "[blocked]") || !strings.Contains(res.Content, "waiting on: #1") {
		t.Errorf("blocked create content: %q", res.Content)
	}
	// Blocked task still sweeps (harmless, idempotent) — but a self-task
	// does not.
	before := d.sweeps
	res = run(t, create, `{"subject":"review it all"}`)
	if res.IsError {
		t.Fatalf("self create: %s", res.Content)
	}
	if d.sweeps != before {
		t.Errorf("self-task create should not sweep")
	}
}

func TestCreateToolValidation(t *testing.T) {
	s, _, lookup := testKit()
	create := NewCreate(s, lookup, func() []string { return []string{"explore"} })

	cases := []struct {
		input, wantErr string
	}{
		{`{"subject":"x","worker":{"agent_type":"nope"}}`, "unknown worker.agent_type"},
		{`{"subject":"x","worker":{"agent_type":"explore","isolation":"vm"}}`, "isolation"},
		{`{"subject":"x","depends_on":["9"]}`, "dependency not found"},
		{`{"subject":"x","verify":"auto"}`, "requires a worker"},
		{`{"subject":""}`, "subject"},
	}
	for _, c := range cases {
		res := run(t, create, c.input)
		if !res.IsError || !strings.Contains(res.Content, c.wantErr) {
			t.Errorf("input %s: want error containing %q, got %q", c.input, c.wantErr, res.Content)
		}
	}
}

func TestUpdateToolSelfTaskWalkAndForceUnblock(t *testing.T) {
	s, d, lookup := testKit()
	create := NewCreate(s, lookup, nil)
	update := NewUpdate(s, lookup)

	run(t, create, `{"subject":"step 1"}`)                                                      // #1 self
	run(t, create, `{"subject":"step 2","depends_on":["1"],"worker":{"agent_type":"explore"}}`) // #2 blocked worker

	res := run(t, update, `{"task_id":"1","status":"running"}`)
	if res.IsError {
		t.Fatalf("start self-task: %s", res.Content)
	}
	res = run(t, update, `{"task_id":"1","status":"completed"}`)
	if res.IsError || !strings.Contains(res.Content, "unblocked: #2") {
		t.Errorf("complete should report the unblocked dependent: %q", res.Content)
	}
	if d.sweeps == 0 {
		t.Error("completion should sweep the engine")
	}

	// Force-unblock path: fresh blocked task, override its dep.
	run(t, create, `{"subject":"later","depends_on":["2"]}`) // #3 blocked (dep 2 pending)
	before := d.sweeps
	res = run(t, update, `{"task_id":"3","status":"pending","note":"branch abandoned"}`)
	if res.IsError {
		t.Fatalf("force-unblock: %s", res.Content)
	}
	if d.sweeps != before+1 {
		t.Error("force-unblock should sweep")
	}

	res = run(t, update, `{"task_id":"1"}`)
	if !res.IsError || !strings.Contains(res.Content, "nothing to do") {
		t.Errorf("empty update: %q", res.Content)
	}
	res = run(t, update, `{"task_id":"2","status":"running"}`)
	if !res.IsError || !strings.Contains(res.Content, "engine dispatches") {
		t.Errorf("hand-starting a worker task should be denied with the matrix reason: %q", res.Content)
	}
}

func TestUpdateToolDelete(t *testing.T) {
	s, _, lookup := testKit()
	create := NewCreate(s, lookup, nil)
	update := NewUpdate(s, lookup)

	run(t, create, `{"subject":"oops"}`)
	res := run(t, update, `{"task_id":"1","status":"deleted"}`)
	if res.IsError || !strings.Contains(res.Content, "deleted task #1") {
		t.Errorf("delete: %q", res.Content)
	}
	if _, ok := s.Get("1"); ok {
		t.Error("task should be gone")
	}
}

func TestVerifyToolApproveAndReject(t *testing.T) {
	s, d, lookup := testKit()
	create := NewCreate(s, lookup, nil)
	verify := NewVerify(s, lookup)

	run(t, create, `{"subject":"impl","worker":{"agent_type":"explore"}}`)                    // #1
	run(t, create, `{"subject":"docs","depends_on":["1"],"worker":{"agent_type":"explore"}}`) // #2

	// Engine-side happenings: dispatch + worker report.
	if _, err := s.Dispatch("1", "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteWork("1", "did the thing", false); err != nil {
		t.Fatal(err)
	}

	res := run(t, verify, `{"task_id":"2","approve":true}`)
	if !res.IsError || !strings.Contains(res.Content, "not verifying") {
		t.Errorf("verifying-guard: %q", res.Content)
	}

	before := d.sweeps
	res = run(t, verify, `{"task_id":"1","approve":true,"note":"checked the diff"}`)
	if res.IsError || !strings.Contains(res.Content, "→ completed") || !strings.Contains(res.Content, "unblocked: #2") {
		t.Errorf("approve: %q", res.Content)
	}
	if d.sweeps != before+1 {
		t.Error("approve should sweep")
	}

	// Round 2: reject flow on #2.
	if _, err := s.Dispatch("2", "w2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteWork("2", "half done", false); err != nil {
		t.Fatal(err)
	}
	res = run(t, verify, `{"task_id":"2","approve":false,"note":"cover the error path"}`)
	if res.IsError || !strings.Contains(res.Content, "re-queued") || !strings.Contains(res.Content, "fresh worker") {
		t.Errorf("reject: %q", res.Content)
	}
	got, _ := s.Get("2")
	if got.Status != StatusPending || len(got.Comments) != 1 {
		t.Errorf("rejected task: %+v", got)
	}
}

func TestListAndGetTools(t *testing.T) {
	s, _, lookup := testKit()
	create := NewCreate(s, lookup, nil)
	list := NewList(s)
	get := NewGet(s)

	res := run(t, list, `{}`)
	if res.IsError || !strings.Contains(res.Content, "board is empty") {
		t.Errorf("empty list: %q", res.Content)
	}

	run(t, create, `{"subject":"a","worker":{"agent_type":"explore"},"verify":"auto","description":"briefing A"}`)
	run(t, create, `{"subject":"b","depends_on":["1"]}`)

	res = run(t, list, `{}`)
	for _, want := range []string{"#1 [pending] a", "worker: explore", "verify: auto", "#2 [blocked] b", "waiting on: #1", "2 total"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("list missing %q in:\n%s", want, res.Content)
		}
	}
	res = run(t, list, `{"status":"blocked"}`)
	if strings.Contains(res.Content, "#1 [pending]") || !strings.Contains(res.Content, "#2 [blocked]") {
		t.Errorf("filtered list: %q", res.Content)
	}

	if _, err := s.Dispatch("1", "w1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteWork("1", "the result body", true); err != nil {
		t.Fatal(err)
	}
	res = run(t, get, `{"task_id":"1"}`)
	for _, want := range []string{"#1 [verifying] a", "briefing A", "## Result (WORKER FAILED)", "the result body", "blocks: #2", "worker daemon: w1"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("get missing %q in:\n%s", want, res.Content)
		}
	}
	res = run(t, get, `{"task_id":"42"}`)
	if !res.IsError {
		t.Errorf("get missing task should error: %q", res.Content)
	}
}

// Tools stay functional with no engine attached (nil lookups) — the
// embedder-without-engine contract.
func TestToolsNilDispatcher(t *testing.T) {
	s := NewStore()
	create := NewCreate(s, nil, nil)
	update := NewUpdate(s, nil)
	verify := NewVerify(s, nil)

	res := run(t, create, `{"subject":"a","worker":{"agent_type":"anything-goes"}}`)
	if res.IsError {
		t.Fatalf("create without lookups: %s", res.Content)
	}
	if _, err := s.Dispatch("1", "w"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompleteWork("1", "r", false); err != nil {
		t.Fatal(err)
	}
	if res := run(t, verify, `{"task_id":"1","approve":true}`); res.IsError {
		t.Fatalf("verify without dispatcher: %s", res.Content)
	}
	if res := run(t, update, `{"task_id":"1","note":"post-mortem"}`); res.IsError {
		t.Fatalf("note on completed: %s", res.Content)
	}
}

func TestSchemasAreValidJSON(t *testing.T) {
	s, _, lookup := testKit()
	for _, e := range []interface {
		Name() string
		Schema() json.RawMessage
	}{
		NewCreate(s, lookup, nil), NewUpdate(s, lookup), NewVerify(s, lookup), NewList(s), NewGet(s),
	} {
		var v map[string]any
		if err := json.Unmarshal(e.Schema(), &v); err != nil {
			t.Errorf("%s schema: %v", e.Name(), err)
		}
		if !strings.HasPrefix(e.Name(), "wf_task_") {
			t.Errorf("wire name %q must be wf_-prefixed (swarm owns task_*)", e.Name())
		}
	}
	if got := len(Names()); got != 5 {
		t.Errorf("Names() = %d entries, want 5", got)
	}
	_ = fmt.Sprintf // keep fmt for future assertions
}
