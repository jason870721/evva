package agent

import (
	"context"
	"fmt"
	"log/slog"

	agent_impl "github.com/johnny1110/evva/internal/agent"
	config "github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/hooks"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/skill"
	"github.com/johnny1110/evva/pkg/ui"
)

// ErrIterLimit is returned by Run / Continue when the agent reaches its
// loop-iteration cap before the model stops calling tools. Callers pause
// and call Continue to resume. Re-exported from the internal agent so a
// pkg-only host can errors.Is against it.
var ErrIterLimit = agent_impl.ErrIterLimit

// Config is the declarative input for the one-call constructor New. Every
// field is optional: an empty Config builds the bundled "evva" persona against
// the process-wide config (config.Get()). Imperative extras — an event sink, a
// root context, a custom permission broker — are layered on as Options.
type Config struct {
	// Provider optionally overrides the config's default LLM provider for this
	// agent ("anthropic", "openai", "deepseek", "ollama"). Empty uses
	// AppConfig.DefaultProvider.
	Provider string
	// Model optionally overrides the config's default model id (e.g.
	// "claude-sonnet-4-6"). Empty uses AppConfig.DefaultModel, or — when
	// Provider is set — that provider's first model.
	Model string
	// MaxIters overrides the loop iteration cap. Zero uses the config default
	// (from <app>-config.yml, else 10).
	MaxIters int
	// PermissionMode sets the initial permission stance: "default",
	// "accept_edits", "plan", "bypass". Empty falls back to
	// AppConfig.PermissionMode, then "default". Headless hosts with no approval
	// surface should set "bypass" so tool calls don't auto-deny.
	PermissionMode string
	// Persona names the main-tier persona to start as ("evva", a disk persona,
	// or one Register'd on Personas). Empty falls back to
	// AppConfig.DefaultProfile, then "evva". An unknown name degrades to "evva"
	// rather than failing the boot.
	Persona string
	// Personas is the catalog the agent resolves the active persona, the
	// /profile picker, and the subagent kinds through. Nil builds the default
	// from disk (BuildAgentRegistry over AppConfig.AppHome); supply one to add
	// in-code personas (the evva→nono pattern).
	Personas *AgentRegistry
	// PermissionStore is the rule store the permission gate consults. Nil loads
	// project + user rules from disk (permission.Load over AppConfig.WorkDir /
	// AppHome).
	PermissionStore *permission.Store
	// LLMOptions are sampling overrides (temperature, max tokens, ...) baked
	// into the resolved persona's profile. Empty leaves provider defaults.
	LLMOptions []llm.Option
	// AppConfig is the runtime configuration the agent reads from. Nil falls
	// back to config.Get() — the process-wide singleton. Downstream apps that
	// want a non-default AppHome (e.g. ~/.myapp/) pass a *config.Config they
	// built via config.Load(...).
	AppConfig *config.Config
}

