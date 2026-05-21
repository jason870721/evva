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
		appVersion = DefaultAppVersion
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

	// load deployment-level vars from .env (logging, app env, dir overrides)
	godotenv.Load(appHome + "/.env")

	cfgPath := filepath.Join(appHome, "config", appName+"-config.yml")
	fileCfg, created, err := LoadFileConfig(cfgPath)
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
		TavilyAPIKey:         fileCfg.TavilyAPIKey,
		FetchMaxBytes:        fileCfg.FetchMaxBytes,
		DefaultProvider:      defProvider,
		DefaultModel:         defModel,
		DefaultEffort:        fileCfg.DefaultEffort,
		DefaultProfile:       fileCfg.DefaultProfile,
		PermissionMode:       fileCfg.PermissionMode,

		LoadedAt: time.Now(),
	}

	setupGlobalParam(cfg)
	setupWorkDirParam(cfg, workdir)
	setupLLMProviderConfig(cfg, fileCfg)

	return cfg, nil
}
