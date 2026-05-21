package llm

import "github.com/johnny1110/evva/pkg/constant"

// APIConfig carries the per-provider credentials a llm.Client needs to talk
// to its backend. The host (cmd/evva or a downstream consumer) constructs
// one APIConfig per provider from whatever config source it uses (YAML,
// env vars, secret manager) and passes it to the registry-resolved
// ClientFactory.
//
// ApiURL is the base endpoint; empty means "use the provider's built-in
// default" (each provider's New() applies its own fallback). ApiSecret is
// the API key for cloud providers; local providers like Ollama leave it
// empty. Models lists the model identifiers this provider knows about —
// kept for parity with the legacy configs.LLMProviderAPIConfig shape,
// but neither the registry nor the built-in providers read it today.
type APIConfig struct {
	ApiURL    string
	ApiSecret string
	Models    []constant.Model
}
