package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/toolset"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/tools"
)

// steerLLM is a stub client whose first call streams a few chunks and then
// parks on ctx.Done() — the shape of a model mid-way through a long wrong
// answer. Later calls return whatever the script says, so the loop can be
// driven to a terminal turn after the interject.
type steerLLM struct {
	mu     sync.Mutex
	calls  int
	chunks []string
	// after is returned by every call past the first.
	after llm.Response
	// firstReturnsWhole makes call #1 return `after` immediately instead of
	// blocking — used to reach the tool phase, which is where the steer is
	// aimed in the STE-3 tests.
	firstReturnsWhole bool
	// sawSink records whether the loop supplied a real chunk sink.
	sawSink bool
	// streaming is signalled once the first call has emitted its chunks and
	// is about to park — the only moment at which a steer is genuinely
	// "mid-answer". Polling `running` instead would usually land during
	// Run's preamble, where nothing is in flight to cut.
	streaming chan struct{}
}

func (s *steerLLM) Name() string               { return "steer" }
func (s *steerLLM) Model() string              { return "steer-model" }
func (s *steerLLM) SupportsDeferLoading() bool { return false }
func (s *steerLLM) Apply(...llm.Option)        {}

func (s *steerLLM) setAfter(r llm.Response) {
	s.mu.Lock()
	s.after = r
	s.mu.Unlock()
}

func (s *steerLLM) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func (s *steerLLM) sawRealSink() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sawSink
}

func (s *steerLLM) Complete(ctx context.Context, _ []llm.Message, _ []tools.Tool) (llm.Response, error) {
	return s.Stream(ctx, nil, nil, llm.DiscardChunks)
}

func (s *steerLLM) Stream(ctx context.Context, _ []llm.Message, _ []tools.Tool, sink llm.ChunkSink) (llm.Response, error) {
	s.mu.Lock()
	s.calls++
	n := s.calls
	whole := s.firstReturnsWhole
	after := s.after
	chunks := s.chunks
	if _, ok := sink.(*chunkAdapter); ok {
		s.sawSink = true
	}
	s.mu.Unlock()

	if n > 1 || whole {
		return after, nil
	}
	for _, c := range chunks {
		sink.OnChunk(llm.Chunk{Kind: llm.ChunkText, Delta: c})
	}
	if s.streaming != nil {
		close(s.streaming)
	}
	<-ctx.Done()
	return llm.Response{}, llm.ErrInterrupted
}

// blockingTool parks until its context is cancelled, then reports whatever
// partial output it had managed to produce — a stand-in for `bash` running
// a long command.
type blockingTool struct {
	started chan struct{}
	partial string
}

