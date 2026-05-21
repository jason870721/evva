package agent

// Tests in this package construct real agents that resolve providers
// through pkg/llm.DefaultRegistry. The blank import registers the
// bundled providers (anthropic/deepseek/ollama) so the tests don't have
// to do it themselves.
import _ "github.com/johnny1110/evva/pkg/llm/builtins"
