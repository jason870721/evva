package agent

import (
	"context"
	"errors"
	"sync"

	"github.com/johnny1110/evva/internal/constant"

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
func (a *Agent) Run(ctx context.Context, prompt string) (string, error) {
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: prompt})
	a.logger.Debug("run.start", "name", a.Name, "prompt_bytes", len(prompt),
		"messages", len(a.session.Messages), "prompt", prompt)
	return a.runLoop(ctx) // core agent loop.
}

// Continue resumes a paused agent without appending a new user message.
// Used after ErrIterLimit (the "press enter to keep going" path) and after
// /resume reloads a session snapshot.
func (a *Agent) Continue(ctx context.Context) (string, error) {
	a.logger.Debug("run.continue", "name", a.Name, "messages", len(a.session.Messages))
	return a.runLoop(ctx)
}

// runLoop is the shared body of Run and Continue. It assumes the session
// already contains whatever messages the caller wants to seed (a fresh
// RoleUser for Run; nothing extra for Continue).
func (a *Agent) runLoop(ctx context.Context) (string, error) {
	for iter := 0; iter < a.maxIters; iter++ {

		// Honor cancellation at the top of every iteration.
		if err := ctx.Err(); err != nil {
			return "", a.interrupted(err)
		}

		// 2 levels of compacting: micro, full.
		a.compact(a.session)

		// Drain any completed async subagents. This is the only delivery
		// channel for async work — results that arrived during the
		// previous LLM call (or while the loop was paused between user
		// prompts) surface here as a synthetic user message before the
		// next Complete call sees the conversation.
		a.drainAsyncSubagents()

		a.logger.Debug("turn.start", "iter", iter, "messages", len(a.session.Messages))
		resp, err := a.thinking(ctx, iter)
		if err != nil {
			if errors.Is(err, llm.ErrInterrupted) {
				return "", a.interrupted(err)
			}
			return "", a.crush("thinking", err)
		}

		// render content and thinking to the UI
		a.text(resp.Usage, resp.Thinking, resp.Content)

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

		// no tool calls -> done.
		if len(resp.ToolCalls) == 0 {
			a.logger.Debug("run.end",
				"iter", iter,
				"content_bytes", len(resp.Content),
				"thinking_bytes", len(resp.Thinking),
			)
			return a.done(iter, resp), nil // break loop.
		}

		// Dispatch every tool call from this turn in parallel. Tool results
		// are collected in call order so the resulting RoleTool message lines
		// up with the assistant's ToolCalls by index.
		toolResults, toolErr := a.dispatchToolCalls(ctx, resp.ToolCalls)
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
			return "", a.crush("tool_use", toolErr)
		}

		// Append a single RoleTool message carrying every result. Providers
		// fan this out on the wire as they require (Anthropic: one user
		// message with N tool_result blocks; OpenAI-style: N tool-role
		// messages).
		a.session.Append(llm.Message{
			Role:        llm.RoleTool,
			ToolResults: toolResults,
		})

		a.turnOver(iter)
	}

	// Iteration cap. Not fatal — UI can prompt "press enter to keep going", caller invokes Continue.
	a.logger.Info("run.iter_limit", "reached", a.maxIters)
	return "", a.limitBreak()
}

func (a *Agent) done(iter int, resp llm.Response) string {
	if a.IsSubagent() {
		// subagent done -> ready to report.
		a.status = constant.READY_REPORT
		a.getParentSpawnGroup().Report(a.ID, resp.Content)
	} else {
		// main agent done -> idle.
		a.status = constant.IDLE
		a.emit(event.KindRunEnd, func(e *event.Event) {
			e.RunEnd = &event.RunEndPayload{
				Iters:    iter,
				Content:  resp.Content,
				Thinking: resp.Thinking,
			}
		})
	}

	return resp.Content
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
		call           *tools.Call
		tool           tools.Tool
		resolveToolErr error // recoverable: surface as IsError ToolResult
	}

	preps := make([]prepared, len(calls))
	for i, call := range calls {
		tool, err := a.ResolveTool(tools.ToolName(call.Name))
		preps[i] = prepared{call: call, tool: tool, resolveToolErr: err}
	}

	results := make([]*llm.ToolResult, len(calls))
	errs := make([]error, len(calls)) // system level error not tool result error.

	var wg sync.WaitGroup
	for i := range preps {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = a.execTool(ctx, preps[i].call, preps[i].tool, preps[i].resolveToolErr)
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
