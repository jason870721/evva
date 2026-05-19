package shell

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/tools"
)

// Phase 1 analysis — BashTool.Execute code paths:
//   - decode input
//   - empty command rejected
//   - run_in_background rejected (reserved)
//   - dangerouslyDisableSandbox rejected (reserved)
//   - timeout normalization (nil → default; ≤0 → default; > max → max)
//   - successful exit (code 0) returns Content
//   - non-zero exit returns IsError with stdout + exit-code suffix
//   - timeout returns IsError with "timed out" and partial output
//   - ctx cancellation returns IsError plus go-level error

func TestBash_RejectsEmptyCommand(t *testing.T) {
	tool := &BashTool{}
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{"command":"   "}`))
	if !res.IsError || !strings.Contains(res.Content, "required") {
		t.Errorf("expected 'required' error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestBash_RejectsRunInBackground(t *testing.T) {
	tool := &BashTool{}
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"command":"echo hi","run_in_background":true}`))
	if !res.IsError || !strings.Contains(res.Content, "run_in_background") {
		t.Errorf("expected run_in_background rejection; got %q", res.Content)
	}
}

func TestBash_HappyPath_ReturnsStdout(t *testing.T) {
	tool := &BashTool{}
	res, err := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"command":"echo hello-world"}`))

	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected IsError; content=%q", res.Content)
	}
	if !strings.Contains(res.Content, "hello-world") {
		t.Errorf("expected stdout in content; got %q", res.Content)
	}
}

func TestBash_NonZeroExitIsError(t *testing.T) {
	tool := &BashTool{}
	// `exit 7` returns code 7 with no stdout.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"command":"exit 7"}`))

	if !res.IsError {
		t.Fatal("expected IsError on non-zero exit")
	}
	if !strings.Contains(res.Content, "exit code 7") {
		t.Errorf("expected 'exit code 7' marker; got %q", res.Content)
	}
}

func TestBash_StderrFoldedIntoContent(t *testing.T) {
	tool := &BashTool{}
	// `>&2 echo foo` writes to stderr; BashTool merges stdout+stderr.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"command":">&2 echo to-stderr"}`))
	if res.IsError {
		t.Fatalf("expected success; got %q", res.Content)
	}
	if !strings.Contains(res.Content, "to-stderr") {
		t.Errorf("stderr should be folded into Content; got %q", res.Content)
	}
}

func TestBash_TimeoutTriggersError(t *testing.T) {
	tool := &BashTool{}
	// timeout=200ms; sleep 5 → must time out promptly.
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"command":"sleep 5","timeout":200}`))

	if !res.IsError || !strings.Contains(res.Content, "timed out") {
		t.Errorf("expected timeout error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

func TestBash_TimeoutCappedAtMax(t *testing.T) {
	// Verify the clamp: passing a >max value doesn't error out (it just
	// clamps). We can't observe the clamped duration from outside without
	// timing-sensitive flakes, so the smoke test is "does the call still
	// succeed for a fast command when an absurd timeout is supplied".
	tool := &BashTool{}
	res, _ := tool.Execute(context.Background(), tools.NopLogger(),
		json.RawMessage(`{"command":"echo ok","timeout":999999999999}`))
	if res.IsError {
		t.Errorf("oversized timeout should be clamped (still succeed for fast cmd); got %q", res.Content)
	}
	if !strings.Contains(res.Content, "ok") {
		t.Errorf("expected 'ok' in output; got %q", res.Content)
	}
}

func TestBash_ContextCancelledReturnsError(t *testing.T) {
	tool := &BashTool{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	res, err := tool.Execute(ctx, tools.NopLogger(), json.RawMessage(`{"command":"sleep 5"}`))

	if err == nil {
		t.Fatal("expected go-level error on cancelled ctx")
	}
	if !res.IsError {
		t.Fatal("expected IsError on cancelled ctx")
	}
}

func TestBash_DecodeError(t *testing.T) {
	tool := &BashTool{}
	res, _ := tool.Execute(context.Background(), tools.NopLogger(), json.RawMessage(`{not json`))
	if !res.IsError || !strings.Contains(res.Content, "decode") {
		t.Errorf("expected decode error; got isErr=%v content=%q", res.IsError, res.Content)
	}
}

// TestBash_TimeoutKillsSubprocessTree exercises the bug fix: a shell
// that backgrounds a sleep is still killed when the timeout fires.
// Without the process-group SIGKILL + WaitDelay plumbing in Execute,
// cmd.Run() blocks indefinitely because the orphan sleep inherits the
// stdout pipe — the test would hang past its own deadline.
//
// We assert the call returns inside a small bound (250ms timeout +
// 2s WaitDelay grace + 1s slack) and surfaces the "timed out" error.
func TestBash_TimeoutKillsSubprocessTree(t *testing.T) {
	tool := &BashTool{}
	deadline := time.After(5 * time.Second)
	done := make(chan struct {
		res    tools.Result
		err    error
		ranFor time.Duration
	}, 1)
	go func() {
		start := time.Now()
		res, err := tool.Execute(context.Background(), tools.NopLogger(),
			json.RawMessage(`{"command":"sleep 30 & echo backgrounded; sleep 30","timeout":250}`))
		done <- struct {
			res    tools.Result
			err    error
			ranFor time.Duration
		}{res, err, time.Since(start)}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("unexpected go error: %v", got.err)
		}
		if !got.res.IsError || !strings.Contains(got.res.Content, "timed out") {
			t.Errorf("expected timeout error; got %+v", got.res)
		}
		if got.ranFor > 4*time.Second {
			t.Errorf("timeout teardown took %s — process group / WaitDelay not effective", got.ranFor)
		}
	case <-deadline:
		t.Fatal("bash Execute hung past 5s — subprocess kept the pipe open")
	}
}

func TestBash_DefaultTimeoutAppliedWhenZero(t *testing.T) {
	// timeout=0 should fall through to default (2 min). We don't wait
	// 2 min — just verify a quick command still works when timeout is
	// passed as 0 (defensive against the "<= 0" branch silently breaking).
	tool := &BashTool{}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, _ := tool.Execute(ctx, tools.NopLogger(), json.RawMessage(`{"command":"echo ok","timeout":0}`))
	if res.IsError {
		t.Errorf("timeout=0 should fall through to default; got %q", res.Content)
	}
}
