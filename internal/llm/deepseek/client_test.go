package deepseek

import (
	"testing"

	"github.com/johnny1110/evva/internal/llm"
)

func TestEffectiveModel_LowEffort(t *testing.T) {
	c := &Client{
		model:  "deepseek-v4-pro",
		params: llm.LLMParams{Effort: 1},
	}
	if got := c.effectiveModel(); got != "deepseek-v4-flash" {
		t.Errorf("effort=low: expected v4-flash, got %s", got)
	}
}

func TestEffectiveModel_MediumEffort(t *testing.T) {
	c := &Client{
		model:  "deepseek-v4-pro",
		params: llm.LLMParams{Effort: 2},
	}
	if got := c.effectiveModel(); got != c.model {
		t.Errorf("effort=medium: expected configured model %s, got %s", c.model, got)
	}
}

func TestEffectiveModel_HighEffort(t *testing.T) {
	c := &Client{
		model:  "deepseek-v4-pro",
		params: llm.LLMParams{Effort: 3},
	}
	if got := c.effectiveModel(); got != c.model {
		t.Errorf("effort=high: expected configured model %s, got %s", c.model, got)
	}
}

func TestEffectiveModel_UltraEffort(t *testing.T) {
	c := &Client{
		model:  "deepseek-v4-pro",
		params: llm.LLMParams{Effort: 4},
	}
	if got := c.effectiveModel(); got != c.model {
		t.Errorf("effort=ultra: expected configured model %s, got %s", c.model, got)
	}
}

func TestEffectiveModel_ZeroEffort(t *testing.T) {
	c := &Client{
		model:  "deepseek-v4-pro",
		params: llm.LLMParams{Effort: 0},
	}
	if got := c.effectiveModel(); got != c.model {
		t.Errorf("effort=0: expected configured model %s, got %s", c.model, got)
	}
}
