// Package config carries the runtime configuration the evva agent and its
// bundled tools read at startup and during a session.
//
// The package is brand-neutral: a Config is constructed via Load(appName,
// appHome, workdir) so downstream apps can choose their own home directory
// (e.g. ~/.myapp/) and binary name. LoadDefault preserves evva's historical
// behavior (~/.evva/) for the bundled CLI.
//
// There is no package-level singleton. Callers construct one Config per
// process (or per agent, if running multiple agents with different
// configurations) and pass it through agent.New via WithConfig.
package config

import (
	"fmt"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/version"
)

// Version is the canonical version string injected at build time via ldflags:
//
//	go build -ldflags "-X github.com/johnny1110/evva/pkg/config.Version=v1.2.3"
//
// When empty (dev builds, go run), DefaultAppVersion is used as the fallback.
var Version string

// CommitSHA is the git commit hash injected at build time via ldflags. Empty
// in dev builds.
var CommitSHA string

// BuildDate is the UTC build timestamp injected at build time. Empty in dev
// builds.
var BuildDate string

// Default values that appear unchanged across all Config instances.
const (
	DefaultAppName    = "evva"
	DefaultAppVersion = version.Version
)

// DisplayVersion returns the best available version string: the ldflags-injected
// Version, or DefaultAppVersion if not set, followed by the commit and build
// date when available. The result is meant for --version output.
func DisplayVersion() string {
	v := Version
	if v == "" {
		v = DefaultAppVersion
	}
	extra := ""
	if CommitSHA != "" {
		extra += " commit=" + CommitSHA
	}
	if BuildDate != "" {
		extra += " built=" + BuildDate
	}
	return v + extra
}

