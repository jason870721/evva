package permission

import "testing"

func TestMatchShell(t *testing.T) {
	tests := []struct {
		pattern string
		cmd     string
		want    bool
	}{
		// Exact
		{"npm install", "npm install", true},
		{"npm install", "npm install -g foo", false},
		{"npm install", "npm", false},

		// Legacy prefix (npm:* matches npm and anything starting with "npm ")
		{"npm:*", "npm", true},
		{"npm:*", "npm install", true},
		{"npm:*", "npm install --save", true},
		{"npm:*", "npmplus", false},

		// Trailing-star wildcard — ref's "git *" matches bare "git" too
		{"git *", "git", true},
		{"git *", "git status", true},
		{"git *", "githubcli", false},

		// Mid-pattern wildcard: arbitrary characters between fixed segments
		{"git * --quiet", "git status --quiet", true},
		{"git * --quiet", "git status", false},

		// Wildcard with an escaped literal asterisk segment: `echo *\*`
		// (an unescaped * then a literal *) is a wildcard pattern that
		// requires a literal `*` somewhere in the tail.
		{`echo *\*`, "echo foo *", true},
		{`echo *\*`, "echo foo bar", false},
	}
	for _, tc := range tests {
		if got := matchShell(tc.pattern, tc.cmd); got != tc.want {
			t.Errorf("matchShell(%q, %q) = %v want %v", tc.pattern, tc.cmd, got, tc.want)
		}
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"./src/**", "src/foo/bar.go", false}, // doublestar treats ./ literally; project rules use repo-relative
		{"src/**", "src/foo/bar.go", true},
		{"src/**/*.go", "src/foo/bar.go", true},
		{"src/**/*.go", "src/foo/bar.ts", false},
		{"/abs/**", "/abs/x.txt", true},
		{"/abs/**", "/other/x.txt", false},
	}
	for _, tc := range tests {
		if got := matchPath(tc.pattern, tc.path); got != tc.want {
			t.Errorf("matchPath(%q, %q) = %v want %v", tc.pattern, tc.path, got, tc.want)
		}
	}
}

func TestExtractBashCommand(t *testing.T) {
	got := extractBashCommand([]byte(`{"command":"npm install","timeout":1000}`))
	if got != "npm install" {
		t.Errorf("got %q want %q", got, "npm install")
	}

	got = extractBashCommand([]byte(`{"command":"echo \"hi\""}`))
	if got != `echo "hi"` {
		t.Errorf("escaped quote: got %q want %q", got, `echo "hi"`)
	}

	got = extractBashCommand([]byte(`{"other":"foo"}`))
	if got != "" {
		t.Errorf("missing field: got %q want empty", got)
	}
}

func TestMatchToolCall(t *testing.T) {
	bashCall := ToolCall{Name: "bash", Input: []byte(`{"command":"npm install foo"}`)}
	readCall := ToolCall{Name: "read", Input: []byte(`{"file_path":"src/x.go"}`)}

	// Tool-wide rule matches any call to that tool.
	if !matchToolCall(Rule{ToolName: "bash"}, bashCall) {
		t.Error("tool-wide bash rule should match any bash call")
	}

	// Wrong tool never matches.
	if matchToolCall(Rule{ToolName: "edit"}, bashCall) {
		t.Error("edit rule should not match bash call")
	}

	// Shell content rule.
	if !matchToolCall(Rule{ToolName: "bash", Content: "npm:*"}, bashCall) {
		t.Error("npm:* should match 'npm install foo'")
	}

	// Path content rule.
	if !matchToolCall(Rule{ToolName: "read", Content: "src/**"}, readCall) {
		t.Error("src/** should match 'src/x.go'")
	}

	// Path mismatch.
	if matchToolCall(Rule{ToolName: "read", Content: "lib/**"}, readCall) {
		t.Error("lib/** should not match 'src/x.go'")
	}
}
