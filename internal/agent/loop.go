package agent

import (
	"context"
	"errors"
	"fmt"
	"github.com/johnny1110/evva/internal/constant"
	"strings"
	"sync"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/tools"
)

// ErrIterLimit is returned by Run / Continue when the loop hits maxIters
// without the model producing a terminal text response. The agent is
// paused, not failed — call Continue(ctx) to resume from the same session.
var ErrIterLimit = errors.New("agent: iteration limit reached")

// Run drives the agent to completion for a single user turn.
//
// It appends a RoleUser{prompt} message to the session and then loops:
// LLM completion → if tool_use, dispatch all tool calls in parallel, append
// the collected results as a single RoleTool message, repeat. The loop exits
// when the model emits no tool calls (normal terminal), the context is
// cancelled, the iteration cap is hit (ErrIterLimit), or a Go-level error
// aborts.
//
// Events flow to the agent's Sink. The returned llm.Response is the final
// assistant turn — or the zero value when the loop ended without one.
func (a *Agent) Run(ctx context.Context, prompt string) (llm.Response, error) {
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: prompt})
	a.emit(event.KindRunStart, func(e *event.Event) {
		e.RunStart = &event.RunStartPayload{Prompt: prompt}
	})
	a.logger.Debug("run.start", "name", a.Name, "prompt_bytes", len(prompt),
		"messages", len(a.session.Messages), "prompt", prompt)
	return a.runLoop(ctx) // core agent loop.
}

// Continue resumes a paused agent without appending a new user message.
// Used after ErrIterLimit (the "press enter to keep going" path) and after
// /resume reloads a session snapshot.
func (a *Agent) Continue(ctx context.Context) (llm.Response, error) {
	a.emit(event.KindRunResume, func(e *event.Event) {
		e.RunResume = &event.RunResumePayload{FromMessageIndex: len(a.session.Messages)}
	})
	a.logger.Debug("run.resume", "messages", len(a.session.Messages))
	return a.runLoop(ctx)
}

// runLoop is the shared body of Run and Continue. It assumes the session
// already contains whatever messages the caller wants to seed (a fresh
// RoleUser for Run; nothing extra for Continue).
func (a *Agent) runLoop(ctx context.Context) (llm.Response, error) {
	cfg := config.Get()

	for iter := 0; iter < a.maxIters; iter++ {
		// Honor cancellation at the top of every iteration.
		if err := ctx.Err(); err != nil {
			return llm.Response{}, a.handleCtxErr(err)
		}

		// Drain any completed async subagents. This is the only delivery
		// channel for async work — results that arrived during the
		// previous LLM call (or while the loop was paused between user
		// prompts) surface here as a synthetic user message before the
		// next Complete call sees the conversation.
		a.drainAsyncSubagents()

		a.emit(event.KindTurnStart, func(e *event.Event) {
			e.Turn = &event.TurnPayload{Iteration: iter}
		})
		a.logger.Debug("turn.start", "iter", iter, "messages", len(a.session.Messages))

		// TODO: in v2.0 version add compact logic here, token threshold > 80% (microcompact) > 80% sec time(fullcompact)

		resp, err := a.llmCall(ctx)
		if err != nil {
			return llm.Response{}, err
		}

		// Fold the turn's reported usage into the session total and surface
		// both. Zero-usage turns (provider didn't report) still fire the
		// event so the TUI's per-turn ticker stays in sync.
		a.session.AddUsage(resp.Usage)
		a.emit(event.KindUsage, func(e *event.Event) {
			e.Usage = &event.UsagePayload{
				Turn:       resp.Usage,
				Cumulative: a.session.Usage,
			}
		})

		// Stream the model's text out before considering tool dispatch — the
		// UI shows thinking/text immediately, then the tool calls.
		if cfg.DisplayThinking && resp.Thinking != "" {
			a.emit(event.KindThinking, func(e *event.Event) {
				e.Thinking = &event.TextPayload{Text: resp.Thinking}
			})
		}
		if resp.Content != "" {
			a.emit(event.KindText, func(e *event.Event) {
				e.Text = &event.TextPayload{Text: resp.Content}
			})
		}

		// Append the assistant turn — including every tool_use block — so the
		// next LLM call sees a valid request/result pairing. ThinkingSignature
		// is opaque to us but must be carried round-trip for Anthropic's
		// extended thinking + tool use combo.
		a.session.Append(llm.Message{
			Role:              llm.RoleAssistant,
			Content:           resp.Content,
			Thinking:          resp.Thinking,
			ThinkingSignature: resp.ThinkingSignature,
			ToolCalls:         resp.ToolCalls,
		})

		if len(resp.ToolCalls) == 0 {
			// Terminal: the model is done.
			a.emit(event.KindRunEnd, func(e *event.Event) {
				e.RunEnd = &event.RunEndPayload{Final: resp}
			})
			a.logger.Debug("run.end",
				"iter", iter,
				"content_bytes", len(resp.Content),
				"thinking_bytes", len(resp.Thinking),
			)
			return resp, nil // break loop.
		}

		// Dispatch every tool call from this turn in parallel. Tool results
		// are collected in call order so the resulting RoleTool message lines
		// up with the assistant's ToolCalls by index.
		toolResults, toolErr := a.dispatchToolCalls(ctx, resp.ToolCalls)

		a.emit(event.KindTurnEnd, func(e *event.Event) {
			e.Turn = &event.TurnPayload{Iteration: iter}
		})
		a.logger.Debug("turn.end",
			"iter", iter,
			"tool_calls", len(resp.ToolCalls),
			"content_bytes", len(resp.Content),
			"thinking_bytes", len(resp.Thinking),
		)

		if toolErr != nil {
			// Go-level tool failures abort (panics, IO, etc.). Result.IsError
			// from a tool is already handled inside dispatchToolCalls —
			// returned as nil error here so the loop continues.
			return llm.Response{}, toolErr
		}

		// Append a single RoleTool message carrying every result. Providers
		// fan this out on the wire as they require (Anthropic: one user
		// message with N tool_result blocks; OpenAI-style: N tool-role
		// messages).
		a.session.Append(llm.Message{
			Role:        llm.RoleTool,
			ToolResults: toolResults,
		})
	}

	// Iteration cap. Not fatal — UI can prompt "press enter to keep going",
	// caller invokes Continue.
	a.emit(event.KindIterLimit, func(e *event.Event) {
		e.IterLimit = &event.IterLimitPayload{Reached: a.maxIters}
	})
	a.logger.Info("run.iter_limit", "reached", a.maxIters)
	return llm.Response{}, ErrIterLimit
}

