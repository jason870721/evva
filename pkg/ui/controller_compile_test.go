package ui_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/todo"
	"github.com/johnny1110/evva/pkg/ui"
)

// publicOnlyController satisfies ui.Controller using nothing but public
// evva packages. This file imports zero internal/* — so the fact that it
// compiles is the proof that every parameter and return type on the
// Controller surface is reachable from a downstream module. This is the
// v2.1 acceptance gate: a separate-module UI can implement the contract
// without importing evva internals.
type publicOnlyController struct{}

func (publicOnlyController) Run(context.Context, string) (string, error)           { return "", nil }
func (publicOnlyController) Continue(context.Context) (string, error)              { return "", nil }
func (publicOnlyController) Messages() []llm.Message                               { return nil }
func (publicOnlyController) Usage() llm.Usage                                      { return llm.Usage{} }
func (publicOnlyController) LastTurnInputTokens() int                              { return 0 }
func (publicOnlyController) TodoStore() *todo.TodoStore                            { return nil }
func (publicOnlyController) DaemonState() *daemon.DaemonState                      { return nil }
func (publicOnlyController) EnqueueUserPrompt(string)                              {}
func (publicOnlyController) Logger() *slog.Logger                                  { return nil }
func (publicOnlyController) Model() string                                         { return "" }
func (publicOnlyController) AgentID() string                                       { return "" }
func (publicOnlyController) MaxIterations() int                                    { return 0 }
func (publicOnlyController) SetMaxIterations(int)                                  {}
func (publicOnlyController) SwitchLLM(constant.LLMProvider, constant.Model) error  { return nil }
func (publicOnlyController) SwitchProfile(string) error                            { return nil }
func (publicOnlyController) ProfileName() string                                   { return "" }
func (publicOnlyController) ListMainProfiles() []ui.ProfileChoice                  { return nil }
func (publicOnlyController) Effort() string                                        { return "" }
func (publicOnlyController) SetEffort(string) error                                { return nil }
func (publicOnlyController) LLMTemperature() *float64                              { return nil }
func (publicOnlyController) LLMTopK() *int                                         { return nil }
func (publicOnlyController) LLMTopP() *float64                                     { return nil }
func (publicOnlyController) SetLLMTemperature(*float64) error                      { return nil }
func (publicOnlyController) SetLLMTopK(*int) error                                 { return nil }
func (publicOnlyController) SetLLMTopP(*float64) error                             { return nil }
func (publicOnlyController) Skills() []ui.Skill                                    { return nil }
func (publicOnlyController) MCPServers() []ui.MCPServerInfo                        { return nil }
func (publicOnlyController) Compact(context.Context, string) error                 { return nil }
func (publicOnlyController) PermissionModeName() string                            { return "" }
func (publicOnlyController) CyclePermissionMode() string                           { return "" }
func (publicOnlyController) RespondPermission(string, ui.PermissionDecision) error { return nil }
func (publicOnlyController) RespondQuestion(string, ui.QuestionResponse) error     { return nil }
func (publicOnlyController) ClearSession() error                                   { return nil }
func (publicOnlyController) ListSessions() ([]ui.SessionInfo, []string)            { return nil, nil }
func (publicOnlyController) ResumeSession(string) error                            { return nil }

// Compile-time proof that the public-only stub satisfies the contract.
var _ ui.Controller = (*publicOnlyController)(nil)

// TestControllerSurfaceIsPublic exists so `go test` exercises the package;
// the load-bearing assertion is the var _ above, enforced at compile time.
func TestControllerSurfaceIsPublic(t *testing.T) {
	// The compile-time `var _ ui.Controller` assertion above is the real
	// proof; this keeps `go test` from reporting the package as empty.
	var c ui.Controller = publicOnlyController{}
	if c.Model() != "" {
		t.Errorf("stub Model() should return empty, got %q", c.Model())
	}
}
