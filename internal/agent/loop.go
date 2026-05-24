package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/johnny1110/evva/pkg/constant"

	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// ErrIterLimit is returned by Run / Continue when the loop hits maxIters
// without the model producing a terminal text response. The agent is
// paused, not failed — call Continue(ctx) to resume from the same session.
var ErrIterLimit = errors.New("agent: iteration limit reached")

// ErrRunInProgress is returned by Run / Continue when another goroutine
// is already executing the loop for this agent. Concurrent runs would
// race on session.Messages and corrupt the assistant-toolcall →
// tool-result invariant the LLM providers require, so the second caller
// fails fast instead.
var ErrRunInProgress = errors.New("agent: run already in progress")

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
	if !a.running.CompareAndSwap(false, true) {
		// Another goroutine owns the loop. Refuse fast — appending the
		// user message here would orphan the in-flight assistant turn's
		// tool_calls (no matching tool_result yet) and every subsequent
		// provider call would 400. Don't read a.session here: the owning
		// goroutine is mutating it concurrently.
		a.logger.Warn("run.rejected", "reason", "already running")
		return "", ErrRunInProgress
	}
	defer a.running.Store(false)

	// Inject any plan-mode reminders before the user's prompt lands so
	// the model sees them framed correctly relative to the input — the
	// reminder explains the current mode, the user's text comes next.
	// drainUserPrompts handles the same job for prompts queued mid-run.
	if !a.IsSubagent() {
		for _, reminder := range a.computePlanModeAttachments() {
			a.session.Append(llm.Message{Role: llm.RoleUser, Content: reminder})
		}
	}
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: prompt})
	a.logger.Debug("run.start", "name", a.Name, "prompt_bytes", len(prompt),
		"messages", len(a.session.Messages), "prompt", prompt)
	return a.runLoop(ctx) // core agent loop.
}

// Continue resumes a paused agent without appending a new user message.
// Used after ErrIterLimit (the "press enter to keep going" path) and after
// /resume reloads a session snapshot.
func (a *Agent) Continue(ctx context.Context) (string, error) {
	if !a.running.CompareAndSwap(false, true) {
		a.logger.Warn("continue.rejected", "reason", "already running")
		return "", ErrRunInProgress
	}
	defer a.running.Store(false)

	a.logger.Debug("run.continue", "name", a.Name, "messages", len(a.session.Messages))
	return a.runLoop(ctx)
}

