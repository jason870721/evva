package lsp

import (
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools/lsp/protocol"
)

func TestDiagnosticKeySetAddContains(t *testing.T) {
	s := newDiagnosticKeySet(3)

	if s.Contains("a") {
		t.Error("expected a not to exist")
	}
	s.Add("a")
	if !s.Contains("a") {
		t.Error("expected a to exist after Add")
	}
}

func TestDiagnosticKeySetLRUEviction(t *testing.T) {
	s := newDiagnosticKeySet(3)
	s.Add("a")
	s.Add("b")
	s.Add("c")
	// Access a to make it most recent.
	s.Contains("a") // does not affect recency
	s.Add("a")      // moves a to back
	s.Add("d")      // should evict b (oldest)

	if !s.Contains("a") {
		t.Error("expected a to survive")
	}
	if !s.Contains("c") {
		t.Error("expected c to survive")
	}
	if !s.Contains("d") {
		t.Error("expected d to exist")
	}
	if s.Contains("b") {
		t.Error("expected b to be evicted")
	}
}

func TestDiagnosticKeySetDuplicateAdd(t *testing.T) {
	s := newDiagnosticKeySet(2)
	s.Add("a")
	s.Add("b")
	s.Add("a") // duplicate, moves to back
	s.Add("c") // should evict b

	if s.Contains("a") && s.Contains("c") && !s.Contains("b") {
		// correct
	} else {
		t.Error("duplicate add should not increase count")
	}
}

func TestDiagnosticRegistryRegister(t *testing.T) {
	r := NewDiagnosticRegistry()

	diags := []protocol.Diagnostic{
		{
			Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 5}},
			Severity: protocol.SeverityError,
			Message:  "undefined variable",
		},
	}

	r.Register("gopls", "file:///test/main.go", diags)

	drained := r.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 pending diagnostic, got %d", len(drained))
	}
	if drained[0].ServerName != "gopls" {
		t.Errorf("expected server name gopls, got %q", drained[0].ServerName)
	}
	if len(drained[0].Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(drained[0].Diagnostics))
	}
	if drained[0].Diagnostics[0].Message != "undefined variable" {
		t.Errorf("expected message, got %q", drained[0].Diagnostics[0].Message)
	}
}

func TestDiagnosticRegistryDedup(t *testing.T) {
	r := NewDiagnosticRegistry()

	diags := []protocol.Diagnostic{
		{
			Range:    protocol.Range{Start: protocol.Position{Line: 1, Character: 0}, End: protocol.Position{Line: 1, Character: 10}},
			Severity: protocol.SeverityError,
			Message:  "syntax error",
		},
	}

	// First registration.
	r.Register("gopls", "file:///test/main.go", diags)
	drained := r.Drain()
	if len(drained) != 1 {
		t.Fatalf("first drain: expected 1, got %d", len(drained))
	}

	// Same diagnostic again — should be deduplicated.
	r.Register("gopls", "file:///test/main.go", diags)
	drained = r.Drain()
	if len(drained) != 0 {
		t.Fatalf("second drain: expected 0 (dedup), got %d", len(drained))
	}
}

func TestDiagnosticRegistryPerFileCap(t *testing.T) {
	r := NewDiagnosticRegistry()

	// Create 15 diagnostics (cap is 10).
	diags := make([]protocol.Diagnostic, 15)
	for i := range diags {
		diags[i] = protocol.Diagnostic{
			Range:    protocol.Range{Start: protocol.Position{Line: uint32(i), Character: 0}, End: protocol.Position{Line: uint32(i), Character: 5}},
			Severity: protocol.SeverityWarning,
			Message:  "warning",
		}
	}

	r.Register("gopls", "file:///test/main.go", diags)
	drained := r.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 file entry, got %d", len(drained))
	}
	if len(drained[0].Diagnostics) > defaultMaxPerFile {
		t.Errorf("expected max %d diagnostics per file, got %d",
			defaultMaxPerFile, len(drained[0].Diagnostics))
	}
}

func TestDiagnosticRegistryDrainClears(t *testing.T) {
	r := NewDiagnosticRegistry()

	r.Register("gopls", "file:///test/main.go", []protocol.Diagnostic{
		{Range: protocol.Range{}, Severity: protocol.SeverityError, Message: "err"},
	})

	_ = r.Drain()
	drained := r.Drain()
	if len(drained) != 0 {
		t.Errorf("expected empty after drain, got %d", len(drained))
	}
}

