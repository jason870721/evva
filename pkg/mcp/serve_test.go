package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
)

func TestLoadServeConfigAbsentIsDormant(t *testing.T) {
	cfg, warns := LoadServeConfig(t.TempDir(), t.TempDir())
	if len(warns) != 0 {
		t.Errorf("missing settings should not warn: %v", warns)
	}
	if len(cfg.Expose) != 0 {
		t.Errorf("nothing configured should expose nothing, got %v", cfg.Expose)
	}
	if cfg.Timeout != DefaultPersonaTimeout {
		t.Errorf("timeout = %v, want the default %v", cfg.Timeout, DefaultPersonaTimeout)
	}
}

func TestLoadServeConfigReadsBlock(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"mcpServe":{"expose":[{"kind":"persona","name":"explore"},{"kind":"TOOL","name":" grep "}],"timeout":90}}`)

	cfg, warns := LoadServeConfig("", home)
	if len(warns) != 0 {
		t.Fatalf("unexpected warnings: %v", warns)
	}
	if len(cfg.Expose) != 2 {
		t.Fatalf("expose = %v, want 2 entries", cfg.Expose)
	}
	// Kind lowercased, name trimmed — an operator's stray whitespace or caps
	// should not be a startup failure.
	if cfg.Expose[1] != (ExposeSpec{Kind: "tool", Name: "grep"}) {
		t.Errorf("entry not normalised: %+v", cfg.Expose[1])
	}
	if cfg.Timeout != 90*time.Second {
		t.Errorf("timeout = %v, want 90s", cfg.Timeout)
	}
}

func TestLoadServeConfigProjectReplacesUser(t *testing.T) {
	// Element-wise merging would mean a project could only ever widen what the
	// user config exposed, never narrow it. Replacement is the safe default.
	home, work := t.TempDir(), t.TempDir()
	writeSettings(t, home, `{"mcpServe":{"expose":[{"kind":"persona","name":"wide-open"},{"kind":"tool","name":"grep"}]}}`)
	writeSettings(t, filepath.Join(work, ".evva"), `{"mcpServe":{"expose":[{"kind":"tool","name":"tree"}]}}`)

	cfg, _ := LoadServeConfig(work, home)
	if len(cfg.Expose) != 1 || cfg.Expose[0].Name != "tree" {
		t.Errorf("project block should replace the user block wholesale, got %v", cfg.Expose)
	}
}

func TestLoadServeConfigWarnsAndSkipsBadEntries(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"mcpServe":{"expose":[{"kind":"wat","name":"x"},{"kind":"tool","name":""},{"kind":"tool","name":"grep"}],"timeout":99999}}`)

	cfg, warns := LoadServeConfig("", home)
	if len(warns) != 3 {
		t.Errorf("want 3 warnings (bad kind, empty name, timeout range), got %d: %v", len(warns), warns)
	}
	// A malformed entry is dropped, not fatal — the rest of the file still
	// loads, mirroring how mcpServers handles a bad server entry.
	if len(cfg.Expose) != 1 || cfg.Expose[0].Name != "grep" {
		t.Errorf("good entry should survive, got %v", cfg.Expose)
	}
	if cfg.Timeout != DefaultPersonaTimeout {
		t.Errorf("out-of-range timeout should fall back to the default, got %v", cfg.Timeout)
	}
}

func TestLoadServeConfigWarnsOnInvalidJSON(t *testing.T) {
	home := t.TempDir()
	writeSettings(t, home, `{"mcpServe": `)
	_, warns := LoadServeConfig("", home)
	if len(warns) != 1 || !strings.Contains(warns[0].Error(), "invalid json") {
		t.Errorf("want one invalid-json warning, got %v", warns)
	}
}

// --- BuildServer validation -------------------------------------------------

func stubProvider(names ...string) ToolProvider {
	set := map[string]bool{}
	for _, n := range names {
		set[n] = true
	}
	return func(name string) (tools.Tool, error) {
		if !set[name] {
			return nil, os.ErrNotExist
		}
		return &fakeTool{name: name, schema: `{"type":"object"}`,
			exec: func(context.Context, json.RawMessage) (tools.Result, error) {
				return tools.Result{Content: "ok"}, nil
			}}, nil
	}
}

func TestBuildServerRefusesEmptyAllowlist(t *testing.T) {
	// Listening with nothing behind it is indistinguishable from a
	// misconfiguration, so it must not be a valid state.
	_, err := BuildServer(ServeOptions{})
	if err == nil || !strings.Contains(err.Error(), "nothing configured to expose") {
		t.Fatalf("err = %v, want a nothing-to-expose refusal", err)
	}
}