func (b *blockingTool) Name() string            { return "slow" }
func (b *blockingTool) Description() string     { return "blocks until cancelled" }
func (b *blockingTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (b *blockingTool) Execute(ctx context.Context, _ *slog.Logger, _ json.RawMessage) (tools.Result, error) {
	select {
	case b.started <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return tools.Result{IsError: true, Content: b.partial}, ctx.Err()
}

// fastTool completes immediately — the other half of a parallel fan-out,
// there to prove the interrupted-name summary reads results rather than
// assuming the whole batch died.
type fastTool struct{}

func (fastTool) Name() string            { return "quick" }
func (fastTool) Description() string     { return "returns at once" }
func (fastTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (fastTool) Execute(context.Context, *slog.Logger, json.RawMessage) (tools.Result, error) {
	return tools.Result{Content: "done"}, nil
}

func newSteerAgent(client llm.Client) *Agent {
	a := newTestAgent(client)
	a.toolState = toolset.NewToolState()
	a.active = map[string]tools.Tool{}
	a.maxIters.Store(6)
	return a
}

// userTurns extracts the user-role message bodies, which is where both the
// steer and the honesty note land.
func userTurns(a *Agent) []string {
	var out []string
	for _, m := range a.session.GetMessages() {
		if m.Role == llm.RoleUser {
			out = append(out, m.Content)
		}
	}
	return out
}

// interjectOn steers as soon as ready fires, off the test goroutine so the
// Run under test keeps driving the loop.
func interjectOn(t *testing.T, a *Agent, ready <-chan struct{}, text string) {
	t.Helper()
	go func() {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			return
		}
		_ = a.Interject(text)
	}()
}

// TestInterjectDuringStreamKeepsPartialAndContinues is STE-2's acceptance
// criterion: steering mid-answer truncates the turn, preserves the text the
// user already watched arrive, records why, delivers the message — and does
// NOT end the run.
func TestInterjectDuringStreamKeepsPartialAndContinues(t *testing.T) {
	stub := &steerLLM{
		chunks:    []string{"I will now ", "delete everything"},
		after:     llm.Response{Content: "understood, stopping"},
		streaming: make(chan struct{}),
	}
	a := newSteerAgent(stub)
	a.profile.Stream = true

	interjectOn(t, a, stub.streaming, "no! tests live in ./scripts")

	out, err := a.Run(context.Background(), "initial")
	if err != nil {
		t.Fatalf("Run returned %v — an interject must not end the turn", err)
	}
	if out != "understood, stopping" {
		t.Errorf("final content = %q, want the post-steer answer", out)
	}
	if !stub.sawRealSink() {
		t.Error("streaming profile did not receive a real chunk sink")
	}

	msgs := a.session.GetMessages()
	var partial string
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && strings.HasPrefix(m.Content, "I will now") {
			partial = m.Content
		}
	}
	if partial != "I will now delete everything" {
		t.Errorf("partial assistant turn = %q, want the streamed prefix preserved", partial)
	}

	turns := userTurns(a)
	if len(turns) < 3 {
		t.Fatalf("user turns = %v, want prompt + note + steer", turns)
	}
	if !strings.Contains(turns[len(turns)-2], "interrupted by the user") {
		t.Errorf("note turn = %q, want the honesty note", turns[len(turns)-2])
	}
	if turns[len(turns)-1] != "no! tests live in ./scripts" {
		t.Errorf("last user turn = %q, want the steer text", turns[len(turns)-1])
	}
	// Order matters: the note must precede the steer so the model reads
	// "you were cut off" before it reads what to do instead.
	noteIdx, steerIdx := -1, -1
	for i, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		if strings.Contains(m.Content, "interrupted by the user") {
			noteIdx = i
		}
		if m.Content == "no! tests live in ./scripts" {
			steerIdx = i
		}
	}
	if noteIdx < 0 || steerIdx < 0 || noteIdx > steerIdx {
		t.Errorf("note at %d must come before steer at %d", noteIdx, steerIdx)
	}
}

