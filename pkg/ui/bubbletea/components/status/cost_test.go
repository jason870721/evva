package status

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

func TestHumanCost(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "$0.00"},
		{-1, "$0.00"},     // negative clamps to $0.00
		{0.004, "$0.004"}, // sub-cent keeps 3 decimals
		{0.009, "$0.009"}, // still sub-cent
		{0.0123, "$0.01"}, // at/above a cent → plain cents
		{0.38, "$0.38"},
		{12.5, "$12.50"},
		{1234.6, "$1235"}, // big totals drop the cents
	}
	for _, c := range cases {
		if got := humanCost(c.in); got != c.want {
			t.Errorf("humanCost(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// A priced model shows a $ figure in the spend cell.
func TestRenderSpendPricedShowsCost(t *testing.T) {
	u := llm.Usage{InputTokens: 1_000_000, OutputTokens: 200_000}
	out := renderSpend(u, string(constant.OPUS_4_8), theme.Default())
	if !strings.Contains(out, "$") {
		t.Errorf("priced model should show a $ cost, got: %q", out)
	}
}

// An unpriced model shows tokens only — no misleading $0.00 cost.
func TestRenderSpendUnpricedHidesCost(t *testing.T) {
	u := llm.Usage{InputTokens: 1_000_000, OutputTokens: 200_000}
	out := renderSpend(u, "some-unlisted-model", theme.Default())
	if strings.Contains(out, "$") {
		t.Errorf("unpriced model should NOT show a cost, got: %q", out)
	}
}

// A local Ollama model is "known but free", so it DOES show a cost: $0.00.
func TestRenderSpendFreeModelShowsZero(t *testing.T) {
	u := llm.Usage{InputTokens: 5_000_000, OutputTokens: 5_000_000}
	out := renderSpend(u, string(constant.QWEN_3_6), theme.Default())
	if !strings.Contains(out, "$0.00") {
		t.Errorf("free model should show $0.00, got: %q", out)
	}
}
