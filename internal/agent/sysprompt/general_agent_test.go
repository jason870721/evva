package sysprompt

import (
	"strings"
	"testing"
)

func TestGeneralAgent_HasSharedPrefix(t *testing.T) {
	got := buildGeneralPrompt(PromptContext{})
	for _, want := range []string{
		"You are an agent for evva",
		"don't gold-plate",
		"Your strengths:",
		"Guidelines:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q from general agent prompt", want)
		}
	}
}

func TestGeneralAgent_NoMemorySection(t *testing.T) {
	ctx := PromptContext{
		WorkdirMemory:    "do-not-leak-project",
		MemoryIndex:      "do-not-leak-index",
		EnableAutoMemory: true,
	}
	got := buildGeneralPrompt(ctx)
	if strings.Contains(got, "do-not-leak") {
		t.Errorf("General prompt leaked memory content")
	}
}

func TestGeneralAgent_MentionsRead(t *testing.T) {
	got := buildGeneralPrompt(PromptContext{})
	if !strings.Contains(got, "`read`") {
		t.Errorf("General agent should mention the read tool by name")
	}
}

func TestGeneralAgent_NoSkillsSection(t *testing.T) {
	// AdvertiseSkills: false — the builder must ignore threaded-in Skills.
	got := buildGeneralPrompt(PromptContext{Skills: []SkillRef{{Name: "commit", Description: "y"}}})
	if strings.Contains(got, "# Skills") {
		t.Errorf("General subagent prompt must not contain a # Skills section")
	}
}
