package agent

import (
	"context"
	"fmt"
	"sync"

	"github.com/johnny1110/evva/pkg/mcp"
)

// mcpserve.go is the concrete mcp.PersonaSpawner (MCP-2): the piece that
// actually turns an inbound MCP tool call into a full evva agent run.
//
// It lives here rather than in pkg/mcp because this package already imports
// pkg/mcp for the client half — the reverse edge would be an import cycle.
// pkg/mcp owns the protocol and the trust framing; this owns agent
// construction. Hosts embedding evva can substitute their own spawner
// without touching either.

// PersonaSpawner runs personas headlessly for an MCP server, one fresh agent
// per call. Build it with NewPersonaSpawner.
type PersonaSpawner struct {
	base Config

	// registry is resolved once, at construction, so a malformed agents/
	// directory fails at startup rather than on some later call. It also
	// gives Personas() and RunPersona() the same view — a persona that
	// validated at startup cannot vanish before it is invoked.
	registry *AgentRegistry

	// mu guards nothing about the agents themselves (each call builds its
	// own); it serialises the warnings slice for callers that read it.
	mu       sync.Mutex
	warnings []string
}

// NewPersonaSpawner builds a spawner over base. base supplies the provider,
// model, permission stance and AppConfig every served persona inherits; its
// Persona field is ignored, since each call names its own.
//
// Registry load warnings (a malformed on-disk persona, say) are returned
// rather than logged, so the caller decides whether they are fatal.
func NewPersonaSpawner(base Config) (*PersonaSpawner, []string) {
	reg := base.Personas
	var warns []string
	if reg == nil {
		home := ""
		if base.AppConfig != nil {
			home = base.AppConfig.AppHome
		}
		reg, warns = BuildAgentRegistry(home)
	}
	return &PersonaSpawner{base: base, registry: reg, warnings: warns}, warns
}

// Personas lists the main-tier personas this spawner can run.
func (s *PersonaSpawner) Personas() []mcp.PersonaInfo {
	defs := s.registry.ListMain()
	out := make([]mcp.PersonaInfo, 0, len(defs))
	for _, d := range defs {
		out = append(out, mcp.PersonaInfo{Name: d.Name, WhenToUse: d.WhenToUse})
	}
	return out
}

// RunPersona runs one turn of persona against prompt and returns its final
// answer.
//
// A fresh agent — and therefore a fresh session — is built per call. That is
// the v1 contract, not an implementation detail: concurrent MCP calls must not
// see each other's conversation, and an external caller must not be able to
// accumulate state inside the operator's evva across calls.
//
// No sink is installed, so the default brokers auto-deny anything the persona
// asks approval for. Under the default permission mode that leaves the
// read-only tool set working and everything dangerous denied — the intended
// posture for a caller who is not the operator.
func (s *PersonaSpawner) RunPersona(ctx context.Context, persona, prompt string) (string, error) {
	if _, ok := s.registry.Get(persona); !ok {
		return "", fmt.Errorf("unknown persona %q", persona)
	}

	cfg := s.base
	cfg.Persona = persona
	cfg.Personas = s.registry

	ag, err := New(cfg, WithRootContext(ctx))
	if err != nil {
		return "", fmt.Errorf("build persona %q: %w", persona, err)
	}
	defer ag.Shutdown()

	// A persona that hits its iteration cap has not failed — it paused. There
	// is nobody to press Enter over MCP, so report the cap plainly instead of
	// hanging or pretending the partial answer is final.
	answer, err := ag.Run(ctx, prompt)
	if err != nil {
		return "", err
	}
	return answer, nil
}

// Warnings returns any registry load warnings from construction.
func (s *PersonaSpawner) Warnings() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.warnings...)
}
