package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/johnny1110/evva/internal/agent/attachments"
	"github.com/johnny1110/evva/internal/tools/mode"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/hooks"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/daemon"
	"github.com/johnny1110/evva/pkg/tools/shell"
)

// Each function below maps to one transition of constant.AgentStatus. They
// are the only sites in the agent layer that mutate a.status, so the file
// reads as a state-by-state reference:
//
//   INIT         AgentStatus = "init"
//   THINKING     AgentStatus = "thinking"      — thinking()
//   EXECUTING    AgentStatus = "executing"     — execTool()
//   COMPACTING   AgentStatus = "compacting"    — compact() (compact.go)
//   TEXTING      AgentStatus = "texting"       — text()
//   IDLE         AgentStatus = "idle"          — turnOver(), done() (loop.go)
//   INTERRUPTED  AgentStatus = "interrupted"   — interrupted()
//   CRUSHED      AgentStatus = "crushed"       — crush()
//   MAX_ITERS    AgentStatus = "max_iters"     — limitBreak() (agent.go)
//   READY_REPORT AgentStatus = "ready_report"  — done() (loop.go)
//
// Each method sets a.status, emits the appropriate event for the main agent
// (or pushes the equivalent update through the parent's agentDaemon entry
// in the parent's DaemonState for a subagent), and either returns the
// state-machine's outcome or hands off to the next step in the loop.

// interrupted converts a raw ctx error into the llm.ErrInterrupted contract
// the rest of the codebase agrees on, and emits the cancellation event.
func (a *Agent) interrupted(err error) error {
	a.status = constant.INTERRUPTED
	if a.IsSubagent() {
		if ad := a.getOwnDaemon(); ad != nil {
			ad.Crush("[subagent loop interrupted]", err, daemon.StatusKilled)
		}
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
// Before the user prompts land, computePlanModeAttachments runs once
// per call. When plan mode is active (or just exited) it returns
// <system-reminder>-wrapped messages that get prepended to the session
// — this is how the model learns turn-to-turn that it is currently in
// plan mode (the static system prompt only knows plan mode exists as a
// concept).
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
	for _, reminder := range a.computePlanModeAttachments() {
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: reminder})
	}
	for _, p := range prompts {
		// UserPromptSubmit for mid-run prompts: same semantics as the
		// primary Run() path — block drops the prompt, additionalContext
		// is appended. Fires with background ctx since drainUserPrompts
		// runs between turns (no per-call ctx available).
		effectivePrompt := p
		if a.hookDispatcher.Has(hooks.EventUserPromptSubmit) {
			addCtx, blocked, reason, he := a.hookDispatcher.FireUserPromptSubmit(context.Background(), p)
			if he != nil {
				a.logger.Warn("hooks.userpromptsubmit.drain", "err", he)
			}
			if blocked {
				a.emit(event.KindError, func(e *event.Event) {
					e.Error = &event.ErrorPayload{Stage: "UserPromptSubmit(drain)", Message: reason}
				})
				a.logger.Info("hooks.userpromptsubmit.drain.blocked", "reason", reason)
				continue
			}
			if addCtx != "" {
				effectivePrompt = effectivePrompt + "\n" + addCtx
			}
		}
		a.session.Append(llm.Message{Role: llm.RoleUser, Content: effectivePrompt})
	}
	a.logger.Debug("user_prompts.drained", "count", len(prompts))
}

// computePlanModeAttachments runs the per-turn attachment computer and
// returns the reminder texts to prepend (already wrapped in
// <system-reminder> tags). Returns nil when no reminders are owed.
//
// Workflow variant defaults to "interview" (the iterative pair-planning
// loop). Set EVVA_PLAN_MODE_WORKFLOW=v2 to opt into the 5-phase
// ref-Claude-Code workflow.
func (a *Agent) computePlanModeAttachments() []string {
	if a.planModeState == nil {
		return nil
	}
	planPath := mode.PlanFilePath(a.workdir, a.planModeState.PlanName())
	planExists := false
	if planPath != "" {
		if info, err := os.Stat(planPath); err == nil && !info.IsDir() && info.Size() > 0 {
			planExists = true
		}
	}
	variant := attachments.WorkflowInterview
	if v := strings.ToLower(strings.TrimSpace(os.Getenv("EVVA_PLAN_MODE_WORKFLOW"))); v == "v2" {
		variant = attachments.WorkflowV2
	}
	return attachments.ComputePlanMode(attachments.Input{
		State:           a.planModeState,
		Mode:            a.PermissionMode(),
		PlanFilePath:    planPath,
		PlanExists:      planExists,
		WorkflowVariant: variant,
	})
}

