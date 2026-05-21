package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	config "github.com/johnny1110/evva/pkg/config"
	agent_impl "github.com/johnny1110/evva/internal/agent"
	"github.com/johnny1110/evva/internal/memdir"
	"github.com/johnny1110/evva/internal/permission"
	"github.com/johnny1110/evva/internal/question"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/pkg/constant"
)

// Config is the public constructor input for creating an agent.
// Provider and Model are the only required fields.
type Config struct {
	// Provider is the LLM provider name: "anthropic", "openai", "deepseek", "ollama".
	Provider string
	// Model is the model id, e.g. "claude-sonnet-4-6". When empty, the
	// provider's cheapest model is used.
	Model string
	// MaxIters overrides the default loop iteration cap. Zero means use the
	// default (from <app>-config.yml or 10).
	MaxIters int
	// PermissionMode sets the initial permission stance: "default", "bypass",
	// "accept_edits", "plan". Empty means "bypass" (all tool calls auto-approved).
	PermissionMode string
	// AppConfig is the runtime configuration the agent reads from. Nil falls
	// back to config.LoadDefault() / config.Get() — the historical singleton.
	// Downstream apps that want a non-default AppHome (e.g. ~/.myapp/) pass
	// a *config.Config they built via config.Load(...).
	AppConfig *config.Config
}

// New constructs an Agent ready to call Run. When cfg.AppConfig is nil it
// loads the process-wide default (config.Get(), which initializes via
// LoadDefault on first use); pass an explicit *config.Config to target a
// custom AppHome.
//
// Set API keys via the <app>-config.yml file under cfg.AppHome (or env
// vars consumed by your own loader for downstream apps).
func New(cfg Config) (Agent, error) {
	appCfg := cfg.AppConfig
	if appCfg == nil {
		appCfg = config.Get()
	}

	provider, ok := constant.GetProvider(cfg.Provider)
	if !ok {
		return nil, fmt.Errorf("agent: unknown provider %q", cfg.Provider)
	}
	model := constant.Model(cfg.Model)
	if cfg.Model == "" {
		model = provider.Models[0]
	}

	wd, _ := os.Getwd()
	memSnap := memdir.Load(wd, appCfg.AppHome, appCfg.GetEnableAutoMemory())

	prof := agent_impl.Main(appCfg, provider, model, nil, memSnap, nil)

	permStore, _ := permission.Load(wd, appCfg.AppHome)
	permBroker := permission.NewBroker()
	permMode := permission.ModeBypass
	if cfg.PermissionMode != "" {
		if m, ok := permission.ParseMode(cfg.PermissionMode); ok {
			permMode = m
		}
	}
	permission.SetOnRequest(permBroker, func(req permission.ApprovalRequest) {
		_ = permBroker.Respond(req.ID, permission.Decision{
			Behavior: permission.BehaviorDeny,
			Reason:   "no interactive approval surface in programmatic mode",
		})
	})

	qBroker := question.NewBroker()
	question.SetOnRequest(qBroker, func(req question.Request) {
		_ = qBroker.Respond(req.ID, question.Response{})
	})

	inner, err := agent_impl.New(nil, prof,
		agent_impl.WithName(appCfg.AppName),
		agent_impl.WithConfig(appCfg),
		agent_impl.WithMaxIterations(cfg.MaxIters),
		agent_impl.WithPermissionStore(permStore),
		agent_impl.WithPermissionBroker(permBroker),
		agent_impl.WithPermissionMode(permMode),
		agent_impl.WithQuestionBroker(qBroker),
		agent_impl.WithPersona("evva"),
	)
	if err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}

	return &agentAdapter{inner: inner}, nil
}

// agentAdapter wraps *agent_impl.Agent to implement the public Agent interface.
// Methods that return/take internal types (ui.Skill, ui.PermissionDecision,
// etc.) are converted to the pkg equivalents.
type agentAdapter struct {
	inner *agent_impl.Agent
}

func (a *agentAdapter) Run(ctx context.Context, prompt string) (string, error) {
	return a.inner.Run(ctx, prompt)
}

func (a *agentAdapter) Continue(ctx context.Context) (string, error) {
	return a.inner.Continue(ctx)
}

func (a *agentAdapter) Session() SessionInfo {
	s := a.inner.Session()
	u := s.Usage
	return SessionInfo{
		MessageCount:    len(s.GetMessages()),
		InputTokens:     u.InputTokens,
		OutputTokens:    u.OutputTokens,
		LastInputTokens: s.LastTurnInputTokens(),
	}
}

func (a *agentAdapter) Logger() *slog.Logger { return a.inner.Logger() }

func (a *agentAdapter) Model() string { return a.inner.Model() }

