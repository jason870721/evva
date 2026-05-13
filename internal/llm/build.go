package llm

import (
	"fmt"
	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/constant"
	"github.com/johnny1110/evva/internal/llm/claude"
	"github.com/johnny1110/evva/internal/llm/deepseek"
	"github.com/johnny1110/evva/internal/llm/ollama"
)

// Of factory mode
func Of(provider constant.LLMProvider, model constant.Model, opts []Option) (Client, error) {

	cfg := config.Get()
	api, ok := cfg.LLMProviderConfig[provider.Name]
	if !ok {
		return nil, fmt.Errorf("provider: [%s] API_KEY not set", provider.Name)
	}

	switch provider {
	case constant.ANTHROPIC:
		return claude.New(api, string(model), opts...), nil
	case constant.DEEPSEEK:
		return deepseek.New(api, string(model), opts...), nil
	case constant.OLLAMA:
		return ollama.New(api, string(model), opts...), nil
	default:
		return nil, fmt.Errorf("unknown provider %q (want anthropic | deepseek | ollama)", provider.Name)
	}

}
