package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/joho/godotenv"
)

// LoadOptions tunes Load. Zero-value fields fall back to LoadDefault
// behavior — AppName="evva", AppHome=~/.evva/, WorkDir=os.Getwd(),
// AppVersion=DefaultAppVersion. Downstream apps that want a different
// home dir or app name fill in the relevant fields.
type LoadOptions struct {
	AppName    string // brand identifier; drives the AppHome layout. Defaults to "evva".
	AppHome    string // absolute path; defaults to ~/.<AppName>/.
	WorkDir    string // process cwd; defaults to os.Getwd().
	AppVersion string // version string for diagnostics; defaults to DefaultAppVersion.

	// EnvAliases maps the caller's preferred env-var names onto evva's
	// canonical ones BEFORE godotenv.Load runs. Useful when a downstream
	// app advertises friendlier spellings — e.g. `{"LOGDIR": "LOG_DIR",
	// "LOGLEVEL": "LOG_LEVEL"}` lets a friday user write either form in
	// `~/.friday/.env` and have evva's loader pick it up.
	//
	// The promotion is non-overriding: an alias only seeds the canonical
	// name when that canonical name is unset. Existing canonical exports
	// win, so a deliberate `LOG_DIR=...` is never clobbered by a stray
	// alias.
	EnvAliases map[string]string

	// EnvOverrides runs AFTER the YAML + canonical env-vars have built
	// the Config. Each entry gets the populated *Config and can fold in
	// env vars that don't have a native hook inside Load (e.g.
	// MAX_ITERS → cfg.SetMaxIterations). The first error short-circuits
	// the rest and is returned from Load wrapped with the failing
	// override's Name — `config: EnvOverrides[<name>]: <wrapped>` — so
	// a downstream app with several overrides can identify the culprit
	// without grepping stack traces.
	//
	// Use this to translate downstream-flavoured env conventions
	// (APIKEY → cfg.SetProviderCredentials, MAX_ITERS → cfg.SetMaxIterations)
	// in one place instead of post-Load shim code at every call site.
	EnvOverrides []EnvOverride

	// ProviderCredentials wires LLM provider credentials from env vars
	// declaratively — the alternative to writing a separate EnvOverride
	// that reads os.Getenv(...) and calls cfg.SetProviderCredentials.
	//
	// Keyed by provider name (matches pkg/llm.DefaultRegistry); the
	// ProviderCredsFromEnv value names the env vars to read. EnvAliases
	// promotion runs first, so aliased names (e.g. `APIKEY` → `DEEPSEEK_API_KEY`)
	// reach this layer through their canonical form. Empty env-var values
	// are passed through to SetProviderCredentials — the agent's LLM-build
	// step will surface "API_KEY not set" loudly on first Run.
	//
	// Example:
	//
	//	LoadOptions{
	//	    ProviderCredentials: map[string]config.ProviderCredsFromEnv{
	//	        "deepseek": {APIKeyEnv: "DEEPSEEK_API_KEY",
	//	                    APIURLDefault: constant.DEEPSEEK.ApiUrl},
	//	    },
	//	}
	//
	// Applied AFTER the YAML loader populates LLMProviderConfig but
	// BEFORE EnvOverrides run, so an EnvOverride can still mutate the
	// installed creds if it needs to.
	ProviderCredentials map[string]ProviderCredsFromEnv

	// SeedEnvTemplate is written to <AppHome>/.env on first launch when
	// the file is missing. Useful for closing the "evva creates the YAML
	// but the user doesn't know to create .env" first-run gap.
	//
	// Empty means "don't write a .env template" (the historical behaviour).
	// An existing .env is never overwritten, even with SeedEnvTemplate set.
	SeedEnvTemplate string
}

// EnvOverride is one entry in LoadOptions.EnvOverrides. The Name is used
// only for diagnostics — when Fn returns an error, Load wraps it as
// `config: EnvOverrides[<Name>]: <err>` so the failing override is
// identifiable in logs without re-running Load.
type EnvOverride struct {
	Name string
	Fn   func(*Config) error
}

// ProviderCredsFromEnv declares which env vars carry a provider's
// credentials. Empty fields are skipped (the YAML default wins for that
// slot). See LoadOptions.ProviderCredentials.
type ProviderCredsFromEnv struct {
	// APIKeyEnv is the env-var name carrying the provider's API key
	// (e.g. "DEEPSEEK_API_KEY"). Empty means "no key from env".
	APIKeyEnv string
	// APIURLEnv is the env-var name carrying the provider's API URL.
	// Most consumers leave this empty and lean on APIURLDefault.
	APIURLEnv string
	// APIURLDefault is the URL to use when APIURLEnv is empty or unset.
	// Typically a constant from pkg/constant (e.g. constant.DEEPSEEK.ApiUrl).
	APIURLDefault string
}