// New is the one-call constructor: it absorbs the whole bootstrap a host used
// to hand-wire — persona resolution (with an evva fallback), EVVA.md /
// USER_PROFILE.md memory + skill auto-load, the permission store + mode, and
// the approval / question brokers — driven entirely by Config plus Options.
//
// With no Options it builds a headless agent (the default brokers auto-deny any
// approval the model requests, so a request never parks a goroutine). Pass
// WithSink to surface approval + question events to an interactive UI (resolve
// them via Agent.RespondPermission / RespondQuestion) and WithRootContext so
// Ctrl-C reaches every background worker — that combination is all a ~40-line
// host needs for a full TUI with personas, permissions, /resume, and /compact.
//
// Options are applied after the Config-derived wiring, so a host Option always
// wins on conflict (e.g. WithName, WithMaxIterations).
func New(cfg Config, opts ...Option) (Agent, error) {
	appCfg := cfg.AppConfig
	if appCfg == nil {
		appCfg = config.Get()
	}

	// Provider / model overrides land on the config the persona resolver reads.
	// Both default to the config's own DefaultProvider / DefaultModel when left
	// empty, so the common path is purely config-driven.
	if cfg.Provider != "" {
		provider, ok := constant.GetProvider(cfg.Provider)
		if !ok {
			return nil, fmt.Errorf("agent: unknown provider %q", cfg.Provider)
		}
		appCfg.DefaultProvider = provider
		if cfg.Model != "" {
			appCfg.DefaultModel = constant.Model(cfg.Model)
		} else {
			appCfg.DefaultModel = provider.Models[0]
		}
	} else if cfg.Model != "" {
		appCfg.DefaultModel = constant.Model(cfg.Model)
	}

	// Persona catalog: caller-supplied (in-code personas) or built from disk.
	reg := cfg.Personas
	var regWarns []string
	if reg == nil {
		reg, regWarns = BuildAgentRegistry(appCfg.AppHome)
	}

	// Persona name: Config → config default → "evva".
	personaName := cfg.Persona
	if personaName == "" {
		personaName = appCfg.DefaultProfile
	}
	if personaName == "" {
		personaName = "evva"
	}

	// Resolve the initial profile (memory + skills auto-loaded). A bad persona
	// name falls back to evva rather than blocking the boot.
	prof, memWarns, err := ResolveMainProfile(appCfg, reg, personaName, cfg.LLMOptions...)
	if err != nil {
		personaName = "evva"
		prof, memWarns, err = ResolveMainProfile(appCfg, reg, personaName, cfg.LLMOptions...)
		if err != nil {
			return nil, fmt.Errorf("agent: resolve persona: %w", err)
		}
	}

	// Permission store + mode. Mode precedence: Config > config YAML > default.
	permStore := cfg.PermissionStore
	if permStore == nil {
		permStore, _ = permission.Load(appCfg.WorkDir, appCfg.AppHome)
	}
	// Hook registry. Loaded from the same settings.json files the
	// permission store reads; warnings surface on the agent logger
	// alongside regWarns / memWarns. A nil or empty registry is safe —
	// the dispatcher noops.
	hookReg, hookWarns := hooks.Load(appCfg.WorkDir, appCfg.AppHome)
	permMode := PermissionDefault
	for _, candidate := range []string{cfg.PermissionMode, appCfg.PermissionMode} {
		if candidate == "" {
			continue
		}
		if m := PermissionMode(candidate); m.Valid() {
			permMode = m
			break
		}
	}

	// Config-derived wiring first; host opts last so they win on conflict.
	base := []Option{
		WithName(appCfg.AppName),
		WithConfig(appCfg),
		WithMaxIterations(cfg.MaxIters),
		WithPersonaRegistry(reg),
		WithPersona(personaName),
		WithPermissionStore(permStore),
		WithPermissionMode(permMode),
		WithHookRegistry(hookReg),
	}
	base = append(base, opts...)

	ag, err := NewWithProfile(prof, base...)
	if err != nil {
		return nil, err
	}
	for _, w := range regWarns {
		ag.Logger().Warn("agent: registry", "msg", w)
	}
	for _, w := range memWarns {
		ag.Logger().Warn("agent: memory", "msg", w)
	}
	for _, w := range hookWarns {
		ag.Logger().Warn("agent: hooks", "msg", w.Error())
	}
	return ag, nil
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

func (a *agentAdapter) LLMTemperature() *float64 { return a.inner.LLMTemperature() }
func (a *agentAdapter) LLMTopK() *int           { return a.inner.LLMTopK() }
func (a *agentAdapter) LLMTopP() *float64       { return a.inner.LLMTopP() }

func (a *agentAdapter) SetLLMTemperature(v *float64) error { return a.inner.SetLLMTemperature(v) }
func (a *agentAdapter) SetLLMTopK(v *int) error            { return a.inner.SetLLMTopK(v) }
func (a *agentAdapter) SetLLMTopP(v *float64) error        { return a.inner.SetLLMTopP(v) }

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

// ReloadSkills implements the optional SkillReloader seam (RP-10): it swaps this
// agent's skill catalog at runtime and re-renders the system prompt. Callers reach
// it by type-asserting an Agent to SkillReloader. Call at a run boundary.
func (a *agentAdapter) ReloadSkills(reg *skill.Registry) error {
	return a.inner.ReloadSkills(reg)
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
	return a.inner.RespondQuestion(id, ui.QuestionResponse{
		Answers:      resp.Answers,
		MultiAnswers: resp.MultiAnswers,
		Annotations:  convertQuestionAnnotations(resp.Annotations),
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

// Controller implements Agent. The inner *agent.Agent already satisfies
// ui.Controller, so a host can hand the result straight to UI.Attach.
func (a *agentAdapter) Controller() ui.Controller {
	return a.inner
}

// Shutdown implements Agent. Cancels the agent's root context, tearing down
// the signal pump and every background worker (bash tasks, monitors,
// subagents) it spawned.
func (a *agentAdapter) Shutdown() { a.inner.Shutdown() }