// TestInterjectWithoutPartialAppendsNoEmptyAssistantTurn covers the
// non-streaming provider: nothing is recoverable, and an empty assistant
// message would be rejected by several providers.
func TestInterjectWithoutPartialAppendsNoEmptyAssistantTurn(t *testing.T) {
	stub := &steerLLM{after: llm.Response{Content: "ok"}, streaming: make(chan struct{})}
	a := newSteerAgent(stub)
	a.profile.Stream = false

	interjectOn(t, a, stub.streaming, "stop")

	if _, err := a.Run(context.Background(), "initial"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, m := range a.session.GetMessages() {
		if m.Role == llm.RoleAssistant && m.Content == "" && len(m.ToolCalls) == 0 {
			t.Fatal("appended an empty assistant turn — providers reject those")
		}
	}
}

// TestInterjectBetweenPhasesDeliversWithoutAFalseNote is the other half of
// the honesty contract. A steer that lands while nothing is in flight — the
// window between Run taking the running flag and the first LLM call — has
// interrupted nothing, so it must arrive as an ordinary drained turn with
// NO "you were interrupted" note attached. The note is a factual claim
// about the transcript, not a decoration on the feature.
func TestInterjectBetweenPhasesDeliversWithoutAFalseNote(t *testing.T) {
	stub := &steerLLM{firstReturnsWhole: true, after: llm.Response{Content: "ok"}}
	a := newSteerAgent(stub)

	// Steer before Run is even called: running is false, so this is
	// rejected — and the message must NOT be left queued.
	if err := a.Interject("too early"); !errors.Is(err, ErrNoRunToInterject) {
		t.Fatalf("pre-run interject = %v", err)
	}
	// Now queue one the polite way and let the loop take it.
	a.EnqueueUserPrompt("mid-flight thought")

	if _, err := a.Run(context.Background(), "initial"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	turns := userTurns(a)
	for _, tr := range turns {
		if strings.Contains(tr, "interrupted") {
			t.Errorf("nothing was interrupted, but history claims it was: %q", tr)
		}
		if tr == "too early" {
			t.Error("a rejected interject leaked into the conversation")
		}
	}
	if len(turns) != 2 || turns[1] != "mid-flight thought" {
		t.Errorf("user turns = %v, want [initial, mid-flight thought]", turns)
	}
}

// TestArmedSteerIsClearedAtTheIterationTop pins the placement of the
// disarm. Arming exists only for the window AFTER a boundary's drains; an
// arming that survived into the next iteration would cancel an LLM call the
// user never saw start.
func TestArmedSteerIsClearedAtTheIterationTop(t *testing.T) {
	stub := &steerLLM{firstReturnsWhole: true, after: llm.Response{Content: "done"}}
	a := newSteerAgent(stub)

	// Arm directly, as an interject between phases would.
	a.phase.interrupt("the user")
	a.toolState.UserPromptQueue().EnqueueAt(toolset.SteerInterject, "steer")

	out, err := a.Run(context.Background(), "initial")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "done" {
		t.Errorf("content = %q — the stale arming cut a call it should not have", out)
	}
	if stub.callCount() != 1 {
		t.Errorf("llm calls = %d, want 1", stub.callCount())
	}
	for _, tr := range userTurns(a) {
		if strings.Contains(tr, "interrupted") {
			t.Errorf("false interrupt note in history: %q", tr)
		}
	}
}

// TestInterjectDuringToolPairsResultAndContinues is STE-3's acceptance
// criterion: the tool dies, its result is still paired with the tool_use
// that asked for it, the content says so, and the run survives.
func TestInterjectDuringToolPairsResultAndContinues(t *testing.T) {
	slow := &blockingTool{started: make(chan struct{}, 1), partial: "compiling...\nstep 3 of 40"}
	stub := &steerLLM{
		firstReturnsWhole: true,
		after: llm.Response{ToolCalls: []*tools.Call{
			{ID: "call-1", Name: "slow", Input: json.RawMessage(`{}`)},
		}},
	}
	a := newSteerAgent(stub)
	a.active["slow"] = slow

	go func() {
		<-slow.started
		// Second call onward returns a terminal turn so the loop can end.
		stub.setAfter(llm.Response{Content: "stopped"})
		_ = a.Interject("wrong build target")
	}()

	out, err := a.Run(context.Background(), "build it")
	if err != nil {
		t.Fatalf("Run returned %v — a tool interject must not crush the run", err)
	}
	if out != "stopped" {
		t.Errorf("final content = %q", out)
	}

	var toolMsg *llm.Message
	for i, m := range a.session.GetMessages() {
		if m.Role == llm.RoleTool {
			toolMsg = &a.session.GetMessages()[i]
		}
	}
	if toolMsg == nil || len(toolMsg.ToolResults) != 1 {
		t.Fatal("no paired tool-result message for the cancelled call")
	}
	res := toolMsg.ToolResults[0]
	if res.ID != "call-1" {
		t.Errorf("result id = %q, want call-1 — pairing is what keeps providers from 400ing", res.ID)
	}
	if !strings.HasPrefix(res.Content, interruptedToolMarker) {
		t.Errorf("result content = %q, want the interrupted marker", res.Content)
	}
	if !strings.Contains(res.Content, "step 3 of 40") {
		t.Errorf("result dropped the partial output: %q", res.Content)
	}
	if !res.IsError {
		t.Error("interrupted result should be flagged IsError")
	}

	turns := userTurns(a)
	var note string
	for _, tt := range turns {
		if strings.Contains(tt, "interrupted this turn") {
			note = tt
		}
	}
	if !strings.Contains(note, "`slow`") {
		t.Errorf("note = %q, want it to name the killed tool", note)
	}
	if turns[len(turns)-1] != "wrong build target" {
		t.Errorf("last user turn = %q, want the steer", turns[len(turns)-1])
	}
}

// TestInterruptedToolNamesReadsResults proves the note names only the calls
// that actually died — a fast tool in a parallel batch that finished before
// the cancellation arrived must not be reported as interrupted.
func TestInterruptedToolNamesReadsResults(t *testing.T) {
	calls := []*tools.Call{
		{ID: "a", Name: "quick"},
		{ID: "b", Name: "slow"},
	}
	results := []*llm.ToolResult{
		{ID: "a", Content: "done"},
		{ID: "b", Content: interruptedToolMarker + "\npartial"},
	}
	got := interruptedToolNames(calls, results)
	if len(got) != 1 || got[0] != "slow" {
		t.Errorf("interruptedToolNames = %v, want [slow]", got)
	}
	if n := interruptedToolNames(calls, nil); n != nil {
		t.Errorf("short results slice = %v, want nil", n)
	}
}

// TestAbortStillAborts is the guard that keeps Esc meaning Esc. Cancelling
// the run's own context must end the turn even though the same mechanism
// (a cancelled context) now also carries steering.
func TestAbortStillAborts(t *testing.T) {
	stub := &steerLLM{
		chunks:    []string{"working"},
		after:     llm.Response{Content: "never reached"},
		streaming: make(chan struct{}),
	}
	a := newSteerAgent(stub)
	a.profile.Stream = true

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-stub.streaming
		cancel()
	}()

	_, err := a.Run(ctx, "go")
	if !errors.Is(err, llm.ErrInterrupted) {
		t.Fatalf("Run err = %v, want llm.ErrInterrupted", err)
	}
	if stub.callCount() > 1 {
		t.Errorf("llm called %d times — an abort must not resume the loop", stub.calls)
	}
}

// TestPhaseInterjectedPrefersParent locks the precedence rule: when an abort
// and a steer race, abort wins. Without this, Esc during an in-flight
// interject would silently turn into "keep going".
func TestPhaseInterjectedPrefersParent(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	var p phase
	pctx, done := p.begin(parent)
	if !p.interrupt("the user") {
		t.Fatal("interrupt() = false with a live phase")
	}
	if !phaseInterjected(parent, pctx) {
		t.Fatal("steer alone should read as interjected")
	}
	cancelParent()
	if phaseInterjected(parent, pctx) {
		t.Error("with the parent cancelled, abort must win over the steer")
	}
	done()
}

// TestPhaseCloseIsIdempotentAndClearsHandle guards against a finished phase
// being resurrected by a late steer.
func TestPhaseCloseIsIdempotentAndClearsHandle(t *testing.T) {
	var p phase
	if p.interrupt("the user") {
		t.Error("interrupt() = true with no phase open")
	}
	// That interrupt armed the next phase. The loop clears the arming at
	// the top of each iteration; do the same here so this test measures
	// close-idempotency and nothing else.
	p.disarm()
	ctx, done := p.begin(context.Background())
	done()
	done()
	if p.interrupt("the user") {
		t.Error("interrupt() = true after the phase closed")
	}
	if !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Errorf("closed phase cause = %v, want context.Canceled", context.Cause(ctx))
	}
}

