package permission

import "testing"

func TestParseRule(t *testing.T) {
	tests := []struct {
		in       string
		toolName string
		content  string
		ok       bool
	}{
		{"", "", "", false},
		{"bash", "bash", "", true},
		{"bash(npm install)", "bash", "npm install", true},
		{"bash()", "bash", "", true},
		{"bash(*)", "bash", "", true},
		// escaped parens inside content survive parsing
		{`bash(echo \(hi\))`, "bash", `echo (hi)`, true},
		// malformed forms collapse to tool-wide
		{"(foo)", "(foo)", "", true},
		// nested parens in content are tolerated as long as the outer pair
		// is unescaped and the close is at end-of-string. ref's parser:
		// first-unescaped-open + last-unescaped-close.
		{"bash(a(b)c)", "bash", "a(b)c", true},
	}
	for _, tc := range tests {
		gotName, gotContent, gotOK := ParseRule(tc.in)
		if gotOK != tc.ok {
			t.Errorf("ParseRule(%q): ok=%v want %v", tc.in, gotOK, tc.ok)
			continue
		}
		if !tc.ok {
			continue
		}
		if gotName != tc.toolName {
			t.Errorf("ParseRule(%q): toolName=%q want %q", tc.in, gotName, tc.toolName)
		}
		if gotContent != tc.content {
			t.Errorf("ParseRule(%q): content=%q want %q", tc.in, gotContent, tc.content)
		}
	}
}

func TestFormatRuleRoundtrip(t *testing.T) {
	cases := [][2]string{
		{"bash", ""},
		{"bash", "npm install"},
		{"bash", `echo (hi)`},
		{"bash", `printf "test\nvalue"`},
	}
	for _, c := range cases {
		formatted := FormatRule(c[0], c[1])
		name, content, ok := ParseRule(formatted)
		if !ok {
			t.Errorf("roundtrip(%q,%q): formatted=%q failed to parse", c[0], c[1], formatted)
			continue
		}
		if name != c[0] || content != c[1] {
			t.Errorf("roundtrip(%q,%q): got (%q,%q) via %q", c[0], c[1], name, content, formatted)
		}
	}
}
