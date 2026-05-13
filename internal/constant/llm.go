package constant

type LLMProvider struct {
	Name   string
	ApiUrl string
	Models []Model
}

// Use var for struct "constants"
var (
	OLLAMA    = LLMProvider{Name: "ollama", ApiUrl: "http://localhost:11434", Models: []Model{QWEN_3_6}}
	ANTHROPIC = LLMProvider{Name: "anthropic", ApiUrl: "https://api.anthropic.com", Models: []Model{SONNET_4_6, OPUS_4_7}}
	DEEPSEEK  = LLMProvider{Name: "deepseek", ApiUrl: "https://api.deepseek.com", Models: []Model{DEEPSEEK_V4_FLASH, DEEPSEEK_V4_PRO}}
	OPENAI    = LLMProvider{Name: "openai", ApiUrl: "https://api.openai.com", Models: []Model{GPT_5_5}}
)

type Model string

const (
	// OLLAMA
	QWEN_3_6 Model = "qwen3:6"

	// ANTHROPIC
	SONNET_4_6 Model = "claude-sonnet-4-6"
	OPUS_4_7   Model = "claude-opus-4-7"

	// DEEPSEEK
	DEEPSEEK_V4_FLASH Model = "deepseek-v4-flash"
	DEEPSEEK_V4_PRO   Model = "deepseek-v4-pro"

	// OPENAI
	GPT_5_5 Model = "gpt-5.5"
)

func GetAllProviders() []LLMProvider {
	return []LLMProvider{OLLAMA, ANTHROPIC, DEEPSEEK, OPENAI}
}

func GetProvider(name string) (LLMProvider, bool) {
	for _, pvd := range GetAllProviders() {
		if pvd.Name == name {
			return pvd, true
		}
	}

	return LLMProvider{}, false
}
