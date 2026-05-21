package llm

import "testing"

// Phase 1 analysis — Usage surface:
//   - Usage is a value type with five integer counters
//   - Add returns the per-field sum (does NOT mutate the receiver)
//   - Total returns InputTokens + OutputTokens (cache fields are subsets of
//     InputTokens per Anthropic's accounting, so NOT double-counted)

func TestUsage_Add_PerFieldSum(t *testing.T) {
	a := Usage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 10, CacheCreationTokens: 5, ReasoningTokens: 20}
	b := Usage{InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheCreationTokens: 4, ReasoningTokens: 5}

	got := a.Add(b)

	want := Usage{InputTokens: 101, OutputTokens: 52, CacheReadTokens: 13, CacheCreationTokens: 9, ReasoningTokens: 25}
	if got != want {
		t.Errorf("Add: got %+v, want %+v", got, want)
	}
}

func TestUsage_Add_ZeroValueIsIdentity(t *testing.T) {
	a := Usage{InputTokens: 7, OutputTokens: 3}
	if got := a.Add(Usage{}); got != a {
		t.Errorf("Add(zero) should be identity; got %+v want %+v", got, a)
	}
	if got := (Usage{}).Add(a); got != a {
		t.Errorf("zero.Add(a) should equal a; got %+v want %+v", got, a)
	}
}

func TestUsage_Add_DoesNotMutateReceiver(t *testing.T) {
	a := Usage{InputTokens: 10}
	_ = a.Add(Usage{InputTokens: 5})
	if a.InputTokens != 10 {
		t.Errorf("Add mutated receiver: got %d, want 10", a.InputTokens)
	}
}

func TestUsage_Total_SumsInputAndOutput(t *testing.T) {
	u := Usage{InputTokens: 100, OutputTokens: 25}
	if got, want := u.Total(), 125; got != want {
		t.Errorf("Total: got %d, want %d", got, want)
	}
}

func TestUsage_Total_DoesNotDoubleCountCacheFields(t *testing.T) {
	// Per the package doc: CacheRead/CacheCreation are subsets of
	// InputTokens, so adding them to Total would double-count. Lock this
	// down so a future refactor doesn't silently break the contract.
	u := Usage{InputTokens: 100, OutputTokens: 50, CacheReadTokens: 30, CacheCreationTokens: 70}
	if got, want := u.Total(), 150; got != want {
		t.Errorf("Total double-counted cache fields: got %d, want %d", got, want)
	}
}

func TestUsage_Total_ZeroValue(t *testing.T) {
	var u Usage
	if got := u.Total(); got != 0 {
		t.Errorf("zero-value Total: got %d, want 0", got)
	}
}

func TestUsage_Add_AcrossSeveralTurns(t *testing.T) {
	// Sanity: simulating the agent's per-turn AddUsage rollup.
	turns := []Usage{
		{InputTokens: 100, OutputTokens: 50},
		{InputTokens: 80, OutputTokens: 40, CacheReadTokens: 20},
		{InputTokens: 120, OutputTokens: 60, ReasoningTokens: 10},
	}
	var total Usage
	for _, t := range turns {
		total = total.Add(t)
	}
	want := Usage{InputTokens: 300, OutputTokens: 150, CacheReadTokens: 20, ReasoningTokens: 10}
	if total != want {
		t.Errorf("rollup: got %+v, want %+v", total, want)
	}
}
