package constant

import (
	"math"
	"testing"
)

const costEps = 1e-9

func approx(a, b float64) bool { return math.Abs(a-b) < costEps }

// CostUSD on a cacheless slice is just input*rate + output*rate.
func TestCostUSDNoCache(t *testing.T) {
	p := Pricing{Input: 3, Output: 15}
	got := p.CostUSD(1_000_000, 1_000_000, 0, 0)
	if want := 18.0; !approx(got, want) {
		t.Fatalf("CostUSD = %v, want %v", got, want)
	}
}

// Cache tokens are a SUBSET of the input count: they must be billed at
// their own rate AND carved out of the uncached-input pool, never both.
// Uses a synthetic rate card so the assertion is independent of the
// live MODEL_PRICING values.
func TestCostUSDCacheSubsetNoDoubleCount(t *testing.T) {
	p := Pricing{Input: 10, Output: 100, CacheRead: 1, CacheWrite: 20}
	// in=1M of which 200k cache-read + 100k cache-write ⇒ 700k uncached.
	got := p.CostUSD(1_000_000, 500_000, 200_000, 100_000)
	want := 0.7*10 + 0.2*1 + 0.1*20 + 0.5*100 // 7 + 0.2 + 2 + 50 = 59.2
	if !approx(got, want) {
		t.Fatalf("CostUSD = %v, want %v", got, want)
	}
}

// If a provider ever reports cache tokens exceeding the input count, the
// uncached pool clamps at zero rather than going negative (which would
// credit the user back real money).
func TestCostUSDClampsNegativeUncached(t *testing.T) {
	p := Pricing{Input: 10, Output: 0, CacheRead: 1, CacheWrite: 2}
	got := p.CostUSD(100, 0, 80, 80) // cache(160) > in(100)
	want := 80*1.0/1e6 + 80*2.0/1e6  // uncached clamped to 0
	if !approx(got, want) {
		t.Fatalf("CostUSD = %v, want %v", got, want)
	}
	if got < 0 {
		t.Fatalf("CostUSD must never be negative, got %v", got)
	}
}

// CostOf reports ok=true for a priced model and returns the same number
// as the underlying rate card.
func TestCostOfKnownModel(t *testing.T) {
	cost, ok := CostOf(SONNET_4_6, 1_000_000, 0, 0, 0)
	if !ok {
		t.Fatal("CostOf(SONNET_4_6) ok = false, want true")
	}
	if want := MODEL_PRICING[SONNET_4_6].Input; !approx(cost, want) {
		t.Fatalf("CostOf = %v, want %v", cost, want)
	}
}

// An unpriced model returns ok=false so the HUD can hide the cell rather
// than show a misleading $0.00.
func TestCostOfUnknownModel(t *testing.T) {
	cost, ok := CostOf(Model("some-unlisted-model"), 1_000_000, 1_000_000, 0, 0)
	if ok {
		t.Fatalf("CostOf(unknown) ok = true, want false")
	}
	if cost != 0 {
		t.Fatalf("CostOf(unknown) cost = %v, want 0", cost)
	}
}

// A local Ollama model is priced at zero: ok=true (it is known), cost=0
// (it is genuinely free) — distinct from the unknown case above.
func TestCostOfFreeLocalModel(t *testing.T) {
	cost, ok := CostOf(QWEN_3_6, 5_000_000, 5_000_000, 0, 0)
	if !ok {
		t.Fatal("CostOf(QWEN_3_6) ok = false, want true (free, not unknown)")
	}
	if cost != 0 {
		t.Fatalf("CostOf(QWEN_3_6) cost = %v, want 0", cost)
	}
}

// Every model with a context window should also have a rate card, so the
// HUD never silently drops cost for a first-class model.
func TestEveryContextModelIsPriced(t *testing.T) {
	for m := range MODEL_CONTEXT_SIZE {
		if _, ok := MODEL_PRICING[m]; !ok {
			t.Errorf("model %q has a context size but no MODEL_PRICING entry", m)
		}
	}
}
