package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/webapi"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// This is the Phase-1 DoD gate (SPRD-1-13): one hermetic end-to-end exercise of
// the whole swarm — leader decomposes + dispatches, a worker runs and reports,
// the leader verifies + completes, then a restart resumes the work — driven by a
// DETERMINISTIC, transcript-driven fake LLM (no network, loopback only). The
// supervisor + bus + drains do the orchestration; the script only decides each
// member's next move from what it can see, so reaching `completed` proves the
// 5-state machine, the message round-trip, and the wake/drain plumbing all work
// together.

const (
	e2eProvider = "e2e_stub"
	e2eModel    = "e2e-model"
	kickoff     = "KICKOFF: build the widget and assign it to worker-a, then verify."
)

var taskRe = regexp.MustCompile(`task #(\d+)`)

// scriptedClient is a pure function of the transcript: it inspects the
// conversation it is handed and returns the next tool call (or final text). The
// same code drives every member; role falls out of what each member can see
// (only a worker's transcript carries an assignment; only the leader's carries
// the KICKOFF / the worker's report).
type scriptedClient struct {
	model string
	calls atomic.Int32
}

func (c *scriptedClient) Name() string             { return e2eProvider }
func (c *scriptedClient) Model() string            { return c.model }
func (*scriptedClient) SupportsDeferLoading() bool { return false }
func (c *scriptedClient) Stream(ctx context.Context, m []llm.Message, ts []tools.Tool, _ llm.ChunkSink) (llm.Response, error) {
	return c.Complete(ctx, m, ts)
}
func (*scriptedClient) Apply(...llm.Option) {}

func (c *scriptedClient) Complete(_ context.Context, msgs []llm.Message, _ []tools.Tool) (llm.Response, error) {
	n := c.calls.Add(1)
	text := transcriptText(msgs)

	// WORKER: assigned a task and not yet reported → report it done, once.
	if id, ok := assignedTaskID(text); ok && !strings.Contains(text, "Message delivered to leader") {
		return call(n, "send_message",
			fmt.Sprintf(`{"to":"leader","body":"REPORT task #%d done: widget built","ref_task":%d}`, id, id)), nil
	}

	// LEADER: driven by the KICKOFF prompt.
	if strings.Contains(text, "KICKOFF") {
		switch {
		case !strings.Contains(text, "Created task #"):
			return call(n, "task_create", `{"title":"widget","assignee":"worker-a","spec":"build the widget"}`), nil
		case !strings.Contains(text, "set running"):
			return call(n, "task_assign", fmt.Sprintf(`{"task_id":%d}`, firstTaskID(text))), nil
		case strings.Contains(text, "REPORT task") && !strings.Contains(text, "verifying"):
			return call(n, "task_update_status", fmt.Sprintf(`{"task_id":%d,"status":"verifying"}`, firstTaskID(text))), nil
		case strings.Contains(text, "verifying") && !strings.Contains(text, "verified and completed"):
			return call(n, "task_verify", fmt.Sprintf(`{"task_id":%d,"approve":true}`, firstTaskID(text))), nil
		default:
			return llm.Response{Content: "leader: all done"}, nil
		}
	}

	return llm.Response{Content: "idle"}, nil
}

// call wraps one tool invocation as an LLM response.
func call(seq int32, name, input string) llm.Response {
	return llm.Response{ToolCalls: []*tools.Call{{ID: fmt.Sprintf("tc-%d", seq), Name: name, Input: []byte(input)}}}
}

// transcriptText flattens every text surface of the transcript the script keys
// off: user/assistant content plus tool-result content.
func transcriptText(msgs []llm.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Content)
		b.WriteByte('\n')
		for _, r := range m.ToolResults {
			b.WriteString(r.Content)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// assignedTaskID returns the task id from a folded "You are assigned task #N"
// message (worker side).
func assignedTaskID(text string) (int64, bool) {
	if !strings.Contains(text, "assigned task #") {
		return 0, false
	}
	return firstTaskID(text), true
}

func firstTaskID(text string) int64 {
	m := taskRe.FindStringSubmatch(text)
	if m == nil {
		return 0
	}
	var id int64
	fmt.Sscanf(m[1], "%d", &id)
	return id
}

func init() {
	if !llm.DefaultRegistry().Has(e2eProvider) {
		_ = llm.DefaultRegistry().Register(e2eProvider, func(_ llm.APIConfig, model string, _ ...llm.Option) (llm.Client, error) {
			return &scriptedClient{model: model}, nil
		})
	}
}

// writeTeamFixture lays down a ≥3-agent swarm: a leader + two workers (worker-a
// does the work; worker-b is never tasked, so it proves idle == no run).
func writeTeamFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	put := func(p, content string) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("evva-swarm.yml", "name: team\nleader:\n  agent: leader\nworkers:\n  - agent: worker-a\n  - agent: worker-b\nsettings:\n  permission_mode: bypass\n  max_iterations: 10\n")
	put("agents/main/leader/system_prompt.md", "You are the leader.")
	put("agents/sub/worker-a/system_prompt.md", "You are worker-a.")
	put("agents/sub/worker-b/system_prompt.md", "You are worker-b.")
	return dir
}

