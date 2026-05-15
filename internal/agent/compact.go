package agent

import (
	"context"
	"fmt"
	"strings"

	config "github.com/johnny1110/evva/configs"
	"github.com/johnny1110/evva/internal/agent/event"
	"github.com/johnny1110/evva/internal/constant"
	"github.com/johnny1110/evva/internal/llm"
	"github.com/johnny1110/evva/internal/session"
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

// compact runs at the top of every iteration. It compares the last
// turn's input-token count against the model's context size — when the
// ratio exceeds the auto-compact threshold the session is reshaped to
// free room. See package-level note on micro vs full escalation.
func (a *Agent) compact(ctx context.Context, s *session.Session) {
	cfg := config.Get()

	if a.IsSubagent() {
		// no compacting for subagents.
		return
	}

	modelStr := constant.Model(a.llm.Model())
	maxContextSize := constant.MODEL_CONTEXT_SIZE[modelStr]
	if maxContextSize == 0 {
		// Unknown model — we can't reason about ratio. Skip rather
		// than guess, the user keeps the full transcript.
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
	if usageRatio < cfg.AutoCompactThreshold {
		return // safe.
	}

	a.status = constant.COMPACTING

	if s.IsMicroCompacted() {
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "full", UsageRatio: usageRatio}
		})
		a.fullCompact(ctx, s)
	} else {
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
	)
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
func (a *Agent) fullCompact(ctx context.Context, s *session.Session) {
	prompt := buildSummarizationPrompt(s.GetMessages())
	summarizer := []llm.Message{{Role: llm.RoleUser, Content: prompt}}

	resp, err := a.llm.Complete(ctx, summarizer, nil)
	if err != nil {
		a.logger.Warn("compact.full.failed", "err", err)
		return
	}

	brief := strings.TrimSpace(resp.Content)
	if brief == "" {
		a.logger.Warn("compact.full.empty", "model", a.llm.Model())
		return
	}

	s.AddUsage(resp.Usage)

	rebuilt := []llm.Message{
		{
			Role: llm.RoleUser,
			Content: "[CONTEXT BRIEF — the session was compacted to manage context budget. " +
				"The following summary is your working memory; the earlier transcript is gone.]\n\n" +
				brief +
				"\n\nProceed with the Next Step described above.",
		},
	}
	s.FullCompact(rebuilt)
	a.logger.Info("compact.full",
		"brief_bytes", len(brief),
		"summary_in_tokens", resp.Usage.InputTokens,
		"summary_out_tokens", resp.Usage.OutputTokens,
	)
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
			if len(content) > summaryToolResultMaxBytes {
				content = content[:summaryToolResultMaxBytes] + "…(truncated)"
			}
			fmt.Fprintf(b, "%s: %s\n", tag, content)
		}
		b.WriteString("\n")
	}
}
