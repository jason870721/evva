package config

import (
	"os"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// LLMProviderAPIConfig is retained as a type alias for backward compat.
// New code should reference llm.APIConfig directly; this alias is removed
// when Phase 13a completes and the configs package becomes a thin shim.
type LLMProviderAPIConfig = llm.APIConfig

// setupGlobalParam ensures the global config directories exist. All
// user-tunable values are now sourced from evva-config.yml in load();
// this function only handles directory provisioning.
func setupGlobalParam(cfg *AppConfig) {
	_ = os.MkdirAll(cfg.EvvaHome, 0o755)
	_ = os.MkdirAll(cfg.EvvaHomeSkillsDir, 0o755)
}

// setupLLMProviderConfig wires per-provider credentials from the YAML
// file config. Providers with an empty api_url fall back to the
// constant's built-in default. Anthropic/DeepSeek/OpenAI need an api_key
// to be listed; Ollama is local and key-less.
func setupLLMProviderConfig(cfg *AppConfig, fc FileConfig) {
	cfg.LLMProviderConfig = map[string]LLMProviderAPIConfig{}

	register := func(provider constant.LLMProvider, fileEntry FileProviderConfig, requireKey bool) {
		//if requireKey && fileEntry.APIKey == "" {
		//	return
		//}
		url := fileEntry.APIURL
		if url == "" {
			url = provider.ApiUrl
		}
		cfg.LLMProviderConfig[provider.Name] = LLMProviderAPIConfig{
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

