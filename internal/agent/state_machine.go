package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/constant"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/permission"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/meta"
	"github.com/johnny1110/evva/internal/tools/shell"
)

// Each function below maps to one transition of constant.AgentStatus. They
// are the only sites in the agent layer that mutate a.status, so the file
// reads as a state-by-state reference:
//
//   INIT         AgentStatus = "init"
//   THINKING     AgentStatus = "thinking"      — thinking()
//   EXECUTING    AgentStatus = "executing"     — execTool()
//   DRAINING     AgentStatus = "draining"      — drainAsyncSubagents()
//   COMPACTING   AgentStatus = "compacting"    — compact() (compact.go)
//   TEXTING      AgentStatus = "texting"       — text()
//   IDLE         AgentStatus = "idle"          — turnOver(), done() (loop.go)
//   INTERRUPTED  AgentStatus = "interrupted"   — interrupted()
//   CRUSHED      AgentStatus = "crushed"       — crush()
//   MAX_ITERS    AgentStatus = "max_iters"     — limitBreak() (agent.go)
//   READY_REPORT AgentStatus = "ready_report"  — done() (loop.go)
//
// Each method sets a.status, emits the appropriate event for the main agent
// (or pushes the equivalent update through the parent's SpawnGroup for a
// subagent), and either returns the state-machine's outcome or hands off to
// the next step in the loop.

// interrupted converts a raw ctx error into the llm.ErrInterrupted contract
// the rest of the codebase agrees on, and emits the cancellation event.
func (a *Agent) interrupted(err error) error {
	a.status = constant.INTERRUPTED
	if a.IsSubagent() {
		// subagent using spawnGroup to sync info.
		a.getParentSpawnGroup().Crush(a.ID, "[subagent loop interrupted]", err)
		return err
	}

	// main agent using emit to sync info.
	a.emit(event.KindRunCancelled, nil)
	a.logger.Info("run.cancelled", "err", err)
	if errors.Is(err, context.Canceled) {
		return llm.ErrInterrupted
	}
	return err
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
	if a.IsSubagent() || !a.toolState.HasAgentGroupPanel() {
		// subagents don't have subagents.
		return
	}

	// completed = async agent done reports.
	completed := a.toolState.AgentGroup().DrainCompleted()
	if len(completed) == 0 {
		return
	}

	a.status = constant.DRAINING
	a.emit(event.KindDrainingInfo, nil)

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

// drainWakeupPrompts pulls every prompt the SCHEDULE_WAKEUP tool has
// enqueued since the last iteration and appends each one as a fresh
// RoleUser message — so the next LLM call sees them exactly as if the
// user just typed them.
//
// Runs at the top of every iteration alongside drainAsyncSubagents.
// The queue is per-ToolState (per-agent), so subagents can use the tool
// too without crossing wires — though in practice only the root profile
// exposes SCHEDULE_WAKEUP today.
func (a *Agent) drainWakeupPrompts() {
	if !a.toolState.HasWakeupQueue() {
		return
	}
	prompts := a.toolState.WakeupQueue().Drain()
	if len(prompts) == 0 {
		return
	}
	for _, p := range prompts {
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: p})
	}
	a.logger.Debug("wakeup.drained", "count", len(prompts))
}

// drainUserPrompts pulls every prompt the user typed while a Run was
// already in flight and appends each one as a fresh RoleUser message.
// Mirror of drainAsyncSubagents and drainWakeupPrompts — the agent
// loop reads from a side-channel between iterations, so the prompt
// lands AFTER the previous turn's tool_results and the conversation
// stays well-formed for the next LLM call.
//
// Subagents are skipped: they have no user-facing input channel, so
// the queue on their per-agent ToolState is always empty in practice.
// We gate explicitly for clarity (and to avoid the lazy allocation
// when the tool was never built).
func (a *Agent) drainUserPrompts() {
	if a.IsSubagent() || !a.toolState.HasUserPromptQueue() {
		return
	}
	prompts := a.toolState.UserPromptQueue().Drain()
	if len(prompts) == 0 {
		return
	}
	for _, p := range prompts {
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: p})
	}
	a.logger.Debug("user_prompts.drained", "count", len(prompts))
}

// thinking opens an LLM call to advance the conversation. The actual
// transport work (Complete vs Stream branching, chunk routing) lives in
// llmCall; this method exists only to set the status and emit the
// turn-start event so the UI shows a heartbeat before the LLM responds.
func (a *Agent) thinking(ctx context.Context, iter int) (llm.Response, error) {
	a.status = constant.THINKING

	if a.IsSubagent() {
		a.getParentSpawnGroup().Status(a.ID, constant.THINKING)
	} else {
		a.emit(event.KindTurnStart, func(e *event.Event) {
			e.Turn = &event.TurnPayload{Iteration: iter}
		})
	}

	return a.llmCall(ctx)
}

