package loader

import (
	"fmt"
	"os"

	"github.com/johnny1110/evva/pkg/tools"
	"gopkg.in/yaml.v3"
)

// toolsYml is the on-disk schema for <agent>/tools.yml.
type toolsYml struct {
	Active   []tools.ToolName `yaml:"active"`
	Deferred []tools.ToolName `yaml:"deferred"`
}

// metaYml is the on-disk schema for <agent>/meta.yml.
type metaYml struct {
	As        []string `yaml:"as"`
	Model     string   `yaml:"model"`
	WhenToUse string   `yaml:"when_to_use"`

	// InjectMemory, when true, injects the workdir EVVA.md + USER_PROFILE.md
	// snapshot into this agent's system prompt at session start. Default
	// false matches the legacy behavior — opt-in keeps disk personas minimal
	// unless the author explicitly wants project / user context.
	InjectMemory bool `yaml:"inject_memory"`

	// AdvertiseSkills, when true, surfaces the user-installed skill catalog
	// to this agent. Default false. Built-in evva flips this to true via
	// its Go-defined AgentDefinition; disk personas opt in here.
	AdvertiseSkills bool `yaml:"advertise_skills"`

	// OutputStyle optionally pins this persona's output style (a teaching
	// persona defaulting to "Learning", say). When set it wins over the
	// user's configured output_style while this persona is active. Empty =
	// follow the user's config.
	OutputStyle string `yaml:"output_style"`
}

func readToolsYml(path string) (toolsYml, error) {
	var out toolsYml
	b, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read tools.yml: %w", err)
	}
	if err := yaml.Unmarshal(b, &out); err != nil {
		return out, fmt.Errorf("parse tools.yml: %w", err)
	}
	return out, nil
}

func readMetaYml(path string) (metaYml, error) {
	var out metaYml
	b, err := os.ReadFile(path)
	if err != nil {
		return out, fmt.Errorf("read meta.yml: %w", err)
	}
	if err := yaml.Unmarshal(b, &out); err != nil {
		return out, fmt.Errorf("parse meta.yml: %w", err)
	}
	return out, nil
}
