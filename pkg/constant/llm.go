package constant

import "slices"

type LLMProvider struct {
	Name   string
	ApiUrl string
	Models []Model
}

// Use var for struct "constants"
// Models are ordered by price
var (
	OLLAMA    = LLMProvider{Name: "ollama", ApiUrl: "http://localhost:11434", Models: []Model{QWEN_3_6}}
	ANTHROPIC = LLMProvider{Name: "anthropic", ApiUrl: "https://api.anthropic.com", Models: []Model{SONNET_4_6, OPUS_4_8}}
	DEEPSEEK  = LLMProvider{Name: "deepseek", ApiUrl: "https://api.deepseek.com", Models: []Model{DEEPSEEK_V4_FLASH, DEEPSEEK_V4_PRO}}
	// OPENAI — all currently listed models are reasoning-class (gpt-5 / o-series).
	// If a non-reasoning model (gpt-4*, gpt-3.5*) is added here later, update
	// pkg/llm/openai/client.go isReasoningModel to match.
	OPENAI = LLMProvider{Name: "openai", ApiUrl: "https://api.openai.com", Models: []Model{GPT_5_4_MINI, GPT_5_5}}
	// GLM — Zhipu AI / z.ai, reached over its Anthropic-compatible endpoint
	// (same wire format as ANTHROPIC; pkg/llm/glm copies that engine and swaps
	// auth to Authorization: Bearer). ApiUrl is the z.ai Anthropic gateway, so
	// pkg/llm/glm hits ApiUrl + "/v1/messages".
	GLM = LLMProvider{Name: "glm", ApiUrl: "https://api.z.ai/api/anthropic", Models: []Model{GLM_4_6, GLM_5_2}}
)

type Model string

const (
	// OLLAMA
	QWEN_3_6 Model = "qwen3.6"

	// ANTHROPIC
	SONNET_4_6 Model = "claude-sonnet-4-6"
	OPUS_4_8   Model = "claude-opus-4-8"

	// DEEPSEEK
	DEEPSEEK_V4_FLASH Model = "deepseek-v4-flash"
	DEEPSEEK_V4_PRO   Model = "deepseek-v4-pro"

	// OPENAI
	GPT_5_4_MINI Model = "gpt-5.4-mini"
	GPT_5_5      Model = "gpt-5.5"

	// GLM (Zhipu / z.ai). glm-4.6 is the cheaper coding model (normal tier),
	// glm-5.2 the flagship (big tier). Both are reached over the Anthropic-
	// compatible endpoint; vision input requires a vision-capable GLM model.
	GLM_4_6 Model = "glm-4.6"
	GLM_5_2 Model = "glm-5.2"
)

func GetAllProviders() []LLMProvider {
	return []LLMProvider{OLLAMA, ANTHROPIC, DEEPSEEK, OPENAI, GLM}
}

func GetProvider(name string) (LLMProvider, bool) {
	for _, pvd := range GetAllProviders() {
		if pvd.Name == name {
			return pvd, true
		}
	}

	return LLMProvider{}, false
}

// GetModel resolves a string to its typed Model constant. Mirrors
// GetProvider; the canonical model registry is MODEL_CONTEXT_SIZE.
func GetModel(name string) (Model, bool) {
	for m := range MODEL_CONTEXT_SIZE {
		if string(m) == name {
			return m, true
		}
	}
	return Model(""), false
}

// ProviderOfModel returns the provider that lists m in its Models. Lets a
// caller holding only a model id (e.g. a per-member `model:` pin in a swarm
// profile.yml) derive the provider to route through.
func ProviderOfModel(m Model) (LLMProvider, bool) {
	for _, p := range GetAllProviders() {
		if slices.Contains(p.Models, m) {
			return p, true
		}
	}
	return LLMProvider{}, false
}

// ModelForLevel returns the model to use at a given capability tier:
//
//   - level 1 (default) → the smallest / cheapest model the provider lists
//     (Models[0]). Suitable for routine work; "normal" tier.
//   - level 2 → the largest model the provider lists (Models[len-1]).
//     Suitable for hard reasoning; "big" tier. More expensive — the LLM
//     should only pick this when the task genuinely needs deeper thinking.
//
// Providers with only one configured model collapse both tiers to that
// model. Levels outside {1, 2} clamp to the nearest valid tier.
func (p LLMProvider) ModelForLevel(level int) Model {
	if len(p.Models) == 0 {
		return ""
	}
	if level >= 2 {
		return p.Models[len(p.Models)-1]
	}
	return p.Models[0]
}

// for completion
var MODEL_CONTEXT_SIZE = map[Model]int{
	QWEN_3_6:          262_144,
	SONNET_4_6:        500_000,
	OPUS_4_8:          500_000,
	DEEPSEEK_V4_FLASH: 1_000_000,
	DEEPSEEK_V4_PRO:   1_000_000,
	GPT_5_4_MINI:      400_000,
	GPT_5_5:           1_050_000,
	GLM_4_6:           200_000,
	GLM_5_2:           1_000_000,
}

