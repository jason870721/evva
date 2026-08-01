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

// The context ladder has three rungs, escalated lazily and in order of
// increasing cost and loss:
//
//   - Prune (rung 1, free): replace the body of large, old, recoverable
//     tool results with a tombstone stating what was removed and how to
//     get it back. No LLM call. RE-RUNNABLE — every pass can reclaim
//     whatever the session has grown since the last one.
//
//   - Span compaction (rung 2, one bounded LLM call): summarize only the
//     OLDEST span of the transcript and splice the brief in front of the
//     surviving tail. Partially lossy, but the recent working set stays
//     verbatim.
//
//   - Full compaction (rung 3, expensive): compress the entire
//     conversation into a single "context brief" — Original Task / Done /
//     Current Target / Next Step / Key Context — and replace Messages
//     with one User message carrying the brief plus a "proceed"
//     instruction. Falls back gracefully on failure.
//
// Escalation is measured, not assumed: each rung runs at most once per
// iteration, and the loop makes a real LLM call before compact() is
// consulted again — so the next rung fires only when the previous one
// demonstrably failed to get the prompt under threshold. A rung that
// finds nothing to do falls through immediately rather than burning an
// iteration.
//
// Naming note: rung 2 is "span", not "micro". evva shipped a
// `microCompact` for four minors that made no LLM call and elided tool
// results — i.e. a crude version of rung 1. The vocabulary is preserved
// where it is user- or disk-facing ("micro" remains an accepted alias in
// the /compact chooser; the snapshot field is still `micro_compacted`)
// and corrected everywhere else.

const (
	// summaryToolResultMaxBytes caps each tool result rendered into the
	// summarizer prompt. Bounds the summarizer's own input size when the
	// transcript has many long results.
	summaryToolResultMaxBytes = 600

	// spanCompactFraction is the share of the transcript rung 2 folds
	// into a brief. Half is the useful middle: enough to matter, little
	// enough that the model keeps the recent working set verbatim.
	spanCompactFraction = 0.5

	// spanCompactMinMessages is the transcript length below which span
	// compaction is pointless — there is no "oldest span" worth an LLM
	// call, so the ladder skips straight to full.
	spanCompactMinMessages = 8
)

// Compact is the manual entry point invoked by the TUI's /compact
// chooser. Unlike the auto path it bypasses the threshold check and
// the micro→full escalation — the user explicitly picked a kind.
//
// Refuses with ErrRunInProgress when a Run is currently driving the
// loop, same guard SwitchLLM uses; the caller (TUI) surfaces that as
// a hint rather than queueing.
//
// kind is "prune" (alias "micro"), "span", or "full"; any other value is
// an error.
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
	case "prune", "micro":
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "prune"}
		})
		a.logger.Info("compact.manual", "kind", "prune")
		a.pruneContext(a.session)
		a.status = constant.IDLE
	case "span":
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: "span"}
		})
		a.logger.Info("compact.manual", "kind", "span")
		a.spanCompact(ctx, a.session)
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
		return fmt.Errorf("agent: unknown compact kind %q (want \"prune\", \"span\" or \"full\")", kind)
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
	spanDone := s.IsSpanCompacted()
	if usageRatio < threshold {
		a.logger.Info("compact.check",
			"decision", "skip:under_threshold",
			"model", string(modelStr),
			"max_context", maxContextSize,
			"last_turn_input", currentUsage,
			"usage_ratio", usageRatio,
			"threshold", threshold,
			"span_done", spanDone,
		)
		return // safe.
	}

	a.status = constant.COMPACTING

	decide := func(kind string) {
		a.logger.Info("compact.check",
			"decision", "trigger:"+kind,
			"model", string(modelStr),
			"max_context", maxContextSize,
			"last_turn_input", currentUsage,
			"usage_ratio", usageRatio,
			"threshold", threshold,
			"span_done", spanDone,
		)
		a.emit(event.KindCompacting, func(e *event.Event) {
			e.Compacting = &event.CompactingPayload{Type: kind, UsageRatio: usageRatio}
		})
	}

	// Rung 1 — prune. Free and recoverable, so it is always tried first
	// and is never gated by a spent-once flag. Returning here is not
	// giving up: the iteration continues, makes a real LLM call, and the
	// NEXT compact.check measures the actual post-prune prompt. That is
	// what makes the escalation evidence-based instead of speculative.
	if plan := a.planPrune(s); !plan.Empty() {
		decide("prune")
		a.applyPrune(s, plan)
		return
	}

	// Rung 2 — span compaction. Nothing left to prune for free, so pay
	// for one bounded summarization of the oldest span.
	if !spanDone && cfg.GetSpanEnabled() {
		decide("span")
		a.spanCompact(ctx, s)
		return
	}

	// Rung 3 — full compaction.
	decide("full")
	a.fullCompact(ctx, s)
}

