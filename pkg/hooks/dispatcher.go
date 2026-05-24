package hooks

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"
)

// BaseFactory builds a fresh BasePayload each time a hook fires. Live
// fields (PermissionMode in particular) can change mid-session, so the
// dispatcher rebuilds the base every fire instead of caching it.
type BaseFactory func() BasePayload

// Dispatcher is the per-agent surface for firing hooks. It holds a
// reference to the shared *Registry plus per-agent context (logger,
// base-payload factory). Subagents construct their own Dispatcher from
// the parent's Registry so agent_id / agent_type are baked into payloads.
//
// A nil *Dispatcher is safe — the Fire methods all noop. Callers don't
// need to nil-guard.
type Dispatcher struct {
	reg     *Registry
	logger  *slog.Logger
	baseFn  BaseFactory
	envBase []string // common env vars added to every subprocess
}

// NewDispatcher builds a Dispatcher. baseFn is called once per fire to
// snapshot the live envelope.
func NewDispatcher(reg *Registry, logger *slog.Logger, baseFn BaseFactory, projectDir string) *Dispatcher {
	if logger == nil {
		logger = slog.Default()
	}
	if baseFn == nil {
		baseFn = func() BasePayload { return BasePayload{} }
	}
	env := os.Environ()
	if projectDir != "" {
		env = append(env, "EVVA_PROJECT_DIR="+projectDir)
	}
	return &Dispatcher{
		reg:     reg,
		logger:  logger,
		baseFn:  baseFn,
		envBase: env,
	}
}

// Has reports whether any hooks are configured for e. The agent loop
// uses this to skip payload-building when no hook would fire.
func (d *Dispatcher) Has(e Event) bool {
	if d == nil || d.reg == nil {
		return false
	}
	return d.reg.HasAny(e)
}

// runOne dispatches a single Command and returns its parsed Decision.
// Honors the Async flag — async hooks return an empty Decision after
// firing in a goroutine.
func (d *Dispatcher) runOne(ctx context.Context, cmd Command, payload []byte, defaultTimeout time.Duration) (Decision, bool, string) {
	switch cmd.Type {
	case TypeCommand:
		if cmd.Async {
			runCommandAsync(ctx, d.logger, cmd, payload, d.envSnapshot())
			return Decision{}, false, ""
		}
		res := runCommand(ctx, d.logger, cmd, payload, d.envSnapshot(), defaultTimeout)
		switch {
		case res.timedOut:
			d.logger.Warn("hooks.timeout.skip", "cmd", truncate(cmd.Command, 80))
			return Decision{}, false, ""
		case res.exitCode == 2:
			reason := extractReason(res, "hook exit 2")
			return Decision{}, true, reason
		case res.exitCode == 0:
			return parseDecision(res.stdout), false, ""
		default:
			d.logger.Info("hooks.nonblocking_error",
				"cmd", truncate(cmd.Command, 80),
				"exit", res.exitCode,
				"stderr", truncate(string(res.stderr), 200),
			)
			return Decision{}, false, ""
		}
	case TypeHTTP:
		if cmd.Async {
			runHTTPAsync(ctx, d.logger, cmd, payload)
			return Decision{}, false, ""
		}
		if err := runHTTP(ctx, d.logger, cmd, payload, 10*time.Second); err != nil {
			d.logger.Info("hooks.http.nonblocking_error", "url", cmd.URL, "err", err)
		}
		return Decision{}, false, ""
	default:
		return Decision{}, false, ""
	}
}

func (d *Dispatcher) envSnapshot() []string {
	out := make([]string, len(d.envBase))
	copy(out, d.envBase)
	base := d.baseFn()
	if base.SessionID != "" {
		out = append(out, "EVVA_SESSION_ID="+base.SessionID)
	}
	if base.AgentID != "" {
		out = append(out, "EVVA_AGENT_ID="+base.AgentID)
	}
	return out
}

