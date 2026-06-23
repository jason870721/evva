package fs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

// --- unit: lineTrimmedMatch -------------------------------------------------

func TestLineTrimmedMatch_UniqueIndentDrift(t *testing.T) {
	// File indents the whole if-block one extra tab vs the model's old_string.
	content := "func f() {\n\tif x {\n\t\treturn\n\t}\n}\n"
	old := "if x {\n\treturn\n}" // model used one-less tab everywhere

	actual, reindent, n := lineTrimmedMatch(content, old)
	if n != 1 {
		t.Fatalf("want exactly 1 match, got %d", n)
	}
	want := "\tif x {\n\t\treturn\n\t}"
	if actual != want {
		t.Fatalf("actualOld = %q, want %q", actual, want)
	}
	if !strings.Contains(content, actual) {
		t.Fatalf("actualOld %q is not a verbatim substring of content", actual)
	}
	if reindent == nil {
		t.Fatalf("expected a reindenter for a uniform one-tab delta")
	}
	got := reindent("if x {\n\tdoThing()\n}")
	if want := "\tif x {\n\t\tdoThing()\n\t}"; got != want {
		t.Fatalf("reindent = %q, want %q", got, want)
	}
}

func TestLineTrimmedMatch_Ambiguous(t *testing.T) {
	content := "func a() {\n\tif x {\n\t\treturn\n\t}\n}\n" +
		"func b() {\n\tif x {\n\t\treturn\n\t}\n}\n"
	old := "if x {\n\treturn\n}"

	if _, _, n := lineTrimmedMatch(content, old); n != 2 {
		t.Fatalf("want 2 ambiguous matches, got %d", n)
	}
}

func TestLineTrimmedMatch_NoContentBearingLineRefused(t *testing.T) {
	content := "a\n\nb\n   \nc\n"
	for _, old := range []string{"", "   ", "\n\n", "  \n\t"} {
		if _, _, n := lineTrimmedMatch(content, old); n != 0 {
			t.Errorf("blank/whitespace-only old %q should not match; got n=%d", old, n)
		}
	}
}

func TestLineTrimmedMatch_SignatureLongerThanFile(t *testing.T) {
	if _, _, n := lineTrimmedMatch("one\ntwo\n", "a\nb\nc\nd"); n != 0 {
		t.Fatalf("oversized signature must not match; got n=%d", n)
	}
}

func TestLineTrimmedMatch_TrailingNewlineIncludesTerminator(t *testing.T) {
	content := "x\n\tkeep me\ny\n"
	// old ends with "\n" → the match should span the line's terminator too.
	actual, _, n := lineTrimmedMatch(content, "keep me\n")
	if n != 1 {
		t.Fatalf("want 1 match, got %d", n)
	}
	if want := "\tkeep me\n"; actual != want {
		t.Fatalf("actualOld = %q, want %q (terminator included)", actual, want)
	}
}

// --- unit: buildReindent ----------------------------------------------------

