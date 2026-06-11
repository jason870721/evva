package sysprompt

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

// Tests for diskToolsGuideSection (RP-19): per-tool gating, protocol gating,
// bit-stability, and the two authoring invariants of the curated table
// (order coverage, self-containment). Compose-level integration lives in
// TestComposeDiskMainPrompt_* below.

func TestDiskToolsGuide_GatesPerTool(t *testing.T) {
	active := []tools.ToolName{tools.READ_FILE, tools.BASH, tools.HTTP_REQUEST}
	got := diskToolsGuideSection(active, nil)

	for _, n := range active {
		if !strings.Contains(got, "- `"+string(n)+"` — ") {
			t.Errorf("guide missing line for owned tool %q:\n%s", n, got)
		}
	}
	// Every other builtin must be absent — a mention of a tool the profile
	// doesn't admit invites a hallucinated call. Tool names are always
	// rendered backticked, so the backticked form is the precise probe
	// (bare words like "read" appear in ordinary prose).
	owned := map[tools.ToolName]bool{}
	for _, n := range active {
		owned[n] = true
	}
	for n := range toolGuidelines {
		if owned[n] {
			continue
		}
		if strings.Contains(got, "`"+string(n)+"`") {
			t.Errorf("guide mentions unowned tool %q:\n%s", n, got)
		}
	}
	if !strings.Contains(got, "Make independent tool calls in parallel") {
		t.Errorf("guide missing the always-on parallel-calls rule:\n%s", got)
	}
}

func TestDiskToolsGuide_DeferredProtocolGating(t *testing.T) {
	noDeferred := diskToolsGuideSection([]tools.ToolName{tools.READ_FILE}, nil)
	if strings.Contains(noDeferred, "## Deferred tools") {
		t.Errorf("deferred protocol must not render without deferred tools:\n%s", noDeferred)
	}

	withDeferred := diskToolsGuideSection([]tools.ToolName{tools.READ_FILE}, []tools.ToolName{tools.WEB_SEARCH})
	if !strings.Contains(withDeferred, "## Deferred tools and `tool_search`") {
		t.Errorf("deferred protocol missing when deferred tools exist:\n%s", withDeferred)
	}
	// tool_search is treated as owned whenever deferred is non-empty, even if
	// the caller forgot the auto-mount — the protocol teaches it by name, so
	// the section must stay self-consistent.
	if !strings.Contains(withDeferred, "- `tool_search` — ") {
		t.Errorf("tool_search line missing despite non-empty deferred:\n%s", withDeferred)
	}
	// The deferred tool itself gets its usage line too.
	if !strings.Contains(withDeferred, "- `web_search` — ") {
		t.Errorf("deferred tool's usage line missing:\n%s", withDeferred)
	}
}

func TestDiskToolsGuide_TodoProtocolGating(t *testing.T) {
	without := diskToolsGuideSection([]tools.ToolName{tools.READ_FILE}, nil)
	if strings.Contains(without, "## Multi-step work") {
		t.Errorf("todo protocol must not render without todo_write:\n%s", without)
	}
	with := diskToolsGuideSection([]tools.ToolName{tools.READ_FILE, tools.TODO_WRITE}, nil)
	if !strings.Contains(with, "## Multi-step work (`todo_write`)") {
		t.Errorf("todo protocol missing when todo_write is owned:\n%s", with)
	}
}

func TestDiskToolsGuide_EmptyToolsRendersNothing(t *testing.T) {
	if got := diskToolsGuideSection(nil, nil); got != "" {
		t.Errorf("no tools should render no section; got:\n%s", got)
	}
}

func TestDiskToolsGuide_UnknownNamesAreSkipped(t *testing.T) {
	// Swarm custom tools and MCP-discovered names have no curated entry;
	// they must not produce a line (their teaching lives elsewhere), but
	// generic mechanics still render.
	got := diskToolsGuideSection([]tools.ToolName{"send_message", "mcp__gh__create_issue"}, nil)
	if strings.Contains(got, "send_message") || strings.Contains(got, "mcp__gh__create_issue") {
		t.Errorf("unknown tool names must not be rendered:\n%s", got)
	}
	if !strings.Contains(got, "Make independent tool calls in parallel") {
		t.Errorf("generic mechanics should still render for custom-only toolsets:\n%s", got)
	}
}

