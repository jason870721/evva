package configtool

import (
	"strings"
	"testing"
)

// A10 — the prompt body enumerates every supported setting plus the leader
// description.
func TestGeneratePromptContainsEveryKey(t *testing.T) {
	body := generatePrompt()
	for _, k := range AllKeys() {
		if !strings.Contains(body, "- "+k) {
			t.Errorf("prompt missing setting %q", k)
		}
	}
	if !strings.Contains(body, description) {
		t.Error("prompt missing leader description")
	}
	// Enum options should be enumerated for options-typed settings.
	if !strings.Contains(body, `"low", "medium", "high", "ultra"`) {
		t.Error("prompt missing default_effort options")
	}
}

// Prompt must be byte-stable across calls — non-determinism (random map
// order, embedded timestamps) would break provider prompt-prefix caching.
func TestGeneratePromptStable(t *testing.T) {
	if a, b := generatePrompt(), generatePrompt(); a != b {
		t.Error("generatePrompt() is non-deterministic across calls")
	}
}
