package sysprompt

import (
	"strings"
	"testing"
)

func TestExploreAgent_DeclaresReadOnly(t *testing.T) {
	got := buildExplorePrompt(PromptContext{})
	for _, want := range []string{
		"READ-ONLY",
		"STRICTLY PROHIBITED",
		"You do NOT have access to file editing tools",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing required banner %q", want)
		}
	}
}

func TestExploreAgent_NoMemorySection(t *testing.T) {
	// Even when callers accidentally pass memory through PromptContext, the
	// Explore builder must ignore it. AgentDefinition.OmitMemory: true is
	// the contract; this test ensures the builder honors it structurally
	// (subagent prompts do not have a memory section at all).
	ctx := PromptContext{
		WorkdirMemory:    "should-not-appear",
		MemoryIndex:      "should-not-appear-either",
		EnableAutoMemory: true,
	}
	got := buildExplorePrompt(ctx)
	if strings.Contains(got, "should-not-appear") {
		t.Errorf("Explore prompt leaked memory content")
	}
	if strings.Contains(got, "Project memory") || strings.Contains(got, "Memory index") {
		t.Errorf("Explore prompt should not have memory headings")
	}
}

func TestExploreAgent_MentionsReadGrepTreeBash(t *testing.T) {
	got := buildExplorePrompt(PromptContext{})
	for _, want := range []string{"`read`", "`grep`", "`tree`", "`bash`"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected tool reference %q in Explore prompt", want)
		}
	}
}

func TestExploreAgent_NoTaskPlanningOrSkills(t *testing.T) {
	got := buildExplorePrompt(PromptContext{Skills: []SkillRef{{Name: "x", Description: "y"}}})
	for _, banned := range []string{"# Multi-step work", "# Skills", "task_create"} {
		if strings.Contains(got, banned) {
			t.Errorf("Explore should not include %q", banned)
		}
	}
}