// prunePolicy resolves the operator's configured prune tunables.
func (a *Agent) prunePolicy() session.PrunePolicy {
	return session.PrunePolicy{
		MinBytes:          a.cfg.GetPruneMinBytes(),
		KeepRecentTurns:   a.cfg.GetPruneKeepTurns(),
		KeepRecentResults: a.cfg.GetPruneKeepResults(),
	}
}

// planPrune builds the prune plan for the current history. Split from
// applyPrune so the ladder can ask "is there free work available?"
// without committing to it.
func (a *Agent) planPrune(s *session.Session) session.PrunePlan {
	if !a.cfg.GetPruneEnabled() {
		return session.PrunePlan{}
	}
	return session.PlanPrune(session.BuildLedger(s.GetMessages(), "", s.PinSet()), a.prunePolicy())
}

// applyPrune installs a plan's tombstones. No LLM call, and no ladder
// state is touched — pruning stays repeatable.
func (a *Agent) applyPrune(s *session.Session, plan session.PrunePlan) {
	s.ReplaceMessages(session.ApplyPrune(s.GetMessages(), plan))
	a.logger.Info("compact.prune",
		"tombstoned_results", plan.Count(),
		"freed_bytes", plan.Bytes,
		"last_turn_input_before", s.LastTurnInputTokens(),
	)
	a.emit(event.KindCompactingEnd, func(e *event.Event) {
		e.CompactingEnd = &event.CompactingEndPayload{Type: "prune", OK: true}
	})
}

// pruneContext is the manual /compact entry point for rung 1. Reports
// whether anything was tombstoned.
func (a *Agent) pruneContext(s *session.Session) bool {
	plan := a.planPrune(s)
	if plan.Empty() {
		a.logger.Info("compact.prune.skipped", "reason", "nothing_prunable")
		a.emit(event.KindCompactingEnd, func(e *event.Event) {
			e.CompactingEnd = &event.CompactingEndPayload{Type: "prune", OK: true}
		})
		return false
	}
	a.applyPrune(s, plan)
	return true
}

// spanBoundary picks the index where the folded span ends and the
// verbatim tail begins, targeting frac of the transcript.
//
// The boundary MUST NOT land on a RoleTool message: a tool result is only
// well-formed when the assistant tool_use that requested it is still
// present, and everything before the boundary is about to be deleted.
// Requiring msgs[end] to be a non-tool message is sufficient — in a
// well-formed transcript an assistant turn carrying ToolCalls is always
// immediately followed by the RoleTool message answering them, so any
// non-tool message at `end` proves the preceding calls were all answered.
//
// Returns 0 when no safe boundary leaves a useful tail, which the caller
// reads as "skip this rung".
func spanBoundary(msgs []llm.Message, frac float64) int {
	if len(msgs) < spanCompactMinMessages {
		return 0
	}
	// Leave at least two messages standing, so the tail is a real
	// working set rather than a single dangling turn.
	maxEnd := len(msgs) - 2
	target := int(float64(len(msgs)) * frac)
	if target > maxEnd {
		target = maxEnd
	}
	for end := target; end > 0; end-- {
		if msgs[end].Role != llm.RoleTool {
			return end
		}
	}
	return 0
}

