package agent

import (
	"sort"
	"sync"

	"github.com/johnny1110/evva/internal/agent/loader"
	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/pkg/tools"
)

// AgentSpec is the closure-free view of an agent definition. The public
// pkg/agent registry constructs one from its public AgentDefinition DTO and
// hands it to DefinitionFromSpec, so pkg/agent never imports the sysprompt
// package. SystemPrompt is the full prompt body — wrapped in the same static
// closure disk-loaded agents use (see loader.loadOne).
type AgentSpec struct {
	Name            string
	WhenToUse       string
	As              []string
	InjectMemory    bool
	AdvertiseSkills bool
	ActiveTools     []tools.ToolName
	DeferredTools   []tools.ToolName
	Model           string
	SystemPrompt    string
	LongRunning     bool
}

// DefinitionFromSpec builds a sysprompt.AgentDefinition from a closure-free
// AgentSpec. The system prompt body is captured by value into a static
// BuildSystemPrompt closure (the disk-agent pattern); InjectMemory maps to the
// internal OmitMemory flag. Used by the public pkg/agent.AgentRegistry.
func DefinitionFromSpec(spec AgentSpec) sysprompt.AgentDefinition {
	body := spec.SystemPrompt
	return sysprompt.AgentDefinition{
		Name:              spec.Name,
		WhenToUse:         spec.WhenToUse,
		OmitMemory:        !spec.InjectMemory,
		AdvertiseSkills:   spec.AdvertiseSkills,
		BuildSystemPrompt: func(_ sysprompt.PromptContext) string { return body },
		As:                spec.As,
		ActiveTools:       spec.ActiveTools,
		DeferredTools:     spec.DeferredTools,
		Model:             spec.Model,
		PromptBody:        body,
		LongRunning:       spec.LongRunning,
	}
}

// SpecFromDefinition is the inverse of DefinitionFromSpec: a closure-free view
// of a definition for the public registry. SystemPrompt is recovered from
// PromptBody (empty for built-ins, whose prompt is assembled internally and
// isn't reconstructable as a single string).
func SpecFromDefinition(def sysprompt.AgentDefinition) AgentSpec {
	return AgentSpec{
		Name:            def.Name,
		WhenToUse:       def.WhenToUse,
		As:              def.As,
		InjectMemory:    !def.OmitMemory,
		AdvertiseSkills: def.AdvertiseSkills,
		ActiveTools:     def.ActiveTools,
		DeferredTools:   def.DeferredTools,
		Model:           def.Model,
		SystemPrompt:    def.PromptBody,
		LongRunning:     def.LongRunning,
	}
}

func specsFromDefs(defs []sysprompt.AgentDefinition) []AgentSpec {
	out := make([]AgentSpec, len(defs))
	for i, d := range defs {
		out[i] = SpecFromDefinition(d)
	}
	return out
}

// GetSpec returns the closure-free spec for name. Powers the public registry's
// Get without exposing sysprompt.AgentDefinition.
func (r *AgentRegistry) GetSpec(name string) (AgentSpec, bool) {
	def, ok := r.Get(name)
	if !ok {
		return AgentSpec{}, false
	}
	return SpecFromDefinition(def), true
}

// ListMainSpecs / ListSubagentSpecs are the closure-free counterparts of
// ListMain / ListSubagent for the public registry.
func (r *AgentRegistry) ListMainSpecs() []AgentSpec     { return specsFromDefs(r.ListMain()) }
func (r *AgentRegistry) ListSubagentSpecs() []AgentSpec { return specsFromDefs(r.ListSubagent()) }

// LoadDiskAgents reads the on-disk personas under <evvaHome>/agents/ as
// closure-free specs (no built-ins). Wraps loader.Load for the public
// pkg/agent.LoadDiskAgents.
func LoadDiskAgents(evvaHome string) ([]AgentSpec, []loader.Warning) {
	defs, warns := loader.Load(evvaHome)
	return specsFromDefs(defs), warns
}

