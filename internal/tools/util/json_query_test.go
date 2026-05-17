package util

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONQuery_HappyPath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		path  string
		want  string
	}{
		{
			name:  "top-level field",
			input: `{"result": "ok", "status": 200}`,
			path:  ".result",
			want:  `"ok"`,
		},
		{
			name:  "nested field",
			input: `{"data": {"user": {"name": "Alice"}}}`,
			path:  ".data.user.name",
			want:  `"Alice"`,
		},
		{
			name:  "array index",
			input: `{"items": ["a", "b", "c"]}`,
			path:  ".items[1]",
			want:  `"b"`,
		},
		{
			name:  "negative index",
			input: `{"items": [1, 2, 3]}`,
			path:  ".items[-1]",
			want:  "3",
		},
		{
			name:  "mixed path",
			input: `{"data": {"items": [{"name": "x"}, {"name": "y"}]}}`,
			path:  ".data.items[1].name",
			want:  `"y"`,
		},
		{
			name:  "number result",
			input: `{"count": 42}`,
			path:  ".count",
			want:  "42",
		},
		{
			name:  "boolean result",
			input: `{"ok": true}`,
			path:  ".ok",
			want:  "true",
		},
		{
			name:  "null result",
			input: `{"val": null}`,
			path:  ".val",
			want:  "null",
		},
		{
			name:  "object result",
			input: `{"nested": {"a": 1}}`,
			path:  ".nested",
			want:  `{"a":1}`,
		},
	}
	tool := &jsonQueryTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, _ := json.Marshal(jsonQueryInput{Input: tt.input, Path: tt.path})
			res, _ := tool.Execute(context.Background(), in)
			if res.IsError {
				t.Fatalf("unexpected error: %s", res.Content)
			}
			if res.Content != tt.want {
				t.Errorf("got %q, want %q", res.Content, tt.want)
			}
		})
	}
}

func TestJSONQuery_Errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		path  string
		want  string
	}{
		{
			name:  "invalid json",
			input: `{bad`,
			path:  ".x",
			want:  "invalid JSON",
		},
		{
			name:  "missing field",
			input: `{"a": 1}`,
			path:  ".b",
			want:  `key "b" not found`,
		},
		{
			name:  "index non-array",
			input: `{"a": 1}`,
			path:  ".a[0]",
			want:  "cannot index non-array",
		},
		{
			name:  "dot on non-object",
			input: `[1,2]`,
			path:  ".[0].x",
			want:  "cannot index non-object",
		},
		{
			name:  "index out of range",
			input: `[1,2]`,
			path:  ".[5]",
			want:  "out of range",
		},
		{
			name:  "empty input",
			input: "",
			path:  ".x",
			want:  "input is required",
		},
		{
			name:  "empty path",
			input: `{}`,
			path:  "",
			want:  "path is required",
		},
	}
	tool := &jsonQueryTool{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, _ := json.Marshal(jsonQueryInput{Input: tt.input, Path: tt.path})
			res, _ := tool.Execute(context.Background(), in)
			if !res.IsError {
				t.Fatalf("expected error, got content=%q", res.Content)
			}
			if !strings.Contains(res.Content, tt.want) {
				t.Errorf("error %q should contain %q", res.Content, tt.want)
			}
		})
	}
}

func TestJSONQuery_DecodeError(t *testing.T) {
	tool := &jsonQueryTool{}
	res, _ := tool.Execute(context.Background(), json.RawMessage(`{bogus`))
	if !res.IsError || !strings.Contains(res.Content, "decode") {
		t.Errorf("expected decode error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestJSONQuery_RootArray(t *testing.T) {
	tool := &jsonQueryTool{}
	in, _ := json.Marshal(jsonQueryInput{Input: `[10, 20, 30]`, Path: ".[1]"})
	res, _ := tool.Execute(context.Background(), in)
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Content)
	}
	if res.Content != "20" {
		t.Errorf("got %q, want 20", res.Content)
	}
}

func TestJSONQuery_EmptyPathWholeValue(t *testing.T) {
	// Empty path is blocked by validation above, but an edge case worth
	// checking: the full root value is returned when path consumes nothing.
	// (This test confirms the early-return validation fires, not a silent nil.)
	tool := &jsonQueryTool{}
	in, _ := json.Marshal(jsonQueryInput{Input: `{"a":1}`, Path: "."})
	res, _ := tool.Execute(context.Background(), in)
	if !res.IsError || !strings.Contains(res.Content, "empty field name") {
		t.Errorf("expected empty-field error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}