// spanCompact folds the oldest span of the transcript into a brief and
// splices it in front of the surviving tail. One LLM call, bounded by
// construction: the summarizer sees only the span, and each tool result
// inside it is capped at summaryToolResultMaxBytes.
//
// Failure is non-fatal and non-advancing — a transport error leaves the
// session untouched and the flag unset, so the next iteration retries
// this rung rather than skipping to full compaction on a transient.
//
// When no safe span boundary exists the rung escalates immediately
// rather than marking itself spent for nothing.
func (a *Agent) spanCompact(ctx context.Context, s *session.Session) {
	msgs := s.GetMessages()
	end := spanBoundary(msgs, spanCompactFraction)
	if end == 0 {
		a.logger.Info("compact.span.skipped", "reason", "no_safe_boundary", "messages", len(msgs))
		a.fullCompact(ctx, s)
		return
	}

	prompt := buildSpanSummarizationPrompt(msgs[:end])
	resp, err := a.llm.Complete(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		a.logger.Warn("compact.span.failed", "err", err)
		a.emit(event.KindCompactingEnd, func(e *event.Event) {
			e.CompactingEnd = &event.CompactingEndPayload{Type: "span", OK: false, Err: err.Error()}
		})
		return
	}
	brief := strings.TrimSpace(resp.Content)
	if brief == "" {
		a.logger.Warn("compact.span.empty", "model", a.llm.Model())
		a.emit(event.KindCompactingEnd, func(e *event.Event) {
			e.CompactingEnd = &event.CompactingEndPayload{Type: "span", OK: false, Err: "empty summary"}
		})
		return
	}

	header := "[EARLIER CONTEXT FOLDED — the first part of this session was summarized to manage " +
		"context budget. Everything after this block is the verbatim recent transcript.]\n\n" + brief
	if pinned := session.RenderPinned(msgs[:end], session.BuildLedger(msgs[:end], "", s.PinSet())); pinned != "" {
		header += "\n\n" + pinned
	}

	tail := msgs[end:]
	var rebuilt []llm.Message
	if tail[0].Role == llm.RoleUser {
		// Merge into the surviving user message rather than emitting a
		// second one: back-to-back user messages are accepted by some
		// providers and rejected by others, and there is no reason to
		// find out which at runtime.
		merged := tail[0]
		merged.Content = header + "\n\n" + merged.Content
		rebuilt = append([]llm.Message{merged}, tail[1:]...)
	} else {
		rebuilt = append([]llm.Message{{Role: llm.RoleUser, Content: header}}, tail...)
	}

	// AddUsage, not RecordTurn: the summarizer's InputTokens describes
	// the span we just folded, not the size of the prompt the next turn
	// will send. Feeding it to the compaction gauge would re-trigger the
	// ladder immediately.
	s.AddUsage(resp.Usage)
	s.SpanCompact(rebuilt)
	a.logger.Info("compact.span",
		"folded_messages", end,
		"kept_messages", len(tail),
		"brief_bytes", len(brief),
		"summary_in_tokens", resp.Usage.InputTokens,
		"summary_out_tokens", resp.Usage.OutputTokens,
	)
	a.emit(event.KindCompactingEnd, func(e *event.Event) {
		e.CompactingEnd = &event.CompactingEndPayload{Type: "span", OK: true, BriefTokens: resp.Usage.OutputTokens}
	})
	a.persistSession()
}

// spanSummarizationInstructions differs from the full-compaction brief in
// one load-bearing way: the transcript after the span is NOT gone, so the
// summary must not claim to be complete working memory. It is a preamble
// to still-present detail.
const spanSummarizationInstructions = `You are compressing the EARLY portion of a developer's session with their AI coding assistant. The recent portion of the conversation is still present verbatim and follows your summary — you are writing a preamble to it, not replacing it.

Produce a tight markdown brief with these three sections:

## Original Task
What the developer asked for at the start.

## Established So Far
Decisions taken, files touched, approaches ruled out. Bullet list, specific: paths, identifiers, error messages.

## Still Relevant
Constraints, conventions, or context from this early span that later turns depend on. Bullet list. Omit anything the recent transcript already makes obvious.

Do not describe what the assistant is currently doing — the verbatim transcript that follows covers that. No preamble or commentary outside the three sections.`

// buildSpanSummarizationPrompt renders the folded span for the
// summarizer, reusing the same flattening as full compaction.
func buildSpanSummarizationPrompt(span []llm.Message) string {
	var b strings.Builder
	b.WriteString(spanSummarizationInstructions)
	b.WriteString("\n\n---\n\nEARLY CONVERSATION SPAN TO SUMMARIZE:\n\n")
	for _, m := range span {
		renderMessageForSummary(&b, m)
	}
	return b.String()
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

	body := "[CONTEXT BRIEF — the session was compacted to manage context budget. " +
		"The following summary is your working memory; the earlier transcript is gone.]\n\n" + brief
	// Pins are the operator's explicit "do not lose this", so they
	// survive even the rung that discards everything else. Re-injected
	// verbatim BEFORE the proceed instruction so the model reads them as
	// standing context rather than as the next action.
	msgs := s.GetMessages()
	if pinned := session.RenderPinned(msgs, session.BuildLedger(msgs, "", s.PinSet())); pinned != "" {
		body += "\n\n" + pinned
	}
	body += "\n\nProceed with the Next Step described above."
	rebuilt := []llm.Message{{Role: llm.RoleUser, Content: body}}

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
