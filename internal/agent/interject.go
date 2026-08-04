package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/ui"
)

// ErrNoRunToInterject is returned by Interject when no run is in flight.
// The caller's correct response is to start a normal Run — there is
// nothing to interrupt, so the message is not "urgent", it is just the
// next turn.
var ErrNoRunToInterject = errors.New("agent: no run in flight to interject")

// errInterjected is the cancellation CAUSE that distinguishes "the user
// steered mid-turn" from "the user aborted the turn" and from "the
// request timed out". Both arrive at the same place — a cancelled
// context — and they must not be treated alike: an abort tears the run
// down, an interject cuts one phase short and keeps going.
//
// It is never returned to a caller and never reaches the model. The only
// thing that reads it is phaseInterjected, via context.Cause.
var errInterjected = errors.New("agent: phase cancelled by user interject")

// phase is the agent's mid-turn cancellation seam.
//
// Before STE-2 there was exactly ONE context for an entire run — created
// by the UI in startRun and threaded unchanged through the loop, the LLM
// call and every tool. Cancelling it was all-or-nothing, which is why
// the only mid-run gesture evva had was "abort the turn". A phase is a
// cancel-with-cause child scoped to the current LLM call or the current
// tool batch, so a steer can cut exactly that much and leave the turn
// standing.
//
// Exactly one phase is live at a time — the loop is single-goroutine
// between phases — but Interject is called from the UI goroutine, so the
// handle is mutex-guarded.
type phase struct {
	mu     sync.Mutex
	cancel context.CancelCauseFunc
	// armed records a steer that arrived while no phase was open — between
	// two iterations, or in the window after Run takes the running flag but
	// before the first LLM call. Without it the steer would be swallowed and
	// the user would watch the very call they tried to pre-empt run to
	// completion, which is the exact failure this wave exists to remove.
	// The next phase to open is cancelled at birth instead.
	armed bool
	// src names who steered, for the honesty note. "the user" in the solo
	// TUI; a swarm member name when a teammate's urgent mail did it. The
	// model reads this, so it must be the truth and not a placeholder — a
	// worker told "the user interrupted you" when it was actually the leader
	// would reason about the wrong party.
	src string
}

// begin opens a phase under parent and returns the phase context plus its
// closer. The closer is idempotent and safe to defer; it clears the
// handle before cancelling so a concurrent Interject can never resurrect
// a phase that has already finished.
func (p *phase) begin(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	p.mu.Lock()
	p.cancel = cancel
	armed := p.armed
	p.armed = false
	p.mu.Unlock()
	if armed {
		cancel(errInterjected)
	}
	return ctx, func() {
		p.mu.Lock()
		p.cancel = nil
		p.mu.Unlock()
		// A cause of nil leaves any cause already set by Interject intact —
		// context.CancelCauseFunc keeps the FIRST cause, so the honest
		// reason survives the defer that runs after it.
		cancel(nil)
	}
}

// interrupt cancels the live phase with the interject cause, or arms the
// next one when the loop is between phases. The bool reports which
// happened — true means something was actually cut short — and is
// reported to the UI, not acted on.
func (p *phase) interrupt(src string) bool {
	if src == "" {
		src = defaultInterjectSource
	}
	p.mu.Lock()
	cancel := p.cancel
	p.src = src
	if cancel == nil {
		p.armed = true
	}
	p.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel(errInterjected)
	return true
}

// source reports who steered, defaulting when nothing set it.
func (p *phase) source() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.src == "" {
		return defaultInterjectSource
	}
	return p.src
}

// disarm drops a stale arming. A steer can win the race against a run that
// was already finishing, and the flag must not survive into the next run —
// the message itself is still queued and lands on that run's first drain,
// which is the polite delivery it now deserves.
func (p *phase) disarm() {
	p.mu.Lock()
	p.armed = false
	p.mu.Unlock()
}

// defaultInterjectSource is who the note names when the caller did not say.
// "the user" is right for the solo TUI, which is every interject that does
// not come through the swarm.
const defaultInterjectSource = "the user"

// phaseInterjected reports whether pctx was cut by an interject rather
// than by an abort or a timeout.
//
// The parent check comes first and is not redundant: when the user aborts
// while an interject is already in flight, both causes are true of the
// tree, and abort must win — otherwise Esc would silently become "steer"
// and the turn the user tried to kill would carry on.
func phaseInterjected(parent, pctx context.Context) bool {
	if parent.Err() != nil {
		return false
	}
	return errors.Is(context.Cause(pctx), errInterjected)
}

