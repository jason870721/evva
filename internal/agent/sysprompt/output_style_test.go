package sysprompt

import (
	"strings"
	"testing"
)

// Tests for the output-style overlay in buildMainPrompt /
// ComposeDiskMainPrompt: default no-op (A7), append mode (A3), replace
// mode dropping ONLY the doing-tasks doctrine (A4, ref's
// keepCodingInstructions === true check), placement, and the disk-persona
// composition (A9's prompt half).

func styledCtx(keep bool) PromptContext {
	ctx := mainCtx()
	ctx.OutputStyleName = "pirate"
	ctx.OutputStylePrompt = "Speak like a pirate. STYLE-BODY-MARKER."
	ctx.OutputStyleKeepCoding = keep
	return ctx
}

func TestMainAgent_OutputStyle_DefaultIsNoOp(t *testing.T) {
	// Zero-value style fields must leave the prompt byte-identical to a
	// build that never knew the feature: coding intro, doctrine present,
	// no style section. Also true when only the name is set (the resolver
	// hands back Name="default", Prompt="").
	plain := buildMainPrompt(mainCtx())
	named := mainCtx()
	named.OutputStyleName = "default"
	if got := buildMainPrompt(named); got != plain {
		t.Fatal("default style with empty prompt must be byte-identical to the style-less build")
	}
	if strings.Contains(plain, "# Output Style:") {
		t.Error("style-less prompt must not carry an output-style section")
	}
	if !strings.Contains(plain, "# Doing tasks") {
		t.Error("style-less prompt lost the doing-tasks doctrine")
	}
	if !strings.Contains(plain, "an interactive coding agent for the terminal") {
		t.Error("style-less prompt lost the coding intro")
	}
}

func TestMainAgent_OutputStyle_AppendKeepsDoctrine(t *testing.T) {
	got := buildMainPrompt(styledCtx(true))
	for _, want := range []string{
		"# Output Style: pirate",
		"STYLE-BODY-MARKER",
		"# Doing tasks",
		`according to your "Output Style" below`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("append mode missing %q", want)
		}
	}
	if strings.Contains(got, "an interactive coding agent for the terminal") {
		t.Error("styled prompt should swap the coding intro for the output-style intro")
	}
}

func TestMainAgent_OutputStyle_ReplaceDropsOnlyDoingTasks(t *testing.T) {
	got := buildMainPrompt(styledCtx(false))
	if strings.Contains(got, "# Doing tasks") {
		t.Error("replace mode must drop the doing-tasks doctrine")
	}
	// Everything that is harness mechanics — not voice — stays.
	for _, want := range []string{
		"# Output Style: pirate",
		"# Executing actions with care",
		"# Tools",
		"# Tone and style",
		"# Communicating with the user",
		"# Multi-step work",
		"# Session-specific guidance",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("replace mode must keep %q", want)
		}
	}
}

func TestMainAgent_OutputStyle_PlacementAfterEnvBeforeSessionGuidance(t *testing.T) {
	got := buildMainPrompt(styledCtx(true))
	style := strings.Index(got, "# Output Style: pirate")
	env := strings.Index(got, "# Environment")
	session := strings.Index(got, "# Session-specific guidance")
	if style < env || style > session {
		t.Errorf("style section out of place: env=%d style=%d session=%d", env, style, session)
	}
}

func TestComposeDiskMainPrompt_OutputStyleAfterBody(t *testing.T) {
	ctx := styledCtx(true)
	def := AgentDefinition{Name: "tutor", As: []string{"main"}}
	got := ComposeDiskMainPrompt("PERSONA-BODY-MARKER", ctx, def)
	body := strings.Index(got, "PERSONA-BODY-MARKER")
	style := strings.Index(got, "# Output Style: pirate")
	if body < 0 || style < 0 {
		t.Fatalf("missing body (%d) or style (%d) in disk compose", body, style)
	}
	if style < body {
		t.Error("style overlay must render after the persona body so the user's voice choice wins")
	}
}

func TestComposeDiskMainPrompt_NoStyleIsNoOp(t *testing.T) {
	ctx := mainCtx()
	def := AgentDefinition{Name: "tutor", As: []string{"main"}}
	got := ComposeDiskMainPrompt("PERSONA-BODY-MARKER", ctx, def)
	if strings.Contains(got, "# Output Style:") {
		t.Error("style-less disk compose must not carry an output-style section")
	}
}
