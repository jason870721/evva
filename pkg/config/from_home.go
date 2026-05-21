package config

import (
	"os"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// setupGlobalParam ensures the per-user home directories exist. All
// user-tunable values are sourced from the YAML in Load(); this function
// only handles directory provisioning.
func setupGlobalParam(cfg *Config) {
	_ = os.MkdirAll(cfg.AppHome, 0o755)
	_ = os.MkdirAll(cfg.AppHomeSkillsDir, 0o755)
}

// setupLLMProviderConfig wires per-provider credentials from the YAML
// file config. Providers with an empty api_url fall back to the
// constant's built-in default. Anthropic/DeepSeek/OpenAI need an api_key
// to be listed; Ollama is local and key-less.
func setupLLMProviderConfig(cfg *Config, fc FileConfig) {
	cfg.LLMProviderConfig = map[string]llm.APIConfig{}

	register := func(provider constant.LLMProvider, fileEntry FileProviderConfig, requireKey bool) {
		url := fileEntry.APIURL
		if url == "" {
			url = provider.ApiUrl
		}
		cfg.LLMProviderConfig[provider.Name] = llm.APIConfig{
			ApiURL:    url,
			ApiSecret: fileEntry.APIKey,
			Models:    provider.Models,
		}
	}

	register(constant.OLLAMA, fc.Providers[constant.OLLAMA.Name], false)
	register(constant.ANTHROPIC, fc.Providers[constant.ANTHROPIC.Name], true)
	register(constant.DEEPSEEK, fc.Providers[constant.DEEPSEEK.Name], true)
	register(constant.OPENAI, fc.Providers[constant.OPENAI.Name], true)
}