// Interject delivers a user message into a RUNNING turn at once: it
// queues the text at interject level and cancels whatever phase is in
// flight — the LLM stream or the running tool batch — so the loop
// reaches its next drain immediately instead of at the end of a
// four-minute command.
//
// The turn is not torn down. The cancelled phase leaves an honest,
// paired record in the transcript (a truncated assistant turn, or
// interrupted tool results), the message lands as the next user turn,
// and the model keeps going with both facts in hand.
//
// Returns ErrNoRunToInterject when nothing is running — the caller
// should Run instead. Safe to call from any goroutine.
func (a *Agent) Interject(prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	if !a.running.Load() {
		return ErrNoRunToInterject
	}
	a.toolState.UserPromptQueue().EnqueueAt(toolset.SteerInterject, prompt)
	return a.cutPhase(defaultInterjectSource, len(prompt))
}

// InterjectSignal cuts the in-flight phase WITHOUT queuing anything, for a
// caller whose message reaches the agent by another channel — today the
// swarm inbox drainer, which folds urgent mail from its own durable store.
// Queuing there too would deliver the same body twice.
//
// src names who steered, and lands verbatim in the note the model reads.
func (a *Agent) InterjectSignal(src string) error {
	if !a.running.Load() {
		return ErrNoRunToInterject
	}
	return a.cutPhase(src, 0)
}

// cutPhase is the shared tail of both entry points: cancel, log, announce.
func (a *Agent) cutPhase(src string, promptBytes int) error {
	cut := a.phase.interrupt(src)
	a.logger.Info("run.interject", "cut_phase", cut, "source", src, "prompt_bytes", promptBytes)
	a.emit(event.KindInterject, func(e *event.Event) {
		e.Interject = &event.InterjectPayload{CutPhase: cut, Source: src}
	})
	return nil
}

// InterjectUserPrompt implements ui.Controller. Named apart from
// Interject so the interface reads as the pair it forms with
// EnqueueUserPrompt — the two ways a UI can hand a running agent a
// message — rather than as an unrelated verb.
func (a *Agent) InterjectUserPrompt(prompt string) error { return a.Interject(prompt) }

// PendingPrompts implements ui.Controller, mapping the internal queue
// entries onto the plain-typed view the ui package can hold without
// importing internal/toolset.
func (a *Agent) PendingPrompts() []ui.PendingPrompt {
	entries := a.pendingPrompts()
	if len(entries) == 0 {
		return nil
	}
	out := make([]ui.PendingPrompt, 0, len(entries))
	for _, e := range entries {
		out = append(out, ui.PendingPrompt{ID: e.ID, Text: e.Text, Level: e.Level.String()})
	}
	return out
}

// EnqueueUserPromptAt is the leveled form of EnqueueUserPrompt: it is
// what a UI calls when the user chose HOW to send. SteerInterject routes
// to Interject (queue + cut); SteerQueue is the historical polite path.
func (a *Agent) EnqueueUserPromptAt(level toolset.SteerLevel, prompt string) error {
	if level == toolset.SteerInterject {
		return a.Interject(prompt)
	}
	a.EnqueueUserPrompt(prompt)
	return nil
}

// pendingPrompts returns the queued-but-undelivered user messages in the
// order the model will receive them, in their internal form. PendingPrompts
// is the ui.Controller-facing projection of this.
func (a *Agent) pendingPrompts() []toolset.PendingPrompt {
	if !a.toolState.HasUserPromptQueue() {
		return nil
	}
	return a.toolState.UserPromptQueue().Pending()
}

// RevokePendingPrompt drops a queued message before it is delivered.
// False means the drain already took it — the model has it, and unsending
// is no longer possible.
func (a *Agent) RevokePendingPrompt(id string) bool {
	if !a.toolState.HasUserPromptQueue() {
		return false
	}
	return a.toolState.UserPromptQueue().Revoke(id)
}

// interjectNoteLLM is the system note appended after an interject cut an
// LLM call short. Written in the second person and stating plainly what
// was lost, because the model's next move depends on knowing that its
// half-finished plan is neither complete nor authoritative.
func interjectNoteLLM(src string) string {
	return "<system-reminder>Your response was interrupted by " + src + " before it " +
		"finished. Any text above from that turn is partial, and any tool calls it was about to " +
		"make were never issued. The message that caused the interruption follows — read it " +
		"before continuing.</system-reminder>"
}