// Config holds all parsed runtime configuration. Most fields are populated
// once during Load and treated as read-only; the small subset that the
// /config and /model setters mutate at runtime is guarded by c.mu.
//
// AppHome-prefixed paths point inside the per-user home dir
// (~/.<app>/) where skills, USER_PROFILE.md, evva-config.yml, and logs
// live. WorkDir-prefixed paths point inside the process's current working
// directory where workdir-local resources (skills, EVVA.md, plans) live.
type Config struct {
	// OS / runtime
	OS string

	// Logging
	LogLevel  string  // default: "info"
	LogFormat string  // default: "text"
	LogDir    *string // default: nil → stdout only

	// Application
	AppEnv     string // default: "development"
	AppName    string // default: "evva" — the binary / brand name; drives AppHome layout.
	AppVersion string

	// Per-user home dir layout
	AppHome            string
	AppHomeSkillsDir   string
	AppHomeUserProfile string
	AppHomeConfigFile  string // absolute path to <app>-config.yml under AppHome/config/

	AutoCompactThreshold float64

	// Workdir layout
	WorkDir          string
	WorkDirSkillsDir string

	// llm providers(from <app>-config.yml) key: provider name, value: provider APIConfig
	LLMProviderConfig map[string]APIConfig

	// DefaultProvider / DefaultModel are the (provider, model) the agent
	// boots with. Sourced from <app>-config.yml; the /model switch updates
	// them in-memory and persists via SaveFile().
	DefaultProvider constant.LLMProvider
	DefaultModel    constant.Model

	// DefaultEffort is the user-facing effort level name: low|medium|high|ultra.
	// Defaults to "medium". Sourced from <app>-config.yml; /effort updates it.
	DefaultEffort string

	// DefaultProfile is the persona the root agent boots into ("evva", "nono",
	// etc). Sourced from <app>-config.yml; /profile updates it. Empty falls
	// back to "evva" at bootstrap so old configs keep working.
	DefaultProfile string

	// PermissionMode is the startup permission stance: one of
	// default|accept_edits|plan|bypass|auto. The -permission-mode CLI flag
	// overrides this at boot; the TUI's Shift+Tab cycle mutates the
	// in-memory value via SetPermissionMode (not yet persisted).
	PermissionMode string

	// Loaded metadata
	LoadedAt time.Time
	// DefaultMaxIterations is the loop's safety cap. Hitting it emits a
	// KindIterLimit event and pauses the agent; the caller may invoke
	// Continue(ctx) to keep going.
	DefaultMaxIterations int
	// DefaultMaxTokens is the per-completion output-token cap passed to
	// the LLM. 0 → let the provider apply its own default.
	DefaultMaxTokens int

	// UI
	DisplayThinking bool

	// Auto-memory subsystem. When true (default), the system prompt carries the
	// typed-memory guidance + the MEMORY.md index, the permission write carve-out
	// is active, and per-turn recall runs. The model writes memory files itself
	// with write/edit (no dedicated tool). /config (or hand-edit) flips this;
	// EVVA_AUTO_MEMORY=0 forces off at boot regardless of the YAML.
	EnableAutoMemory bool

	// EnableMemoryRecall gates the per-turn relevance side-query (only meaningful
	// when EnableAutoMemory is true). Default true; the cost-sensitive escape
	// hatch keeps the index but drops the extra completion per turn.
	EnableMemoryRecall bool

	// MemoryRecallModel optionally pins the recall side-query model id. Empty →
	// a cheap model within the active provider (anthropic: sonnet, deepseek:
	// flash, openai: gpt-5.4-mini at medium effort; ollama/other: the active
	// model + the main agent's effort). See internal/agent recall wiring.
	MemoryRecallModel string

	// Web tools
	TavilyAPIKey  string // empty → web_search reports "not configured"
	FetchMaxBytes int    // cap on extracted text returned by web_fetch

	// CustomConfig holds downstream-app-defined settings. Reads/writes go
	// through GetCustom / SetCustom / DeleteCustom under c.mu. Values
	// round-trip through YAML as a `custom:` section; complex types are
	// encoded as whatever gopkg.in/yaml.v3 produces for any (typically a
	// map[string]any tree). Consumers cast at use-site — pkg/config does
	// not know the value shapes.
	//
	// Use this slot for downstream-private secrets/settings that don't fit
	// the typed fields above (e.g. friday's broker URL, a billing token,
	// feature flags). evva itself never reads from CustomConfig.
	CustomConfig map[string]any

	// LLMParamsTemperature / LLMParamsTopP / LLMParamsTopK are session-only
	// sampling knobs the /config form mutates at runtime. nil → provider
	// default (not sent in API request). Reset to nil on every evva start;
	// never persisted to YAML.
	LLMParamsTemperature *float64
	LLMParamsTopP        *float64
	LLMParamsTopK        *int

	// mu guards the runtime-mutable fields below (DisplayThinking,
	// AutoCompactThreshold, DefaultMaxIterations, DefaultMaxTokens,
	// FetchMaxBytes, TavilyAPIKey, LLMProviderConfig, CustomConfig,
	// LLMParamsTemperature, LLMParamsTopP, LLMParamsTopK) once
	// Load returns. Use the Get* / Set* accessors — direct field reads
	// from outside this package race the UI goroutine's edits.
	mu sync.RWMutex

	// saveMu serializes write-back to AppHomeConfigFile. Separate from
	// mu so a slow disk write doesn't block agent-loop reads.
	saveMu sync.Mutex
}

// Clone returns a shallow copy of c with fresh mutexes. Used by callers
// that need to override a small subset of fields (notably WorkDir) for a
// scoped agent — the AgentTool isolation path does this so a subagent
// can run with cfg.WorkDir = <worktree path> while the parent keeps its
// own. The copy reads through c.mu so concurrent mutations don't tear
// across fields.
//
// "Shallow" — the LLMProviderConfig map is reused by reference. That's
// safe today because providers are loaded once at boot and never mutated
// after; if that invariant ever changes, this method should deep-copy
// the map.
func (c *Config) Clone() *Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	clone := &Config{
		OS:                   c.OS,
		LogLevel:             c.LogLevel,
		LogFormat:            c.LogFormat,
		LogDir:               c.LogDir,
		AppEnv:               c.AppEnv,
		AppName:              c.AppName,
		AppVersion:           c.AppVersion,
		AppHome:              c.AppHome,
		AppHomeSkillsDir:     c.AppHomeSkillsDir,
		AppHomeUserProfile:   c.AppHomeUserProfile,
		AppHomeConfigFile:    c.AppHomeConfigFile,
		AutoCompactThreshold: c.AutoCompactThreshold,
		WorkDir:              c.WorkDir,
		WorkDirSkillsDir:     c.WorkDirSkillsDir,
		LLMProviderConfig:    c.LLMProviderConfig,
		DefaultProvider:      c.DefaultProvider,
		DefaultModel:         c.DefaultModel,
		DefaultEffort:        c.DefaultEffort,
		DefaultProfile:       c.DefaultProfile,
		PermissionMode:       c.PermissionMode,
		LoadedAt:             c.LoadedAt,
		DefaultMaxIterations: c.DefaultMaxIterations,
		DefaultMaxTokens:     c.DefaultMaxTokens,
		DisplayThinking:      c.DisplayThinking,
		EnableAutoMemory:     c.EnableAutoMemory,
		EnableMemoryRecall:   c.EnableMemoryRecall,
		MemoryRecallModel:    c.MemoryRecallModel,
		TavilyAPIKey:         c.TavilyAPIKey,
		FetchMaxBytes:        c.FetchMaxBytes,
		LLMParamsTemperature: c.LLMParamsTemperature,
		LLMParamsTopP:        c.LLMParamsTopP,
		LLMParamsTopK:        c.LLMParamsTopK,
	}
	if c.CustomConfig != nil {
		clone.CustomConfig = make(map[string]any, len(c.CustomConfig))
		for k, v := range c.CustomConfig {
			clone.CustomConfig[k] = v
		}
	}
	return clone
}