// LoadDefault returns a Config populated with evva's historical defaults:
// AppName="evva", AppHome=~/.evva/, WorkDir=os.Getwd(). Intended for the
// bundled cmd/evva binary and for backward-compatible callers.
//
// Startup failures (missing/invalid YAML, unknown provider/model) bail
// with os.Exit so the user gets a clear single-line error rather than a
// panic stack from deep inside the agent boot path.
func LoadDefault() *Config {
	cfg, err := Load(LoadOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "evva: %v\n", err)
		os.Exit(1)
	}
	return cfg
}

// Load parses env vars + the per-user YAML and returns a populated Config.
// Each LoadOptions field has a sensible default (see LoadOptions doc).
//
// Unlike LoadDefault, Load returns an error instead of calling os.Exit so
// downstream hosts can surface it through their own error path.
func Load(opts LoadOptions) (*Config, error) {
	appName := opts.AppName
	if appName == "" {
		appName = DefaultAppName
	}
	appVersion := opts.AppVersion
	if appVersion == "" {
		appVersion = Version
		if appVersion == "" {
			appVersion = DefaultAppVersion
		}
	}
	appHome := opts.AppHome
	if appHome == "" {
		homeDir, _ := os.UserHomeDir()
		if runtime.GOOS == "windows" {
			appHome = homeDir + `\.` + appName
		} else {
			appHome = homeDir + "/." + appName
		}
	}
	workdir := opts.WorkDir
	if workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			wd = "."
		}
		workdir = wd
	}

	// Promote any caller-declared env-var aliases into evva's canonical
	// names BEFORE godotenv reads .env. Non-overriding: an existing
	// canonical export wins.
	applyEnvAliases(opts.EnvAliases)

	// Seed <AppHome>/.env on first launch if the caller supplied a
	// template AND the file doesn't already exist. Runs before
	// godotenv.Load so the template's vars take effect on the very
	// first run, not the second.
	seedEnvTemplate(appHome, opts.SeedEnvTemplate)

	// load deployment-level vars from .env (logging, app env, dir overrides)
	godotenv.Load(appHome + "/.env")

	// Re-apply aliases after godotenv runs so a .env file using the alias
	// form (e.g. `LOGDIR=/var/log/friday`) also promotes into the
	// canonical name. godotenv.Load is non-overriding, so this two-pass
	// approach lets the alias work whether the user exports it in the
	// shell or writes it in .env.
	applyEnvAliases(opts.EnvAliases)

	cfgPath := filepath.Join(appHome, "config", appName+"-config.yml")
	fileCfg, created, err := LoadFileConfig(cfgPath, appName)
	if err != nil {
		return nil, err
	}
	if created {
		fmt.Fprintf(os.Stderr,
			"%s: wrote new config to %s — fill in your API keys to use cloud providers.\n",
			appName, cfgPath)
	}

	defProvider, defModel, err := ResolveDefaultModel(fileCfg.DefaultProvider, fileCfg.DefaultModel)
	if err != nil {
		return nil, err
	}

	enableAutoMem := true
	if fileCfg.EnableAutoMemory != nil {
		enableAutoMem = *fileCfg.EnableAutoMemory
	}
	// Env override: EVVA_AUTO_MEMORY=0/false forces off regardless of YAML.
	if v := os.Getenv("EVVA_AUTO_MEMORY"); v != "" {
		switch v {
		case "0", "false", "FALSE", "off", "OFF", "no", "NO":
			enableAutoMem = false
		case "1", "true", "TRUE", "on", "ON", "yes", "YES":
			enableAutoMem = true
		}
	}

	enableMemRecall := true
	if fileCfg.EnableMemoryRecall != nil {
		enableMemRecall = *fileCfg.EnableMemoryRecall
	}

	cfg := &Config{
		AppName:    appName,
		AppVersion: appVersion,
		OS:         runtime.GOOS,
		AppEnv:     getEnvDefaultLowerCase("APP_ENV", "dev"),

		// log
		LogLevel:  getEnvDefaultLowerCase("LOG_LEVEL", "info"),
		LogFormat: getEnvDefaultLowerCase("LOG_FORMAT", "text"),
		LogDir:    resolveLogDir(appHome),

		// per-user home dir
		AppHome:            appHome,
		AppHomeSkillsDir:   appHome + "/" + getEnvDefault("SKILLS_DIR", "skills"),
		AppHomeUserProfile: appHome + "/" + getEnvDefault("USER_PROFILE", "user_profile.md"),
		AppHomeConfigFile:  cfgPath,

		// from YAML
		DefaultMaxIterations: fileCfg.MaxIterations,
		DefaultMaxTokens:     fileCfg.MaxTokens,
		AutoCompactThreshold: fileCfg.AutoCompactThreshold,
		DisplayThinking:      fileCfg.DisplayThinking,
		EnableAutoMemory:     enableAutoMem,
		EnableMemoryRecall:   enableMemRecall,
		MemoryRecallModel:    fileCfg.MemoryRecallModel,
		TavilyAPIKey:         fileCfg.TavilyAPIKey,
		FetchMaxBytes:        fileCfg.FetchMaxBytes,
		DefaultProvider:      defProvider,
		DefaultModel:         defModel,
		DefaultEffort:        fileCfg.DefaultEffort,
		DefaultProfile:       fileCfg.DefaultProfile,
		PermissionMode:       fileCfg.PermissionMode,
		CustomConfig:         fileCfg.Custom,

		LoadedAt: time.Now(),
	}

	setupGlobalParam(cfg)
	setupWorkDirParam(cfg, workdir)
	setupLLMProviderConfig(cfg, fileCfg)

	// Apply declarative provider-credentials wiring (Round 2 of
	// friday's SDK feedback). Runs BEFORE EnvOverrides so an override
	// can still mutate the installed creds if it needs to.
	if err := applyProviderCredentials(cfg, opts.ProviderCredentials); err != nil {
		return nil, err
	}

	// Apply caller-declared env overrides last so they can mutate the
	// already-populated cfg (e.g. fold MAX_ITERS into DefaultMaxIterations
	// without a post-Load shim). Short-circuits on first error; the
	// wrapping includes the failing override's Name for diagnostics.
	//
	// An empty Name would render the wrapped error as
	// "config: EnvOverrides[]: ..." — which is unhelpful — so reject
	// nameless entries at validation time.
	for i, ov := range opts.EnvOverrides {
		if ov.Fn == nil {
			continue
		}
		if ov.Name == "" {
			return nil, fmt.Errorf("config: EnvOverrides[%d]: Name is required", i)
		}
		if err := ov.Fn(cfg); err != nil {
			return nil, fmt.Errorf("config: EnvOverrides[%s]: %w", ov.Name, err)
		}
	}

	return cfg, nil
}

