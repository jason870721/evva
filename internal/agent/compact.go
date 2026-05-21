package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
)

// Compaction has two levels, escalated lazily:
//
//   - Micro (level 1): elide the Content of every older RoleTool message
//     while preserving ToolResult.ID and IsError so the request stays
//     well-formed. Recent tool results stay verbatim; older ones become
//     short placeholders. Cheap, local, no LLM call.
//
//   - Full (level 2): ask the LLM to compress the entire conversation
//     into a single "context brief" — Original Task / Done / Current
//     Target / Next Step / Key Context — and replace Messages with one
//     User message carrying the brief plus a "proceed" instruction.
//     One LLM call; falls back gracefully on failure.
//
// Escalation: micro first; if it already ran on this session (and we're
// still over budget) the next compact goes full.

const (
	// microCompactKeepRecent is the number of trailing RoleTool messages
	// (one per parallel-dispatch turn) that micro-compact leaves untouched.
	// Older RoleTool messages have their ToolResult.Content elided.
	microCompactKeepRecent = 8

	// microCompactPlaceholder is the stand-in Content stored on elided
	// ToolResults. Short and self-describing — the model recognizes it as
	// "you've already seen this result; act on what came after."
	microCompactPlaceholder = "[elided by auto micro-compact]"

	// summaryToolResultMaxBytes caps each tool result rendered into the
	// summarizer prompt. Bounds the summarizer's own input size when the
	// transcript has many long results.
	summaryToolResultMaxBytes = 600
)

// Compact is the manual entry point invoked by the TUI's /compact
// chooser. Unlike the auto path it bypasses the threshold check and
// the micro→full escalation — the user explicitly picked a kind.
//
// Refuses with ErrRunInProgress when a Run is currently driving the
// loop, same guard SwitchLLM uses; the caller (TUI) surfaces that as
// a hint rather than queueing.
//
// kind is "micro" or "full"; any other value is an error.
func (a *Agent) Compact(ctx context.Context, kind string) error {
	if a.IsSubagent() {
		return fmt.Errorf("agent: subagents do not support manual compaction")
	}
	if a.running.Load() {
		return ErrRunInProgress
	}
	if !a.running.CompareAndSwap(false, true) {
		return ErrRunInProgress
	}
	defer a.running.Store(false)

	a.status = constant.COMPACTING

	switch kind {
	case "micro":
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "micro"}
		})
		a.logger.Info("compact.manual", "kind", "micro")
		a.microCompact(a.session)
		a.status = constant.IDLE
	case "full":
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "full"}
		})
		a.logger.Info("compact.manual", "kind", "full")
		a.fullCompact(ctx, a.session)
		a.status = constant.IDLE
	default:
		a.status = constant.IDLE
		return fmt.Errorf("agent: unknown compact kind %q (want \"micro\" or \"full\")", kind)
	}
	a.logger.Info("compact.done")
	a.emit(event.KindIdle, func(e *event.Event) {})
	return nil
}

