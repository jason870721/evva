package config

import (
	"os"
)

// setupGlobalParam ensures the global config directories exist.
func setupGlobalParam(cfg *AppConfig) {
	_ = os.MkdirAll(cfg.GlobalCfgDir, 0o755)
	_ = os.MkdirAll(cfg.GlobalSkillsDir, 0o755)
}

func setupLLMProviderConfig(cfg *AppConfig) {
	cfg.LLMProviderConfig = map[LLMProvider]LLMProviderAPIConfig{}

	// Ollama is local — no API key required.
	ollamaURL := getEnvDefault("OLLAMA_API_URL", "http://localhost:11434")
	cfg.LLMProviderConfig[Ollama] = LLMProviderAPIConfig{ApiURL: ollamaURL}

	if key := getEnvNullable("ANTHROPIC_API_KEY"); key != nil {
		url := getEnvDefault("ANTHROPIC_API_URL", "https://api.anthropic.com")
		cfg.LLMProviderConfig[Anthropic] = LLMProviderAPIConfig{ApiURL: url, ApiSecret: *key}
	}

	if key := getEnvNullable("DEEPSEEK_API_KEY"); key != nil {
		url := getEnvDefault("DEEPSEEK_API_URL", "https://api.deepseek.com")
		cfg.LLMProviderConfig[Deepseek] = LLMProviderAPIConfig{ApiURL: url, ApiSecret: *key}
	}

	if key := getEnvNullable("OPENAI_API_KEY"); key != nil {
		url := getEnvDefault("OPENAI_API_URL", "https://api.openai.com")
		cfg.LLMProviderConfig[OpenAI] = LLMProviderAPIConfig{ApiURL: url, ApiSecret: *key}
	}
}
