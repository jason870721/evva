package mcp

import (
	"context"
	"io"
	"log/slog"
	"sort"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/johnny1110/evva/pkg/tools"
	pubtoolset "github.com/johnny1110/evva/pkg/toolset"
)

// Manager holds every Client and is the seam internal/agent threads
// through ToolState. Safe for concurrent use.
type Manager struct {
	mu       sync.RWMutex
	clients  map[string]*Client
	logger   *slog.Logger
	evvaHome string
	prompt   OAuthPromptFn

	// reg is the registry RegisterFactories was called with. Captured so the
	// per-server authenticate tool can register the real tool factories once
	// a needs-auth server reconnects. nil until RegisterFactories runs.
	reg *pubtoolset.Registry

	// onToolsChanged, if installed by the host (internal/agent), is invoked
	// after the authenticate flow discovers a server's tools so the agent
	// can refresh its deferred allowlist. nil-safe.
	onToolsChanged func()
}

// OpenOptions carries the host-supplied dependencies the Manager needs at
// construction time. Every field is optional with a defined nil/zero
// behavior so Open's signature stays stable as the option set grows.
type OpenOptions struct {
	// Logger receives mcp.* slog entries. nil yields a discard logger.
	Logger *slog.Logger

	// EvvaHome is the resolved per-user home dir used for binary-blob
	// persistence under <EvvaHome>/mcp-blobs. Empty disables blob writes.
	EvvaHome string

	// OAuthPrompt is called when an HTTP MCP server requires OAuth. nil
	// disables the interactive flow — needs-auth servers surface a clear
	// error if their authenticate tool is invoked.
	OAuthPrompt OAuthPromptFn
}

// NewManager returns an empty Manager configured from opts.
func NewManager(opts OpenOptions) *Manager {
	lg := opts.Logger
	if lg == nil {
		lg = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Manager{
		clients:  map[string]*Client{},
		logger:   lg,
		evvaHome: opts.EvvaHome,
		prompt:   opts.OAuthPrompt,
	}
}

// Open is the one-call constructor: build a Manager, connect every
// non-disabled server in parallel, fetch each connected server's tool
// catalog, and return the result. Connection failures do not abort —
// they surface as Warnings and a per-server failed/needs-auth status.
func Open(ctx context.Context, cfg *Config, opts OpenOptions) (*Manager, []Warning) {
	m := NewManager(opts)
	if cfg == nil || len(cfg.Servers) == 0 {
		return m, nil
	}

	var (
		wg    sync.WaitGroup
		wmu   sync.Mutex
		warns []Warning
	)
	for _, sc := range cfg.Servers {
		if sc.Disabled {
			m.add(&Client{
				Name: sc.Name, Config: sc, status: StatusDisabled,
				logger: m.logger, evvaHome: m.evvaHome,
			})
			continue
		}
		sc := sc
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := &Client{
				Name: sc.Name, Config: sc, logger: m.logger,
				evvaHome: m.evvaHome, status: StatusPending,
				promptFn: m.prompt,
			}
			// Boot connect runs WITHOUT an OAuth handler: an unauthenticated
			// 401 lands the server in needs-auth, and the per-server
			// authenticate tool drives a second connect with a live prompt
			// (the question broker isn't wired at boot).
			if err := c.connect(ctx); err != nil {
				wmu.Lock()
				warns = append(warns, Warning{Path: sc.Name, Err: err})
				wmu.Unlock()
			}
			switch c.Status() {
			case StatusConnected:
				m.logger.Info("mcp: connect", "server", sc.Name, "status", "connected")
			case StatusNeedsAuth:
				m.logger.Info("mcp: connect", "server", sc.Name, "status", "needs-auth")
			default:
				m.logger.Warn("mcp: connect failed", "server", sc.Name, "err", c.lastErr)
			}
			m.add(c)
		}()
	}
	wg.Wait()

	// Pull tools for connected servers in parallel.
	for _, c := range m.list() {
		c := c
		if c.Status() != StatusConnected {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.ListTools(ctx); err != nil {
				wmu.Lock()
				warns = append(warns, Warning{Path: c.Name, Err: err})
				wmu.Unlock()
			}
		}()
	}
	wg.Wait()
	return m, warns
}

