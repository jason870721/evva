package constant

type LLMProvider struct {
	Name   string
	ApiUrl string
	Models []Model
}

// Use var for struct "constants"
// Models are ordered by price
var (
	OLLAMA    = LLMProvider{Name: "ollama", ApiUrl: "http://localhost:11434", Models: []Model{QWEN_3_6}}
	ANTHROPIC = LLMProvider{Name: "anthropic", ApiUrl: "https://api.anthropic.com", Models: []Model{SONNET_4_6, OPUS_4_7}}
	DEEPSEEK  = LLMProvider{Name: "deepseek", ApiUrl: "https://api.deepseek.com", Models: []Model{DEEPSEEK_V4_FLASH, DEEPSEEK_V4_PRO}}
	// OPENAI — all currently listed models are reasoning-class (gpt-5 / o-series).
	// If a non-reasoning model (gpt-4*, gpt-3.5*) is added here later, update
	// pkg/llm/openai/client.go isReasoningModel to match.
	OPENAI = LLMProvider{Name: "openai", ApiUrl: "https://api.openai.com", Models: []Model{GPT_5_4_MINI, GPT_5_5}}
)

type Model string

const (
	// OLLAMA
	QWEN_3_6 Model = "qwen3.6"

	// ANTHROPIC
	SONNET_4_6 Model = "claude-sonnet-4-6"
	OPUS_4_7   Model = "claude-opus-4-7"

	// DEEPSEEK
	DEEPSEEK_V4_FLASH Model = "deepseek-v4-flash"
	DEEPSEEK_V4_PRO   Model = "deepseek-v4-pro"

	// OPENAI
	GPT_5_4_MINI Model = "gpt-5.4-mini"
	GPT_5_5      Model = "gpt-5.5"
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

// GetModel resolves a string to its typed Model constant. Mirrors
// GetProvider; the canonical model registry is MODEL_CONTEXT_SIZE.
func GetModel(name string) (Model, bool) {
	for m := range MODEL_CONTEXT_SIZE {
		if string(m) == name {
			return m, true
		}
	}
	return Model(""), false
}

// ModelForLevel returns the model to use at a given capability tier:
//
//   - level 1 (default) → the smallest / cheapest model the provider lists
//     (Models[0]). Suitable for routine work; "normal" tier.
//   - level 2 → the largest model the provider lists (Models[len-1]).
//     Suitable for hard reasoning; "big" tier. More expensive — the LLM
//     should only pick this when the task genuinely needs deeper thinking.
//
// Providers with only one configured model collapse both tiers to that
// model. Levels outside {1, 2} clamp to the nearest valid tier.
func (p LLMProvider) ModelForLevel(level int) Model {
	if len(p.Models) == 0 {
		return ""
	}
	if level >= 2 {
		return p.Models[len(p.Models)-1]
	}
	return p.Models[0]
}

// for completion
var MODEL_CONTEXT_SIZE = map[Model]int{
	QWEN_3_6:          262_144,
	SONNET_4_6:        500_000,
	OPUS_4_7:          500_000,
	DEEPSEEK_V4_FLASH: 1_000_000,
	DEEPSEEK_V4_PRO:   1_000_000,
	GPT_5_4_MINI:      400_000,
	GPT_5_5:           1_050_000,
}
