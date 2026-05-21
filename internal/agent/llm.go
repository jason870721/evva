package agent

import (
	"fmt"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// buildLLMClient resolves a (provider, model) pair against the supplied
// Config's per-provider APIConfig and the process-wide llm.DefaultRegistry.
//
// Replaces the old internal/llmfactory.Of path: the registry already knows
// every built-in provider (via pkg/llm/builtins) plus any custom provider
// a downstream host registered before the first agent boots.
func buildLLMClient(cfg *config.Config, provider constant.LLMProvider, model constant.Model, opts []llm.Option) (llm.Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("agent: nil config when building llm client for %q", provider.Name)
	}
	api, ok := cfg.LLMProviderConfig[provider.Name]
	if !ok {
		return nil, fmt.Errorf("provider: [%s] API_KEY not set", provider.Name)
	}
	return llm.DefaultRegistry().Build(provider.Name, string(model), api, opts)
}
