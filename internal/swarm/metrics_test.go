package swarm

import "testing"

// RP-28: countRunTokens bucket boundaries (<1k / <10k / <50k / ≥50k) and the
// usual nil-receiver safety for hand-built lite spaces.
func TestCountRunTokensBuckets(t *testing.T) {
	m := newSpaceMetrics()
	for _, total := range []int{0, 999, 1_000, 9_999, 10_000, 49_999, 50_000, 1_000_000} {
		m.countRunTokens("w", total)
	}
	snap := m.members["w"].RunTokens
	if snap != [4]int64{2, 2, 2, 2} {
		t.Errorf("RunTokens = %v, want [2 2 2 2] (two values per bucket)", snap)
	}

	var nilMetrics *spaceMetrics
	nilMetrics.countRunTokens("w", 123) // must not panic
}
