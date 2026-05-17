package util

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/johnny1110/evva/internal/tools"
)

type jsonQueryTool struct{}

func (t *jsonQueryTool) Name() string { return "json_query" }

func (t *jsonQueryTool) Description() string {
	return "Extract a value from a JSON blob using a simple path expression.\n\n" +
		"Path syntax: dot-notation for object fields, brackets for array indices.\n" +
		"Examples:\n" +
		"  .result            → top-level field\n" +
		"  .data.items[0].name → nested traversal\n" +
		"  .items[-1]          → last element (negative indices)\n\n" +
		"Use this instead of verbose `python3 -c \"import json,sys; ...\"` one-liners\n" +
		"when piping curl output or processing API responses."
}

func (t *jsonQueryTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["input","path"],
		"properties":{
			"input":{"type":"string","description":"The raw JSON string to query."},
			"path":{"type":"string","description":"Path expression, e.g. .result or .data.items[0].name"}
		}
	}`)
}

type jsonQueryInput struct {
	Input string `json:"input"`
	Path  string `json:"path"`
}

func (t *jsonQueryTool) Execute(_ context.Context, input json.RawMessage) (tools.Result, error) {
	var in jsonQueryInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("json_query: decode: %v", err)}, nil
	}
	if in.Input == "" {
		return tools.Result{IsError: true, Content: "json_query: input is required"}, nil
	}
	if in.Path == "" {
		return tools.Result{IsError: true, Content: "json_query: path is required"}, nil
	}

	var root any
	if err := json.Unmarshal([]byte(in.Input), &root); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("json_query: invalid JSON: %v", err)}, nil
	}

	val, err := resolvePath(root, in.Path)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("json_query: %v", err)}, nil
	}

	out, err := json.Marshal(val)
	if err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("json_query: marshal result: %v", err)}, nil
	}
	return tools.Result{Content: string(out)}, nil
}

// resolvePath walks a simple path expression into a JSON value.
// Supported: .field, [N], [-N] (from end), chained arbitrarily.
func resolvePath(root any, path string) (any, error) {
	cur := root
	rest := path

	for rest != "" {
		switch rest[0] {
		case '.':
			rest = rest[1:]
			// ".[N]" — dot is a no-op before bracket, stay on current value.
			if len(rest) > 0 && rest[0] == '[' {
				continue
			}
			key, remainder, err := nextIdent(rest)
			if err != nil {
				return nil, err
			}
			obj, ok := cur.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("cannot index non-object with .%s", key)
			}
			var found bool
			cur, found = obj[key]
			if !found {
				return nil, fmt.Errorf("key %q not found", key)
			}
			rest = remainder

		case '[':
			rest = rest[1:]
			idxStr, remainder, err := nextBracket(rest)
			if err != nil {
				return nil, err
			}
			idx, err := strconv.Atoi(idxStr)
			if err != nil {
				return nil, fmt.Errorf("invalid array index %q", idxStr)
			}
			arr, ok := cur.([]any)
			if !ok {
				return nil, fmt.Errorf("cannot index non-array with [%d]", idx)
			}
			if idx < 0 {
				idx = len(arr) + idx
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index [%d] out of range (len %d)", idx, len(arr))
			}
			cur = arr[idx]
			rest = remainder

		default:
			return nil, fmt.Errorf("unexpected character %q in path at %q", string(rest[0]), rest)
		}
	}
	return cur, nil
}

// nextIdent reads a bare identifier until ., [, or end-of-string.
func nextIdent(s string) (ident, remainder string, err error) {
	i := 0
	for i < len(s) && s[i] != '.' && s[i] != '[' {
		i++
	}
	if i == 0 {
		return "", s, fmt.Errorf("empty field name after '.'")
	}
	return s[:i], s[i:], nil
}

// nextBracket reads until ].
func nextBracket(s string) (content, remainder string, err error) {
	i := strings.IndexByte(s, ']')
	if i < 0 {
		return "", s, fmt.Errorf("unclosed [")
	}
	return s[:i], s[i+1:], nil
}