// Pricing is a model's USD rate card, every rate expressed per 1,000,000
// tokens. A zero-value Pricing means "genuinely free" (a locally hosted
// Ollama model costs nothing to run) — distinct from a model that is
// absent from MODEL_PRICING, which is "unpriced / unknown" (see CostOf).
//
// CacheRead / CacheWrite price Anthropic-style prompt-cache tokens: a
// READ re-uses a previously cached prefix at a steep discount, while the
// first WRITE of that prefix carries a surcharge. Providers without
// prompt caching report zero cache tokens (see llm.Usage), so for them
// these rates are never exercised and simply mirror Input.
type Pricing struct {
	Input      float64 // uncached input
	Output     float64 // output — hidden reasoning tokens are a subset, billed here
	CacheRead  float64 // cached-prefix re-read
	CacheWrite float64 // cached-prefix first write
}

// CostUSD prices one usage slice. The arguments mirror llm.Usage: in is
// the FULL input-token count, of which cacheRead + cacheWrite are a
// subset (Anthropic's accounting), and out is the output-token count
// (hidden reasoning tokens are already inside out). Cached tokens are
// billed at their own rates and carved out of the uncached-input pool so
// they are never double-charged.
func (p Pricing) CostUSD(in, out, cacheRead, cacheWrite int) float64 {
	// Defensive: a provider should never over-report cache, but if it
	// did, clamp the uncached pool at zero rather than credit money back.
	uncachedIn := max(in-cacheRead-cacheWrite, 0)
	const perToken = 1.0 / 1_000_000.0
	return float64(uncachedIn)*p.Input*perToken +
		float64(cacheRead)*p.CacheRead*perToken +
		float64(cacheWrite)*p.CacheWrite*perToken +
		float64(out)*p.Output*perToken
}

// MODEL_PRICING is the per-model USD rate card, keyed like
// MODEL_CONTEXT_SIZE. Rates are USD per 1M tokens and were verified
// against each provider's published list price via web search in June
// 2026 — treat them as sane defaults for the cost HUD, not invoices; an
// operator on negotiated rates can edit them here. A model absent from
// this map is "unpriced" and the HUD hides its dollar figure rather than
// guessing.
var MODEL_PRICING = map[Model]Pricing{
	// Ollama — runs on local hardware, no per-token charge.
	QWEN_3_6: {},

	// Anthropic (verified 2026-05-28). Prompt caching cuts cached input
	// ~90% (read ≈ 0.1× input); 5-min cache write ≈ 1.25× input.
	SONNET_4_6: {Input: 3.00, Output: 15.00, CacheRead: 0.30, CacheWrite: 3.75},
	OPUS_4_8:   {Input: 5.00, Output: 25.00, CacheRead: 0.50, CacheWrite: 6.25},

	// DeepSeek (verified 2026-06). OpenAI-compatible wire; the client does
	// not surface cache tokens, so the cache rates mirror Input and never
	// actually fire (DeepSeek's real cache-hit rate is far lower).
	DEEPSEEK_V4_FLASH: {Input: 0.14, Output: 0.28, CacheRead: 0.14, CacheWrite: 0.14},
	DEEPSEEK_V4_PRO:   {Input: 1.74, Output: 3.48, CacheRead: 1.74, CacheWrite: 1.74},

	// OpenAI (verified 2026-06). Reasoning-class; cache tokens not
	// surfaced by the client, so the cache rates mirror Input.
	GPT_5_4_MINI: {Input: 0.75, Output: 4.50, CacheRead: 0.75, CacheWrite: 0.75},
	GPT_5_5:      {Input: 5.00, Output: 30.00, CacheRead: 5.00, CacheWrite: 5.00},

	// GLM / z.ai (verified 2026-06). Rides the Anthropic wire format, so
	// it DOES surface cache tokens; cache-read uses z.ai's discounted rate
	// and cache-write mirrors input (z.ai publishes no write surcharge).
	GLM_4_6: {Input: 0.43, Output: 1.74, CacheRead: 0.11, CacheWrite: 0.43},
	GLM_5_2: {Input: 1.40, Output: 4.40, CacheRead: 0.26, CacheWrite: 1.40},
}

// CostOf prices a usage slice for model m against MODEL_PRICING. ok is
// false when m has no rate card — the caller should then hide the cost
// rather than render a misleading $0.00. A model priced at zero (e.g. a
// local Ollama model) returns (0, true): genuinely free, not unknown.
func CostOf(m Model, in, out, cacheRead, cacheWrite int) (cost float64, ok bool) {
	p, found := MODEL_PRICING[m]
	if !found {
		return 0, false
	}
	return p.CostUSD(in, out, cacheRead, cacheWrite), true
}
