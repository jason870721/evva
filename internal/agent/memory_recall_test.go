package agent

import (
	"testing"

	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/llm"
)

func mediumEffort() int { return llm.ParseEffort("medium") }

// TestRecallTarget_PerActiveProvider pins the default (provider → model, effort)
// resolution so a model-constant typo or a future Models[] reorder is caught.
func TestRecallTarget_PerActiveProvider(t *testing.T) {
	cases := []struct {
		name       string
		provider   constant.LLMProvider
		active     constant.Model
		effort     string
		wantModel  constant.Model
		wantEffort int
	}{
		{"anthropic", constant.ANTHROPIC, constant.OPUS_4_7, "high", constant.SONNET_4_6, mediumEffort()},
		{"deepseek", constant.DEEPSEEK, constant.DEEPSEEK_V4_PRO, "ultra", constant.DEEPSEEK_V4_FLASH, mediumEffort()},
		{"openai", constant.OPENAI, constant.GPT_5_5, "low", constant.GPT_5_4_MINI, mediumEffort()},
		// Ollama mirrors the main agent: active model + the main agent's effort.
		{"ollama", constant.OLLAMA, constant.QWEN_3_6, "high", constant.QWEN_3_6, llm.ParseEffort("high")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &Agent{
				cfg:     &config.Config{},
				profile: Profile{LLMProvider: c.provider, LLMModel: c.active},
				effort:  c.effort,
			}
			p, m, e := a.recallTarget()
			if p.Name != c.provider.Name {
				t.Errorf("provider: got %q, want %q", p.Name, c.provider.Name)
			}
			if m != c.wantModel {
				t.Errorf("model: got %q, want %q", m, c.wantModel)
			}
			if e != c.wantEffort {
				t.Errorf("effort: got %d, want %d", e, c.wantEffort)
			}
		})
	}
}

func TestRecallTarget_OverrideWinsWhenCredentialed(t *testing.T) {
	// Active provider is DeepSeek, but an explicit recall model on a credentialed
	// Anthropic wins (at medium effort).
	a := &Agent{
		cfg: &config.Config{
			MemoryRecallModel: string(constant.SONNET_4_6),
			LLMProviderConfig: map[string]config.APIConfig{
				constant.ANTHROPIC.Name: {ApiSecret: "sk-test"},
			},
		},
		profile: Profile{LLMProvider: constant.DEEPSEEK, LLMModel: constant.DEEPSEEK_V4_PRO},
		effort:  "medium",
	}
	p, m, e := a.recallTarget()
	if p.Name != constant.ANTHROPIC.Name || m != constant.SONNET_4_6 || e != mediumEffort() {
		t.Errorf("credentialed override should win: got (%s, %s, %d)", p.Name, m, e)
	}
}

func TestRecallTarget_OverrideIgnoredWhenProviderUncredentialed(t *testing.T) {
	// Same override, but no Anthropic key → fall back to the active provider's
	// per-provider default (DeepSeek → v4-flash).
	a := &Agent{
		cfg: &config.Config{
			MemoryRecallModel: string(constant.SONNET_4_6),
			LLMProviderConfig: map[string]config.APIConfig{}, // no keys
		},
		profile: Profile{LLMProvider: constant.DEEPSEEK, LLMModel: constant.DEEPSEEK_V4_PRO},
		effort:  "medium",
	}
	p, m, _ := a.recallTarget()
	if p.Name != constant.DEEPSEEK.Name || m != constant.DEEPSEEK_V4_FLASH {
		t.Errorf("uncredentialed override should fall back to active provider default; got (%s, %s)", p.Name, m)
	}
}
