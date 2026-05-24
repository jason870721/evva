package hooks

import "testing"

func TestMatchTool(t *testing.T) {
	tests := []struct {
		name     string
		matcher  string
		toolName string
		want     bool
	}{
		{"empty matcher matches all", "", "bash", true},
		{"empty matcher matches any tool", "", "read", true},
		{"literal match", "bash", "bash", true},
		{"literal mismatch", "bash", "read", false},
		{"glob wildcard", "ba*", "bash", true},
		{"glob wildcard no match", "ba*", "read", false},
		{"glob single char", "ba?h", "bash", true},
		{"bad glob returns false", "[", "bash", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchTool(tt.matcher, tt.toolName)
			if got != tt.want {
				t.Errorf("matchTool(%q, %q) = %v, want %v", tt.matcher, tt.toolName, got, tt.want)
			}
		})
	}
}
