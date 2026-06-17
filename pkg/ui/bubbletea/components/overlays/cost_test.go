package overlays

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
)

// NewCost returns nil for a nil ctrl so the App hints "no controller
// attached" instead of opening an empty overlay.
func TestNewCostNilCtrl(t *testing.T) {
	if o := NewCost(nil); o != nil {
		t.Errorf("NewCost(nil) should return nil, got %+v", o)
	}
}

func TestCostKeyModalHint(t *testing.T) {
	c := &Cost{}
	if c.Key() != "cost" {
		t.Errorf("Key = %q, want cost", c.Key())
	}
	if !c.Modal() {
		t.Error("Modal should be true")
	}
	if c.Hint() == "" {
		t.Error("Hint should be non-empty")
	}
}

func TestCostClosingKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyEnter},
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
	} {
		c := &Cost{model: string(constant.OPUS_4_8)}
		if closed, _ := c.Update(key); !closed {
			t.Errorf("%v should close the overlay", key)
		}
	}
}

// A priced model renders the four-column breakdown with computed costs.
func TestCostViewPriced(t *testing.T) {
	c := &Cost{
		model: string(constant.OPUS_4_8),
		usage: llm.Usage{InputTokens: 1_000_000, OutputTokens: 200_000},
	}
	out := c.View(80, theme.Default())
	// Opus 4.8: input 5.00, output 25.00 ⇒ 1M*5/1M = $5.0000 input,
	// 200k*25/1M = $5.0000 output, $10.0000 total.
	for _, want := range []string{"claude-opus-4-8", "input", "output", "total", "$5.0000", "$10.0000"} {
		if !strings.Contains(out, want) {
			t.Errorf("priced View() missing %q in:\n%s", want, out)
		}
	}
}

// Cache rows appear only when the session accrued cache tokens.
func TestCostViewCacheRows(t *testing.T) {
	c := &Cost{
		model: string(constant.OPUS_4_8),
		usage: llm.Usage{InputTokens: 1_000_000, OutputTokens: 100_000,
			CacheReadTokens: 200_000, CacheCreationTokens: 50_000},
	}
	out := c.View(80, theme.Default())
	for _, want := range []string{"cache read", "cache write"} {
		if !strings.Contains(out, want) {
			t.Errorf("cache View() missing %q in:\n%s", want, out)
		}
	}

	// No cache tokens ⇒ no cache rows.
	c2 := &Cost{
		model: string(constant.OPUS_4_8),
		usage: llm.Usage{InputTokens: 1_000_000, OutputTokens: 100_000},
	}
	out2 := c2.View(80, theme.Default())
	if strings.Contains(out2, "cache read") {
		t.Errorf("no-cache View() should not show a cache row, got:\n%s", out2)
	}
}

// An unpriced model shows tokens only and says so — no fabricated cost.
func TestCostViewUnpriced(t *testing.T) {
	c := &Cost{
		model: "some-unlisted-model",
		usage: llm.Usage{InputTokens: 500_000, OutputTokens: 100_000},
	}
	out := c.View(80, theme.Default())
	if !strings.Contains(out, "unpriced") {
		t.Errorf("unpriced View() should note it is unpriced, got:\n%s", out)
	}
	if strings.Contains(out, "$/1M") {
		t.Errorf("unpriced View() should drop the rate/cost columns, got:\n%s", out)
	}
}

// A local Ollama model is free: it shows the no-charge note, not a table
// of $0.0000 rows.
func TestCostViewFreeLocal(t *testing.T) {
	c := &Cost{
		model: string(constant.QWEN_3_6),
		usage: llm.Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000},
	}
	out := c.View(80, theme.Default())
	if !strings.Contains(out, "no per-token charge") {
		t.Errorf("free-model View() should note no charge, got:\n%s", out)
	}
}