// applyProviderCredentials walks LoadOptions.ProviderCredentials and
// calls cfg.SetProviderCredentials for each entry. Empty env-var names
// pass through as empty values — SetProviderCredentials accepts them
// and the agent's LLM-build step surfaces the missing-key error later.
func applyProviderCredentials(cfg *Config, m map[string]ProviderCredsFromEnv) error {
	for name, src := range m {
		var apiKey, apiURL string
		if src.APIKeyEnv != "" {
			apiKey = os.Getenv(src.APIKeyEnv)
		}
		if src.APIURLEnv != "" {
			apiURL = os.Getenv(src.APIURLEnv)
		}
		if apiURL == "" {
			apiURL = src.APIURLDefault
		}
		if err := cfg.SetProviderCredentials(name, apiURL, apiKey); err != nil {
			return fmt.Errorf("config: ProviderCredentials[%s]: %w", name, err)
		}
	}
	return nil
}

// seedEnvTemplate writes template to <appHome>/.env when the file is
// missing and template is non-empty. Failures are logged to stderr but
// not fatal — a write-protected home dir shouldn't break startup.
func seedEnvTemplate(appHome, template string) {
	if template == "" || appHome == "" {
		return
	}
	envPath := filepath.Join(appHome, ".env")
	if _, err := os.Stat(envPath); err == nil {
		return // file exists; never overwrite
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "config: stat %s: %v\n", envPath, err)
		return
	}
	if err := os.MkdirAll(appHome, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "config: mkdir %s: %v\n", appHome, err)
		return
	}
	if err := os.WriteFile(envPath, []byte(template), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "config: seed .env at %s: %v\n", envPath, err)
		return
	}
	fmt.Fprintf(os.Stderr, "config: seeded %s — edit it to set API keys.\n", envPath)
}

// applyEnvAliases promotes the values of alias env vars into the
// canonical names listed in m. Non-overriding: an existing canonical
// export is never clobbered. Empty values are skipped.
func applyEnvAliases(m map[string]string) {
	for alias, canonical := range m {
		if alias == "" || canonical == "" {
			continue
		}
		v := os.Getenv(alias)
		if v == "" {
			continue
		}
		if os.Getenv(canonical) != "" {
			continue
		}
		_ = os.Setenv(canonical, v)
	}
}
