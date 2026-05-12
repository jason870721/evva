package config

type LLMProvider string

const (
	Ollama    LLMProvider = "ollama"
	Anthropic LLMProvider = "anthropic"
	Deepseek  LLMProvider = "deepseek"
	OpenAI    LLMProvider = "openai"
)

type LLMProviderAPIConfig struct {
	ApiURL    string
	ApiSecret string
}
