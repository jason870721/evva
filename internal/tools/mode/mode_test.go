package mode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/permission"
)

// fakeController is a minimal PlanModeController for tests. Tracks mode
// mutations so assertions can read them back.
type fakeController struct {
	mode      atomic.Value // permission.Mode
	prePlan   atomic.Value // permission.Mode
	workdir   string
	broker    permission.Broker
	agentID   string
	modeCalls []string
	mu        sync.Mutex
}

func newFakeController(t *testing.T, initialMode permission.Mode) *fakeController {
	t.Helper()
	c := &fakeController{
		workdir: t.TempDir(),
		agentID: "test-agent",
	}
	c.mode.Store(initialMode)
	c.prePlan.Store(permission.Mode(""))
	return c
}

func (c *fakeController) PermissionMode() permission.Mode {
	v := c.mode.Load()
	if v == nil {
		return ""
	}
	return v.(permission.Mode)
}
func (c *fakeController) SetPermissionMode(m permission.Mode) {
	c.mu.Lock()
	c.modeCalls = append(c.modeCalls, string(m))
	c.mu.Unlock()
	// Mirror Agent.SetPermissionMode's transition-hub side effect: when
	// the call flips into / out of plan mode, stash / clear prePlanMode
	// just like the real agent does. The mode tools rely on this
	// contract — they call SetPermissionMode and expect PrePlanMode to
	// reflect the transition.
	prev := c.PermissionMode()
	if m != permission.ModePlan && prev == permission.ModePlan {
		c.SetPrePlanMode("")
	} else if m == permission.ModePlan && prev != permission.ModePlan {
		c.SetPrePlanMode(prev)
	}
	c.mode.Store(m)
}
func (c *fakeController) PrePlanMode() permission.Mode {
	v := c.prePlan.Load()
	if v == nil {
		return ""
	}
	return v.(permission.Mode)
}
func (c *fakeController) SetPrePlanMode(m permission.Mode) { c.prePlan.Store(m) }
func (c *fakeController) PlanName() string                  { return "" }
func (c *fakeController) SetPlanName(name string)           {}
func (c *fakeController) Workdir() string                  { return c.workdir }
func (c *fakeController) Broker() permission.Broker        { return c.broker }
func (c *fakeController) AgentID() string                  { return c.agentID }

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// scriptedBroker auto-responds to every Request with the canned decision.
// Useful for ExitPlanMode tests where we want to drive the approve/reject
// path without standing up a real UI.
type scriptedBroker struct {
	decision permission.Decision
	got      chan permission.ApprovalRequest
}

func newScriptedBroker(d permission.Decision) *scriptedBroker {
	return &scriptedBroker{decision: d, got: make(chan permission.ApprovalRequest, 1)}
}

func (b *scriptedBroker) Request(_ context.Context, req permission.ApprovalRequest) (permission.Decision, error) {
	select {
	case b.got <- req:
	default:
	}
	return b.decision, nil
}
func (b *scriptedBroker) Respond(string, permission.Decision) error { return nil }
func (b *scriptedBroker) Cancel(string) error                       { return nil }