// thinking opens an LLM call to advance the conversation. The actual
// transport work (Complete vs Stream branching, chunk routing) lives in
// llmCall; this method exists only to set the status and emit the
// turn-start event so the UI shows a heartbeat before the LLM responds.
func (a *Agent) thinking(ctx context.Context, iter int) (llm.Response, error) {
	a.status = constant.THINKING

	if a.IsSubagent() {
		if ad := a.getOwnDaemon(); ad != nil {
			ad.Phase(constant.THINKING)
		}
	} else {
		a.emit(event.KindTurnStart, func(e *event.Event) {
			e.Turn = &event.TurnPayload{Iteration: iter}
		})
	}

	return a.llmCall(ctx)
}

// crush is the terminal transition for Go-level failures the loop can't
// recover from — LLM transport errors, tool panics, etc. Subagent crashes
// surface to the parent via the parent's agentDaemon entry in
// DaemonState; root-agent crashes emit KindError so the TUI can show a
// banner.
func (a *Agent) crush(stage string, err error) error {
	a.status = constant.CRUSHED

	if a.IsSubagent() {
		if ad := a.getOwnDaemon(); ad != nil {
			ad.Crush("[subagent crushed]", err, daemon.StatusFailed)
		}
		return err
	}

	a.emit(event.KindError, func(e *event.Event) {
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		e.Error = &event.ErrorPayload{Stage: stage, Err: err, Message: msg}
	})

	a.logger.Error("run.crushed", "stage", stage, "err", err)
	return err
}

// text records the LLM's reply on the session and broadcasts it to the
// UI. Always emits KindUsage; emits whole-block KindThinking and KindText
// only in non-streaming mode (in streaming mode the chunk events already
// painted those blocks progressively — see internal/agent/stream.go).
// Subagents skip all emissions; the parent's agentDaemon entry carries their state.
func (a *Agent) text(usage llm.Usage, thinking string, content string) {
	a.session.RecordTurn(usage)
	a.status = constant.TEXTING

	if a.IsSubagent() {
		return
	}

	cfg := a.cfg

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
		if ad := a.getOwnDaemon(); ad != nil {
			ad.Crush("[subagent paused at iteration limit]", ErrIterLimit, daemon.StatusFailed)
		}
		return ErrIterLimit
	}

	a.emit(event.KindIterLimit, func(e *event.Event) {
		e.IterLimit = &event.IterLimitPayload{Iters: int(a.maxIters.Load())}
	})

	// Notification hook: fire-and-forget for out-of-band events.
	// Uses Background ctx since limitBreak is called without a request context.
	a.hookDispatcher.FireNotification(context.Background(), "iteration limit reached", "Iteration Limit", "iter_limit")

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
		if ad := a.getOwnDaemon(); ad != nil {
			ad.Phase(constant.EXECUTING)
		}
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

	// PreToolUse hooks fire before the permission gate. A hook can block
	// the tool (skip gate + execute), mutate the input, or override the
	// permission decision. effectiveInput is the input the tool actually
	// executes with — it starts as the model's call.Input and may be
	// replaced by hookSpecificOutput.updatedInput.
	effectiveInput := call.Input
	var postCtx string
	var permOverride string

	if a.hookDispatcher.Has(hooks.EventPreToolUse) {
		dec, he := a.hookDispatcher.FirePreToolUse(ctx, call.Name, effectiveInput, call.ID)
		if he != nil {
			a.logger.Warn("hooks.pretooluse", "err", he)
		}
		if dec != nil {
			if dec.Blocked {
				return a.toolError(call, dec.BlockReason), nil
			}
			if len(dec.UpdatedInput) > 0 {
				effectiveInput = dec.UpdatedInput
			}
			if dec.AdditionalContext != "" {
				postCtx = dec.AdditionalContext
			}
			permOverride = dec.PermissionDecision
		}
	}

	if denied, denyResult := a.permissionGateWithOverride(ctx, call, effectiveInput, permOverride); denied {
		return denyResult, nil
	}

	toolLogger := a.logger.With("tool", call.Name, "tool_id", call.ID)
	result, err := tool.Execute(ctx, toolLogger, effectiveInput)
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

	content := result.Content
	if a.hookDispatcher.Has(hooks.EventPostToolUse) {
		add, he := a.hookDispatcher.FirePostToolUse(ctx, call.Name, effectiveInput, result.Content, call.ID, result.IsError)
		if add != "" {
			content = content + "\n" + add
		}
		if he != nil {
			a.logger.Warn("hooks.posttooluse", "err", he)
		}
	}
	if postCtx != "" {
		content = content + "\n" + postCtx
	}

	if !a.IsSubagent() {
		a.emit(event.KindToolUseResult, func(e *event.Event) {
			e.ToolUseResult = &event.ToolUseResultPayload{
				ToolID:        call.ID,
				Content:       content,
				IsError:       result.IsError,
				Metadata:      result.Metadata,
				ContentBlocks: result.ContentBlocks,
			}
		})
	}

	return &llm.ToolResult{
		ID:            call.ID,
		Content:       content,
		IsError:       result.IsError,
		ContentBlocks: result.ContentBlocks,
	}, nil
}

