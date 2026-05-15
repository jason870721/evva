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
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/internal/tools"
	"github.com/johnny1110/evva/internal/tools/meta"
)

// INIT         AgentStatus = "init"
// THINKING     AgentStatus = "thinking"
// EXECUTING    AgentStatus = "executing"
// INTERRUPTED  AgentStatus = "interrupted"
// IDLE         AgentStatus = "idle"
// MAX_ITERS    AgentStatus = "max_iters"
// SAVING       AgentStatus = "saving"
// COMPACTING   AgentStatus = "compacting"
// READY_REPORT AgentStatus = "ready_report"
// SHUTDOWN     AgentStatus = "shutdown"
// CRUSHED      AgentStatus = "crushed"

// interrupted converts a raw ctx error into the llm.ErrInterrupted contract
// the rest of the codebase agrees on, and emits the cancellation event.
func (a *Agent) interrupted(err error) error {
	a.status = constant.INTERRUPTED
	if a.IsSubagent() {
		// subagent using spawnGroup to sync info.
		a.getParentSpawnGroup().Crush(a.ID, err)
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
	a.status = constant.DRAINING
}

func (a *Agent) thinking(ctx context.Context, iter int) (llm.Response, error) {
	a.status = constant.THINKING

	// UI render
	if a.IsSubagent() {
		a.getParentSpawnGroup().Status(a.ID, constant.THINKING)
	} else {
		a.emit(event.KindTurnStart, func(e *event.Event) {
			e.Turn = &event.TurnPayload{Iteration: iter}
		})
	}

	// llm call is real thinking part.
	return a.llmCall(ctx)
}

func (a *Agent) compact(ctx context.Context, s *session.Session) {
	cfg := config.Get()

	if a.IsSubagent() {
		// no compacting for subagents.
		return
	}

	modelStr := constant.Model(a.llm.Model())
	maxContextSize := constant.MODEL_CONTEXT_SIZE[modelStr]
	currentUsage := a.Session().Usage.Total()
	usageRatio := float64(currentUsage) / float64(maxContextSize)
	if usageRatio < cfg.AutoCompactThreshold {
		return // safe.
	}

	a.status = constant.COMPACTING

	if s.IsMicroCompacted() {
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "full", UsageRatio: usageRatio}
		})

		// TODO: call llm do full compact. write summary prompt here.
		// a.llm.Complete(ctx, ?, a.exposeTools)
		s.FullCompact(s.Messages) // set full compacted message.
	} else {
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "micro", UsageRatio: usageRatio}
		})

		// TODO do microcompact compact all tool use result block and keep recent 8 blocks.
		s.MicroCompact(s.Messages) // set micro compacted message.
	}

}

func (a *Agent) crush(stage string, err error) error {
	a.status = constant.CRUSHED

	if a.IsSubagent() {
		a.getParentSpawnGroup().Crush(a.ID, err)
		return err
	}

	a.emit(event.KindError, func(e *event.Event) {
		e.Error = &event.ErrorPayload{Stage: stage, Err: err}
	})

	a.logger.Error("run.crushed", "stage", stage, "err", err)
	return err
}

func (a *Agent) text(usage llm.Usage, thinking string, content string) {
	a.session.AddUsage(usage)
	a.status = constant.TEXTING

	if a.IsSubagent() {
		// subagent don't need sync text to ui.
		return
	}

	cfg := config.Get()

	a.emit(event.KindUsage, func(e *event.Event) {
		e.Usage = &event.UsagePayload{
			Turn:       usage,
			Cumulative: a.session.Usage,
		}
	})
	if cfg.DisplayThinking && thinking != "" {
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

func (a *Agent) turnOver(iter int) {
	a.status = constant.IDLE

	if a.IsSubagent() {
		return
	}

	a.emit(event.KindTurnEnd, func(e *event.Event) {
		e.Turn = &event.TurnPayload{Iteration: iter}
	})
}

// execTool executes a single tool call and emits its lifecycle events.
// It always returns a non-nil *llm.ToolResult unless it returns a Go-level
// error (panic / transport failure). Resolution failures and tool-reported
// errors flow back as IsError ToolResults so the model can recover.
func (a *Agent) execTool(ctx context.Context, call *tools.Call, tool tools.Tool, resolveToolErr error) (*llm.ToolResult, error) {
	a.status = constant.EXECUTING
	a.logger.Debug("tool.dispatch", "name", call.Name, "tool_id", call.ID)

	// UI render execute tool name and input.
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

	// UI render tool error.
	if resolveToolErr != nil {
		msg := resolveToolErr.Error()
		a.logger.Warn("tool.reject", "name", call.Name, "err", resolveToolErr)

		if !a.IsSubagent() {
			// only main agent can emit tool error.
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

	result, err := tool.Execute(ctx, call.Input)
	if err != nil {
		// system level error, not tool error.
		a.logger.Error("tool.exec.fail", "name", call.Name, "err", err)
		return nil, err
	}

	a.logger.Debug("tool.result",
		"name", call.Name,
		"is_error", result.IsError,
		"bytes", len(result.Content),
	)

	if !a.IsSubagent() {
		// only main agent can emit tool result.
		a.emit(event.KindToolUseResult, func(e *event.Event) {
			e.ToolUseResult = &event.ToolUseResultPayload{
				ToolID:   call.ID,
				Content:  result.Content,
				IsError:  result.IsError,
				Metadata: result.Metadata,
			}
		})
	}

	return &llm.ToolResult{ID: call.ID, Content: result.Content, IsError: result.IsError}, nil
}

// private methods ======================================

func (a *Agent) getParentSpawnGroup() *meta.SpawnGroup {
	if a.Parent == nil {
		return nil
	}

	return a.Parent.ToolState().AgentGroup()
}