func TestDiskToolsGuide_BitStable(t *testing.T) {
	// Same tool sets — regardless of declaration order — must produce
	// byte-identical text (RP-5 prompt-cache prefix stability).
	a := diskToolsGuideSection(
		[]tools.ToolName{tools.BASH, tools.READ_FILE, tools.TODO_WRITE},
		[]tools.ToolName{tools.EXCEL, tools.WEB_SEARCH},
	)
	b := diskToolsGuideSection(
		[]tools.ToolName{tools.TODO_WRITE, tools.BASH, tools.READ_FILE},
		[]tools.ToolName{tools.WEB_SEARCH, tools.EXCEL},
	)
	if a != b {
		t.Errorf("guide is not order-independent:\n--- a ---\n%s\n--- b ---\n%s", a, b)
	}
	if c := diskToolsGuideSection(
		[]tools.ToolName{tools.BASH, tools.READ_FILE, tools.TODO_WRITE},
		[]tools.ToolName{tools.EXCEL, tools.WEB_SEARCH},
	); a != c {
		t.Error("guide is not stable across repeated renders of the same input")
	}
}

func TestToolGuideOrder_MatchesTableExactly(t *testing.T) {
	seen := map[tools.ToolName]bool{}
	for _, n := range toolGuideOrder {
		if seen[n] {
			t.Errorf("toolGuideOrder lists %q twice", n)
		}
		seen[n] = true
		if _, ok := toolGuidelines[n]; !ok {
			t.Errorf("toolGuideOrder lists %q which has no toolGuidelines entry", n)
		}
	}
	for n := range toolGuidelines {
		if !seen[n] {
			t.Errorf("toolGuidelines entry %q missing from toolGuideOrder — its line would never render", n)
		}
	}
}

func TestToolGuidelines_SelfContained(t *testing.T) {
	// A guideline must never name ANOTHER tool in backticks: the other tool
	// may be absent from the persona's lists, and a backticked mention is
	// exactly the form the model reads as "callable".
	for own, text := range toolGuidelines {
		if strings.TrimSpace(text) == "" {
			t.Errorf("guideline for %q is empty", own)
		}
		for other := range toolGuidelines {
			if other == own {
				continue
			}
			if strings.Contains(text, "`"+string(other)+"`") {
				t.Errorf("guideline for %q names another tool %q — lines must be self-contained", own, other)
			}
		}
	}
}

func TestComposeDiskMainPrompt_GuideBeforeBodyAndCatalogAfter(t *testing.T) {
	ctx := PromptContext{
		AgentName:     "vero",
		OS:            "linux",
		Shell:         "zsh",
		WorkDir:       "/tmp",
		EvvaHome:      "/tmp/.evva",
		DeferredTools: []DeferredToolSpec{{Name: "web_search"}},
	}
	def := AgentDefinition{
		Name:        "vero",
		ActiveTools: []tools.ToolName{tools.READ_FILE, tools.BASH},
	}
	body := "You are vero, a careful analyst.\n\n# Working in a swarm\nprotocol text here."
	prompt := ComposeDiskMainPrompt(body, ctx, def)

	guideAt := strings.Index(prompt, "# Tools")
	bodyAt := strings.Index(prompt, "You are vero, a careful analyst.")
	// Probe the catalog by its section heading — the guide's protocol text
	// also says "<available-deferred-tools>" when pointing at the block.
	catalogAt := strings.Index(prompt, "# Available deferred tools")
	if guideAt < 0 || bodyAt < 0 || catalogAt < 0 {
		t.Fatalf("missing section (guide=%d body=%d catalog=%d):\n%s", guideAt, bodyAt, catalogAt, prompt)
	}
	if !(guideAt < bodyAt && bodyAt < catalogAt) {
		t.Errorf("section order wrong: want guide(%d) < body(%d) < catalog(%d)", guideAt, bodyAt, catalogAt)
	}
	if !strings.Contains(prompt, "<available-deferred-tools>\nweb_search\n</available-deferred-tools>") {
		t.Errorf("deferred catalog should list web_search:\n%s", prompt)
	}
	if !strings.Contains(prompt, body) {
		t.Error("persona body must be embedded verbatim")
	}
}

func TestComposeDiskMainPrompt_NoToolsNoGuideNoCatalog(t *testing.T) {
	ctx := PromptContext{AgentName: "p", OS: "linux", WorkDir: "/tmp"}
	def := AgentDefinition{Name: "p"}
	prompt := ComposeDiskMainPrompt("persona body", ctx, def)
	if strings.Contains(prompt, "# Tools") {
		t.Errorf("tool-less persona should have no tools guide:\n%s", prompt)
	}
	if strings.Contains(prompt, "<available-deferred-tools>") {
		t.Errorf("tool-less persona should have no deferred catalog:\n%s", prompt)
	}
}
