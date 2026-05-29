package sysprompt_test

// End-to-end: write tempdir EVVA.md + memory/MEMORY.md (+ an individual memory
// file), load via memdir, build the main prompt, and assert the typed-memory
// guidance + the index render — while the individual memory BODY does NOT leak
// into the static prompt (A4). Proves the memdir → PromptContext →
// MainAgent.BuildSystemPrompt wiring for the typed-memory directory.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/internal/agent/sysprompt"
	"github.com/johnny1110/evva/internal/memdir"
)

func TestMemdir_IndexAndGuidanceRenderBodiesDoNot(t *testing.T) {
	workdir := t.TempDir()
	evvaHome := t.TempDir()

	writeFile(t, filepath.Join(workdir, memdir.ProjectMemoryFile),
		"## Repo rules\n- Use gofmt.")
	// The model-maintained index (always injected into the static prompt).
	writeFile(t, memdir.MemoryIndexPath(evvaHome),
		"- [no-db-mocks](feedback/no-db-mocks.md) — integration tests hit a real DB")
	// An individual memory body that must NOT appear in the static prompt (A4);
	// it is only ever surfaced per-turn via the recall <system-reminder>.
	writeFile(t, filepath.Join(memdir.MemoryDir(evvaHome), "feedback", "no-db-mocks.md"),
		"---\nname: no-db-mocks\ndescription: real DB only\ntype: feedback\n---\nSECRET_BODY_DO_NOT_INJECT")

	snap := memdir.Load(workdir, evvaHome, true)
	if snap.MemoryDir == "" {
		t.Fatal("MemoryDir should be set when auto-memory is on")
	}

	ctx := sysprompt.PromptContext{
		AgentName:        "evva",
		OS:               "darwin",
		WorkDir:          workdir,
		EvvaHome:         evvaHome,
		Env:              "prod",
		WorkdirMemory:    snap.WorkdirMemory,
		MemoryIndex:      snap.MemoryIndex,
		EnableAutoMemory: true,
	}
	prompt := sysprompt.MainAgent.BuildSystemPrompt(ctx)

	for _, want := range []string{
		"# Project memory (from EVVA.md)",
		"## Repo rules",
		"# Memory",         // typed-memory guidance block (A11)
		"## Types of memory",
		"# Memory index (from " + evvaHome + "/memory/MEMORY.md)", // the index section (A4)
		"no-db-mocks.md",   // the index line
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("rendered prompt missing %q", want)
		}
	}
	// A4: only the index, never an individual memory body, in the static prompt.
	if strings.Contains(prompt, "SECRET_BODY_DO_NOT_INJECT") {
		t.Errorf("individual memory body leaked into the static system prompt")
	}
	// The deleted two-store model must be gone from the prompt.
	if strings.Contains(prompt, "USER_PROFILE.md") || strings.Contains(prompt, "update_user_profile") {
		t.Errorf("prompt still references the deleted user-profile model")
	}
}

func TestMemdir_AutoMemoryOffSuppressesMemorySections(t *testing.T) {
	workdir := t.TempDir()
	evvaHome := t.TempDir()
	writeFile(t, memdir.MemoryIndexPath(evvaHome), "- [x](x.md) — hook")

	// Auto-memory off: Load doesn't read the index or create the dir, and the
	// prompt gate suppresses both the guidance and the index (A9).
	snap := memdir.Load(workdir, evvaHome, false)
	if snap.MemoryIndex != "" || snap.MemoryDir != "" {
		t.Fatalf("auto-memory off should yield empty MemoryIndex/MemoryDir; got %+v", snap)
	}
	ctx := sysprompt.PromptContext{
		AgentName:        "evva",
		EvvaHome:         evvaHome,
		Env:              "prod",
		MemoryIndex:      snap.MemoryIndex,
		EnableAutoMemory: false,
	}
	prompt := sysprompt.MainAgent.BuildSystemPrompt(ctx)
	for _, banned := range []string{"# Memory index", "## Types of memory"} {
		if strings.Contains(prompt, banned) {
			t.Errorf("auto-memory off: prompt should not contain %q", banned)
		}
	}
}

func TestMemdir_ExploreSubagentIgnoresMemory(t *testing.T) {
	// Belt-and-suspenders: even if a caller wires memory into an Explore
	// PromptContext by mistake, the Explore builder's hand-written prompt has no
	// memory section at all, so nothing leaks.
	ctx := sysprompt.PromptContext{
		WorkdirMemory:    "secret-from-evva-md",
		MemoryIndex:      "secret-from-index",
		EnableAutoMemory: true,
	}
	prompt := sysprompt.ExploreAgent.BuildSystemPrompt(ctx)
	if strings.Contains(prompt, "secret-from") {
		t.Errorf("Explore subagent leaked memory content into its prompt")
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