// TestPhaseCloseKeepsTheFirstCause pins the reason begin's closer passes
// nil: the deferred close runs AFTER the steer, and must not overwrite the
// cause the loop is about to read.
func TestPhaseCloseKeepsTheFirstCause(t *testing.T) {
	var p phase
	ctx, done := p.begin(context.Background())
	p.interrupt("the user")
	done()
	if !errors.Is(context.Cause(ctx), errInterjected) {
		t.Errorf("cause after close = %v, want errInterjected", context.Cause(ctx))
	}
}

// TestInterjectWithoutRunIsRejected: nothing to interrupt means this is
// just the next prompt, and the caller should Run.
func TestInterjectWithoutRunIsRejected(t *testing.T) {
	a := newSteerAgent(&steerLLM{})
	if err := a.Interject("hello"); !errors.Is(err, ErrNoRunToInterject) {
		t.Errorf("Interject while idle = %v, want ErrNoRunToInterject", err)
	}
	if a.toolState.UserPromptQueue().Len() != 0 {
		t.Error("a rejected interject must not leave the message queued")
	}
	// Blank input is a no-op, not an error — an accidental keypress should
	// not be reported as a failure.
	if err := a.Interject("   "); err != nil {
		t.Errorf("blank Interject = %v, want nil", err)
	}
}