// toolError returns a blocked tool result without executing or consulting
// the permission gate. Called by the PreToolUse hook path when a hook
// blocks the tool (exit 2 or decision=block).
func (a *Agent) toolError(call *tools.Call, reason string) *llm.ToolResult {
	msg := reason
	if msg == "" {
		msg = "hook blocked"
	}
	if !a.IsSubagent() {
		a.emit(event.KindToolUseResult, func(e *event.Event) {
			e.ToolUseResult = &event.ToolUseResultPayload{
				ToolID:  call.ID,
				Content: msg,
				IsError: true,
			}
		})
	}
	return &llm.ToolResult{ID: call.ID, Content: msg, IsError: true}
}

// permissionGateWithOverride is permissionGate + the hook's permissionDecision
// override. effectiveInput is the input the tool will actually execute with
// (possibly mutated by hookSpecificOutput.updatedInput). override is one of
// "" | "allow" | "deny" | "ask" — "" means no hook opinion, fall through to
// the regular gate.
func (a *Agent) permissionGateWithOverride(ctx context.Context, call *tools.Call, effectiveInput []byte, override string) (bool, *llm.ToolResult) {
	switch override {
	case "deny":
		a.logger.Info("permission.hook_override", "tool", call.Name, "decision", "deny")
		msg := "hook denied"
		return true, a.toolError(call, msg)
	case "allow":
		a.logger.Info("permission.hook_override", "tool", call.Name, "decision", "allow")
		return false, nil
	case "ask":
		// Fall through to the gate — the gate will run the normal
		// Decide+Broker path as if no rule auto-decided.
	default:
		// "" — no hook opinion; run the gate normally with the effective input.
	}

	store := a.permissionStore
	if store == nil {
		return false, nil
	}

	mode := a.PermissionMode()
	hint := buildHint(call)

	// a.memSnap.MemoryDir is the resolved <appHome>/memory dir when auto-memory
	// is on, "" when off — so it doubles as the carve-out gate (A9). The model
	// maintains its typed memory files with write/edit; a write confined to that
	// dir auto-allows without a prompt (default + accept-edits; plan mode still
	// denies). See pkg/permission.isAutoMemWrite.
	pcall := permission.ToolCall{Name: call.Name, Input: effectiveInput}
	d := permission.Decide(pcall, mode, store, hint, a.workdir, a.memSnap.MemoryDir)

	// When the hook says "ask", force the Ask branch even if Decide
	// returned Allow or Deny. This lets a hook prompt the user for a
	// decision the rule engine would have auto-decided.
	if override == "ask" && d.Behavior != permission.BehaviorAsk {
		d.Behavior = permission.BehaviorAsk
		d.Reason = "hook asked"
	}

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
			AgentID:          a.ID,
			ToolName:         call.Name,
			ToolInput:        effectiveInput,
			InputDescription: extractInputDescription(effectiveInput),
			Mode:             mode,
			Reason:           d.Reason,
			Hint:             hint,
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

// extractInputDescription pulls a top-level `description` string out of a
// tool call's raw JSON input. Bash's input schema includes such a field
// (the model fills it with "what this command does in active voice");
// future tools that adopt the same convention get the same UX without a
// per-tool switch. Returns "" when the input isn't a JSON object, has no
// `description` key, or the value isn't a string.
func extractInputDescription(input json.RawMessage) string {
	if len(input) == 0 {
		return ""
	}
	var probe struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(input, &probe); err != nil {
		return ""
	}
	return probe.Description
}