// crush is the terminal transition for Go-level failures the loop can't
// recover from — LLM transport errors, tool panics, etc. Subagent crashes
// surface to the parent via SpawnGroup; root-agent crashes emit
// KindError so the TUI can show a banner.
func (a *Agent) crush(stage string, err error) error {
	a.status = constant.CRUSHED

	if a.IsSubagent() {
		a.getParentSpawnGroup().Crush(a.ID, "[subagent crushed]", err)
		return err
	}

	a.emit(event.KindError, func(e *event.Event) {
		e.Error = &event.ErrorPayload{Stage: stage, Err: err}
	})

	a.logger.Error("run.crushed", "stage", stage, "err", err)
	return err
}

// text records the LLM's reply on the session and broadcasts it to the
// UI. Always emits KindUsage; emits whole-block KindThinking and KindText
// only in non-streaming mode (in streaming mode the chunk events already
// painted those blocks progressively — see internal/agent/stream.go).
// Subagents skip all emissions; the parent SpawnGroup carries their state.
func (a *Agent) text(usage llm.Usage, thinking string, content string) {
	a.session.RecordTurn(usage)
	a.status = constant.TEXTING

	if a.IsSubagent() {
		return
	}

	cfg := config.Get()

	a.emit(event.KindUsage, func(e *event.Event) {
		e.Usage = &event.UsagePayload{
			Turn:       usage,
			Cumulative: a.session.Usage,
		}
	})

	if a.profile.Stream {
		return
	}

	if cfg.GetDisplayThinking() && thinking != "" {
		a.emit(event.KindThinking, func(e *event.Event) {
			e.Thinking = &event.TextPayload{Text: thinking}
		})
	}
	if content != "" {
		a.emit(event.KindText, func(e *event.Event) {
			e.Text = &event.TextPayload{Text: content}
		})
	}
}

// turnOver marks the end of one tool-using iteration. Distinct from done()
// (loop.go) which marks the end of an entire run.
func (a *Agent) turnOver(iter int) {
	a.status = constant.IDLE

	if a.IsSubagent() {
		return
	}

	a.emit(event.KindTurnEnd, func(e *event.Event) {
		e.Turn = &event.TurnPayload{Iteration: iter}
	})
}

func (a *Agent) limitBreak() error {
	a.status = constant.MAX_ITERS
	if a.IsSubagent() {
		a.getParentSpawnGroup().Crush(a.ID, "[subagent paused at iteration limit]", ErrIterLimit)
		return ErrIterLimit
	}

	a.emit(event.KindIterLimit, func(e *event.Event) {
		e.IterLimit = &event.IterLimitPayload{Reached: int(a.maxIters.Load())}
	})

	return ErrIterLimit
}

// execTool executes a single tool call and emits its lifecycle events.
// It always returns a non-nil *llm.ToolResult unless it returns a Go-level
// error (panic / transport failure). Resolution failures and tool-reported
// errors flow back as IsError ToolResults so the model can recover.
func (a *Agent) execTool(ctx context.Context, call *tools.Call, tool tools.Tool, resolveToolErr error) (*llm.ToolResult, error) {
	a.status = constant.EXECUTING
	a.logger.Debug("tool.dispatch", "name", call.Name, "tool_id", call.ID)

	if a.IsSubagent() {
		a.getParentSpawnGroup().Status(a.ID, constant.EXECUTING)
	} else {
		a.emit(event.KindToolUseStart, func(e *event.Event) {
			e.ToolUseStart = &event.ToolUseStartPayload{
				Name:   call.Name,
				Input:  call.Input,
				ToolID: call.ID,
			}
		})
	}

	if resolveToolErr != nil {
		msg := resolveToolErr.Error()
		a.logger.Warn("tool.reject", "name", call.Name, "err", resolveToolErr)

		if !a.IsSubagent() {
			a.emit(event.KindToolUseResult, func(e *event.Event) {
				e.ToolUseResult = &event.ToolUseResultPayload{
					ToolID:  call.ID,
					Content: msg,
					IsError: true,
				}
			})
		}

		return &llm.ToolResult{ID: call.ID, Content: msg, IsError: true}, nil
	}

	if denied, denyResult := a.permissionGate(ctx, call); denied {
		return denyResult, nil
	}

	toolLogger := a.logger.With("tool", call.Name, "tool_id", call.ID)
	result, err := tool.Execute(ctx, toolLogger, call.Input)
	if err != nil {
		// Go-level failure, not a tool-reported error.
		a.logger.Error("tool.exec.fail", "name", call.Name, "err", err)
		return &llm.ToolResult{
			ID:      call.ID,
			Content: fmt.Sprintf("evva system level error, detail: %s\n", err.Error()),
			IsError: true}, err
	}

	a.logger.Debug("tool.result",
		"name", call.Name,
		"is_error", result.IsError,
		"bytes", len(result.Content),
	)

	if !a.IsSubagent() {
		a.emit(event.KindToolUseResult, func(e *event.Event) {
			e.ToolUseResult = &event.ToolUseResultPayload{
				ToolID:        call.ID,
				Content:       result.Content,
				IsError:       result.IsError,
				Metadata:      result.Metadata,
				ContentBlocks: result.ContentBlocks,
			}
		})
	}

	return &llm.ToolResult{
		ID:            call.ID,
		Content:       result.Content,
		IsError:       result.IsError,
		ContentBlocks: result.ContentBlocks,
	}, nil
}