// compact runs at the top of every iteration. It compares the last
// turn's input-token count against the model's context size — when the
// ratio exceeds the auto-compact threshold the session is reshaped to
// free room. See package-level note on micro vs full escalation.
//
// Every call logs one `compact.check` INFO line with the live inputs
// and the decision (skip:<reason> / trigger:<kind>) so the workflow is
// debuggable from grep alone.
func (a *Agent) compact(ctx context.Context, s *session.Session) {
	cfg := a.cfg

	if a.IsSubagent() {
		// no compacting for subagents.
		a.logger.Info("compact.check", "decision", "skip:subagent")
		return
	}

	modelStr := constant.Model(a.llm.Model())
	maxContextSize := constant.MODEL_CONTEXT_SIZE[modelStr]
	if maxContextSize == 0 {
		// Unknown model — we can't reason about ratio. Skip rather
		// than guess, the user keeps the full transcript.
		a.logger.Info("compact.check",
			"decision", "skip:unknown_model",
			"model", string(modelStr),
		)
		return
	}
	// Ratio is measured against the LAST turn's input tokens, not
	// cumulative Usage.Total. Cumulative grows across turns and stays
	// elevated even after compaction shrinks the prompt — ratio against
	// it would (a) trigger prematurely once enough turns add up, and
	// (b) re-trigger on every iteration after a full-compact because
	// the cumulative tally still reflects the pre-compact prompts.
	// LastTurnInputTokens is the actual size of the prompt the LLM
	// just had to process, which is what the threshold cares about.
	currentUsage := a.Session().LastTurnInputTokens()
	usageRatio := float64(currentUsage) / float64(maxContextSize)
	threshold := cfg.GetAutoCompactThreshold()
	microDone := s.IsMicroCompacted()
	if usageRatio < threshold {
		a.logger.Info("compact.check",
			"decision", "skip:under_threshold",
			"model", string(modelStr),
			"max_context", maxContextSize,
			"last_turn_input", currentUsage,
			"usage_ratio", usageRatio,
			"threshold", threshold,
			"micro_done", microDone,
		)
		return // safe.
	}

	a.status = constant.COMPACTING

	if microDone {
		a.logger.Info("compact.check",
			"decision", "trigger:full",
			"model", string(modelStr),
			"max_context", maxContextSize,
			"last_turn_input", currentUsage,
			"usage_ratio", usageRatio,
			"threshold", threshold,
			"micro_done", microDone,
		)
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "full", UsageRatio: usageRatio}
		})
		a.fullCompact(ctx, s)
	} else {
		a.logger.Info("compact.check",
			"decision", "trigger:micro",
			"model", string(modelStr),
			"max_context", maxContextSize,
			"last_turn_input", currentUsage,
			"usage_ratio", usageRatio,
			"threshold", threshold,
			"micro_done", microDone,
		)
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "micro", UsageRatio: usageRatio}
		})
		a.microCompact(s)
	}
}

// microCompact walks the session, identifies every RoleTool message, and
// replaces the Content of each ToolResult on older ones with a short
// placeholder. The most recent microCompactKeepRecent RoleTool messages
// stay verbatim. ToolResult.ID and IsError survive untouched so the
// next LLM request still matches tool_use/tool_result pairs correctly.
//
// No LLM call. Always flips s.microCompacted=true so the next compact
// escalates to full.
func (a *Agent) microCompact(s *session.Session) {
	msgs := s.GetMessages()

	// Indices of every RoleTool message in order.
	toolMsgIdx := make([]int, 0)
	for i, m := range msgs {
		if m.Role == llm.RoleTool {
			toolMsgIdx = append(toolMsgIdx, i)
		}
	}

	// Nothing old enough to elide — record the level transition so the
	// next compact promotes to full, but leave Messages untouched.
	if len(toolMsgIdx) <= microCompactKeepRecent {
		s.MicroCompact(msgs)
		a.logger.Info("compact.micro.skipped", "tool_messages", len(toolMsgIdx))
		a.emit(event.KindCompactingEnd, func(e *event.Event) {
			e.CompactingEnd = &event.CompactingEndPayload{Type: "micro", OK: true}
		})
		return
	}

	keep := make(map[int]struct{}, microCompactKeepRecent)
	for _, idx := range toolMsgIdx[len(toolMsgIdx)-microCompactKeepRecent:] {
		keep[idx] = struct{}{}
	}

	out := make([]llm.Message, len(msgs))
	var elidedMessages, elidedResults, elidedBytes int
	for i, m := range msgs {
		if m.Role != llm.RoleTool {
			out[i] = m
			continue
		}
		if _, recent := keep[i]; recent {
			out[i] = m
			continue
		}
		newResults := make([]*llm.ToolResult, len(m.ToolResults))
		for j, tr := range m.ToolResults {
			elidedBytes += len(tr.Content)
			elidedResults++
			newResults[j] = &llm.ToolResult{
				ID:      tr.ID,
				Content: microCompactPlaceholder,
				IsError: tr.IsError,
			}
		}
		out[i] = llm.Message{Role: llm.RoleTool, ToolResults: newResults}
		elidedMessages++
	}

	s.MicroCompact(out)
	a.logger.Info("compact.micro",
		"elided_tool_messages", elidedMessages,
		"elided_tool_results", elidedResults,
		"elided_bytes", elidedBytes,
		"kept_recent", microCompactKeepRecent,
		"last_turn_input_after", s.LastTurnInputTokens(),
	)
	a.emit(event.KindCompactingEnd, func(e *event.Event) {
		e.CompactingEnd = &event.CompactingEndPayload{Type: "micro", OK: true}
	})
}