// TestEnqueueUserPromptAtRoutesByLevel covers the leveled controller entry.
func TestEnqueueUserPromptAtRoutesByLevel(t *testing.T) {
	a := newSteerAgent(&steerLLM{})
	if err := a.EnqueueUserPromptAt(toolset.SteerQueue, "polite"); err != nil {
		t.Fatalf("queue level: %v", err)
	}
	if got := a.pendingPrompts(); len(got) != 1 || got[0].Level != toolset.SteerQueue {
		t.Fatalf("pendingPrompts = %+v", got)
	}
	// The ui.Controller projection must agree, level name included — the
	// /queue panel renders that string verbatim.
	if got := a.PendingPrompts(); len(got) != 1 || got[0].Level != "queue" || got[0].Text != "polite" {
		t.Fatalf("PendingPrompts = %+v", got)
	}
	// Interject level with no run in flight surfaces the same rejection as
	// Interject itself rather than silently downgrading to a queue.
	if err := a.EnqueueUserPromptAt(toolset.SteerInterject, "urgent"); !errors.Is(err, ErrNoRunToInterject) {
		t.Errorf("interject level while idle = %v, want ErrNoRunToInterject", err)
	}
	id := a.pendingPrompts()[0].ID
	if !a.RevokePendingPrompt(id) {
		t.Error("RevokePendingPrompt = false for a live entry")
	}
	if len(a.pendingPrompts()) != 0 {
		t.Error("revoked entry still pending")
	}
}

// TestTailBytes covers the partial-output cap, including the rune boundary
// a naive slice would split into mojibake.
func TestTailBytes(t *testing.T) {
	if got := tailBytes("short", 100); got != "short" {
		t.Errorf("under cap = %q", got)
	}
	if got := tailBytes("", 4); got != "" {
		t.Errorf("empty = %q", got)
	}
	got := tailBytes("abcdefghij", 4)
	if !strings.HasSuffix(got, "ghij") || !strings.Contains(got, "truncated") {
		t.Errorf("over cap = %q, want a marked tail", got)
	}
	// "日" is 3 bytes; a 4-byte tail of "あ日" lands mid-rune.
	multi := tailBytes("あ日", 4)
	if !strings.HasSuffix(multi, "日") {
		t.Errorf("multibyte tail = %q, want a clean rune boundary", multi)
	}
	for _, r := range multi {
		if r == '�' {
			t.Fatalf("tail contains a replacement rune: %q", multi)
		}
	}
}