func TestDiagnosticRegistryClearFile(t *testing.T) {
	r := NewDiagnosticRegistry()

	r.Register("gopls", "file:///test/main.go", []protocol.Diagnostic{
		{Range: protocol.Range{}, Severity: protocol.SeverityError, Message: "err1"},
	})
	r.Register("gopls", "file:///test/other.go", []protocol.Diagnostic{
		{Range: protocol.Range{}, Severity: protocol.SeverityWarning, Message: "warn"},
	})

	r.ClearFile("file:///test/main.go")

	drained := r.Drain()
	if len(drained) != 1 {
		t.Fatalf("expected 1 remaining file entry, got %d", len(drained))
	}
	if drained[0].FileURI != "file:///test/other.go" {
		t.Errorf("expected other.go to remain, got %s", drained[0].FileURI)
	}
}

func TestDiagnosticSeverityString(t *testing.T) {
	tests := []struct {
		sev  protocol.DiagnosticSeverity
		want string
	}{
		{protocol.SeverityError, "Error"},
		{protocol.SeverityWarning, "Warning"},
		{protocol.SeverityInformation, "Info"},
		{protocol.SeverityHint, "Hint"},
		{protocol.DiagnosticSeverity(99), "Unknown"},
	}

	for _, tt := range tests {
		got := tt.sev.String()
		if got != tt.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tt.sev, got, tt.want)
		}
	}
}

func TestFormatDiagnosticsReminder(t *testing.T) {
	diags := []PendingDiagnostic{
		{
			ServerName: "gopls",
			FilePath:   "internal/agent/loop.go",
			Diagnostics: []protocol.Diagnostic{
				{
					Range:    protocol.Range{Start: protocol.Position{Line: 88, Character: 4}, End: protocol.Position{Line: 88, Character: 24}},
					Severity: protocol.SeverityError,
					Message:  "undefined: drainDaemonSignals",
				},
				{
					Range:    protocol.Range{Start: protocol.Position{Line: 141, Character: 2}, End: protocol.Position{Line: 141, Character: 8}},
					Severity: protocol.SeverityWarning,
					Message:  "unused variable 'result'",
				},
			},
		},
	}

	result := FormatDiagnosticsReminder(diags)
	if !strings.Contains(result, "<system-reminder>") {
		t.Error("expected <system-reminder> wrapper")
	}
	if !strings.Contains(result, "gopls") {
		t.Error("expected server name gopls")
	}
	if !strings.Contains(result, "internal/agent/loop.go") {
		t.Error("expected file path")
	}
	if !strings.Contains(result, "[Error]") {
		t.Error("expected [Error] label")
	}
	if !strings.Contains(result, "[Warning]") {
		t.Error("expected [Warning] label")
	}
	if !strings.Contains(result, "undefined: drainDaemonSignals") {
		t.Error("expected diagnostic message")
	}
}

func TestFormatDiagnosticsReminderEmpty(t *testing.T) {
	result := FormatDiagnosticsReminder(nil)
	if result != "" {
		t.Errorf("expected empty string for nil input, got %q", result)
	}
	result = FormatDiagnosticsReminder([]PendingDiagnostic{})
	if result != "" {
		t.Errorf("expected empty string for empty slice, got %q", result)
	}
}

func TestFileURIToPath(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"file:///test/main.go", "/test/main.go"},
		{"/test/main.go", "/test/main.go"},
		{"file:///home/user/file.txt", "/home/user/file.txt"},
	}

	for _, tt := range tests {
		got := fileURIToPath(tt.uri)
		if got != tt.want {
			t.Errorf("fileURIToPath(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}

func TestDiagIdentityKey(t *testing.T) {
	d := protocol.Diagnostic{
		Range:    protocol.Range{Start: protocol.Position{Line: 0, Character: 0}, End: protocol.Position{Line: 0, Character: 5}},
		Severity: protocol.SeverityError,
		Message:  "undefined",
		Source:   "compiler",
		Code:     "U1000",
	}

	key1 := diagKey("file:///test/main.go", d)
	key2 := diagKey("file:///test/main.go", d)

	if key1 != key2 {
		t.Error("expected same key for same diagnostic")
	}

	d2 := d
	d2.Message = "different message"
	key3 := diagKey("file:///test/main.go", d2)
	if key1 == key3 {
		t.Error("expected different keys for different messages")
	}
}