// permissionGate runs the call through Decide() and, if needed, the
// approval broker. Returns (denied=true, result) when the call should NOT
// reach tool.Execute — the caller turns this into the LLM-visible result.
//
// Skipped when no Store is installed (tests, headless runs without
// permission config). When skipped, the call falls through to Execute as
// if mode were ModeBypass — preserves prior behavior so existing tests
// keep working.
func (a *Agent) permissionGate(ctx context.Context, call *tools.Call) (bool, *llm.ToolResult) {
	store := a.permissionStore
	if store == nil {
		return false, nil
	}

	mode := a.PermissionMode()
	hint := buildHint(call)

	pcall := permission.ToolCall{Name: call.Name, Input: call.Input}
	d := permission.Decide(pcall, mode, store, hint)

	if d.Behavior == permission.BehaviorAsk {
		if a.permissionBroker == nil {
			a.logger.Warn("permission.no_broker", "tool", call.Name)
			return true, &llm.ToolResult{
				ID:      call.ID,
				Content: "permission required but no approval broker is installed",
				IsError: true,
			}
		}
		a.logger.Info("permission.ask", "tool", call.Name, "mode", string(mode), "reason", d.Reason)
		req := permission.ApprovalRequest{
			AgentID:   a.ID,
			ToolName:  call.Name,
			ToolInput: call.Input,
			Mode:      mode,
			Reason:    d.Reason,
			Hint:      hint,
		}
		resp, err := a.permissionBroker.Request(ctx, req)
		if err != nil {
			a.logger.Warn("permission.broker.cancel", "tool", call.Name, "err", err)
		}
		d = resp
		if d.AddRule != nil {
			store.AddSessionRule(*d.AddRule)
			a.logger.Info("permission.rule.added", "tool", d.AddRule.ToolName, "content", d.AddRule.Content, "behavior", string(d.AddRule.Behavior))
		}
	}

	if d.Behavior == permission.BehaviorDeny {
		a.logger.Info("permission.deny", "tool", call.Name, "mode", string(mode), "reason", d.Reason)
		msg := "permission denied: " + d.Reason
		if !a.IsSubagent() {
			a.emit(event.KindToolUseResult, func(e *event.Event) {
				e.ToolUseResult = &event.ToolUseResultPayload{
					ToolID:  call.ID,
					Content: msg,
					IsError: true,
				}
			})
		}
		return true, &llm.ToolResult{ID: call.ID, Content: msg, IsError: true}
	}

	a.logger.Debug("permission.allow", "tool", call.Name, "mode", string(mode), "reason", d.Reason)
	return false, nil
}

// buildHint pre-computes a classifier hint for tools whose risk depends on
// the input. Today only Bash sets a non-zero hint — read/write tools have
// uniform risk by name.
func buildHint(call *tools.Call) permission.Hint {
	if call.Name != "bash" {
		return permission.Hint{}
	}
	cmd := extractBashCommand(call.Input)
	c := shell.Classify(cmd)
	return permission.Hint{
		IsReadOnly:  c.Risk == shell.RiskReadOnly,
		IsCommonFS:  c.IsCommonFS,
		IsDangerous: c.Risk == shell.RiskDangerous,
		Matched:     c.Matched,
		Reason:      c.Reason,
	}
}

// extractBashCommand pulls "command" out of a Bash tool input. Mirrors the
// helper in internal/permission/matcher.go — duplicated here to keep the
// agent package free of a doublestar dep just for this lookup.
func extractBashCommand(raw []byte) string {
	s := string(raw)
	key := `"command"`
	idx := strings.Index(s, key)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(key):]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return ""
	}
	rest = rest[colon+1:]
	for len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t' || rest[0] == '\n' || rest[0] == '\r') {
		rest = rest[1:]
	}
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	var b strings.Builder
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '\\' && i+1 < len(rest) {
			next := rest[i+1]
			switch next {
			case '"':
				b.WriteByte('"')
			case '\\':
				b.WriteByte('\\')
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			default:
				b.WriteByte(next)
			}
			i++
			continue
		}
		if c == '"' {
			return b.String()
		}
		b.WriteByte(c)
	}
	return ""
}

// getParentSpawnGroup is the subagent-only handle on the root agent's
// SpawnGroup panel — the channel through which a subagent's status,
// final report, and crashes propagate up to the parent without going
// through the event sink.
func (a *Agent) getParentSpawnGroup() *meta.SpawnGroup {
	if a.Parent == nil {
		return nil
	}
	return a.Parent.ToolState().AgentGroup()
}
