// Package builtins registers evva's bundled LLM providers (Anthropic,
// DeepSeek, Ollama) into pkg/llm.DefaultRegistry().
//
// Import this package for its side effect when you want the standard
// kit available without picking providers individually:
//
//	import _ "github.com/johnny1110/evva/pkg/llm/builtins"
//
// Downstream apps that want only a subset register the specific
// providers themselves — see pkg/llm/{claude,deepseek,ollama}.Factory.
package builtins

import (
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/llm/claude"
	"github.com/johnny1110/evva/pkg/llm/deepseek"
	"github.com/johnny1110/evva/pkg/llm/ollama"
)

func init() {
	r := llm.DefaultRegistry()
	r.MustRegister(claude.ProviderName, claude.Factory)
	r.MustRegister(deepseek.ProviderName, deepseek.Factory)
	r.MustRegister(ollama.ProviderName, ollama.Factory)
}
