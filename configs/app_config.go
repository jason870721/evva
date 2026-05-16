package config

import (
	"os"
	"runtime"
	"sync"
	"time"

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
	AppEnv  string // default: "development"
	AppName string // default: "app"

	// Global config dir
	EvvaHome             string
	EvvaHomeSkillsDir    string
	EvvaHomeUserProfile  string
	AutoCompactThreshold float64

	// Work dir
	WorkDir          string
	WorkDirSkillsDir string

	// llm providers(from .env) key: provider name, value: provider config
	LLMProviderConfig map[string]LLMProviderAPIConfig

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
}

var (
	instance *AppConfig
	once     sync.Once
)

const AppName = "evva"

// Get returns the singleton AppConfig, initializing it on first call.
// Safe for concurrent use — subsequent calls after the first are lock-free reads.
func Get() *AppConfig {
	once.Do(func() {
		instance = load()
	})
	return instance
}

// load performs the actual env parsing.
// Isolated from Get() so it's independently testable:
// call load() directly in tests without touching the singleton.
func load() *AppConfig {
	homeDir, _ := os.UserHomeDir()
	var EVVA_HOME string
	if runtime.GOOS == "windows" {
		EVVA_HOME = homeDir + `\.` + AppName
	} else {
		EVVA_HOME = homeDir + "/." + AppName
	}

	// load from .env
	godotenv.Load(EVVA_HOME + "/.env")

	cfg := &AppConfig{
		AppName: AppName,
		OS:      runtime.GOOS,
		AppEnv:  getEnvDefaultLowerCase("APP_ENV", "dev"),

		// log
		LogLevel:  getEnvDefaultLowerCase("LOG_LEVEL", "info"),
		LogFormat: getEnvDefaultLowerCase("LOG_FORMAT", "text"),
		LogDir:    getEnvNullable("LOG_DIR"),

		// global config .evva
		EvvaHome:            EVVA_HOME,
		EvvaHomeSkillsDir:   EVVA_HOME + "/" + getEnvDefault("SKILLS_DIR", "skills"),
		EvvaHomeUserProfile: EVVA_HOME + "/" + getEnvDefault("USER_PROFILE", "user_profile.md"),

		DefaultMaxIterations: getEnvDefaultInt("DEFAULT_MAX_ITERATIONS", "30"),
		DefaultMaxTokens:     getEnvDefaultInt("DEFAULT_MAX_TOKENS", "4096"),
		AutoCompactThreshold: getEnvDefaultFloat("AUTO_COMPACT_THRESHOLD", "0.8"),

		LoadedAt: time.Now(),
	}

	setupGlobalParam(cfg)
	setupWorkDirParam(cfg)
	setupLLMProviderConfig(cfg)

	return cfg
}

// IsDevelopment / IsProduction — semantic helpers so call sites
// don't hardcode string literals scattered across the codebase.
func (c *AppConfig) IsDevelopment() bool { return c.AppEnv == "dev" }
func (c *AppConfig) IsProduction() bool  { return c.AppEnv == "prod" }
