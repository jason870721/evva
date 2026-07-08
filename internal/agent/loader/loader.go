// Package loader reads user-authored agent definitions from
// <EVVA_HOME>/agents/{name}/ at startup and turns them into
// sysprompt.AgentDefinition values the agent.Registry can merge with
// Go-defined built-ins (sysprompt.MainAgent, ExploreAgent, GeneralAgent).
//
// Disk layout per agent:
//
//	<EVVA_HOME>/agents/{name}/
//	├── system_prompt.md   # required; full system prompt body
//	├── tools.yml          # required; { active: [...], deferred: [...] }
//	└── meta.yml           # required; { as: [...], model: "...", when_to_use: "..." }
//
// Behavior on bad input mirrors memdir.Load: invalid agents are skipped
// with a Warning, never an error. The session continues with whatever
// agents loaded cleanly.
package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
)

// Warning records a non-fatal problem encountered while loading agents.
// Hosts surface these via slog at startup so operators see what failed
// without the binary refusing to start.
type Warning struct {
	Agent string // directory name, may be "" if the failure was the scan itself
	Path  string // file involved in the warning, "" for directory-level issues
	Err   error
}

func (w Warning) Error() string {
	switch {
	case w.Agent != "" && w.Path != "":
		return fmt.Sprintf("agent %q (%s): %v", w.Agent, w.Path, w.Err)
	case w.Agent != "":
		return fmt.Sprintf("agent %q: %v", w.Agent, w.Err)
	default:
		return w.Err.Error()
	}
}

// Load walks <evvaHome>/agents/ and returns every successfully-parsed
// AgentDefinition along with any non-fatal warnings. evvaHome being empty
// or the directory not existing returns (nil, nil) — disk agents are
// optional.
func Load(evvaHome string) ([]sysprompt.AgentDefinition, []Warning) {
	if evvaHome == "" {
		return nil, nil
	}
	root := filepath.Join(evvaHome, "agents")
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []Warning{{Path: root, Err: fmt.Errorf("read agents dir: %w", err)}}
	}

	var defs []sysprompt.AgentDefinition
	var warns []Warning
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		// Hidden directories (`.git`, `.DS_Store`-style folders) are skipped
		// silently — they aren't agents the user authored.
		if strings.HasPrefix(name, ".") {
			continue
		}
		def, dirWarns := loadOne(root, name)
		warns = append(warns, dirWarns...)
		if def != nil {
			defs = append(defs, *def)
		}
	}
	return defs, warns
}

// loadOne reads one agent directory. Returns (nil, warns) if the agent is
// malformed (missing required file, parse error, empty system prompt).
func loadOne(root, name string) (*sysprompt.AgentDefinition, []Warning) {
	dir := filepath.Join(root, name)
	var warns []Warning

	promptPath := filepath.Join(dir, "system_prompt.md")
	promptBody, err := readRequired(promptPath)
	if err != nil {
		return nil, []Warning{{Agent: name, Path: promptPath, Err: err}}
	}
	if strings.TrimSpace(promptBody) == "" {
		return nil, []Warning{{Agent: name, Path: promptPath, Err: fmt.Errorf("system_prompt.md is empty")}}
	}

	toolsPath := filepath.Join(dir, "tools.yml")
	toolsCfg, err := readToolsYml(toolsPath)
	if err != nil {
		return nil, []Warning{{Agent: name, Path: toolsPath, Err: err}}
	}

	metaPath := filepath.Join(dir, "meta.yml")
	metaCfg, err := readMetaYml(metaPath)
	if err != nil {
		return nil, []Warning{{Agent: name, Path: metaPath, Err: err}}
	}
	if len(metaCfg.As) == 0 {
		return nil, []Warning{{Agent: name, Path: metaPath, Err: fmt.Errorf("meta.yml `as:` is required (must be one of [\"main\"], [\"subagent\"], or [\"main\",\"subagent\"])")}}
	}
	for _, v := range metaCfg.As {
		if v != "main" && v != "subagent" {
			return nil, []Warning{{Agent: name, Path: metaPath, Err: fmt.Errorf("meta.yml `as:` has invalid value %q (must be \"main\" or \"subagent\")", v)}}
		}
	}

	def := &sysprompt.AgentDefinition{
		Name:            name,
		WhenToUse:       metaCfg.WhenToUse,
		OmitMemory:      !metaCfg.InjectMemory,
		AdvertiseSkills: metaCfg.AdvertiseSkills,
		BuildSystemPrompt: func(promptBody string) func(sysprompt.PromptContext) string {
			body := promptBody // capture by value so future iterations don't alias
			return func(_ sysprompt.PromptContext) string { return body }
		}(promptBody),
		As:            metaCfg.As,
		ActiveTools:   toolsCfg.Active,
		DeferredTools: toolsCfg.Deferred,
		Model:         metaCfg.Model,
		OutputStyle:   metaCfg.OutputStyle,
		PromptBody:    promptBody,
	}
	return def, warns
}

// readRequired loads a required file. Missing or unreadable returns an error.
func readRequired(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return string(b), nil
}
