package llm

import "net/http"

// LLMParams holds tunable request parameters shared across providers.
// Pointer fields preserve the "explicitly unset" semantic so each client can
// omit them and fall back to the upstream API's default.
type LLMParams struct {
	Temperature   *float64
	TopP          *float64
	TopK          *int
	MaxTokens     int
	StopSequences []string
	System        string
	Effort        int // from 1~n (every model provider should adapt their own impl)

	// HTTPClient overrides the transport used to talk to the provider.
	// nil → http.DefaultClient.
	HTTPClient *http.Client
}

// Option mutates LLMParams. Options are accepted by every client constructor and
// by the per-client Apply method, so the same knobs work at init and at runtime.
type Option func(*LLMParams)

func WithTemperature(v float64) Option { return func(p *LLMParams) { p.Temperature = &v } }
func WithTopP(v float64) Option        { return func(p *LLMParams) { p.TopP = &v } }
func WithTopK(v int) Option            { return func(p *LLMParams) { p.TopK = &v } }
func WithMaxTokens(v int) Option       { return func(p *LLMParams) { p.MaxTokens = v } }
func WithStopSequences(seqs ...string) Option {
	return func(p *LLMParams) { p.StopSequences = append([]string(nil), seqs...) }
}
func WithSystem(s string) Option           { return func(p *LLMParams) { p.System = s } }
func WithEffort(e int) Option              { return func(p *LLMParams) { p.Effort = e } }
func WithHTTPClient(c *http.Client) Option { return func(p *LLMParams) { p.HTTPClient = c } }

// Apply runs every option against p in order. Later options override earlier ones.
func (p *LLMParams) Apply(opts ...Option) {
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
}

// HTTP returns the configured *http.Client, defaulting to http.DefaultClient.
func (p *LLMParams) HTTP() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return http.DefaultClient
}

// --- effort name ↔ int mapping ---

var effortNames = map[string]int{
	"low":    1,
	"medium": 2,
	"high":   3,
	"ultra":  4,
}

// EffortNames returns the sorted list of valid effort level names.
func EffortNames() []string { return []string{"low", "medium", "high", "ultra"} }

// ParseEffort converts a lowercase effort name to its int value.
// Returns 0 when the name is unknown.
func ParseEffort(name string) int { return effortNames[name] }

// EffortString converts an int level back to its name. Returns "medium" for unknowns.
func EffortString(level int) string {
	for name, n := range effortNames {
		if n == level {
			return name
		}
	}
	return "medium"
}
