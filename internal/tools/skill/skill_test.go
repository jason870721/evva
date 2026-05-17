package skill

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func mustExec(t *testing.T, tool *SkillTool, raw string) (string, bool) {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(raw))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	return res.Content, res.IsError
}

func TestSkillTool_Success(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "hello", "# hello a friendly greeting\n\nBODY_LINES\nmore content\n")
	reg, _ := LoadRegistry(home, "")

	tool := NewSkill(func() *Registry { return reg })
	out, isErr := mustExec(t, tool, `{"skill":"hello"}`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "Follow these instructions for skill `hello`:") {
		t.Errorf("missing instructions header; got %q", out)
	}
	if !strings.Contains(out, "BODY_LINES") {
		t.Errorf("body missing; got %q", out)
	}
	if strings.Contains(out, "args:") {
		t.Errorf("args section should be absent when no args passed; got %q", out)
	}
}

func TestSkillTool_AppendsArgs(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "echo", "# echo describe me\nbody\n")
	reg, _ := LoadRegistry(home, "")
	tool := NewSkill(func() *Registry { return reg })

	out, isErr := mustExec(t, tool, `{"skill":"echo","args":"--verbose"}`)
	if isErr {
		t.Fatalf("unexpected error: %s", out)
	}
	if !strings.Contains(out, "\n\nargs: --verbose") {
		t.Errorf("args section missing or malformed; got %q", out)
	}
}

func TestSkillTool_UnknownSkill(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, home, "one", "# one first skill\nbody\n")
	writeSkill(t, home, "two", "# two second skill\nbody\n")
	reg, _ := LoadRegistry(home, "")
	tool := NewSkill(func() *Registry { return reg })

	out, isErr := mustExec(t, tool, `{"skill":"nope"}`)
	if !isErr {
		t.Fatal("expected IsError for unknown skill")
	}
	if !strings.Contains(out, "available: one, two") {
		t.Errorf("missing available list; got %q", out)
	}
}

func TestSkillTool_EmptyRegistry(t *testing.T) {
	reg, _ := LoadRegistry("", "")
	tool := NewSkill(func() *Registry { return reg })
	out, isErr := mustExec(t, tool, `{"skill":"hello"}`)
	if !isErr {
		t.Fatal("expected IsError")
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("expected '(none)' marker; got %q", out)
	}
}

func TestSkillTool_NilLookup(t *testing.T) {
	tool := NewSkill(nil)
	out, isErr := mustExec(t, tool, `{"skill":"hello"}`)
	if !isErr {
		t.Fatal("expected IsError")
	}
	if !strings.Contains(out, "no registry lookup configured") {
		t.Errorf("expected nil-lookup message; got %q", out)
	}
}

func TestSkillTool_MissingSkillField(t *testing.T) {
	tool := NewSkill(func() *Registry { r, _ := LoadRegistry("", ""); return r })
	out, isErr := mustExec(t, tool, `{}`)
	if !isErr {
		t.Fatal("expected IsError")
	}
	if !strings.Contains(out, "`skill` is required") {
		t.Errorf("expected required-field message; got %q", out)
	}
}
