package tools

import "fmt"

// Registry maps tool names to Tool implementations.
// Built once at startup and injected into the agent — never mutated after init.
type Registry struct {
	tools map[string]Tool
}

func NewRegistry(tools ...ToolName) (*Registry, error) {
	toolList, err := Build(tools)
	if err != nil {
		return nil, err
	}

	r := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range toolList {
		r.tools[t.Name()] = t
	}
	return r, nil
}

func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool %q not registered", name)
	}
	return t, nil
}

func (r *Registry) All() []Tool {
	out := make([]Tool, 0, len(r.tools))
	for _, t := range r.tools {
		out = append(out, t)
	}
	return out
}