func scriptedLoadConfig(appHome string) func(string) (*config.Config, error) {
	return func(workdir string) (*config.Config, error) {
		cfg, err := config.Load(config.LoadOptions{AppName: "e2e", AppHome: appHome, WorkDir: workdir})
		if err != nil {
			return nil, err
		}
		cfg.LLMProviderConfig[e2eProvider] = config.APIConfig{ApiURL: "http://stub", ApiSecret: "x", Models: []constant.Model{e2eModel}}
		cfg.DefaultProvider = constant.LLMProvider{Name: e2eProvider, Models: []constant.Model{e2eModel}}
		cfg.DefaultModel = constant.Model(e2eModel)
		return cfg, nil
	}
}

func pollUntil(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", what)
}

func taskStatus(svc *Service, space string, id int64) string {
	page, _ := svc.Tasks(space)
	for _, t := range page.Tasks {
		if t.ID == id {
			return t.Status
		}
	}
	return ""
}

// roundTrip returns the leader→worker-a assignment and the worker-a→leader
// report messages (nil until each exists), for asserting the two-way exchange.
func roundTrip(svc *Service, space string) (assign, report *webapi.MessageInfo) {
	msgs, _ := svc.Messages(space)
	for i := range msgs {
		m := &msgs[i]
		switch {
		case m.Sender == "leader" && m.Recipient == "worker-a":
			assign = m
		case m.Sender == "worker-a" && m.Recipient == "leader":
			report = m
		}
	}
	return assign, report
}