func TestBuildServerRejectsUnknownPersonaAtStartup(t *testing.T) {
	sp := &fakeSpawner{personas: []string{"explore", "review"}}
	_, err := BuildServer(ServeOptions{
		Expose:  []ExposeSpec{{Kind: ExposePersona, Name: "explorer"}}, // typo
		Spawner: sp,
	})
	if err == nil {
		t.Fatal("a typo'd persona must stop startup, not surface at first call")
	}
	// The message has to be actionable: name what IS available.
	if !strings.Contains(err.Error(), "explore") || !strings.Contains(err.Error(), "review") {
		t.Errorf("error should list the available personas, got %v", err)
	}
}

func TestBuildServerRejectsUnknownTool(t *testing.T) {
	_, err := BuildServer(ServeOptions{
		Expose:   []ExposeSpec{{Kind: ExposeTool, Name: "grep"}},
		Provider: stubProvider(), // resolves nothing
	})
	if err == nil || !strings.Contains(err.Error(), "grep") {
		t.Fatalf("err = %v, want an unresolvable-tool failure naming grep", err)
	}
}

func TestBuildServerRefusesDangerousToolExposure(t *testing.T) {
	// The v1 trust boundary: a persona may use bash under its own permission
	// gate, but handing an external caller a raw bash is a different question
	// and is out of scope.
	for _, name := range []string{"bash", "write_file", "edit_file"} {
		_, err := BuildServer(ServeOptions{
			Expose:   []ExposeSpec{{Kind: ExposeTool, Name: name}},
			Provider: stubProvider(name),
		})
		if err == nil {
			t.Fatalf("%s must not be directly exposable", name)
		}
		if !strings.Contains(err.Error(), "read-oriented") {
			t.Errorf("%s: error should explain the rule, got %v", name, err)
		}
		if !strings.Contains(err.Error(), "expose a persona instead") {
			t.Errorf("%s: error should point at the supported route, got %v", name, err)
		}
	}
}

func TestBuildServerRejectsDuplicateExposure(t *testing.T) {
	_, err := BuildServer(ServeOptions{
		Expose: []ExposeSpec{
			{Kind: ExposeTool, Name: "grep"},
			{Kind: ExposeTool, Name: "grep"},
		},
		Provider: stubProvider("grep"),
	})
	if err == nil || !strings.Contains(err.Error(), "collides") {
		t.Fatalf("err = %v, want a collision failure", err)
	}
}

func TestBuildServerRejectsMissingBackends(t *testing.T) {
	if _, err := BuildServer(ServeOptions{Expose: []ExposeSpec{{Kind: ExposePersona, Name: "x"}}}); err == nil {
		t.Error("persona exposure without a spawner should fail")
	}
	if _, err := BuildServer(ServeOptions{Expose: []ExposeSpec{{Kind: ExposeTool, Name: "grep"}}}); err == nil {
		t.Error("tool exposure without a provider should fail")
	}
	if _, err := BuildServer(ServeOptions{Expose: []ExposeSpec{{Kind: "nonsense", Name: "x"}}}); err == nil {
		t.Error("an unknown kind should fail")
	}
}

func TestBuildServerMountsExactlyTheAllowlist(t *testing.T) {
	sp := &fakeSpawner{personas: []string{"explore", "review"}, answer: "done"}
	srv, err := BuildServer(ServeOptions{
		Expose: []ExposeSpec{
			{Kind: ExposePersona, Name: "explore"},
			{Kind: ExposeTool, Name: "grep"},
		},
		Spawner:  sp,
		Provider: stubProvider("grep", "tree"),
		Logger:   slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	serverT, clientT := mcpsdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("connect: %v", err)
	}
	sess, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "probe", Version: "t"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = sess.Close() }()

	list, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range list.Tools {
		got[tool.Name] = true
	}
	// "review" is a real persona and "tree" a resolvable tool — neither was
	// listed, so neither may appear. Exposure is by allowlist, not by
	// availability.
	want := map[string]bool{"evva_explore": true, "grep": true}
	if len(got) != len(want) {
		t.Fatalf("exposed %v, want exactly %v", got, want)
	}
	for n := range want {
		if !got[n] {
			t.Errorf("missing %q from %v", n, got)
		}
	}
}