// AgentRegistry holds every agent definition known to the runtime —
// Go-defined built-ins (sysprompt.MainAgent, ExploreAgent, GeneralAgent)
// merged with disk-loaded definitions from <EVVA_HOME>/agents/{name}/.
//
// Phase 6 will consume this registry from two sides:
//   - The /profile slash command picker lists agents with ListMain().
//   - The Agent tool's subagent_type schema enum becomes the union of every
//     agent in ListSubagent().
//
// Phase 2 plants the registry and routes subagent resolution through it,
// but the Agent tool's schema enum stays hardcoded — disk-loaded subagents
// will load into the registry but won't be wire-callable until Phase 6
// flips the schema.
type AgentRegistry struct {
	mu   sync.RWMutex
	defs map[string]sysprompt.AgentDefinition
}

// NewAgentRegistry returns an empty registry. Most callers want
// BuildAgentRegistry, which pre-populates built-ins + disk agents.
func NewAgentRegistry() *AgentRegistry {
	return &AgentRegistry{defs: map[string]sysprompt.AgentDefinition{}}
}

// Register adds (or overwrites) a definition. Returns the previous value
// and a bool indicating whether a definition with this name already existed
// — callers (the loader merge step) can warn on duplicates so a disk agent
// silently shadowing a built-in is visible at startup.
func (r *AgentRegistry) Register(def sysprompt.AgentDefinition) (sysprompt.AgentDefinition, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev, ok := r.defs[def.Name]
	r.defs[def.Name] = def
	return prev, ok
}

// Get returns the definition for name. Lookup is case-insensitive to match
// the AGENT tool's subagent_type behavior — "Explore", "explore", "EXPLORE"
// all resolve to the same agent.
func (r *AgentRegistry) Get(name string) (sysprompt.AgentDefinition, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if def, ok := r.defs[name]; ok {
		return def, true
	}
	for k, def := range r.defs {
		if equalFold(k, name) {
			return def, true
		}
	}
	return sysprompt.AgentDefinition{}, false
}

// All returns every definition sorted by name. Useful for diagnostics and
// the Phase 6 picker enumeration paths.
func (r *AgentRegistry) All() []sysprompt.AgentDefinition {
	r.mu.RLock()
	out := make([]sysprompt.AgentDefinition, 0, len(r.defs))
	for _, d := range r.defs {
		out = append(out, d)
	}
	r.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ListMain returns every definition whose `as:` includes "main" — the
// candidates the Phase 6 /profile picker shows.
func (r *AgentRegistry) ListMain() []sysprompt.AgentDefinition {
	out := r.All()
	filtered := out[:0]
	for _, d := range out {
		if d.IsMain() {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// ListSubagent returns every definition whose `as:` includes "subagent" —
// the candidates Phase 6 will surface in the Agent tool's dynamic enum.
func (r *AgentRegistry) ListSubagent() []sysprompt.AgentDefinition {
	out := r.All()
	filtered := out[:0]
	for _, d := range out {
		if d.IsSubagent() {
			filtered = append(filtered, d)
		}
	}
	return filtered
}

// BuildAgentRegistry assembles the runtime registry: built-ins first, then
// disk agents from <evvaHome>/agents/. Disk agents that collide with a
// built-in name are skipped with a warning — built-ins always win so a
// typo in a disk agent's directory name can't silently replace `explore`.
//
// Returns the registry and any warnings the host should log. Never returns
// an error: a missing or malformed disk catalog degrades gracefully.
func BuildAgentRegistry(evvaHome string) (*AgentRegistry, []loader.Warning) {
	r := NewAgentRegistry()
	// Built-ins always present.
	r.Register(sysprompt.MainAgent)
	r.Register(sysprompt.ExploreAgent)
	r.Register(sysprompt.GeneralAgent)
	r.Register(sysprompt.PlanAgent)

	defs, warns := loader.Load(evvaHome)
	for _, def := range defs {
		if _, exists := r.Get(def.Name); exists {
			warns = append(warns, loader.Warning{
				Agent: def.Name,
				Err:   errShadowsBuiltin,
			})
			continue
		}
		r.Register(def)
	}
	return r, warns
}

// errShadowsBuiltin is the sentinel used in warnings when a disk agent
// would shadow a Go-defined built-in. Lifted to a var so callers can
// errors.Is against it if they need to filter warnings.
var errShadowsBuiltin = shadowsBuiltinError("disk agent shadows a built-in of the same name; built-in wins")

type shadowsBuiltinError string

func (e shadowsBuiltinError) Error() string { return string(e) }

// equalFold avoids pulling strings into this file just for one call.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