// TestE2E_FullLoop is the centerpiece (A3/A4/A10): assign → collaborate → verify
// → complete, asserting the 5-state outcome, the message round-trip + mark-read,
// and idle-no-token.
func TestE2E_FullLoop(t *testing.T) {
	appHome := t.TempDir()
	svc := New("127.0.0.1:0")
	svc.loadConfig = scriptedLoadConfig(appHome)
	defer svc.Stop()

	space, err := svc.Register(writeTeamFixture(t), "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Kick the leader (the webapi "leader chat" path).
	if err := svc.Run(space, "leader", kickoff); err != nil {
		t.Fatalf("run leader: %v", err)
	}

	// The whole loop converges to a completed task #1 with no further input.
	pollUntil(t, "task #1 completed", 25*time.Second, func() bool {
		return taskStatus(svc, space, 1) == "completed"
	})

	// Message round-trip both ways, both marked read. drain A marks a message
	// read only *after* the receiving run finishes — which can lag the task
	// reaching `completed` (the verify happens mid-run) — so wait for it rather
	// than reading the instant the task flips.
	pollUntil(t, "both messages marked read", 25*time.Second, func() bool {
		assign, report := roundTrip(svc, space)
		return assign != nil && report != nil && assign.ReadAt != nil && report.ReadAt != nil
	})
	assign, report := roundTrip(svc, space)
	if assign == nil || report == nil {
		t.Fatalf("message round-trip incomplete: assign=%v report=%v", assign != nil, report != nil)
	}

	// Idle == no tokens: worker-b was never tasked, so it never ran → empty
	// transcript (the scheduler is wake-driven, not polling).
	if tr, ok := svc.Transcript(space, "worker-b"); !ok || len(tr) != 0 {
		t.Errorf("idle worker-b should have an empty transcript, got %d turns (ran when it shouldn't)", len(tr))
	}
}

// TestE2E_RestartContinuity (A7): run a full loop to a clean, persisted state,
// kill + rebuild the host from disk, and confirm (a) the ledger survived the
// restart and (b) the reconciled swarm is alive — a new operator message to a
// worker is woken, processed, and marked read on the rebuilt space.
//
// The deterministic mid-flight guarantees (unread reload, transcript resume,
// running-task persist, frozen membership) are proven at the unit level in
// swarm.TestRestartResume; here we exercise the integrated restart path end to
// end without racing a precise in-flight instant.
func TestE2E_RestartContinuity(t *testing.T) {
	appHome := t.TempDir()
	stateDir := t.TempDir()
	loadCfg := scriptedLoadConfig(appHome)
	dir := writeTeamFixture(t)

	svc1 := New("127.0.0.1:0")
	svc1.SetStateDir(stateDir)
	svc1.loadConfig = loadCfg
	space, err := svc1.Register(dir, "")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := svc1.Run(space, "leader", kickoff); err != nil {
		t.Fatalf("run: %v", err)
	}
	pollUntil(t, "task #1 completed (pre-restart)", 25*time.Second, func() bool {
		return taskStatus(svc1, space, 1) == "completed"
	})
	if err := svc1.Stop(); err != nil { // graceful stop preserves spaces.json
		t.Fatalf("stop: %v", err)
	}

	// Restart: rebuild every registered space from disk.
	svc2 := New("127.0.0.1:0")
	svc2.SetStateDir(stateDir)
	svc2.loadConfig = loadCfg
	defer svc2.Stop()
	if err := svc2.Reconcile(); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	spaces := svc2.ListSpaces()
	if len(spaces) != 1 {
		t.Fatalf("after restart: %d spaces, want 1", len(spaces))
	}
	id2 := spaces[0].ID

	// (a) The ledger survived the restart.
	if got := taskStatus(svc2, id2, 1); got != "completed" {
		t.Errorf("task #1 status after restart = %q, want completed (ledger should persist)", got)
	}

	// (b) The reconciled swarm is live: a fresh operator message to a worker is
	// delivered, the worker is woken, and the message is drained (marked read) —
	// proving Reconcile → Reload → supervisor → bus → drain all work post-restart.
	if err := svc2.SendUserMessage(id2, "worker-a", "", "are you back online?"); err != nil {
		t.Fatalf("post-restart message: %v", err)
	}
	pollUntil(t, "post-restart operator message processed", 25*time.Second, func() bool {
		msgs, _ := svc2.Messages(id2)
		for _, m := range msgs {
			if m.Sender == "user" && m.Recipient == "worker-a" && m.ReadAt != nil {
				return true
			}
		}
		return false
	})
}

// TestE2E_MultiSpaceIsolation (A2b): two spaces with identical member names run
// fully isolated; stopping one leaves the other completing its own loop.
func TestE2E_MultiSpaceIsolation(t *testing.T) {
	appHome := t.TempDir()
	svc := New("127.0.0.1:0")
	svc.loadConfig = scriptedLoadConfig(appHome)
	defer svc.Stop()

	a, err := svc.Register(writeTeamFixture(t), "team-a")
	if err != nil {
		t.Fatalf("register A: %v", err)
	}
	b, err := svc.Register(writeTeamFixture(t), "team-b")
	if err != nil {
		t.Fatalf("register B: %v", err)
	}
	if a == b {
		t.Fatal("two spaces share an id")
	}

	// Drive only space A; stop it; space B must still be able to run its own
	// independent loop afterwards — proving no shared store/bus/roster.
	if err := svc.Run(a, "leader", kickoff); err != nil {
		t.Fatalf("run A: %v", err)
	}
	pollUntil(t, "A task completed", 25*time.Second, func() bool {
		return taskStatus(svc, a, 1) == "completed"
	})
	if err := svc.StopSpace(a); err != nil {
		t.Fatalf("stop A: %v", err)
	}

	// B is untouched by A's lifecycle and completes its own loop.
	if !svc.HasSpace(b) {
		t.Fatal("stopping A took B down")
	}
	if err := svc.Run(b, "leader", kickoff); err != nil {
		t.Fatalf("run B: %v", err)
	}
	pollUntil(t, "B task completed after A stopped", 25*time.Second, func() bool {
		return taskStatus(svc, b, 1) == "completed"
	})
	// B's ledger is its own (also task #1, independent row).
	if taskStatus(svc, b, 1) != "completed" {
		t.Error("space B did not complete its isolated loop")
	}
}
