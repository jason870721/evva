package sysprompt_test

// End-to-end: write tempdir EVVA.md + USER_PROFILE.md, load via memdir,
// build main prompt, assert both bodies render under their headings. Proves
// the memdir → PromptContext → MainAgent.BuildSystemPrompt wiring works.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
)

func TestMemdir_LoadsIntoMainPrompt(t *testing.T) {
	workdir := t.TempDir()
	evvaHome := t.TempDir()

	writeFile(t, filepath.Join(workdir, memdir.ProjectMemoryFile),
		"## Repo rules\n- Use gofmt.\n- Table-driven tests preferred.")
	writeFile(t, filepath.Join(evvaHome, memdir.UserProfileFile),
		"## Preferences\n- Terse output.")

	snap := memdir.Load(workdir, evvaHome)
	if len(snap.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", snap.Warnings)
	}

	ctx := sysprompt.PromptContext{
		AgentName:     "evva",
		OS:            "darwin",
		WorkDir:       workdir,
		EvvaHome:      evvaHome,
		Env:           "prod",
		ProjectMemory: snap.ProjectMemory,
		UserProfile:   snap.UserProfile,
	}
	prompt := sysprompt.MainAgent.BuildSystemPrompt(ctx)

	for _, want := range []string{
		"# Project memory (from EVVA.md)",
		"## Repo rules",
		"- Use gofmt.",
		"# User profile (from USER_PROFILE.md)",
		"## Preferences",
		"- Terse output.",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("rendered prompt missing %q\nfull:\n%s", want, prompt)
		}
	}
}

func TestMemdir_AbsentFilesSkipHeadings(t *testing.T) {
	workdir := t.TempDir()
	evvaHome := t.TempDir()

	snap := memdir.Load(workdir, evvaHome)
	if snap.ProjectMemory != "" || snap.UserProfile != "" {
		t.Fatalf("expected empty snapshot; got %+v", snap)
	}

	ctx := sysprompt.PromptContext{
		AgentName: "evva",
		OS:        "darwin",
		WorkDir:   workdir,
		EvvaHome:  evvaHome,
		Env:       "prod",
	}
	prompt := sysprompt.MainAgent.BuildSystemPrompt(ctx)

	for _, banned := range []string{"Project memory", "User profile"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("prompt should not contain %q when memory files are absent", banned)
		}
	}
}

func TestMemdir_ExploreSubagentIgnoresMemoryEvenIfThreaded(t *testing.T) {
	// Belt-and-suspenders: even if a caller wires memory into an Explore
	// PromptContext by mistake, the Explore builder's hand-written prompt
	// has no memory section at all, so nothing leaks.
	ctx := sysprompt.PromptContext{
		ProjectMemory: "secret-from-evva-md",
		UserProfile:   "secret-from-user-profile",
	}
	prompt := sysprompt.ExploreAgent.BuildSystemPrompt(ctx)
	if strings.Contains(prompt, "secret-from") {
		t.Errorf("Explore subagent leaked memory content into its prompt")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