// llmCall wraps llm.Complete with logging + event emission for failure modes.
func (a *Agent) llmCall(ctx context.Context) (llm.Response, error) {
	a.logger.Debug("llm.call",
		"profile", a.profile.Type.String(),
		"messages", len(a.session.Messages),
		"tools", len(a.exposeTools),
	)

	resp, err := a.llm.Complete(ctx, a.session.Messages, a.exposeTools)
	if err != nil {
		if errors.Is(err, llm.ErrInterrupted) {
			a.emit(event.KindRunCancelled, nil)
			a.logger.Info("run.cancelled")
			return llm.Response{}, err
		}
		a.emit(event.KindError, func(e *event.Event) {
			e.Error = &event.ErrorPayload{Stage: "llm", Err: err}
		})
		a.logger.Error("llm.fail", "err", err)
		return llm.Response{}, err
	}

	a.logger.Debug("llm.ok",
		"content_bytes", len(resp.Content),
		"thinking_bytes", len(resp.Thinking),
		"tool_calls", len(resp.ToolCalls),
		"in_tokens", resp.Usage.InputTokens,
		"out_tokens", resp.Usage.OutputTokens,
	)
	return resp, nil
}

// dispatchToolCalls fans out every tool call from one assistant turn,
// running them concurrently. It returns the collected results in the same
// order as calls (index-aligned), or the first Go-level error encountered.
//
// Resolution is done up front in the caller's goroutine so the active-tool
// map is only mutated serially. Tool resolution failures surface as
// IsError ToolResults — the model recovers on the next turn. Only true
// Go-level errors (panic, transport failure inside a tool) abort the run.
func (a *Agent) dispatchToolCalls(ctx context.Context, calls []*tools.Call) ([]*llm.ToolResult, error) {
	type prepared struct {
		call   *tools.Call
		tool   tools.Tool
		resErr error // recoverable: surface as IsError ToolResult
	}

	preps := make([]prepared, len(calls))
	for i, call := range calls {
		tool, err := a.ResolveTool(tools.ToolName(call.Name))
		preps[i] = prepared{call: call, tool: tool, resErr: err}
	}

	results := make([]*llm.ToolResult, len(calls))
	errs := make([]error, len(calls)) // system level error not tool result error.

	var wg sync.WaitGroup
	for i := range preps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = a.runOneTool(ctx, preps[i].call, preps[i].tool, preps[i].resErr)
		}(i)
	}
	wg.Wait()

	// First fatal error wins. Other tools may have completed — their results
	// are discarded since the loop is unwinding.
	for _, e := range errs {
		if e != nil {
			return nil, e
		}
	}
	return results, nil
}

