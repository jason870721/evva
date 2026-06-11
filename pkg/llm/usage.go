package llm

// Usage reports token counts from a single LLM call. Zero values mean
// "unknown / not reported" — fields are populated only when the provider
// returns them, so accumulating zero across turns is safe.
//
// CacheReadTokens / CacheCreationTokens are populated by Anthropic when
// prompt caching is in play and left zero by every other provider.
// ReasoningTokens is the share of output spent on hidden reasoning
// (DeepSeek reasoning_content, OpenAI o1-style chains).
type Usage struct {
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	ReasoningTokens     int
}

// Add returns the per-field sum of u and v. Convenient for cumulating
// usage across turns: total = total.Add(turn).
func (u Usage) Add(v Usage) Usage {
	return Usage{
		InputTokens:         u.InputTokens + v.InputTokens,
		OutputTokens:        u.OutputTokens + v.OutputTokens,
		CacheReadTokens:     u.CacheReadTokens + v.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens + v.CacheCreationTokens,
		ReasoningTokens:     u.ReasoningTokens + v.ReasoningTokens,
	}
}

// Sub returns the per-field difference u − v: the cost of one slice of work
// cut out of a cumulating counter (delta = after.Sub(before)). Mirror of Add.
func (u Usage) Sub(v Usage) Usage {
	return Usage{
		InputTokens:         u.InputTokens - v.InputTokens,
		OutputTokens:        u.OutputTokens - v.OutputTokens,
		CacheReadTokens:     u.CacheReadTokens - v.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens - v.CacheCreationTokens,
		ReasoningTokens:     u.ReasoningTokens - v.ReasoningTokens,
	}
}

// Total returns InputTokens + OutputTokens. Cache fields are subsets of
// InputTokens (per Anthropic's accounting), so they are not double-counted.
func (u Usage) Total() int {
	return u.InputTokens + u.OutputTokens
}