// FirePreToolUse runs every PreToolUse hook whose matcher matches the
// tool name, sequentially, threading updatedInput forward. Returns the
// final decision the agent loop applies before the permission gate.
//
// Returns (nil, nil) when no hooks are configured — caller falls through
// to the gate as normal.
func (d *Dispatcher) FirePreToolUse(ctx context.Context, toolName string, toolInput []byte, toolUseID string) (*PreToolUseDecision, error) {
	if d == nil || d.reg == nil {
		return nil, nil
	}
	configs := d.reg.For(EventPreToolUse)
	if len(configs) == 0 {
		return nil, nil
	}
	currentInput := toolInput
	acc := &PreToolUseDecision{}

	for _, cfg := range configs {
		if !matchTool(cfg.Matcher, toolName) {
			continue
		}
		for _, cmd := range cfg.Hooks {
			base := d.baseFn()
			base.HookEventName = string(EventPreToolUse)
			payload := PreToolUsePayload{
				BasePayload: base,
				ToolName:    toolName,
				ToolInput:   currentInput,
				ToolUseID:   toolUseID,
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			dec, blocked, reason := d.runOne(ctx, cmd, body, 60*time.Second)
			if blocked {
				acc.Blocked = true
				acc.BlockReason = reason
				return acc, nil
			}
			if applyPreToolUse(acc, dec) {
				return acc, nil
			}
			if len(acc.UpdatedInput) > 0 {
				currentInput = acc.UpdatedInput
			}
		}
	}
	if acc.PermissionDecision == "" && len(acc.UpdatedInput) == 0 && acc.AdditionalContext == "" && !acc.Blocked {
		return nil, nil
	}
	return acc, nil
}

// FirePostToolUse runs every PostToolUse hook whose matcher matches the
// tool name. Non-blocking — the only side effect is the returned
// additionalContext string, which the agent loop appends to the tool's
// result content for the LLM's next turn.
func (d *Dispatcher) FirePostToolUse(ctx context.Context, toolName string, toolInput []byte, toolResponse string, toolUseID string, isError bool) (string, error) {
	if d == nil || d.reg == nil {
		return "", nil
	}
	configs := d.reg.For(EventPostToolUse)
	if len(configs) == 0 {
		return "", nil
	}
	var combined strings.Builder

	for _, cfg := range configs {
		if !matchTool(cfg.Matcher, toolName) {
			continue
		}
		for _, cmd := range cfg.Hooks {
			base := d.baseFn()
			base.HookEventName = string(EventPostToolUse)
			payload := PostToolUsePayload{
				BasePayload:  base,
				ToolName:     toolName,
				ToolInput:    toolInput,
				ToolResponse: toolResponse,
				IsError:      isError,
				ToolUseID:    toolUseID,
			}
			body, err := json.Marshal(payload)
			if err != nil {
				return combined.String(), err
			}
			dec, _, _ := d.runOne(ctx, cmd, body, 60*time.Second)
			if ac := extractAdditionalContext(dec); ac != "" {
				if combined.Len() > 0 {
					combined.WriteString("\n")
				}
				combined.WriteString(ac)
			}
		}
	}
	return combined.String(), nil
}

// FireSessionStart fires every SessionStart hook. Returns
// initialUserMessage (a synthetic user prompt prepended to the session)
// and additionalContext (appended to the first real prompt).
func (d *Dispatcher) FireSessionStart(ctx context.Context, source, model string) (initialUserMessage, additionalContext string, err error) {
	if d == nil || d.reg == nil {
		return "", "", nil
	}
	configs := d.reg.For(EventSessionStart)
	if len(configs) == 0 {
		return "", "", nil
	}
	var initParts, ctxParts []string

	for _, cfg := range configs {
		for _, cmd := range cfg.Hooks {
			base := d.baseFn()
			base.HookEventName = string(EventSessionStart)
			payload := SessionStartPayload{
				BasePayload: base,
				Source:      source,
				Model:       model,
			}
			body, mErr := json.Marshal(payload)
			if mErr != nil {
				return "", "", mErr
			}
			dec, _, _ := d.runOne(ctx, cmd, body, 30*time.Second)
			if v := extractInitialUserMessage(dec); v != "" {
				initParts = append(initParts, v)
			}
			if v := extractAdditionalContext(dec); v != "" {
				ctxParts = append(ctxParts, v)
			}
		}
	}
	return strings.Join(initParts, "\n"), strings.Join(ctxParts, "\n"), nil
}

// FireUserPromptSubmit runs every UserPromptSubmit hook. Returns the
// concatenated additionalContext (appended to the prompt), a blocked
// flag, and a reason. When blocked the caller should drop the prompt.
func (d *Dispatcher) FireUserPromptSubmit(ctx context.Context, prompt string) (additionalContext string, blocked bool, blockReason string, err error) {
	if d == nil || d.reg == nil {
		return "", false, "", nil
	}
	configs := d.reg.For(EventUserPromptSubmit)
	if len(configs) == 0 {
		return "", false, "", nil
	}
	var parts []string

	for _, cfg := range configs {
		for _, cmd := range cfg.Hooks {
			base := d.baseFn()
			base.HookEventName = string(EventUserPromptSubmit)
			payload := UserPromptSubmitPayload{
				BasePayload: base,
				Prompt:      prompt,
			}
			body, mErr := json.Marshal(payload)
			if mErr != nil {
				return strings.Join(parts, "\n"), false, "", mErr
			}
			dec, hookBlocked, reason := d.runOne(ctx, cmd, body, 30*time.Second)
			if hookBlocked {
				return "", true, reason, nil
			}
			if b, r := isBlock(dec); b {
				return "", true, r, nil
			}
			if v := extractAdditionalContext(dec); v != "" {
				parts = append(parts, v)
			}
		}
	}
	return strings.Join(parts, "\n"), false, "", nil
}

// FireStop runs every Stop hook. Returns blocked=true if the agent
// should re-enter the loop with the given reason as a synthetic user
// message. stopHookActive=true on the re-entry pass guarantees the
// hook is consulted but its block is no longer honored — prevents
// infinite loops.
func (d *Dispatcher) FireStop(ctx context.Context, lastMessage string, stopHookActive bool) (blocked bool, reason string, err error) {
	if d == nil || d.reg == nil {
		return false, "", nil
	}
	configs := d.reg.For(EventStop)
	if len(configs) == 0 {
		return false, "", nil
	}

	for _, cfg := range configs {
		for _, cmd := range cfg.Hooks {
			base := d.baseFn()
			base.HookEventName = string(EventStop)
			payload := StopPayload{
				BasePayload:          base,
				StopHookActive:       stopHookActive,
				LastAssistantMessage: lastMessage,
			}
			body, mErr := json.Marshal(payload)
			if mErr != nil {
				return false, "", mErr
			}
			dec, hookBlocked, hookReason := d.runOne(ctx, cmd, body, 30*time.Second)
			if hookBlocked && !stopHookActive {
				return true, hookReason, nil
			}
			if b, r := isBlock(dec); b && !stopHookActive {
				return true, r, nil
			}
		}
	}
	return false, "", nil
}

// FireNotification fires every Notification hook. Async by default
// (hooks default to fire-and-forget for HTTP, the dispatcher honors
// each hook's Async flag). Always returns nil — notifications can't
// fail-loud since they're a side channel.
func (d *Dispatcher) FireNotification(ctx context.Context, message, title, ntype string) {
	if d == nil || d.reg == nil {
		return
	}
	configs := d.reg.For(EventNotification)
	if len(configs) == 0 {
		return
	}

	for _, cfg := range configs {
		for _, cmd := range cfg.Hooks {
			base := d.baseFn()
			base.HookEventName = string(EventNotification)
			payload := NotificationPayload{
				BasePayload: base,
				Message:     message,
				Title:       title,
				NType:       ntype,
			}
			body, err := json.Marshal(payload)
			if err != nil {
				continue
			}
			_, _, _ = d.runOne(ctx, cmd, body, 10*time.Second)
		}
	}
}
