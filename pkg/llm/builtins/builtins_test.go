package builtins

import (
	"testing"

	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/llm/claude"
	"github.com/johnny1110/evva/pkg/llm/deepseek"
	"github.com/johnny1110/evva/pkg/llm/ollama"
	"github.com/johnny1110/evva/pkg/llm/openai"
)

// Importing this package's init populates the default registry. Verify
// every bundled provider name is present so accidental removals fail
// CI before they reach a user.
func TestBuiltinsRegistered(t *testing.T) {
	r := llm.DefaultRegistry()
	for _, name := range []string{claude.ProviderName, deepseek.ProviderName, ollama.ProviderName, openai.ProviderName} {
		if !r.Has(name) {
			t.Errorf("DefaultRegistry should have provider %q registered", name)
		}
	}
}
