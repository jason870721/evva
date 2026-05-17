package agent

import (
	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/tools/skill"
)

// Option mutates an Agent during construction. Options are applied after the
// profile is materialized but before the LLM client is initialized so any
// option can influence either side without ordering surprises.
type Option func(*Agent)

// WithSink installs the event consumer. nil sinks become event.Discard at
// emit-time; pass event.Multi{...} to fan out to several consumers.
func WithSink(s event.Sink) Option {
	return func(a *Agent) {
		a.sink = s
	}
}

func WithName(name string) Option {
	return func(a *Agent) {
		a.Name = name
	}
}

func WithAsync(async bool) Option {
	return func(a *Agent) {
		a.asyncMode = async
	}
}

// WithStream toggles streaming completions for this agent. Overrides the
// Profile's Stream field; useful for tests and one-off callers that want to
// force the buffered or chunked path without editing the profile.
func WithStream(stream bool) Option {
	return func(a *Agent) {
		a.profile.Stream = stream
	}
}

// WithSkillRegistry installs the merged skill catalog on the agent's
// ToolState before the first turn. The SKILL tool reads through this
// registry at Execute time; passing nil leaves the SKILL tool with no
// skills available.
//
// The same pointer is shared with subagents when the spawner forwards
// it explicitly — today subagent profiles omit SKILL, so this is
// primarily a root-agent concern.
func WithSkillRegistry(r *skill.Registry) Option {
	return func(a *Agent) {
		a.toolState.SetSkillRegistry(r)
	}
}

// WithMaxIterations overrides DefaultMaxIterations. Pass 0 to use the
// default. Negative values are clamped to 1 (single-turn).
func WithMaxIterations(n int) Option {
	cfg := config.Get()

	return func(a *Agent) {
		switch {
		case n == 0:
			a.maxIters.Store(int64(cfg.DefaultMaxIterations))
		case n < 0:
			a.maxIters.Store(1)
		default:
			a.maxIters.Store(int64(n))
		}
	}
}
