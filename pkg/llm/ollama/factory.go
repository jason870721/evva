package ollama

import "github.com/johnny1110/evva/pkg/llm"

// ProviderName is the registry key under which this client registers.
const ProviderName = "ollama"

// Factory adapts New into a llm.ClientFactory. Registered into
// pkg/llm.DefaultRegistry() by pkg/llm/builtins; downstream apps that
// want to register Ollama on a non-default registry can call this
// directly.
func Factory(cfg llm.APIConfig, model string, opts ...llm.Option) (llm.Client, error) {
	return New(cfg, model, opts...), nil
}