// interjectNoteTool is the same note for the tool-execution phase. It
// names the tools by their call so the model can tell which of a
// parallel batch died and which completed.
func interjectNoteTool(src string, names []string) string {
	var b strings.Builder
	b.WriteString("<system-reminder>")
	b.WriteString(src)
	b.WriteString(" interrupted this turn while ")
	switch len(names) {
	case 0:
		b.WriteString("tools were running")
	case 1:
		b.WriteByte('`')
		b.WriteString(names[0])
		b.WriteString("` was running")
	default:
		b.WriteString("these tools were running: `")
		b.WriteString(strings.Join(names, "`, `"))
		b.WriteByte('`')
	}
	b.WriteString(". Interrupted calls are marked in their results above; any side effects they " +
		"had already caused are NOT undone. The message that caused the interruption follows — " +
		"read it before continuing.</system-reminder>")
	return b.String()
}

// interruptedToolMarker opens every synthesized interrupted tool result.
// It is the string the model reads AND the token interruptedToolNames
// matches on, so the transcript and the summary note can never disagree
// about which calls died.
const interruptedToolMarker = "[interrupted by user before completion]"

// interruptedPartialCap bounds how much of a killed tool's output is kept.
// A cancelled build can have emitted megabytes; the model needs enough to
// see where it got to, not the whole thing. The tail is kept rather than
// the head — the last lines before a kill are what say how far it got.
const interruptedPartialCap = 4096

// interruptedToolResult synthesizes the paired result for a tool whose
// phase was cancelled by an interject, or nil when this cancellation was
// not an interject (an abort, a timeout, or no cancellation at all).
//
// Only the CAUSE is examined, not whether the parent is also cancelled:
// an abort that races an interject still produces an honest paired
// result here, and the loop's own parent-first check is what decides
// that the turn ends rather than continues.
//
// Returning a non-error result on purpose. A killed tool is not a Go-level
// failure — it is an outcome the model should reason about, exactly like a
// non-zero exit code. Surfacing it as an error would abort the run, which
// is the opposite of what steering means.
func (a *Agent) interruptedToolResult(ctx context.Context, call *tools.Call, partial string) *llm.ToolResult {
	if !errors.Is(context.Cause(ctx), errInterjected) {
		return nil
	}
	content := interruptedToolMarker
	if tail := tailBytes(strings.TrimSpace(partial), interruptedPartialCap); tail != "" {
		content += "\n--- partial output before the interrupt ---\n" + tail
	}
	a.logger.Info("tool.interrupted", "name", call.Name, "tool_id", call.ID, "partial_bytes", len(partial))
	if !a.IsSubagent() {
		a.emit(event.KindToolUseResult, func(e *event.Event) {
			e.ToolUseResult = &event.ToolUseResultPayload{
				ToolID:  call.ID,
				Content: content,
				IsError: true,
			}
		})
	}
	return &llm.ToolResult{ID: call.ID, Content: content, IsError: true}
}

// tailBytes returns the last n bytes of s with a marker when it cut, and
// steps back to a rune boundary so a multi-byte character is never split
// into mojibake.
func tailBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[len(s)-n:]
	for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
		cut = cut[1:]
	}
	return "…(truncated)\n" + cut
}

// interruptedToolNames lists the tools whose results this batch marked as
// interrupted, for the system note. It reads the RESULTS rather than
// assuming the whole batch died: a fast tool in a parallel fan-out can
// finish before the cancellation reaches it, and telling the model that
// its completed read was interrupted would be a lie about the one thing
// this wave exists to be honest about.
func interruptedToolNames(calls []*tools.Call, results []*llm.ToolResult) []string {
	var out []string
	for i, c := range calls {
		if i >= len(results) || results[i] == nil {
			continue
		}
		if strings.HasPrefix(results[i].Content, interruptedToolMarker) {
			out = append(out, c.Name)
		}
	}
	return out
}

// foldInterject records the interrupt in the conversation and emits the
// UI marker. partial is the assistant text captured before the cut (empty
// for a non-streaming call, where nothing is recoverable); note is the
// system-reminder explaining what happened.
//
// The partial assistant turn is appended ONLY when non-empty: an
// assistant message with no content and no tool calls is rejected
// outright by several providers, and it would tell the model nothing
// anyway.
func (a *Agent) foldInterject(partial, note string) {
	if strings.TrimSpace(partial) != "" {
		a.session.Append(llm.Message{Role: llm.RoleAssistant, Content: partial})
	}
	a.session.Append(llm.Message{Role: llm.RoleUser, Content: note})
	a.emit(event.KindInterjectFolded, func(e *event.Event) {
		e.Interject = &event.InterjectPayload{PartialBytes: len(partial)}
	})
	a.logger.Debug("run.interject.folded", "partial_bytes", len(partial))
}