// GetCustom returns the value stored under key in CustomConfig.
// ok=false when the key is absent. Reads under c.mu.RLock.
//
// Values are stored as `any` — cast at use-site. After a YAML reload the
// concrete type is whatever yaml.v3 decoded into (typically string, int,
// float64, bool, []any, or map[string]any). Round-tripping through SaveFile
// is lossy for typed Go structs unless the caller (or yaml tags on a
// value-type) preserves the shape.
func (c *Config) GetCustom(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.CustomConfig == nil {
		return nil, false
	}
	v, ok := c.CustomConfig[key]
	return v, ok
}

// SetCustom stores value under key in CustomConfig and persists the change
// to AppHomeConfigFile via SaveFile. An empty key is rejected.
//
// Nil values are stored as-is — call DeleteCustom to remove the entry.
// Concurrent SetCustom calls are serialized by c.mu.
func (c *Config) SetCustom(key string, value any) error {
	if key == "" {
		return fmt.Errorf("config: custom key is required")
	}
	c.mu.Lock()
	if c.CustomConfig == nil {
		c.CustomConfig = map[string]any{}
	}
	c.CustomConfig[key] = value
	c.mu.Unlock()
	return c.SaveFile()
}

// DeleteCustom removes key from CustomConfig and persists. A missing key
// is a no-op (no error). Persists via SaveFile so the YAML reflects the
// removal immediately.
func (c *Config) DeleteCustom(key string) error {
	c.mu.Lock()
	if c.CustomConfig != nil {
		delete(c.CustomConfig, key)
	}
	c.mu.Unlock()
	return c.SaveFile()
}

// GetDisplayThinking returns the current DisplayThinking flag under the
// read lock. Agent code reads this every turn (state_machine.go,
// stream.go); the UI may write it via /config.
func (c *Config) GetDisplayThinking() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DisplayThinking
}

// GetAutoCompactThreshold returns the current threshold under the read
// lock. compact.go reads this every turn.
func (c *Config) GetAutoCompactThreshold() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AutoCompactThreshold
}

// SetDisplayThinking mutates the in-memory flag and persists to disk.
func (c *Config) SetDisplayThinking(v bool) error {
	c.mu.Lock()
	c.DisplayThinking = v
	c.mu.Unlock()
	return c.SaveFile()
}

// GetEnableAutoMemory returns the auto-memory flag under the read lock.
// Read by agent.Main (to decide whether to attach the memory tools) and
// by the sysprompt builder (to decide whether to inject the auto-memory
// guidance section).
func (c *Config) GetEnableAutoMemory() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EnableAutoMemory
}

// SetEnableAutoMemory toggles the auto-memory subsystem and persists.
// Takes effect for the prompt and tool registration on next agent boot.
func (c *Config) SetEnableAutoMemory(v bool) error {
	c.mu.Lock()
	c.EnableAutoMemory = v
	c.mu.Unlock()
	return c.SaveFile()
}

// GetEnableMemoryRecall returns the per-turn recall flag under the read lock.
// Read by the agent loop each user turn to decide whether to run the relevance
// side-query.
func (c *Config) GetEnableMemoryRecall() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.EnableMemoryRecall
}

