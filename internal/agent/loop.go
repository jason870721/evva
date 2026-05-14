package agent

import (
	"context"
	"errors"
	"fmt"

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
// LLM completion → if tool_use, dispatch via ResolveTool, append the result
// as RoleTool, repeat. The loop exits when the model emits text without a
// tool_use (normal terminal), the context is cancelled, the iteration cap
// is hit (ErrIterLimit), or a Go-level error aborts.
//
// Events flow to the agent's Sink. The returned llm.Response is the final
// assistant turn — or the zero value when the loop ended without one.
func (a *Agent) Run(ctx context.Context, prompt string) (llm.Response, error) {
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: prompt})
	a.emit(event.KindRunStart, func(e *event.Event) {
		e.RunStart = &event.RunStartPayload{Prompt: prompt}
	})
	a.logger.Debug("run.start", "prompt_bytes", len(prompt), "messages", len(a.session.Messages))
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
	for iter := 0; iter < a.maxIters; iter++ {
		// Honor cancellation at the top of every iteration.
		if err := ctx.Err(); err != nil {
			return llm.Response{}, a.handleCtxErr(err)
		}

		a.emit(event.KindTurnStart, func(e *event.Event) {
			e.Turn = &event.TurnPayload{Iteration: iter}
		})
		a.logger.Debug("turn.start", "iter", iter, "messages", len(a.session.Messages))

		// TODO: in v2.0 version add compact logic here, token threshold > 80% (microcompact) > 80% sec time(fullcompact)

		resp, err := a.llmCall(ctx)
		if err != nil {
			return llm.Response{}, err
		}

		// Stream the model's text out before considering tool dispatch — the
		// UI shows thinking/text immediately, then the tool call.
		if resp.Thinking != "" {
			a.emit(event.KindThinking, func(e *event.Event) {
				e.Thinking = &event.TextPayload{Text: resp.Thinking}
			})
		}
		if resp.Content != "" {
			a.emit(event.KindText, func(e *event.Event) {
				e.Text = &event.TextPayload{Text: resp.Content}
			})
		}

		// Append the assistant turn — including the tool_use part — so the
		// next LLM call sees a valid request/result pairing.
		a.session.Append(llm.Message{
			Role:     llm.RoleAssistant,
			Content:  resp.Content,
			Thinking: resp.Thinking,
			ToolCall: resp.ToolCall,
			ToolID:   resp.ToolID,
		})

		if resp.ToolCall == nil {
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

		// Dispatch the tool call.
		toolErr := a.dispatchToolCall(ctx, resp.ToolCall, resp.ToolID)
		a.emit(event.KindTurnEnd, func(e *event.Event) {
			e.Turn = &event.TurnPayload{Iteration: iter}
		})
		a.logger.Debug("turn.end",
			"iter", iter,
			"content_bytes", len(resp.Content),
			"thinking_bytes", len(resp.Thinking),
		)
		
		if toolErr != nil {
			// Go-level tool failures abort (panics, IO, etc.). Result.IsError
			// from a tool is already handled inside dispatchToolCall —
			// returned as nil here so the loop continues.
			return llm.Response{}, toolErr
		}
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
		"tool_call", resp.ToolCall != nil,
	)
	return resp, nil
}

// dispatchToolCall resolves and executes a single tool_use. It always
// appends a RoleTool message to the session (whether success or model-
// recoverable error) so the next LLM call's request/result pairing is well-
// formed. It returns a non-nil error ONLY for Go-level failures that should
// abort the loop (tool.Execute panic, transport error, etc.). Resolution
// failures and Result.IsError both flow through ToolUseResult so the model
// can recover on the next turn.
func (a *Agent) dispatchToolCall(ctx context.Context, call *tools.Call, toolID string) error {
	a.emit(event.KindToolUseStart, func(e *event.Event) {
		e.ToolUseStart = &event.ToolUseStartPayload{
			Name:   call.Name,
			Input:  call.Input,
			ToolID: toolID,
		}
	})
	a.logger.Debug("tool.dispatch", "name", call.Name, "tool_id", toolID)

	tool, err := a.ResolveTool(tools.ToolName(call.Name))
	if err != nil {
		// Recoverable: surface to the model.
		msg := err.Error()
		a.emit(event.KindToolUseResult, func(e *event.Event) {
			e.ToolUseResult = &event.ToolUseResultPayload{
				ToolID:  toolID,
				Content: msg,
				IsError: true,
			}
		})
		a.session.Append(llm.Message{
			Role:    llm.RoleTool,
			Content: msg,
			ToolID:  toolID,
		})
		a.logger.Warn("tool.reject", "name", call.Name, "err", err)
		return nil
	}

	result, err := tool.Execute(ctx, call.Input)
	if err != nil {
		// Go-level error — abort. Tool panics and IO failures are bugs that
		// the model can't recover from the same way it can from IsError.
		stage := fmt.Sprintf("tool:%s", call.Name)
		a.emit(event.KindError, func(e *event.Event) {
			e.Error = &event.ErrorPayload{Stage: stage, Err: err}
		})
		a.logger.Error("tool.exec.fail", "name", call.Name, "err", err)
		return err
	}

	a.emit(event.KindToolUseResult, func(e *event.Event) {
		e.ToolUseResult = &event.ToolUseResultPayload{
			ToolID:  toolID,
			Content: result.Content,
			IsError: result.IsError,
		}
	})
	a.session.Append(llm.Message{
		Role:    llm.RoleTool,
		Content: result.Content,
		ToolID:  toolID,
	})
	a.logger.Debug("tool.result",
		"name", call.Name,
		"is_error", result.IsError,
		"bytes", len(result.Content),
	)
	return nil
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
