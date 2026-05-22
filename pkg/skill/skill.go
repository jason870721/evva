package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/johnny1110/evva/pkg/tools"
)

// Names lists every tool name this package contributes. Callers compose this
// into a profile's ActiveTools list the same way they do fs.Names() etc.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.SKILL}
}

// Lookup is the late-binding shape NewSkill accepts. The host loads the
// registry at startup (LoadRegistry or NewRegistry+Add) and installs it on
// the agent's ToolState; the SkillTool reads it through Lookup at Execute
// time so construction order between the tool and the registry doesn't
// matter — mirrors meta.SpawnerLookup and meta.DeferredLookup.
type Lookup func() *Registry

// SkillTool is the LLM-facing tool that invokes a user-installed skill by
// name. Execute returns the SKILL.md body (or BodyFunc output) wrapped as
// an instruction block so the model treats it as guidance to follow, not
// raw content to summarize.
type SkillTool struct {
	lookup Lookup
}

// NewSkill constructs a SkillTool that reads its registry via lookup at
// Execute time. lookup may be nil (yields a clear runtime error if the
// model invokes the tool); it may also return nil (same outcome).
func NewSkill(lookup Lookup) *SkillTool {
	return &SkillTool{lookup: lookup}
}

func (t *SkillTool) Name() string { return string(tools.SKILL) }

func (t *SkillTool) Description() string {
	return "Execute a user-installed skill within the main conversation. " +
		"Skills are Markdown documents under <AppHome>/skills/<name>/SKILL.md " +
		"(or <workdir>/.evva/skills/<name>/SKILL.md, which overrides) that " +
		"give the agent task-specific instructions. " +
		"Set `skill` to the exact name from the available-skills list; " +
		"set `args` to pass any free-form arguments. " +
		"The tool returns the skill's instructions for you to follow on the next turn."
}

func (t *SkillTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["skill"],
		"properties":{
			"skill":{"type":"string","description":"The name of a skill from the available-skills list. Do not guess names."},
			"args":{"type":"string","description":"Optional free-form arguments passed to the skill"}
		}
	}`)
}

type skillInput struct {
	Skill string `json:"skill"`
	Args  string `json:"args"`
}

func (t *SkillTool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	var in skillInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("skill: decode: %v", err)}, nil
	}
	name := strings.TrimSpace(in.Skill)
	if name == "" {
		return tools.Result{IsError: true, Content: "skill: `skill` is required"}, nil
	}
	logger.Debug("skill.dispatch", "name", name, "has_args", in.Args != "")
	if t.lookup == nil {
		return tools.Result{IsError: true, Content: "skill: no registry lookup configured"}, nil
	}
	reg := t.lookup()
	if reg == nil {
		return tools.Result{IsError: true, Content: "skill: no skills are installed"}, nil
	}
	if _, ok := reg.Get(name); !ok {
		avail := strings.Join(reg.Names(), ", ")
		if avail == "" {
			avail = "(none)"
		}
		logger.Warn("skill.unknown", "name", name)
		return tools.Result{
			IsError: true,
			Content: fmt.Sprintf("skill: %q not found; available: %s", name, avail),
		}, nil
	}
	body, err := reg.LoadBody(name)
	if err != nil {
		logger.Warn("skill.fail", "name", name, "stage", "load_body", "err", err)
		return tools.Result{IsError: true, Content: fmt.Sprintf("skill: load body: %v", err)}, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Follow these instructions for skill `%s`:\n\n", name)
	b.WriteString(strings.TrimRight(body, "\n"))
	if args := strings.TrimSpace(in.Args); args != "" {
		b.WriteString("\n\nargs: ")
		b.WriteString(args)
	}
	return tools.Result{Content: b.String()}, nil
}