// SetEnableMemoryRecall toggles the per-turn recall side-query and persists.
func (c *Config) SetEnableMemoryRecall(v bool) error {
	c.mu.Lock()
	c.EnableMemoryRecall = v
	c.mu.Unlock()
	return c.SaveFile()
}

// GetMemoryRecallModel returns the pinned recall-model id ("" → default
// resolution) under the read lock.
func (c *Config) GetMemoryRecallModel() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.MemoryRecallModel
}

// SetMemoryRecallModel pins (or clears, with "") the recall side-query model
// and persists.
func (c *Config) SetMemoryRecallModel(v string) error {
	c.mu.Lock()
	c.MemoryRecallModel = v
	c.mu.Unlock()
	return c.SaveFile()
}

// SetAutoCompactThreshold validates 0 < v <= 1 and persists.
func (c *Config) SetAutoCompactThreshold(v float64) error {
	if v <= 0 || v > 1 {
		return fmt.Errorf("auto_compact_threshold must be in (0, 1], got %g", v)
	}
	c.mu.Lock()
	c.AutoCompactThreshold = v
	c.mu.Unlock()
	return c.SaveFile()
}

// SetProviderCredentials writes the (apiURL, apiKey) pair for the
// named LLM provider into Config.LLMProviderConfig under the mutex.
//
// This is the documented path for downstream apps to install
// credentials at runtime — direct map assignment
// (`cfg.LLMProviderConfig["deepseek"] = ...`) still works but races
// with concurrent reads. SetProviderCredentials takes c.mu so two
// goroutines wiring different providers at startup don't tear.
//
// An empty name is rejected. Unknown provider names are NOT rejected:
// downstream apps register custom providers into pkg/llm's registry
// without touching constant, and the agent's LLM-build step will
// surface the typo if no factory matches. apiURL may be empty —
// providers with a sane default (DeepSeek, Anthropic) fall back to it.
// apiKey may be empty for local providers (Ollama, ...).
//
// Models on the existing APIConfig (if any) are preserved. Pass through
// the public map slot when a custom Models list is also needed.
func (c *Config) SetProviderCredentials(name, apiURL, apiKey string) error {
	if name == "" {
		return fmt.Errorf("config: provider name is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.LLMProviderConfig == nil {
		c.LLMProviderConfig = map[string]APIConfig{}
	}
	existing := c.LLMProviderConfig[name]
	existing.ApiURL = apiURL
	existing.ApiSecret = apiKey
	c.LLMProviderConfig[name] = existing
	return nil
}

// SetMaxIterations validates >0 and persists. NOTE: this only updates
// the YAML default; the live cap on a running agent is on Agent itself
// — call Controller.SetMaxIterations to mutate it.
func (c *Config) SetMaxIterations(n int) error {
	if n <= 0 {
		return fmt.Errorf("max_iterations must be > 0, got %d", n)
	}
	c.mu.Lock()
	c.DefaultMaxIterations = n
	c.mu.Unlock()
	return c.SaveFile()
}

// SetMaxTokens validates >=0 and persists. 0 means "provider default".
// Effective on next launch — the agent's profile snapshots this at
// construction.
func (c *Config) SetMaxTokens(n int) error {
	if n < 0 {
		return fmt.Errorf("max_tokens must be >= 0, got %d", n)
	}
	c.mu.Lock()
	c.DefaultMaxTokens = n
	c.mu.Unlock()
	return c.SaveFile()
}

// SetFetchMaxBytes validates > 0 and persists.
func (c *Config) SetFetchMaxBytes(n int) error {
	if n <= 0 {
		return fmt.Errorf("fetch_max_bytes must be > 0, got %d", n)
	}
	c.mu.Lock()
	c.FetchMaxBytes = n
	c.mu.Unlock()
	return c.SaveFile()
}

// SetDefaultModel updates the (provider, model) pair the agent boots
// with and persists it. Phase-3's runtime /model swap calls this after
// rebuilding the Agent's llm.Client so next launch starts with the
// user's last choice. Validates that the model is actually offered by
// the provider.
func (c *Config) SetDefaultModel(provider constant.LLMProvider, model constant.Model) error {
	found := false
	for _, m := range provider.Models {
		if m == model {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("model %q not offered by provider %q", model, provider.Name)
	}
	c.mu.Lock()
	c.DefaultProvider = provider
	c.DefaultModel = model
	c.mu.Unlock()
	return c.SaveFile()
}

// Effort returns the current effort level name under the read lock.
func (c *Config) Effort() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DefaultEffort
}

// SetDefaultEffort validates the effort level name and persists it.
func (c *Config) SetDefaultEffort(level string) error {
	switch level {
	case "low", "medium", "high", "ultra":
	default:
		return fmt.Errorf("invalid effort level %q: want low|medium|high|ultra", level)
	}
	c.mu.Lock()
	c.DefaultEffort = level
	c.mu.Unlock()
	return c.SaveFile()
}

// SetDefaultProfile persists the chosen persona name. Validation against
// the agent registry happens at the call site (AgentRegistry lives in
// internal/agent, which can't be imported from config without a cycle).
// Empty string is accepted — bootstrap interprets "" as "fall back to evva".
func (c *Config) SetDefaultProfile(name string) error {
	c.mu.Lock()
	c.DefaultProfile = name
	c.mu.Unlock()
	return c.SaveFile()
}

// SetTavilyAPIKey persists the key. Empty string disables web_search.
func (c *Config) SetTavilyAPIKey(s string) error {
	c.mu.Lock()
	c.TavilyAPIKey = s
	c.mu.Unlock()
	return c.SaveFile()
}

// LLMTemperature returns the current temperature or nil (provider default).
func (c *Config) LLMTemperature() *float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LLMParamsTemperature
}

