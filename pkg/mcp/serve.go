package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/tools"
)

// serve.go is the inbound half of evva's MCP integration (MCP-3): the config
// block that says what may be exposed, and the assembly that turns it into a
// running server. The outbound half (Manager/Client, connecting out to other
// people's servers) is unchanged and shares this package's helpers.
//
// The governing posture: nothing is exposed unless the operator listed it, an
// unknown name fails at startup rather than at first call, and an empty list
// refuses to start rather than listening with nothing behind it — a server
// that silently exposes zero tools looks identical to a misconfigured one.

// Exposure kinds.
const (
	// ExposeTool publishes one evva tool as one MCP tool — a thin passthrough
	// for callers wanting a specific capability without a whole persona.
	ExposeTool = "tool"
	// ExposePersona publishes a whole persona, invoked end-to-end per call.
	ExposePersona = "persona"
)

// ExposeSpec is one entry in the allowlist.
type ExposeSpec struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func (e ExposeSpec) String() string { return e.Kind + " " + e.Name }

// ServeConfig is the normalized mcpServe block.
type ServeConfig struct {
	// Expose is the allowlist. Empty means the feature is dormant.
	Expose []ExposeSpec
	// Timeout bounds a single persona call. Zero means DefaultPersonaTimeout.
	Timeout time.Duration
}

// serveFileShape is the JSON shape under the "mcpServe" key — the structural
// inverse of mcpServers (config.go:40). A list rather than a map, because
// entries are (kind, name) pairs with no natural key.
type serveFileShape struct {
	McpServe *struct {
		Expose  []ExposeSpec `json:"expose"`
		Timeout int          `json:"timeout"` // seconds
	} `json:"mcpServe"`
}

// LoadServeConfig reads .evva/settings.json (project) and
// <evvaHome>/settings.json (user) and returns the normalized serve config
// plus non-fatal warnings. Missing files are not errors.
//
// Unlike mcpServers, which merges per-server-name, an mcpServe block REPLACES
// the other scope's block wholesale (project wins). Element-wise merging of an
// allowlist is the wrong default: a project could then only ever widen what
// the user config exposed, never narrow it.
func LoadServeConfig(workdir, evvaHome string) (*ServeConfig, []Warning) {
	cfg := &ServeConfig{}
	var warns []Warning

	load := func(path string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				warns = append(warns, Warning{Path: path, Err: err})
			}
			return
		}
		var shape serveFileShape
		if err := json.Unmarshal(raw, &shape); err != nil {
			warns = append(warns, Warning{Path: path, Err: fmt.Errorf("invalid json: %w", err)})
			return
		}
		if shape.McpServe == nil {
			return
		}
		next := &ServeConfig{}
		for i, e := range shape.McpServe.Expose {
			e.Kind = strings.ToLower(strings.TrimSpace(e.Kind))
			e.Name = strings.TrimSpace(e.Name)
			if e.Kind != ExposeTool && e.Kind != ExposePersona {
				warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServe.expose[%d]: unknown kind %q (want %q or %q)", i, e.Kind, ExposeTool, ExposePersona)})
				continue
			}
			if e.Name == "" {
				warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServe.expose[%d]: name is required", i)})
				continue
			}
			next.Expose = append(next.Expose, e)
		}
		if t := shape.McpServe.Timeout; t != 0 {
			if t < 1 || t > 3600 {
				warns = append(warns, Warning{Path: path, Err: fmt.Errorf("mcpServe.timeout %d out of range [1,3600]", t)})
			} else {
				next.Timeout = time.Duration(t) * time.Second
			}
		}
		cfg = next
	}

	if evvaHome != "" {
		load(filepath.Join(evvaHome, "settings.json"))
	}
	if workdir != "" {
		load(filepath.Join(workdir, ".evva", "settings.json"))
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = DefaultPersonaTimeout
	}
	return cfg, warns
}

// ToolProvider resolves an exposed tool by name.
//
// It is a callback for the same reason PersonaSpawner is an interface: evva's
// built-in tool factories type-assert the runtime's internal ToolState, so
// only a caller inside the runtime can build them. pkg/mcp stays a protocol
// package that knows nothing about how a tool comes into being.
type ToolProvider func(name string) (tools.Tool, error)