func TestBuildReindent(t *testing.T) {
	tests := []struct {
		name      string
		fileBlock []string
		oldBlock  []string
		in        string
		want      string // "" with wantNil=true means buildReindent returns nil
		wantNil   bool
	}{
		{
			name:      "uniform add one tab",
			fileBlock: []string{"\tif x {", "\t\treturn", "\t}"},
			oldBlock:  []string{"if x {", "\treturn", "}"},
			in:        "if x {\n\tcall()\n}",
			want:      "\tif x {\n\t\tcall()\n\t}",
		},
		{
			name:      "uniform strip two spaces",
			fileBlock: []string{"a", "  b"},
			oldBlock:  []string{"  a", "    b"},
			in:        "  a\n    b",
			want:      "a\n  b",
		},
		{
			name:      "no delta -> nil",
			fileBlock: []string{"\ta", "\tb"},
			oldBlock:  []string{"\ta", "\tb"},
			wantNil:   true,
		},
		{
			name:      "inconsistent delta -> nil",
			fileBlock: []string{"\ta", "\t\t\tb"},
			oldBlock:  []string{"a", "b"},
			wantNil:   true,
		},
		{
			name:      "tabs vs spaces (no prefix relationship) -> nil",
			fileBlock: []string{"\ta"},
			oldBlock:  []string{"    a"},
			wantNil:   true,
		},
		{
			name:      "blank lines skipped, delta from content lines",
			fileBlock: []string{"\ta", "", "\tb"},
			oldBlock:  []string{"a", "", "b"},
			in:        "a\n\nb",
			want:      "\ta\n\n\tb",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := buildReindent(tc.fileBlock, tc.oldBlock)
			if tc.wantNil {
				if f != nil {
					t.Fatalf("want nil reindenter")
				}
				return
			}
			if f == nil {
				t.Fatalf("want a reindenter, got nil")
			}
			if got := f(tc.in); got != tc.want {
				t.Fatalf("reindent(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// --- unit: resolveOldString strategy selection ------------------------------

func TestResolveOldString_ExactWinsNoFallback(t *testing.T) {
	m := resolveOldString("hello world\n", "hello")
	if !m.found || m.strategy != matchExact || m.actualOld != "hello" || m.reindent != nil {
		t.Fatalf("exact match expected; got %+v", m)
	}
}

func TestResolveOldString_LineTrimmedFound(t *testing.T) {
	content := "func f() {\n\tif x {\n\t\treturn\n\t}\n}\n"
	m := resolveOldString(content, "if x {\n\treturn\n}")
	if !m.found || m.strategy != matchLineTrimmed || m.ambiguous {
		t.Fatalf("line-trimmed match expected; got %+v", m)
	}
}

func TestResolveOldString_Ambiguous(t *testing.T) {
	content := "\tif x {\n\t\treturn\n\t}\n\tif x {\n\t\treturn\n\t}\n"
	m := resolveOldString(content, "if x {\n\treturn\n}")
	if !m.ambiguous || m.found || m.count != 2 {
		t.Fatalf("ambiguous (count 2) expected; got %+v", m)
	}
}

// --- integration: through EditTool.Execute ----------------------------------

func recordedEdit(t *testing.T, path, content string) *EditTool {
	t.Helper()
	tr := NewReadTracker()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// mtime is unchanged between Record and Execute, so CanEdit allows the
	// edit regardless of the hash (hash is only the mtime-drift fallback).
	tr.Record(path, info.ModTime(), false, HashContent(content))
	return NewEdit(tr, "")
}

func TestEditExecute_WhitespaceTolerantLands(t *testing.T) {
	content := "func f() {\n\tif x {\n\t\treturn\n\t}\n}\n"
	path := writeTempFile(t, content)
	tool := recordedEdit(t, path, content)

	// Model's old_string is under-indented; new_string is in the model's frame.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":`+jstr(path)+`,"old_string":`+jstr("if x {\n\treturn\n}")+
			`,"new_string":`+jstr("if x {\n\tlog()\n\treturn\n}")+`}`))

	if res.IsError {
		t.Fatalf("expected success, got error: %q", res.Content)
	}
	if !strings.Contains(res.Content, "whitespace-tolerant match") {
		t.Errorf("summary should note the fallback; got %q", res.Content)
	}
	got, _ := os.ReadFile(path)
	want := "func f() {\n\tif x {\n\t\tlog()\n\t\treturn\n\t}\n}\n"
	if string(got) != want {
		t.Fatalf("file =\n%q\nwant\n%q", string(got), want)
	}
}

func TestEditExecute_ExactMatchHasNoFallbackNote(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	path := writeTempFile(t, content)
	tool := recordedEdit(t, path, content)

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":`+jstr(path)+`,"old_string":"beta","new_string":"BETA"}`))
	if res.IsError {
		t.Fatalf("unexpected error: %q", res.Content)
	}
	if strings.Contains(res.Content, "whitespace-tolerant") {
		t.Errorf("exact match must not claim a fallback; got %q", res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "alpha\nBETA\ngamma\n" {
		t.Fatalf("file = %q", string(got))
	}
}

func TestEditExecute_AmbiguousFuzzyRejected(t *testing.T) {
	content := "func a() {\n\tif x {\n\t\treturn\n\t}\n}\n" +
		"func b() {\n\tif x {\n\t\treturn\n\t}\n}\n"
	path := writeTempFile(t, content)
	tool := recordedEdit(t, path, content)

	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":`+jstr(path)+`,"old_string":`+jstr("if x {\n\treturn\n}")+
			`,"new_string":`+jstr("if x {\n\tbreak\n}")+`}`))
	if !res.IsError || !strings.Contains(res.Content, "candidate regions") {
		t.Fatalf("expected ambiguity rejection; got isErr=%v content=%q", res.IsError, res.Content)
	}
	got, _ := os.ReadFile(path)
	if string(got) != content {
		t.Fatalf("file must be untouched on ambiguity; got %q", string(got))
	}
}

func TestEditExecute_MarkdownTrailingSpacesPreservedOnFuzzy(t *testing.T) {
	// Two trailing spaces = a hard line break in markdown; the fuzzy path must
	// not strip them (markdown bypasses stripTrailingWhitespacePerLine).
	content := "# Title\n\n  - item one\n  - item two\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := recordedEdit(t, path, content)

	// old_string under-indented vs the file's two-space list indent → fuzzy.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(
		`{"file_path":`+jstr(path)+`,"old_string":`+jstr("- item one\n- item two")+
			`,"new_string":`+jstr("- item one  \n- item two")+`}`))
	if res.IsError {
		t.Fatalf("unexpected error: %q", res.Content)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "item one  \n") {
		t.Fatalf("markdown hard-break trailing spaces stripped: %q", string(got))
	}
}
