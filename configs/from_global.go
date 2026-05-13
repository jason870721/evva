package config

import (
	"github.com/johnny1110/evva/internal/constant"
	"os"
)

// setupGlobalParam ensures the global config directories exist.
func setupGlobalParam(cfg *AppConfig) {
	_ = os.MkdirAll(cfg.GlobalCfgDir, 0o755)
	_ = os.MkdirAll(cfg.GlobalSkillsDir, 0o755)
}

func setupLLMProviderConfig(cfg *AppConfig) {
	cfg.LLMProviderConfig = map[string]LLMProviderAPIConfig{}

	// Ollama is local — no API key required.
	ollamaURL := getEnvDefault("OLLAMA_API_URL", constant.OLLAMA.ApiUrl)
	cfg.LLMProviderConfig[constant.OLLAMA.Name] = LLMProviderAPIConfig{ApiURL: ollamaURL, Models: constant.OLLAMA.Models}

	if key := getEnvNullable("ANTHROPIC_API_KEY"); key != nil {
		url := getEnvDefault("ANTHROPIC_API_URL", constant.ANTHROPIC.ApiUrl)
		cfg.LLMProviderConfig[constant.ANTHROPIC.Name] = LLMProviderAPIConfig{ApiURL: url, ApiSecret: *key, Models: constant.ANTHROPIC.Models}
	}

	if key := getEnvNullable("DEEPSEEK_API_KEY"); key != nil {
		url := getEnvDefault("DEEPSEEK_API_URL", constant.DEEPSEEK.ApiUrl)
		cfg.LLMProviderConfig[constant.DEEPSEEK.Name] = LLMProviderAPIConfig{ApiURL: url, ApiSecret: *key, Models: constant.DEEPSEEK.Models}
	}

	if key := getEnvNullable("OPENAI_API_KEY"); key != nil {
		url := getEnvDefault("OPENAI_API_URL", constant.OPENAI.ApiUrl)
		cfg.LLMProviderConfig[constant.OPENAI.Name] = LLMProviderAPIConfig{ApiURL: url, ApiSecret: *key, Models: constant.OPENAI.Models}
	}
}

type LLMProviderAPIConfig struct {
	ApiURL    string
	ApiSecret string
	Models    []constant.Model
}
