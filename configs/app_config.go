package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/constant"
	"github.com/joho/godotenv"
)

// AppConfig holds all parsed environment configuration.
// Fields are read-only after initialization — treat them as constants.
// Pointer types (e.g. *string) represent "explicitly nullable" values:
// nil means "not set / intentionally absent", distinguishing from "".
type AppConfig struct {
	// OS / runtime
	OS string

	// Logging
	LogLevel  string  // default: "info"
	LogFormat string  // default: "text"
	LogDir    *string // default: nil → stdout only

	// Application
	AppEnv     string // default: "development"
	AppName    string // default: "app"
	AppVersion string

	// Global config dir
	EvvaHome             string
	EvvaHomeSkillsDir    string
	EvvaHomeUserProfile  string
	EvvaHomeConfigFile   string // path to evva-config.yml
	AutoCompactThreshold float64

	// Work dir
	WorkDir          string
	WorkDirSkillsDir string

	// llm providers(from evva-config.yml) key: provider name, value: provider config
	LLMProviderConfig map[string]LLMProviderAPIConfig

	// DefaultProvider / DefaultModel are the (provider, model) the agent
	// boots with. Sourced from evva-config.yml; phase-3 /model switch will
	// update these in-memory and persist via SaveFile().
	DefaultProvider constant.LLMProvider
	DefaultModel    constant.Model

	// DefaultEffort is the user-facing effort level name: low|medium|high|ultra.
	// Defaults to "medium". Sourced from evva-config.yml; /effort updates it.
	DefaultEffort string

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

	// Web tools
	TavilyAPIKey  string // empty → web_search reports "not configured"
	FetchMaxBytes int    // cap on extracted text returned by web_fetch

	// mu guards the runtime-mutable fields below (DisplayThinking,
	// AutoCompactThreshold, DefaultMaxIterations, DefaultMaxTokens,
	// FetchMaxBytes, TavilyAPIKey, LLMProviderConfig) once load() has
	// returned. Use the Get* / Set* accessors — direct field reads from
	// outside this package race the UI goroutine's edits.
	mu sync.RWMutex

	// saveMu serializes write-back to EvvaHomeConfigFile. Separate from
	// mu so a slow disk write doesn't block agent-loop reads.
	saveMu sync.Mutex
}

// GetDisplayThinking returns the current DisplayThinking flag under the
// read lock. Agent code reads this every turn (state_machine.go,
// stream.go); the UI may write it via /config.
func (c *AppConfig) GetDisplayThinking() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DisplayThinking
}

// GetAutoCompactThreshold returns the current threshold under the read
// lock. compact.go reads this every turn.
func (c *AppConfig) GetAutoCompactThreshold() float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.AutoCompactThreshold
}

// SetDisplayThinking mutates the in-memory flag and persists to disk.
func (c *AppConfig) SetDisplayThinking(v bool) error {
	c.mu.Lock()
	c.DisplayThinking = v
	c.mu.Unlock()
	return c.SaveFile()
}

// SetAutoCompactThreshold validates 0 < v <= 1 and persists.
func (c *AppConfig) SetAutoCompactThreshold(v float64) error {
	if v <= 0 || v > 1 {
		return fmt.Errorf("auto_compact_threshold must be in (0, 1], got %g", v)
	}
	c.mu.Lock()
	c.AutoCompactThreshold = v
	c.mu.Unlock()
	return c.SaveFile()
}