func (a *agentAdapter) AgentID() string { return a.inner.AgentID() }

func (a *agentAdapter) MaxIterations() int { return a.inner.MaxIterations() }

func (a *agentAdapter) SetMaxIterations(n int) { a.inner.SetMaxIterations(n) }

func (a *agentAdapter) SwitchLLM(provider constant.LLMProvider, model constant.Model) error {
	return a.inner.SwitchLLM(provider, model)
}

func (a *agentAdapter) SwitchProfile(name string) error {
	return a.inner.SwitchProfile(name)
}

func (a *agentAdapter) ProfileName() string { return a.inner.ProfileName() }

func (a *agentAdapter) ListMainProfiles() []ProfileChoice {
	inner := a.inner.ListMainProfiles()
	out := make([]ProfileChoice, len(inner))
	for i, p := range inner {
		out[i] = ProfileChoice{Name: p.Name, WhenToUse: p.WhenToUse}
	}
	return out
}

func (a *agentAdapter) Effort() string { return a.inner.Effort() }

func (a *agentAdapter) SetEffort(level string) error { return a.inner.SetEffort(level) }

func (a *agentAdapter) Skills() []Skill {
	inner := a.inner.Skills()
	out := make([]Skill, len(inner))
	for i, s := range inner {
		out[i] = Skill{Name: s.Name, Description: s.Description}
	}
	return out
}

func (a *agentAdapter) Compact(ctx context.Context, kind string) error {
	return a.inner.Compact(ctx, kind)
}

func (a *agentAdapter) PermissionModeName() string { return a.inner.PermissionModeName() }

func (a *agentAdapter) CyclePermissionMode() string { return a.inner.CyclePermissionMode() }

func (a *agentAdapter) RespondPermission(id string, dec PermissionDecision) error {
	pd := permission.Decision{Reason: dec.Reason}
	if dec.Behavior == "allow" {
		pd.Behavior = permission.BehaviorAllow
	} else {
		pd.Behavior = permission.BehaviorDeny
	}
	if dec.AddRule != nil {
		pd.AddRule = &permission.Rule{
			ToolName: dec.AddRule.ToolName,
			Content:  dec.AddRule.Content,
			Behavior: permission.BehaviorAllow,
			Source:   permission.SourceSession,
		}
	}
	return a.inner.RespondPermission(id, ui.PermissionDecision{
		Behavior: dec.Behavior,
		Reason:   dec.Reason,
		AddRule:  convertPermissionRuleSeed(dec.AddRule),
	})
}

func convertPermissionRuleSeed(r *PermissionRuleSeed) *ui.PermissionRuleSeed {
	if r == nil {
		return nil
	}
	return &ui.PermissionRuleSeed{
		ToolName: r.ToolName,
		Content:  r.Content,
	}
}

func (a *agentAdapter) RespondQuestion(id string, resp QuestionResponse) error {
	r := question.Response{
		Answers:     resp.Answers,
		Annotations: make(map[string]question.Annotation, len(resp.Annotations)),
	}
	for k, v := range resp.Annotations {
		r.Annotations[k] = question.Annotation{Notes: v.Notes, Preview: v.Preview}
	}
	return a.inner.RespondQuestion(id, ui.QuestionResponse{
		Answers:     resp.Answers,
		Annotations: convertQuestionAnnotations(resp.Annotations),
	})
}

func convertQuestionAnnotations(m map[string]QuestionAnnotation) map[string]ui.QuestionAnnotation {
	if m == nil {
		return nil
	}
	out := make(map[string]ui.QuestionAnnotation, len(m))
	for k, v := range m {
		out[k] = ui.QuestionAnnotation{Notes: v.Notes, Preview: v.Preview}
	}
	return out
}

// ListSessions implements Agent. Forwards to the inner *agent.Agent,
// translating the ui.SessionInfo rows into the public ResumableSession
// type so downstream apps never see pkg/ui types.
func (a *agentAdapter) ListSessions() ([]ResumableSession, []string) {
	rows, warnings := a.inner.ListSessions()
	out := make([]ResumableSession, len(rows))
	for i, r := range rows {
		out[i] = ResumableSession{
			ID:              r.ID,
			FirstUserPrompt: r.FirstUserPrompt,
			UpdatedAt:       r.UpdatedAt,
			CreatedAt:       r.CreatedAt,
			Profile:         r.Profile,
			Provider:        r.Provider,
			Model:           r.Model,
			MessageCount:    r.MessageCount,
		}
	}
	return out, warnings
}

// ResumeSession implements Agent.
func (a *agentAdapter) ResumeSession(id string) error {
	return a.inner.ResumeSession(id)
}