// SetLLMTemperature updates the session-only temperature. nil clears it
// (provider default). Validates 0 ≤ v ≤ 2. Never persisted to disk.
func (c *Config) SetLLMTemperature(v *float64) error {
	if v != nil && (*v < 0 || *v > 2) {
		return fmt.Errorf("temperature must be in [0, 2], got %g", *v)
	}
	c.mu.Lock()
	c.LLMParamsTemperature = v
	c.mu.Unlock()
	return nil
}

// LLMTopK returns the current top_k or nil (provider default).
func (c *Config) LLMTopK() *int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LLMParamsTopK
}

// SetLLMTopK updates the session-only top_k. nil clears it (provider
// default). Validates v > 0. Never persisted to disk.
func (c *Config) SetLLMTopK(v *int) error {
	if v != nil && *v <= 0 {
		return fmt.Errorf("top_k must be > 0, got %d", *v)
	}
	c.mu.Lock()
	c.LLMParamsTopK = v
	c.mu.Unlock()
	return nil
}

// LLMTopP returns the current top_p or nil (provider default).
func (c *Config) LLMTopP() *float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LLMParamsTopP
}

// SetLLMTopP updates the session-only top_p. nil clears it (provider
// default). Validates 0 ≤ v ≤ 1. Never persisted to disk.
func (c *Config) SetLLMTopP(v *float64) error {
	if v != nil && (*v < 0 || *v > 1) {
		return fmt.Errorf("top_p must be in [0, 1], got %g", *v)
	}
	c.mu.Lock()
	c.LLMParamsTopP = v
	c.mu.Unlock()
	return nil
}

// SetProviderAPIKey installs an api key for the named provider and
// persists. Empty key removes the provider from LLMProviderConfig (cloud
// providers require a key to be listed). The constant.LLMProvider must
// already be known.
func (c *Config) SetProviderAPIKey(name, key string) error {
	pvd, ok := constant.GetProvider(name)
	if !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	c.mu.Lock()
	if key == "" && name != constant.OLLAMA.Name {
		delete(c.LLMProviderConfig, name)
	} else {
		existing := c.LLMProviderConfig[name]
		if existing.ApiURL == "" {
			existing.ApiURL = pvd.ApiUrl
		}
		existing.ApiSecret = key
		existing.Models = pvd.Models
		c.LLMProviderConfig[name] = existing
	}
	c.mu.Unlock()
	return c.SaveFile()
}

// SetProviderAPIURL overrides the api_url for the named provider. Empty
// resets to the provider's built-in default.
func (c *Config) SetProviderAPIURL(name, url string) error {
	pvd, ok := constant.GetProvider(name)
	if !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	c.mu.Lock()
	existing := c.LLMProviderConfig[name]
	if url == "" {
		existing.ApiURL = pvd.ApiUrl
	} else {
		existing.ApiURL = url
	}
	if existing.Models == nil {
		existing.Models = pvd.Models
	}
	c.LLMProviderConfig[name] = existing
	c.mu.Unlock()
	return c.SaveFile()
}

