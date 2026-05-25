package agent

import (
	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/skills/bundled"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/skill"
)

// loadDiskSkillRegistry loads the merged home+workdir skill registry from
// the directories advertised by cfg. Empty / missing dirs produce an empty
// registry (the normal first-launch state). nil cfg yields an empty
// registry too — callers that want an explicit empty catalog can pass
// skill.NewRegistry() via WithSkillRegistry to skip the load entirely.
//
// Used both by Main() (when its skills arg is nil — sysprompt fallback)
// and by agent.New (when no registry was injected — SKILL tool fallback).
// Two reads per agent boot is acceptable for startup; deduping would
// require threading a pre-built registry through Profile, which couples
// the public Profile API to skill.Registry.
//
// After the disk catalog loads, evva's first-party bundled skills are
// overlaid via bundled.Register. Bundled is the lowest-precedence tier, so
// a same-named disk skill (Home or WorkDir) silently wins; bundled warnings
// (e.g. a malformed embedded title) join reg.Warnings so callers surface
// them on the agent logger exactly like disk-load warnings.
func loadDiskSkillRegistry(cfg *config.Config) *skill.Registry {
	if cfg == nil {
		return skill.NewRegistry()
	}
	reg, _ := skill.LoadRegistry(cfg.AppHomeSkillsDir, cfg.WorkDirSkillsDir)
	reg.Warnings = append(reg.Warnings, bundled.Register(reg)...)
	return reg
}

// refsFromRegistry flattens a *skill.Registry into the sysprompt-facing
// SkillRef slice. Returns nil for a nil/empty registry so the prompt
// builder's "no skills" branch fires cleanly.
//
// Lives in internal/agent/ (not in sysprompt) because sysprompt has no
// dependency on pkg/skill — the SkillRef is a flat copy of the
// LLM-facing fields.
func refsFromRegistry(r *skill.Registry) []sysprompt.SkillRef {
	if r == nil {
		return nil
	}
	list := r.List()
	if len(list) == 0 {
		return nil
	}
	out := make([]sysprompt.SkillRef, 0, len(list))
	for _, m := range list {
		out = append(out, sysprompt.SkillRef{Name: m.Name, Description: m.Description})
	}
	return out
}