// SetMaxIterations validates >0 and persists. NOTE: this only updates
// the YAML default; the live cap on a running agent is on Agent itself
// — call Controller.SetMaxIterations to mutate it.
func (c *AppConfig) SetMaxIterations(n int) error {
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
func (c *AppConfig) SetMaxTokens(n int) error {
	if n < 0 {
		return fmt.Errorf("max_tokens must be >= 0, got %d", n)
	}
	c.mu.Lock()
	c.DefaultMaxTokens = n
	c.mu.Unlock()
	return c.SaveFile()
}

// SetFetchMaxBytes validates > 0 and persists.
func (c *AppConfig) SetFetchMaxBytes(n int) error {
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
func (c *AppConfig) SetDefaultModel(provider constant.LLMProvider, model constant.Model) error {
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
func (c *AppConfig) Effort() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.DefaultEffort
}

// SetDefaultEffort validates the effort level name and persists it.
func (c *AppConfig) SetDefaultEffort(level string) error {
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

// SetTavilyAPIKey persists the key. Empty string disables web_search.
func (c *AppConfig) SetTavilyAPIKey(s string) error {
	c.mu.Lock()
	c.TavilyAPIKey = s
	c.mu.Unlock()
	return c.SaveFile()
}

// SetProviderAPIKey installs an api key for the named provider and
// persists. Empty key removes the provider from LLMProviderConfig (cloud
// providers require a key to be listed). The constant.LLMProvider must
// already be known.
func (c *AppConfig) SetProviderAPIKey(name, key string) error {
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
func (c *AppConfig) SetProviderAPIURL(name, url string) error {
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

var (
	instance *AppConfig
	once     sync.Once
)

const AppName = "evva"
const AppVersion = "0.1.0"

// Get returns the singleton AppConfig, initializing it on first call.
// Safe for concurrent use — subsequent calls after the first are lock-free reads.
func Get() *AppConfig {
	once.Do(func() {
		instance = load()
	})
	return instance
}

// load performs the actual env + YAML parsing.
// Isolated from Get() so it's independently testable:
// call load() directly in tests without touching the singleton.
//
// Startup failures here (missing/invalid YAML, unknown provider/model)
// bail with os.Exit so the user gets a clear single-line error rather
// than a panic stack from deep inside the agent boot path.
func load() *AppConfig {
	homeDir, _ := os.UserHomeDir()
	var EVVA_HOME string
	if runtime.GOOS == "windows" {
		EVVA_HOME = homeDir + `\.` + AppName
	} else {
		EVVA_HOME = homeDir + "/." + AppName
	}

	// load deployment-level vars from .env (logging, app env, dir overrides)
	godotenv.Load(EVVA_HOME + "/.env")

	cfgPath := filepath.Join(EVVA_HOME, "config", "evva-config.yml")
	fileCfg, created, err := LoadFileConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "evva: %v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Fprintf(os.Stderr,
			"evva: wrote new config to %s — fill in your API keys to use cloud providers.\n",
			cfgPath)
	}

	defProvider, defModel, err := ResolveDefaultModel(fileCfg.DefaultProvider, fileCfg.DefaultModel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "evva: %v\n", err)
		os.Exit(1)
	}

	cfg := &AppConfig{
		AppName:    AppName,
		AppVersion: AppVersion,
		OS:         runtime.GOOS,
		AppEnv:     getEnvDefaultLowerCase("APP_ENV", "dev"),

		// log
		LogLevel:  getEnvDefaultLowerCase("LOG_LEVEL", "info"),
		LogFormat: getEnvDefaultLowerCase("LOG_FORMAT", "text"),
		LogDir:    getEnvNullable("LOG_DIR"),

		// global config .evva
		EvvaHome:            EVVA_HOME,
		EvvaHomeSkillsDir:   EVVA_HOME + "/" + getEnvDefault("SKILLS_DIR", "skills"),
		EvvaHomeUserProfile: EVVA_HOME + "/" + getEnvDefault("USER_PROFILE", "user_profile.md"),
		EvvaHomeConfigFile:  cfgPath,

		// from YAML
		DefaultMaxIterations: fileCfg.MaxIterations,
		DefaultMaxTokens:     fileCfg.MaxTokens,
		AutoCompactThreshold: fileCfg.AutoCompactThreshold,
		DisplayThinking:      fileCfg.DisplayThinking,
		TavilyAPIKey:         fileCfg.TavilyAPIKey,
		FetchMaxBytes:        fileCfg.FetchMaxBytes,
		DefaultProvider:      defProvider,
		DefaultModel:         defModel,
		DefaultEffort:        fileCfg.DefaultEffort,

		LoadedAt: time.Now(),
	}

	setupGlobalParam(cfg)
	setupWorkDirParam(cfg)
	setupLLMProviderConfig(cfg, fileCfg)

	return cfg
}

// SaveFile re-serializes the user-tunable subset to EvvaHomeConfigFile.
// Phase 2's /config setters and phase 3's /model switch both call this.
//
// Snapshots all fields under c.mu.RLock, releases that lock before
// blocking on disk I/O, then takes c.saveMu so concurrent saves don't
// interleave on the file.
func (c *AppConfig) SaveFile() error {
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
	fc := FileConfig{
		MaxIterations:        c.DefaultMaxIterations,
		MaxTokens:            c.DefaultMaxTokens,
		AutoCompactThreshold: c.AutoCompactThreshold,
		DisplayThinking:      c.DisplayThinking,
		DefaultProvider:      c.DefaultProvider.Name,
		DefaultModel:         string(c.DefaultModel),
		DefaultEffort:        c.DefaultEffort,
		FetchMaxBytes:        c.FetchMaxBytes,
		TavilyAPIKey:         c.TavilyAPIKey,
		Providers:            providers,
	}
	path := c.EvvaHomeConfigFile
	c.mu.RUnlock()

	c.saveMu.Lock()
	defer c.saveMu.Unlock()
	return SaveFileConfig(path, fc)
}

// IsDevelopment / IsProduction — semantic helpers so call sites
// don't hardcode string literals scattered across the codebase.
func (c *AppConfig) IsDevelopment() bool { return c.AppEnv == "dev" }
func (c *AppConfig) IsProduction() bool  { return c.AppEnv == "prod" }
