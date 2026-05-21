package claude

import "github.com/johnny1110/evva/pkg/llm"

// ProviderName is the registry key under which this client registers.
// External hosts use this when calling pkg/llm.DefaultRegistry().Has /
// /Build, and it matches the Name field of constant.ANTHROPIC.
const ProviderName = "anthropic"

// Factory adapts New into a llm.ClientFactory. Registered into
// pkg/llm.DefaultRegistry() by pkg/llm/builtins; downstream apps that
// want to register Claude on a non-default registry can call this
// directly.
func Factory(cfg llm.APIConfig, model string, opts ...llm.Option) (llm.Client, error) {
	return New(cfg, model, opts...), nil
}
