package mcp

import (
	"time"
)

// TransportType is the wire-level transport kind.
type TransportType string

const (
	TransportStdio          TransportType = "stdio"
	TransportStreamableHTTP TransportType = "http"
)

// ServerConfig is the parsed shape of one entry under mcpServers.
// Mirrors ref/src/services/mcp/types.ts McpStdioServerConfigSchema +
// McpHTTPServerConfigSchema, simplified to the two transports v1.6 ships.
type ServerConfig struct {
	Name     string // map key from settings.json
	Type     TransportType
	Disabled bool // "disabled": true skips connect

	// Stdio fields
	Command string // required when Type == TransportStdio
	Args    []string
	Env     map[string]string // ${VAR} / ${VAR:-default} expansion happens at Load

	// HTTP fields
	URL     string // required when Type == TransportStreamableHTTP
	Headers map[string]string

	// Common
	Timeout time.Duration // connect timeout; default 30s; max 600s
	Scope   ConfigScope   // Project | User — for telemetry/logging only
}

// ConfigScope identifies where a server config was loaded from. Mirrors
// hooks/skills sourcing — workdir overrides user.
type ConfigScope string

const (
	ScopeUser    ConfigScope = "user"    // <APP_HOME>/settings.json
	ScopeProject ConfigScope = "project" // <workdir>/.evva/settings.json
)

// ServerStatus is the live runtime state of one server's connection.
type ServerStatus string

const (
	StatusConnected ServerStatus = "connected"
	StatusPending   ServerStatus = "pending"    // Connect in flight
	StatusFailed    ServerStatus = "failed"     // Connect returned err; tools=0
	StatusNeedsAuth ServerStatus = "needs-auth" // HTTP 401; auth tool offered
	StatusDisabled  ServerStatus = "disabled"
)

// ServerState is what Manager.Status() returns per server.
type ServerState struct {
	Name          string
	Config        ServerConfig
	Status        ServerStatus
	Error         string    // populated for StatusFailed / StatusNeedsAuth
	ToolCount     int       // number of tools discovered (0 unless Connected)
	ResourceCount int       // 0 unless server advertises resources/list capability
	ConnectedAt   time.Time // zero unless Connected
}
