package config

import (
	"os"

	"github.com/johnny1110/evva/pkg/constant"
)

// setupGlobalParam ensures the per-user home directories exist. All
// user-tunable values are sourced from the YAML in Load(); this function
// only handles directory provisioning.
func setupGlobalParam(cfg *Config) {
	_ = os.MkdirAll(cfg.AppHome, 0o755)
	_ = os.MkdirAll(cfg.AppHomeSkillsDir, 0o755)
}

// setupLLMProviderConfig wires per-provider credentials from the YAML file
// config into LLMProviderConfig. Providers with an empty api_url fall back to
// the constant's built-in default.
//
// It registers EVERY provider in constant.GetAllProviders() — the same source
// SaveFile serializes from — so a newly-added provider (e.g. qwen) round-trips
// through the YAML automatically. A previous hardcoded 5-provider list silently
// dropped any provider not on it, which read back as "my api_key/api_url won't
// persist" and made the client fall back to the default endpoint.
func setupLLMProviderConfig(cfg *Config, fc FileConfig) {
	cfg.LLMProviderConfig = map[string]APIConfig{}

	for _, p := range constant.GetAllProviders() {
		entry := fc.Providers[p.Name]
		url := entry.APIURL
		if url == "" {
			url = p.ApiUrl
		}
		cfg.LLMProviderConfig[p.Name] = APIConfig{
			ApiURL:    url,
			ApiSecret: entry.APIKey,
			Models:    p.Models,
		}
	}
}