// GetMaxIterations returns the agent-loop iteration cap under the read
// lock. Paired with SetMaxIterations.
func (c *Config) GetMaxIterations() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DefaultMaxIterations
}

// GetMaxTokens returns the per-completion output-token cap under the read
// lock. 0 means "provider default". Paired with SetMaxTokens.
func (c *Config) GetMaxTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DefaultMaxTokens
}

// GetFetchMaxBytes returns the web_fetch byte cap under the read lock.
// Paired with SetFetchMaxBytes.
func (c *Config) GetFetchMaxBytes() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.FetchMaxBytes
}

// GetTavilyAPIKey returns the Tavily key under the read lock. Empty means
// web_search is disabled. Paired with SetTavilyAPIKey.
func (c *Config) GetTavilyAPIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.TavilyAPIKey
}

// GetDefaultProfile returns the boot persona name under the read lock.
// Empty falls back to "evva" at bootstrap. Paired with SetDefaultProfile.
func (c *Config) GetDefaultProfile() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DefaultProfile
}

// GetProviderAPIKey returns the stored api key for the named provider
// under the read lock, or "" when the provider has no entry.
func (c *Config) GetProviderAPIKey(name string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LLMProviderConfig[name].ApiSecret
}

// GetProviderAPIURL returns the stored api base URL for the named provider
// under the read lock, or "" when the provider has no entry.
func (c *Config) GetProviderAPIURL(name string) string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.LLMProviderConfig[name].ApiURL
}

// SaveFile re-serializes the user-tunable subset to AppHomeConfigFile.
// The /config setters and the runtime /model switch both call this.
//
// Snapshots all fields under c.mu.RLock, releases that lock before
// blocking on disk I/O, then takes c.saveMu so concurrent saves don't
// interleave on the file.
func (c *Config) SaveFile() error {
	c.mu.RLock()
	providers := map[string]FileProviderConfig{}
	for name, p := range c.LLMProviderConfig {
		providers[name] = FileProviderConfig{
			APIKey: p.ApiSecret,
			APIURL: p.ApiURL,
		}
	}
	// Cloud providers that aren't currently in LLMProviderConfig (no key
	// loaded) still get a placeholder entry in the YAML so the user can
	// hand-edit them later. Same for Ollama.
	for _, pvd := range constant.GetAllProviders() {
		if _, ok := providers[pvd.Name]; !ok {
			providers[pvd.Name] = FileProviderConfig{}
		}
	}
	enableAutoMem := c.EnableAutoMemory
	enableMemRecall := c.EnableMemoryRecall
	var customCopy map[string]any
	if len(c.CustomConfig) > 0 {
		customCopy = make(map[string]any, len(c.CustomConfig))
		for k, v := range c.CustomConfig {
			customCopy[k] = v
		}
	}
	fc := FileConfig{
		MaxIterations:        c.DefaultMaxIterations,
		MaxTokens:            c.DefaultMaxTokens,
		AutoCompactThreshold: c.AutoCompactThreshold,
		DisplayThinking:      c.DisplayThinking,
		DefaultProvider:      c.DefaultProvider.Name,
		DefaultModel:         string(c.DefaultModel),
		DefaultEffort:        c.DefaultEffort,
		DefaultProfile:       c.DefaultProfile,
		PermissionMode:       c.PermissionMode,
		FetchMaxBytes:        c.FetchMaxBytes,
		TavilyAPIKey:         c.TavilyAPIKey,
		EnableAutoMemory:     &enableAutoMem,
		EnableMemoryRecall:   &enableMemRecall,
		MemoryRecallModel:    c.MemoryRecallModel,
		Providers:            providers,
		Custom:               customCopy,
	}
	path := c.AppHomeConfigFile
	c.mu.RUnlock()

	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	return SaveFileConfig(path, fc)
}

// IsDevelopment / IsProduction — semantic helpers so call sites
// don't hardcode string literals scattered across the codebase.
func (c *Config) IsDevelopment() bool { return c.AppEnv == "dev" }
func (c *Config) IsProduction() bool  { return c.AppEnv == "prod" }