func TestEnterPlanMode_FlipsModeAndStashesPrev(t *testing.T) {
	c := newFakeController(t, permission.ModeAcceptEdits)
	tool := NewEnterPlanMode(func() PlanModeController { return c })

	res, err := tool.Execute(context.Background(), nopLogger(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %s", res.Content)
	}
	if c.PermissionMode() != permission.ModePlan {
		t.Errorf("mode: want plan, got %q", c.PermissionMode())
	}
	if c.PrePlanMode() != permission.ModeAcceptEdits {
		t.Errorf("prePlanMode: want accept_edits, got %q", c.PrePlanMode())
	}
	// Plan file should exist (empty).
	planPath := PlanFilePath(c.workdir, c.PlanName())
	if _, err := os.Stat(planPath); err != nil {
		t.Errorf("plan file not created: %v", err)
	}
	if !strings.Contains(res.Content, planPath) {
		t.Errorf("result should mention plan file path; got %q", res.Content)
	}
}

func TestEnterPlanMode_IdempotentWhenAlreadyInPlan(t *testing.T) {
	c := newFakeController(t, permission.ModePlan)
	c.SetPrePlanMode(permission.ModeDefault) // simulate prior entry
	tool := NewEnterPlanMode(func() PlanModeController { return c })

	res, err := tool.Execute(context.Background(), nopLogger(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %s", res.Content)
	}
	if c.PermissionMode() != permission.ModePlan {
		t.Errorf("mode should stay plan, got %q", c.PermissionMode())
	}
	// PrePlanMode must NOT have been overwritten — original was default.
	if c.PrePlanMode() != permission.ModeDefault {
		t.Errorf("prePlanMode should not be touched on re-entry; got %q", c.PrePlanMode())
	}
}

func TestEnterPlanMode_NoControllerReturnsError(t *testing.T) {
	tool := NewEnterPlanMode(nil)
	res, _ := tool.Execute(context.Background(), nopLogger(), nil)
	if !res.IsError {
		t.Errorf("expected IsError when no controller installed; got %+v", res)
	}
}

func TestExitPlanMode_RejectsWhenNotInPlan(t *testing.T) {
	c := newFakeController(t, permission.ModeDefault)
	tool := NewExitPlanMode(func() PlanModeController { return c })

	res, _ := tool.Execute(context.Background(), nopLogger(), nil)
	if !res.IsError {
		t.Errorf("expected IsError when not in plan mode; got %+v", res)
	}
}

func TestExitPlanMode_RejectsEmptyPlanFile(t *testing.T) {
	c := newFakeController(t, permission.ModePlan)
	c.broker = newScriptedBroker(permission.Decision{Behavior: permission.BehaviorAllow})

	// Create an empty plan file.
	planPath := PlanFilePath(c.workdir, c.PlanName())
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("   \n  \n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewExitPlanMode(func() PlanModeController { return c })
	res, _ := tool.Execute(context.Background(), nopLogger(), nil)
	if !res.IsError {
		t.Errorf("expected IsError on empty plan file; got %+v", res)
	}
	if !strings.Contains(res.Content, planPath) {
		t.Errorf("error should mention plan path; got %q", res.Content)
	}
}

func TestExitPlanMode_ApprovalRestoresMode(t *testing.T) {
	c := newFakeController(t, permission.ModePlan)
	c.SetPrePlanMode(permission.ModeAcceptEdits)
	broker := newScriptedBroker(permission.Decision{Behavior: permission.BehaviorAllow})
	c.broker = broker

	// Write a non-empty plan file.
	planPath := PlanFilePath(c.workdir, c.PlanName())
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("# Plan\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewExitPlanMode(func() PlanModeController { return c })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := tool.Execute(ctx, nopLogger(), nil)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError: %s", res.Content)
	}
	if c.PermissionMode() != permission.ModeAcceptEdits {
		t.Errorf("expected restored mode accept_edits, got %q", c.PermissionMode())
	}

	// Verify broker received the plan content as PlanContent.
	select {
	case req := <-broker.got:
		if !strings.Contains(req.PlanContent, "Do the thing.") {
			t.Errorf("broker payload missing plan body; got PlanContent=%q", req.PlanContent)
		}
		if req.ToolName != "exit_plan_mode" {
			t.Errorf("broker payload ToolName: got %q", req.ToolName)
		}
	default:
		t.Errorf("broker did not receive a request")
	}
}

func TestExitPlanMode_RejectionStaysInPlan(t *testing.T) {
	c := newFakeController(t, permission.ModePlan)
	c.SetPrePlanMode(permission.ModeDefault)
	c.broker = newScriptedBroker(permission.Decision{Behavior: permission.BehaviorDeny, Reason: "use Redis instead"})

	planPath := PlanFilePath(c.workdir, c.PlanName())
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("# Plan\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewExitPlanMode(func() PlanModeController { return c })
	res, _ := tool.Execute(context.Background(), nopLogger(), nil)
	if res.IsError {
		t.Errorf("rejection should not surface as IsError (model needs to iterate); got %+v", res)
	}
	if c.PermissionMode() != permission.ModePlan {
		t.Errorf("rejection should keep ModePlan; got %q", c.PermissionMode())
	}
	if !strings.Contains(res.Content, "use Redis instead") {
		t.Errorf("result should surface reject reason; got %q", res.Content)
	}
}

func TestExitPlanMode_NoBrokerAutoRestores(t *testing.T) {
	c := newFakeController(t, permission.ModePlan)
	c.SetPrePlanMode(permission.ModeDefault)
	// broker stays nil

	planPath := PlanFilePath(c.workdir, c.PlanName())
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("plan body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := NewExitPlanMode(func() PlanModeController { return c })
	res, _ := tool.Execute(context.Background(), nopLogger(), nil)
	if res.IsError {
		t.Fatalf("unexpected IsError: %s", res.Content)
	}
	if c.PermissionMode() != permission.ModeDefault {
		t.Errorf("no-broker path should auto-restore to default; got %q", c.PermissionMode())
	}
}

func TestExitPlanMode_AllowedPromptsAreParsed(t *testing.T) {
	// Sanity: the schema accepts allowedPrompts; we don't act on them
	// in v1 but we must not crash if the model provides them.
	c := newFakeController(t, permission.ModePlan)
	c.SetPrePlanMode(permission.ModeDefault)
	c.broker = newScriptedBroker(permission.Decision{Behavior: permission.BehaviorAllow})

	planPath := PlanFilePath(c.workdir, c.PlanName())
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, []byte("plan body\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := json.RawMessage(`{"allowedPrompts":[{"tool":"Bash","prompt":"run tests"}]}`)
	tool := NewExitPlanMode(func() PlanModeController { return c })
	res, err := tool.Execute(context.Background(), nopLogger(), input)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Errorf("unexpected IsError: %s", res.Content)
	}
}
