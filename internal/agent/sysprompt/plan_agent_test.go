package sysprompt

import (
	"strings"
	"testing"
)

// The Plan subagent is a read-only architecture/planning specialist.
// The prompt must (a) declare the read-only constraint clearly, (b)
// describe the 4-phase process, and (c) require the "Critical Files
// for Implementation" output section so the parent agent gets a
// structured handoff.
func TestPlanAgent_PromptContainsKeySections(t *testing.T) {
	got := buildPlanPrompt(PromptContext{})

	for _, want := range []string{
		"software architect and planning specialist for evva",
		"READ-ONLY MODE - NO FILE MODIFICATIONS",
		"## Your Process",
		"## Required Output",
		"Critical Files for Implementation",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan prompt missing %q", want)
		}
	}
}

// PlanAgent must register as a subagent (visible via the AGENT tool's
// subagent_type enum) and NOT as a main-tier persona.
func TestPlanAgent_RegistersAsSubagentOnly(t *testing.T) {
	if !PlanAgent.IsSubagent() {
		t.Errorf("PlanAgent must be a subagent")
	}
	if PlanAgent.IsMain() {
		t.Errorf("PlanAgent must NOT be a main-tier persona")
	}
	if !PlanAgent.OmitMemory {
		t.Errorf("PlanAgent should omit memory injection (parent has full context)")
	}
}

func TestPlanAgent_NoSkillsSection(t *testing.T) {
	// AdvertiseSkills: false — the builder must ignore threaded-in Skills.
	got := buildPlanPrompt(PromptContext{Skills: []SkillRef{{Name: "commit", Description: "y"}}})
	if strings.Contains(got, "# Skills") {
		t.Errorf("Plan subagent prompt must not contain a # Skills section")
	}
}