// fullCompact summarizes the entire session into a single "context
// brief" via one LLM call and replaces s.Messages with that brief
// wrapped as a User message. The brief is structured (Original Task /
// Done So Far / Current Target / Next Step / Key Context) and ends with
// "Proceed with the next step" so the model continues working rather
// than acknowledging.
//
// Failure modes are non-fatal: a transport error, an empty response, or
// a cancelled context simply logs and returns. The session is left
// uncompacted; the next iteration will retry.
//
// The summarization call deliberately passes no tools (we want text,
// not a tool_use loop) and uses Complete (not Stream) since the brief
// is internal — no UI painting needed.
//
// On success the session's cumulative Usage is RESET to reflect the
// post-compact context (in=brief size, out=0) so the HUD reads as a
// fresh start. The pre-compact totals are logged before the reset so
// forensics keeps working. A matching KindUsage event is emitted so
// the TUI re-reads m.usage from the new cumulative.
func (a *Agent) fullCompact(ctx context.Context, s *session.Session) {
	prompt := buildSummarizationPrompt(s.GetMessages())
	summarizer := []llm.Message{{Role: llm.RoleUser, Content: prompt}}

	resp, err := a.llm.Complete(ctx, summarizer, nil)
	if err != nil {
		a.logger.Warn("compact.full.failed", "err", err)
		a.emit(event.KindCompactingEnd, func(e *event.Event) {
			e.CompactingEnd = &event.CompactingEndPayload{Type: "full", OK: false, Err: err.Error()}
		})
		return
	}

	brief := strings.TrimSpace(resp.Content)
	if brief == "" {
		a.logger.Warn("compact.full.empty", "model", a.llm.Model())
		a.emit(event.KindCompactingEnd, func(e *event.Event) {
			e.CompactingEnd = &event.CompactingEndPayload{Type: "full", OK: false, Err: "empty summary"}
		})
		return
	}

	rebuilt := []llm.Message{
		{
			Role: llm.RoleUser,
			Content: "[CONTEXT BRIEF — the session was compacted to manage context budget. " +
				"The following summary is your working memory; the earlier transcript is gone.]\n\n" +
				brief +
				"\n\nProceed with the Next Step described above.",
		},
	}

	// Snapshot the pre-compact cumulative so the log still tells us
	// what we threw away even after FullCompact resets the session's
	// Usage.
	preIn := s.Usage.InputTokens
	preOut := s.Usage.OutputTokens
	briefTokens := resp.Usage.OutputTokens

	s.FullCompact(rebuilt, briefTokens)
	a.logger.Info("compact.full",
		"brief_bytes", len(brief),
		"summary_in_tokens", resp.Usage.InputTokens,
		"summary_out_tokens", resp.Usage.OutputTokens,
		"pre_compact_in", preIn,
		"pre_compact_out", preOut,
		"last_turn_input_after", s.LastTurnInputTokens(),
	)

	// Tell the TUI to redraw the HUD from the now-reset session
	// totals. Turn is zero (no agent turn just landed) and Cumulative
	// reflects the post-compact figure.
	a.emit(event.KindUsage, func(e *event.Event) {
		e.Usage = &event.UsagePayload{Turn: llm.Usage{}, Cumulative: s.Usage}
	})
	a.emit(event.KindCompactingEnd, func(e *event.Event) {
		e.CompactingEnd = &event.CompactingEndPayload{Type: "full", OK: true, BriefTokens: briefTokens}
	})

	// Overwrite the on-disk snapshot with the post-compact state so
	// /resume after a compact lands on the brief, not the pre-compact
	// transcript. Same session-id — the user's resume picker still sees
	// one entry, now containing the summary.
	a.persistSession()
}

