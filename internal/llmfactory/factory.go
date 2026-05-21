// Package llmfactory glues the evva runtime to pkg/llm's provider registry.
//
// Today's responsibility: read the per-provider APIConfig from the
// process-wide AppConfig and forward to pkg/llm.DefaultRegistry().Build.
// Once Phase 13a lands and config is injected as a value, this package
// can be deleted — the agent will call the registry directly.
//
// Adding a new provider does not require changes here. Instead:
//  1. Implement llm.Client (typically under pkg/llm/<name>/ or a
//     downstream-owned package).
//  2. Register the factory: pkg/llm.DefaultRegistry().Register("<name>",
//     factory). For evva's bundled providers, blank-import
//     pkg/llm/builtins (cmd/evva does this).
package llmfactory

import (
	"fmt"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

// Of constructs the concrete llm.Client for the requested provider, looking
// up its APIConfig (URL + secret) from the loaded application config and
// resolving the factory through pkg/llm.DefaultRegistry.
//
// Returns an error when the provider has no APIConfig loaded or when no
// factory is registered for the provider name.
func Of(provider constant.LLMProvider, model constant.Model, opts []llm.Option) (llm.Client, error) {
	cfg := config.Get()
	api, ok := cfg.LLMProviderConfig[provider.Name]
	if !ok {
		return nil, fmt.Errorf("provider: [%s] API_KEY not set", provider.Name)
	}
	client, err := llm.DefaultRegistry().Build(provider.Name, string(model), api, opts)
	if err != nil {
		return nil, err
	}
	return client, nil
}
