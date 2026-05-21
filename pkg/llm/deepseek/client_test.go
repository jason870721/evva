package deepseek

import (
	"testing"
)

func TestDeepseekEffort(t *testing.T) {
	// evva's "low" tier still enables thinking on DeepSeek — "low" is the
	// fast lane, not no-reasoning. Levels 1–4 send thinking=enabled with
	// reasoning_effort climbing through medium → high → xhigh → max.
	tests := []struct {
		level      int
		wantThink  bool
		wantEffort string
	}{
		{0, false, ""},
		{1, true, "medium"}, // evva "low"
		{2, true, "high"},   // evva "medium" (default)
		{3, true, "xhigh"},  // evva "high"
		{4, true, "max"},    // evva "ultra"
		{5, false, ""},
	}
	for _, tt := range tests {
		think, effort := deepseekEffort(tt.level)
		if tt.wantThink && think == nil {
			t.Errorf("deepseekEffort(%d): expected thinking=enabled, got nil", tt.level)
		}
		if !tt.wantThink && think != nil {
			t.Errorf("deepseekEffort(%d): expected thinking=nil, got %+v", tt.level, think)
		}
		if effort != tt.wantEffort {
			t.Errorf("deepseekEffort(%d): effort = %q, want %q", tt.level, effort, tt.wantEffort)
		}
	}
}