// runOneTool executes a single tool call and emits its lifecycle events.
// It always returns a non-nil *llm.ToolResult unless it returns a Go-level
// error (panic / transport failure). Resolution failures and tool-reported
// errors flow back as IsError ToolResults so the model can recover.
func (a *Agent) runOneTool(ctx context.Context, call *tools.Call, tool tools.Tool, resErr error) (*llm.ToolResult, error) {
	a.emit(event.KindToolUseStart, func(e *event.Event) {
		e.ToolUseStart = &event.ToolUseStartPayload{
			Name:   call.Name,
			Input:  call.Input,
			ToolID: call.ID,
		}
	})
	a.logger.Debug("tool.dispatch", "name", call.Name, "tool_id", call.ID)

	if resErr != nil {
		msg := resErr.Error()
		a.emit(event.KindToolUseResult, func(e *event.Event) {
			e.ToolUseResult = &event.ToolUseResultPayload{
				ToolID:  call.ID,
				Content: msg,
				IsError: true,
			}
		})
		a.logger.Warn("tool.reject", "name", call.Name, "err", resErr)
		return &llm.ToolResult{ID: call.ID, Content: msg, IsError: true}, nil
	}

	result, err := tool.Execute(ctx, call.Input)
	if err != nil {
		stage := fmt.Sprintf("tool:%s", call.Name)
		a.emit(event.KindError, func(e *event.Event) {
			e.Error = &event.ErrorPayload{Stage: stage, Err: err}
		})
		a.logger.Error("tool.exec.fail", "name", call.Name, "err", err)
		return nil, err
	}

	a.emit(event.KindToolUseResult, func(e *event.Event) {
		e.ToolUseResult = &event.ToolUseResultPayload{
			ToolID:   call.ID,
			Content:  result.Content,
			IsError:  result.IsError,
			Metadata: result.Metadata,
		}
	})
	a.logger.Debug("tool.result",
		"name", call.Name,
		"is_error", result.IsError,
		"bytes", len(result.Content),
	)
	return &llm.ToolResult{ID: call.ID, Content: result.Content, IsError: result.IsError}, nil
}

// drainAsyncSubagents pulls every async subagent that has reached a
// terminal phase (done or crushed) out of the panel and folds its result
// into the conversation as a synthetic user message. This runs at the top
// of each loop iteration so:
//
//   - Async results that arrived during the previous LLM call land in the
//     next request automatically.
//   - Async results that arrive after the loop exits sit in the panel
//     until the user types again; the next Run picks them up here.
//
// Sync subagents are never drained (the spawner removes them as soon as
// their tool return delivers the result), so this only injects what the
// model could not have already seen.
func (a *Agent) drainAsyncSubagents() {
	if !a.toolState.HasAgentGroupPanel() {
		return
	}

	// completed = async agent done reports.
	completed := a.toolState.AgentGroup().DrainCompleted()
	if len(completed) == 0 {
		return
	}

	var b strings.Builder
	b.WriteString("[Async subagent results]\n")
	for _, s := range completed {
		switch s.Status {
		case constant.CRUSHED.String():
			fmt.Fprintf(&b, "- subagent %s (%s) failed: %s\n", s.Name, s.JobDesc, s.Err)
		case constant.MAX_ITERS.String():
			fmt.Fprintf(&b, "- subagent %s (%s) reached max iters: %s\n", s.Name, s.Type, s.Err)
		default:
			fmt.Fprintf(&b, "- subagent %s (%s) done:\n%s\n", s.Name, s.Type, s.Summary)
		}
	}

	a.session.Append(llm.Message{Role: llm.RoleUser, Content: b.String()})
	a.logger.Debug("subagents.drained", "count", len(completed))
}

// handleCtxErr converts a raw ctx error into the llm.ErrInterrupted contract
// the rest of the codebase agrees on, and emits the cancellation event.
func (a *Agent) handleCtxErr(err error) error {
	a.emit(event.KindRunCancelled, nil)
	a.logger.Info("run.cancelled", "err", err)
	if errors.Is(err, context.Canceled) {
		return llm.ErrInterrupted
	}
	return err
}