// ServeOptions is everything BuildServer needs.
type ServeOptions struct {
	// Expose is the validated-at-startup allowlist.
	Expose []ExposeSpec
	// Spawner runs exposed personas. Required if Expose names any persona.
	Spawner PersonaSpawner
	// Provider resolves exposed tools. Required if Expose names any tool.
	Provider ToolProvider
	// Timeout bounds one persona call; zero uses DefaultPersonaTimeout.
	Timeout time.Duration
	// Version is reported to clients as the server implementation version.
	Version string
	// Logger receives tool-execution logs. Nil discards.
	Logger *slog.Logger
}

// BuildServer validates the allowlist and returns a server with exactly the
// listed tools and personas mounted.
//
// Every failure here is a startup failure by design: a typo'd persona name
// should stop the server coming up, not surface as a confusing "unknown tool"
// to whoever connects an hour later.
func BuildServer(opts ServeOptions) (*mcpsdk.Server, error) {
	if len(opts.Expose) == 0 {
		return nil, errors.New("mcp: nothing configured to expose — add an \"mcpServe\": {\"expose\": [...]} block to settings.json")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultPersonaTimeout
	}

	version := opts.Version
	if version == "" {
		version = "dev"
	}
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "evva", Version: version}, nil)

	// Personas are indexed once so a long allowlist doesn't re-list per entry,
	// and so the error message can name what IS available.
	var personas map[string]PersonaInfo
	if opts.Spawner != nil {
		personas = map[string]PersonaInfo{}
		for _, p := range opts.Spawner.Personas() {
			personas[p.Name] = p
		}
	}

	seen := map[string]string{} // mcp tool name -> the spec that claimed it
	for _, e := range opts.Expose {
		var (
			def     *mcpsdk.Tool
			handler mcpsdk.ToolHandler
		)
		switch e.Kind {
		case ExposePersona:
			if opts.Spawner == nil {
				return nil, fmt.Errorf("mcp: expose %s: no persona spawner configured", e)
			}
			p, ok := personas[e.Name]
			if !ok {
				return nil, fmt.Errorf("mcp: expose %s: unknown persona (available: %s)", e, strings.Join(sortedKeys(personas), ", "))
			}
			def, handler = adaptPersona(opts.Spawner, p, timeout)

		case ExposeTool:
			if opts.Provider == nil {
				return nil, fmt.Errorf("mcp: expose %s: no tool provider configured", e)
			}
			// v1 exposes read-oriented tools only. Handing an external caller
			// a raw bash/edit/write is a different trust boundary from letting
			// a persona use one under its own permission gate, and is
			// deliberately out of scope.
			if !permission.ReadOnlyOrSelfTools[e.Name] {
				return nil, fmt.Errorf("mcp: expose %s: only read-oriented tools may be exposed directly (allowed: %s) — to reach a tool that mutates, expose a persona instead", e, strings.Join(exposableToolNames(), ", "))
			}
			t, err := opts.Provider(e.Name)
			if err != nil {
				return nil, fmt.Errorf("mcp: expose %s: %w", e, err)
			}
			if t == nil {
				return nil, fmt.Errorf("mcp: expose %s: unknown tool", e)
			}
			def, handler = adaptTool(t, logger)

		default:
			return nil, fmt.Errorf("mcp: expose %s: unknown kind %q", e, e.Kind)
		}

		if prev, dup := seen[def.Name]; dup {
			return nil, fmt.Errorf("mcp: expose %s collides with %s — both publish the MCP tool %q", e, prev, def.Name)
		}
		seen[def.Name] = e.String()
		srv.AddTool(def, handler)
	}
	return srv, nil
}

// exposableToolNames lists the read-oriented tools an operator may publish
// directly, so a rejection tells them what would have worked.
func exposableToolNames() []string {
	out := make([]string, 0, len(permission.ReadOnlyOrSelfTools))
	for n := range permission.ReadOnlyOrSelfTools {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]PersonaInfo) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