// summarizationInstructions is the front-matter the summarizer sees.
// Kept terse — the brief is for an LLM to act on, not a human report.
const summarizationInstructions = `You are summarizing a conversation between a developer and their AI coding assistant. The session has grown beyond its context budget and must be compacted into a single brief that the assistant will use as its complete working memory going forward.

Produce a structured markdown brief with EXACTLY these five sections, in this order:

## Original Task
The developer's high-level goal — what they asked for at the start.

## Done So Far
What has been completed. Be specific: file paths, function names, decisions taken. Bullet list.

## Current Target
What the assistant is actively working on right now. One short paragraph.

## Next Step
The single concrete next action. Phrase as an imperative ("Implement X in path/to/file.go", "Run the tests in pkg/Y", ...).

## Key Context
File paths, identifiers, constraints, conventions, error messages, or design choices that future turns must remember. Bullet list. Omit anything the next step doesn't depend on.

Keep the brief tight — enough to continue effectively, not a transcript. Do not include preamble or commentary outside the five sections.`

// buildSummarizationPrompt renders the conversation as a single text
// block paired with the summarization instructions. We deliberately
// flatten tool_use / tool_result into plain text so the LLM treats the
// input as raw content to summarize, not as a live conversation to
// continue. Tool result content is truncated per-result to keep the
// summarizer's own input tractable.
func buildSummarizationPrompt(messages []llm.Message) string {
	var b strings.Builder
	b.WriteString(summarizationInstructions)
	b.WriteString("\n\n---\n\nCONVERSATION TO SUMMARIZE:\n\n")
	for _, m := range messages {
		renderMessageForSummary(&b, m)
	}
	return b.String()
}

// renderMessageForSummary serializes one Message into the summarizer's
// input. Multi-line content is kept; tool result Content is capped at
// summaryToolResultMaxBytes to bound the prompt size on long sessions.
func renderMessageForSummary(b *strings.Builder, m llm.Message) {
	switch m.Role {
	case llm.RoleUser:
		c := strings.TrimSpace(m.Content)
		if c == "" {
			return
		}
		b.WriteString("USER: ")
		b.WriteString(c)
		b.WriteString("\n\n")
	case llm.RoleAssistant:
		if t := strings.TrimSpace(m.Thinking); t != "" {
			b.WriteString("ASSISTANT (thinking): ")
			b.WriteString(t)
			b.WriteString("\n")
		}
		if c := strings.TrimSpace(m.Content); c != "" {
			b.WriteString("ASSISTANT: ")
			b.WriteString(c)
			b.WriteString("\n")
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(b, "TOOL CALL %s(%s)\n", tc.Name, string(tc.Input))
		}
		b.WriteString("\n")
	case llm.RoleTool:
		for _, tr := range m.ToolResults {
			tag := "TOOL RESULT"
			if tr.IsError {
				tag = "TOOL ERROR"
			}
			content := tr.Content
			if content == "" && len(tr.ContentBlocks) > 0 {
				content = llm.RenderContentBlocksAsText(tr.ContentBlocks)
			}
			if len(content) > summaryToolResultMaxBytes {
				content = content[:summaryToolResultMaxBytes] + "…(truncated)"
			}
			fmt.Fprintf(b, "%s: %s\n", tag, content)
		}
		b.WriteString("\n")
	}
}
