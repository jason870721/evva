package repl

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"

	"github.com/johnny1110/evva/pkg/tools"
)

func i64(v int64) *int64 { return &v }

// run marshals in and executes the tool, failing on an unexpected Go error
// (only caller-cancellation returns one, which these tests never trigger).
func run(t *testing.T, in replInput) tools.Result {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	res, gerr := NewREPL("").Execute(context.Background(), tools.NopLogger(), raw)
	if gerr != nil {
		t.Fatalf("unexpected go error: %v", gerr)
	}
	return res
}

// skipUnlessInterp skips the test when no interpreter for lang is on PATH,
// so CI hosts without python/node still pass.
func skipUnlessInterp(t *testing.T, lang string) {
	t.Helper()
	switch lang {
	case "python":
		if _, e := exec.LookPath("python3"); e == nil {
			return
		}
		if _, e := exec.LookPath("python"); e == nil {
			return
		}
		t.Skip("no python interpreter on PATH")
	case "javascript":
		if _, e := exec.LookPath("node"); e == nil {
			return
		}
		t.Skip("no node interpreter on PATH")
	}
}

func TestRepl_ResolveInterpreter(t *testing.T) {
	t.Run("unsupported language", func(t *testing.T) {
		_, _, err := resolveInterpreter("ruby")
		if err == nil || !strings.Contains(err.Error(), "unsupported language") {
			t.Errorf("got err=%v, want one containing 'unsupported language'", err)
		}
	})

	t.Run("python uses -c", func(t *testing.T) {
		skipUnlessInterp(t, "python")
		_, flag, err := resolveInterpreter("python")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if flag != "-c" {
			t.Errorf("python code flag = %q, want -c", flag)
		}
	})

	t.Run("javascript uses -e", func(t *testing.T) {
		skipUnlessInterp(t, "javascript")
		_, flag, err := resolveInterpreter("javascript")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if flag != "-e" {
			t.Errorf("javascript code flag = %q, want -e", flag)
		}
	})
}

func TestRepl_InputValidation(t *testing.T) {
	t.Run("empty code", func(t *testing.T) {
		res := run(t, replInput{Code: "   "})
		if !res.IsError || !strings.Contains(res.Content, "code is required") {
			t.Errorf("got isErr=%v content=%q", res.IsError, res.Content)
		}
	})

	t.Run("decode error", func(t *testing.T) {
		res, _ := NewREPL("").Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{bogus`))
		if !res.IsError || !strings.Contains(res.Content, "decode") {
			t.Errorf("got isErr=%v content=%q", res.IsError, res.Content)
		}
	})

	t.Run("unsupported language", func(t *testing.T) {
		res := run(t, replInput{Code: "x", Language: "ruby"})
		if !res.IsError || !strings.Contains(res.Content, "unsupported language") {
			t.Errorf("got isErr=%v content=%q", res.IsError, res.Content)
		}
	})
}

func TestRepl_Python(t *testing.T) {
	skipUnlessInterp(t, "python")

	t.Run("happy path", func(t *testing.T) {
		res := run(t, replInput{Code: "print(2 + 3)", Language: "python"})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		if strings.TrimSpace(res.Content) != "5" {
			t.Errorf("got %q, want 5", res.Content)
		}
	})

	t.Run("defaults to python when language omitted", func(t *testing.T) {
		res := run(t, replInput{Code: "print('hi')"})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		if strings.TrimSpace(res.Content) != "hi" {
			t.Errorf("got %q, want hi", res.Content)
		}
	})

	t.Run("nonzero exit surfaces stderr and exit code", func(t *testing.T) {
		res := run(t, replInput{Code: "raise ValueError('boom')", Language: "python"})
		if !res.IsError {
			t.Fatalf("expected IsError, got content=%q", res.Content)
		}
		if !strings.Contains(res.Content, "boom") {
			t.Errorf("content %q should contain stderr 'boom'", res.Content)
		}
		if !strings.Contains(res.Content, "exit code") {
			t.Errorf("content %q should contain 'exit code'", res.Content)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		res := run(t, replInput{Code: "import time; time.sleep(5)", Language: "python", Timeout: i64(200)})
		if !res.IsError || !strings.Contains(res.Content, "timed out") {
			t.Errorf("got isErr=%v content=%q, want a 'timed out' error", res.IsError, res.Content)
		}
	})
}

func TestRepl_JavaScript(t *testing.T) {
	skipUnlessInterp(t, "javascript")

	t.Run("happy path", func(t *testing.T) {
		res := run(t, replInput{Code: "console.log(2 + 3)", Language: "javascript"})
		if res.IsError {
			t.Fatalf("unexpected error: %s", res.Content)
		}
		if strings.TrimSpace(res.Content) != "5" {
			t.Errorf("got %q, want 5", res.Content)
		}
	})
}