func (m *Manager) add(c *Client) {
	m.mu.Lock()
	m.clients[c.Name] = c
	m.mu.Unlock()
}

// Client returns the named client or nil.
func (m *Manager) Client(name string) *Client {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clients[name]
}

// Status returns a snapshot of every server's runtime state, sorted by
// name for stable output.
func (m *Manager) Status() []ServerState {
	list := m.list()
	out := make([]ServerState, 0, len(list))
	for _, c := range list {
		c.mu.RLock()
		resourceCount := 0
		if c.caps != nil && c.caps.Resources != nil {
			resourceCount = 1 // capability present; exact count needs a list call
		}
		out = append(out, ServerState{
			Name: c.Name, Config: c.Config, Status: c.status,
			Error: errString(c.lastErr), ToolCount: len(c.tools),
			ResourceCount: resourceCount,
		})
		c.mu.RUnlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) list() []*Client {
	m.mu.RLock()
	out := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, c)
	}
	m.mu.RUnlock()
	return out
}

// DiscoveredToolNames returns every mcp__<server>__<tool> name across all
// connected servers, plus one mcp__<server>__authenticate name per
// needs-auth server, sorted. Called by internal/agent at profile-build
// time to extend the deferred allowlist.
func (m *Manager) DiscoveredToolNames() []string {
	var out []string
	for _, c := range m.list() {
		c.mu.RLock()
		if c.status == StatusConnected {
			for _, t := range c.tools {
				out = append(out, BuildToolName(c.Name, t.Name))
			}
		}
		if c.status == StatusNeedsAuth {
			out = append(out, BuildToolName(c.Name, "authenticate"))
		}
		c.mu.RUnlock()
	}
	sort.Strings(out)
	return out
}

// SetOnToolsChanged installs a callback the Manager fires after the
// authenticate flow discovers a server's real tools, so the host can
// refresh its deferred allowlist. nil-safe.
func (m *Manager) SetOnToolsChanged(fn func()) { m.onToolsChanged = fn }

// RegisterFactories registers a pubtoolset.ToolFactory for every tool
// discovered across every connected server, keyed by the qualified
// mcp__<server>__<tool> name, plus one authenticate factory per
// needs-auth server. Idempotent: duplicate registrations are absorbed
// (same instance, same factory).
func (m *Manager) RegisterFactories(reg *pubtoolset.Registry) {
	m.reg = reg
	for _, c := range m.list() {
		m.registerClientTools(c)
	}
}

// registerClientTools registers the factories for one client's current
// tool set (and its authenticate tool if needs-auth). Called by
// RegisterFactories at boot and by the authenticate tool after a
// successful reconnect discovers the real toolset.
func (m *Manager) registerClientTools(c *Client) {
	if m.reg == nil {
		return
	}
	c.mu.RLock()
	status := c.status
	sdkTools := append([]*mcpsdk.Tool(nil), c.tools...)
	c.mu.RUnlock()

	if status == StatusConnected {
		for _, t := range sdkTools {
			t := t
			name := tools.ToolName(BuildToolName(c.Name, t.Name))
			_ = m.reg.Register(name, func(_ tools.State) (tools.Tool, error) {
				return newMcpTool(c, t), nil
			})
		}
	}
	if status == StatusNeedsAuth {
		name := tools.ToolName(BuildToolName(c.Name, "authenticate"))
		client := c
		_ = m.reg.Register(name, func(_ tools.State) (tools.Tool, error) {
			return newMcpAuthTool(m, client), nil
		})
	}
}

// Shutdown closes every active session. Idempotent; bound to the agent's
// RootContext cancel.
func (m *Manager) Shutdown() {
	for _, c := range m.list() {
		c.mu.Lock()
		if c.session != nil {
			_ = c.session.Close()
			c.session = nil
		}
		c.mu.Unlock()
	}
}

func errString(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
