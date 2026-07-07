package agent

import (
	"slices"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/workflow"
)

func workflowTestConfig(t *testing.T, enabled bool) *config.Config {
	t.Helper()
	cfg, err := config.Load(config.LoadOptions{AppName: "t", AppHome: t.TempDir(), WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	cfg.EnableDynamicWorkflow = enabled
	return cfg
}

// Flag off (the default): the baseline is byte-identical to today — todo
// list mounted, no board, no workflow prompt section.
func TestMainProfile_DynamicWorkflowOff(t *testing.T) {
	cfg := workflowTestConfig(t, false)
	p := Main(cfg, cfg.DefaultProvider, cfg.DefaultModel, []sysprompt.SkillRef{}, memdir.Snapshot{}, nil)

	if !slices.Contains(p.ActiveTools, tools.TODO_WRITE) {
		t.Error("flag off: todo_write must stay mounted")
	}
	for _, n := range workflow.Names() {
		if slices.Contains(p.ActiveTools, n) || slices.Contains(p.DeferredTools, n) {
			t.Errorf("flag off: %q must not be mounted", n)
		}
	}
	if strings.Contains(p.SystemPrompt, "dynamic workflow") {
		t.Error("flag off: prompt must not carry the workflow protocol")
	}
	if !strings.Contains(p.SystemPrompt, string(tools.TODO_WRITE)) {
		t.Error("flag off: prompt must keep the todo protocol")
	}
	if profileHasWorkflowBoard(p) {
		t.Error("flag off: wiring gate must be closed")
	}
}

// Flag on: the board replaces the todo list — tools and prompt swap
// together so the prompt never advertises an unmounted tool.
func TestMainProfile_DynamicWorkflowOn(t *testing.T) {
	cfg := workflowTestConfig(t, true)
	p := Main(cfg, cfg.DefaultProvider, cfg.DefaultModel, []sysprompt.SkillRef{}, memdir.Snapshot{}, nil)

	for _, n := range workflow.Names() {
		if !slices.Contains(p.ActiveTools, n) {
			t.Errorf("flag on: %q must be active", n)
		}
	}
	if slices.Contains(p.ActiveTools, tools.TODO_WRITE) {
		t.Error("flag on: todo_write must be swapped out (one planning surface)")
	}
	for _, want := range []string{
		"# Multi-step work — dynamic workflow",
		string(tools.WF_TASK_CREATE), string(tools.WF_TASK_UPDATE),
		string(tools.WF_TASK_VERIFY), string(tools.WF_TASK_LIST),
	} {
		if !strings.Contains(p.SystemPrompt, want) {
			t.Errorf("flag on: prompt missing %q", want)
		}
	}
	if strings.Contains(p.SystemPrompt, "rewrites the full list every call") {
		t.Error("flag on: the todo protocol must be replaced, not doubled")
	}
	if !profileHasWorkflowBoard(p) {
		t.Error("flag on: wiring gate must be open")
	}
}

// A swarm-resident persona never mounts the solo board, whatever the host
// config says — the swarm has its own ledger and roster.
func TestMainProfileForDef_LongRunningNeverMountsBoard(t *testing.T) {
	cfg := workflowTestConfig(t, true)
	def := sysprompt.MainAgent
	def.LongRunning = true

	p := mainProfileForDef(def, cfg, cfg.DefaultProvider, cfg.DefaultModel, []sysprompt.SkillRef{}, memdir.Snapshot{}, nil, nil, "")

	for _, n := range workflow.Names() {
		if slices.Contains(p.ActiveTools, n) {
			t.Errorf("LongRunning: %q must not be mounted even with the flag on", n)
		}
	}
	if !slices.Contains(p.ActiveTools, tools.TODO_WRITE) {
		t.Error("LongRunning: todo_write must survive")
	}
	if strings.Contains(p.SystemPrompt, "# Multi-step work — dynamic workflow") {
		t.Error("LongRunning: prompt must not carry the workflow protocol")
	}
	if profileHasWorkflowBoard(p) {
		t.Error("LongRunning: wiring gate must be closed")
	}
}
