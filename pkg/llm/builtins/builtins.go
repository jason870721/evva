// Package builtins registers evva's bundled LLM providers (Anthropic,
// DeepSeek, GLM, OpenAI, Ollama, Qwen) into pkg/llm.DefaultRegistry().
//
// Import this package for its side effect when you want the standard
// kit available without picking providers individually:
//
//	import _ "github.com/johnny1110/evva/pkg/llm/builtins"
//
// Downstream apps that want only a subset register the specific
// providers themselves — see pkg/llm/{claude,deepseek,openai,ollama}.Factory.
package builtins

import (
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/llm/claude"
	"github.com/johnny1110/evva/pkg/llm/deepseek"
	"github.com/johnny1110/evva/pkg/llm/glm"
	"github.com/johnny1110/evva/pkg/llm/ollama"
	"github.com/johnny1110/evva/pkg/llm/openai"
	"github.com/johnny1110/evva/pkg/llm/qwen"
)

func init() {
	r := llm.DefaultRegistry()
	r.MustRegister(claude.ProviderName, claude.Factory)
	r.MustRegister(deepseek.ProviderName, deepseek.Factory)
	r.MustRegister(glm.ProviderName, glm.Factory)
	r.MustRegister(ollama.ProviderName, ollama.Factory)
	r.MustRegister(openai.ProviderName, openai.Factory)
	r.MustRegister(qwen.ProviderName, qwen.Factory)

	// Embedders are a SEPARATE registry because the providers that can embed
	// are a strict subset of those that can chat. Anthropic ships no
	// embedding endpoint at all, so registering it here would only produce a
	// runtime failure for anyone who trusted the name being present.
	e := llm.DefaultEmbedderRegistry()
	e.MustRegister(ollama.ProviderName, ollama.EmbedderFactory)
	e.MustRegister(openai.ProviderName, openai.EmbedderFactory)
}