// runLoop is the shared body of Run and Continue. It assumes the session
// already contains whatever messages the caller wants to seed (a fresh
// RoleUser for Run; nothing extra for Continue).
func (a *Agent) runLoop(ctx context.Context) (string, error) {
	for iter := 0; iter < int(a.maxIters.Load()); iter++ {

		// Honor cancellation at the top of every iteration.
		if err := ctx.Err(); err != nil {
			return "", a.interrupted(err)
		}

		// 2 levels of compacting: micro, full.
		a.compact(ctx, a.session)

		// Drain queued wakeup prompts. SCHEDULE_WAKEUP slept inside its
		// Execute and enqueued its prompt on completion; we land it as
		// a fresh user message here so the next LLM call sees it as if
		// the user just typed it.
		a.drainWakeupPrompts()

		// Drain queued user prompts. The UI accepts new input while a
		// Run is in flight and pushes the text to the user-prompt
		// queue; we fold each entry into the session here so the model
		// picks it up on the next LLM call — same safety guarantee as
		// the other two drains (we're between turns, so the previous
		// assistant tool_calls are already answered).
		a.drainUserPrompts()

		// Drain queued daemon signals (lifecycle transitions + stream
		// events from every kind of background unit — bash bg, monitor,
		// async subagent). The per-snapshot TUI updates already fired
		// via Observable; this drain is the model-side delivery vehicle.
		a.drainDaemonSignals()

		// Drain LSP diagnostics delivered asynchronously by the server.
		// Diagnostics are passive (not solicited) so they arrive between
		// turns — this call collects and injects them.
		a.drainLSPDiagnostics()

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
			// Before releasing the run flag, check whether a bg-task
			// result or monitor event arrived during this terminal LLM
			// call. If so, loop one more iteration so the model can
			// react before we hand control back to the user. Same
			// safety as the drain at iter start — we're between turns,
			// every prior assistant tool_calls is answered.
			if a.hasPendingSignals() {
				a.session.Append(llm.Message{
					Role:              llm.RoleAssistant,
					Content:           resp.Content,
					Thinking:          resp.Thinking,
					ThinkingSignature: resp.ThinkingSignature,
				})
				a.persistSession()
				a.logger.Debug("run.continue.pending_signals", "iter", iter)
				continue
			}
			a.logger.Debug("run.end",
				"iter", iter,
				"content_bytes", len(resp.Content),
				"thinking_bytes", len(resp.Thinking),
			)
			// Persist before the terminal return so the final assistant
			// turn lands on disk. Subagent guard lives inside the helper.
			a.persistSession()
			return a.done(iter, resp), nil // break loop.
		}

		// Dispatch every tool call from this turn in parallel. Tool results
		// are collected in call order so the resulting RoleTool message lines
		// up with the assistant's ToolCalls by index.
		toolResults, toolErr := a.dispatchToolCalls(ctx, resp.ToolCalls)

		// Append a single RoleTool message carrying every result. Providers
		// fan this out on the wire as they require (Anthropic: one user
		// message with N tool_result blocks; OpenAI-style: N tool-role
		// messages).
		a.session.Append(llm.Message{
			Role:        llm.RoleTool,
			ToolResults: toolResults,
		})

		// Iteration boundary: persist the post-tool-result state so a
		// crash before the next LLM call doesn't lose this round-trip.
		a.persistSession()

		a.logger.Debug("turn.end",
			"iter", iter,
			"tool_calls", len(resp.ToolCalls),
			"content_bytes", len(resp.Content),
			"thinking_bytes", len(resp.Thinking),
		)
		if toolErr != nil {
			a.logger.Error("dispatchToolCalls have error", "err", toolErr)
		}

		if toolErr != nil {
			// Go-level tool failures abort (panics, IO, etc.). Result.IsError
			// from a tool is already handled inside dispatchToolCalls —
			// returned as nil error here so the loop continues.
			return fmt.Sprintf("agent dispatch tool calls error: %s\n", toolErr.Error()), a.crush("tool_use", toolErr)
		}

		a.turnOver(iter)
	}

	// Iteration cap. Not fatal — UI can prompt "press enter to keep going", caller invokes Continue.
	a.logger.Info("run.iter_limit", "reached", a.maxIters.Load())
	return "", a.limitBreak()
}

func (a *Agent) done(iter int, resp llm.Response) string {
	a.logger.Debug("run.done", "iter", iter, "content_bytes", len(resp.Content))
	if a.IsSubagent() {
		// subagent done -> ready to report. The actual Report call happens
		// in spawn.go after child.Run returns — this method just records the
		// status transition so the parent's agentDaemon Snapshot reflects
		// READY_REPORT during the brief window between done() and Report().
		a.status = constant.READY_REPORT
		if ad := a.getOwnDaemon(); ad != nil {
			ad.Phase(constant.READY_REPORT)
		}
		a.logger.Debug("run.done.subagent", "status", a.status)
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
		a.logger.Debug("run.done.mainagent", "status", a.status)
	}

	return resp.Content
}

// llmCall wraps llm.Complete (or Stream when the profile opts in) with
// logging + event emission for failure modes.
//
// When streaming is enabled the call drives a chunkAdapter that fans each
// text/thinking delta back through the event sink as KindTextChunk /
// KindThinkingChunk. Subagents skip the adapter — they don't emit
// user-facing text events at all (see state_machine.go:text). The final
// Response is identical in shape either way, so the downstream loop is
// unchanged.
func (a *Agent) llmCall(ctx context.Context) (llm.Response, error) {
	a.logger.Debug("llm.call",
		"profile", a.profile.Type.String(),
		"messages", len(a.session.Messages),
		"tools", len(a.exposeTools),
		"stream", a.profile.Stream,
	)

	var (
		resp llm.Response
		err  error
	)
	if a.profile.Stream {
		var sink = llm.DiscardChunks
		if !a.IsSubagent() {
			sink = a.newChunkAdapter()
		}
		resp, err = a.llm.Stream(ctx, a.session.Messages, a.exposeTools, sink)
	} else {
		resp, err = a.llm.Complete(ctx, a.session.Messages, a.exposeTools)
	}
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
			return results, e
		}
	}
	return results, nil
}
