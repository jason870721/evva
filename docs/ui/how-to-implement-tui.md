# How to implement a TUI for evva

A contributor's guide to building a terminal UI (or any frontend) that
drives the evva agent, and to making it selectable at startup with
`evva -tui <name>`.

evva's core runtime never imports a concrete UI. The agent talks to the
outside world through **two narrow interfaces**, both in `pkg/ui` and
`pkg/event`:

- The agent **pushes** events into your UI through `event.Sink`.
- Your UI **drives** the agent through `ui.Controller`.

If your type satisfies `ui.UI` (which is just `event.Sink` + two methods),
the host can hand it the agent and hand the agent back to it — and you have
a working frontend. The bundled `pkg/ui/bubbletea` TUI is *one*
implementation of that contract; nothing about the agent is special-cased
for it. A second TUI, a web frontend, or a JSON-over-websocket bridge are
all the same shape.

This guide is for developers who want to add a new UI under `pkg/ui/`. It
assumes Go familiarity but **no** prior bubbletea knowledge.

> **See also:** [`docs/extending.md`](../extending.md) has the short
> version (one "Custom UI" section among many extension points). This
> document is the deep, dedicated walkthrough.

---

## Table of contents

1. [The big picture](#1-the-big-picture)
2. [The contract: three interfaces](#2-the-contract-three-interfaces)
3. [The event stream](#3-the-event-stream)
4. [The wiring sequence (host responsibility)](#4-the-wiring-sequence-host-responsibility)
5. [Build a minimal UI from scratch](#5-build-a-minimal-ui-from-scratch)
6. [Anatomy of the reference bubbletea TUI](#6-anatomy-of-the-reference-bubbletea-tui)
7. [Building something stylish: the theme system](#7-building-something-stylish-the-theme-system)
8. [Switching UIs at startup: `evva -tui <name>`](#8-switching-uis-at-startup-evva--tui-name)
9. [Checklist and gotchas](#9-checklist-and-gotchas)
10. [Reference: key files](#10-reference-key-files)

---

## 1. The big picture

```
            ┌─────────────────────────────────────────────┐
            │                  your UI                     │
            │  (implements ui.UI = event.Sink + Attach+Run)│
            └───────────────▲───────────────────┬─────────┘
                            │                    │
             agent → UI     │                    │   UI → agent
             event.Event    │                    │   ui.Controller
             (Emit)         │                    │   (Run, Respond*, …)
                            │                    ▼
            ┌───────────────┴───────────────────────────────┐
            │                  pkg/agent                     │
            │   ReAct loop · tools · permissions · session   │
            └────────────────────────────────────────────────┘
```

Two arrows, two interfaces:

| Direction | Interface | What flows |
|-----------|-----------|------------|
| agent → UI | `event.Sink` (one method, `Emit`) | a stream of `event.Event` values — text, tool calls, token usage, store updates, approval/question requests |
| UI → agent | `ui.Controller` (~30 methods) | commands and read-models — `Run` a turn, read the transcript, switch model/profile, answer permission prompts |

The agent layer depends only on `pkg/event` and `pkg/ui` interface types.
It has *no* import of `pkg/ui/bubbletea`. Swapping UIs is a host concern
(`cmd/evva/main.go`), not a runtime concern.

Everything your UI needs is reachable from `pkg/*` — no `internal/*`
imports. This is compiler-enforced: `pkg/ui/controller_compile_test.go`
implements the entire `Controller` surface using only public packages, and
the bundled `examples/full-host/` is a *separate Go module* that builds the
flagship experience from `pkg/*` alone. If those compile, your separate-module
UI can too.

---

## 2. The contract: three interfaces

### 2.1 `ui.UI` — what you implement

From `pkg/ui/ui.go`:

```go
type UI interface {
    event.Sink // Emit(event.Event)

    // Attach hands the UI the controller it uses to drive the agent.
    // Called by the host once, between agent construction and Run.
    Attach(Controller)

    // Run starts the UI's input/render loop and blocks until exit.
    Run(ctx context.Context) error
}
```

Three methods total (`Emit` comes from the embedded `event.Sink`):

- **`Emit(event.Event)`** — the agent calls this from its loop goroutine
  every time something happens. **It must not block.** A render loop should
  hand the event off (channel send, `tea.Program.Send`, ring buffer) and
  return immediately. A slow `Emit` stalls the agent.
- **`Attach(Controller)`** — the host calls this once, after constructing
  the agent and before `Run`. Stash the controller; it's how you send
  commands back.
- **`Run(ctx) error`** — your main loop. Blocks until the user quits, the
  context is cancelled, or a fatal error occurs. This is the host's main
  blocking call.

### 2.2 `event.Sink` — the consumer side

From `pkg/event/sink.go`:

```go
type Sink interface {
    Emit(Event)
}
```

The package also ships useful adapters you'll likely lean on:

- **`event.Discard`** — a no-op sink. Good default for tests.
- **`event.SinkFunc(func(Event))`** — adapts a plain function to a sink.
- **`event.Multi{Sinks: []Sink{...}}`** — fan one event out to several
  sinks (e.g. your UI *and* a structured logger). Fans out in order;
  a slow sink blocks the rest (backpressure beats event loss).
- **`event.BubbleUp{Parent, ParentID}`** — used internally so subagent
  events surface in the parent's stream tagged with `ParentID`. You read
  `Event.ParentID` to route nested events into a sub-panel.

**Concurrency contract (read this twice).** The agent serializes `Emit`
calls *per agent* behind a mutex — even when tools run in parallel, one
agent's events arrive one at a time. But a sink shared across agents (a
global logger, or a parent sink reached through `BubbleUp`) must handle
concurrent `Emit` itself. And `Emit` should be fast; buffer internally if
you need disk/network I/O.

### 2.3 `ui.Controller` — what you drive

The controller is the agent's public API (~30 methods), implemented by
`*agent.Agent` (you get it via `ag.Controller()`). It splits into
**commands** and **read-models**. You don't have to use all of it — a
minimal UI uses three methods; a full TUI uses most. Highlights (full list
in `pkg/ui/ui.go`):

**Driving a turn**

```go
Run(ctx context.Context, prompt string) (string, error)  // one user turn
Continue(ctx context.Context) (string, error)            // resume after iter-limit pause
EnqueueUserPrompt(prompt string)                          // queue a prompt typed mid-run
```

**Read-models (for rendering — cheap, call every frame)**

```go
Messages() []llm.Message            // the live transcript (replay on resume)
Usage() llm.Usage                   // cumulative token usage
LastTurnInputTokens() int           // "how full is the prompt" gauge
TodoStore() *todo.TodoStore         // todo panel backing store (never nil)
DaemonState() *daemon.DaemonState   // subagent/bg-task/monitor chips (nil until first daemon)
Model() string                      // current model id
AgentID() string                    // session id for headers
ProfileName() string                // active persona ("evva", "nono", …)
PermissionModeName() string         // "default"|"accept_edits"|"plan"|"bypass"|"auto"
Effort() string                     // "low"|"medium"|"high"|"ultra"
```

**Answering blocked tool goroutines (critical — see §6.9)**

```go
RespondPermission(id string, decision PermissionDecision) error
RespondQuestion(id string, resp QuestionResponse) error
```

**Runtime mutations (the slash-command / overlay surface)**

```go
SwitchLLM(provider constant.LLMProvider, model constant.Model) error
SwitchProfile(name string) error
SetMaxIterations(int)
SetEffort(level string) error
Compact(ctx context.Context, kind string) error  // "micro" | "full"
CyclePermissionMode() string                       // Shift+Tab order
ResumeSession(id string) error
ListSessions() ([]SessionInfo, []string)
ListMainProfiles() []ProfileChoice
Skills() []Skill                                   // for the slash catalog
Logger() *slog.Logger
```

All parameter and return types (`PermissionDecision`, `QuestionResponse`,
`SessionInfo`, `ProfileChoice`, `Skill`, plus `llm.Usage`, `llm.Message`,
`todo.TodoStore`, `daemon.DaemonState`, `constant.*`) are public. You never
import an evva internal to satisfy or call `Controller`.

---

## 3. The event stream

`event.Event` (in `pkg/event/event.go`) is a **discriminated union**: every
event has a `Kind` and exactly one non-nil typed payload field. No
`interface{}` assertions, no reflection — you switch on `Kind` and read the
matching field, or use the ergonomic `Event.Payload()` helper:

```go
func (u *UI) Emit(e event.Event) {
    switch p := e.Payload().(type) {
    case *event.TextPayload:          // KindText / KindTextChunk
        u.appendAssistantText(p.Text)
    case *event.ToolUseStartPayload:  // KindToolUseStart
        u.showToolCall(p.Name, p.Input)
    case *event.ApprovalNeededPayload:
        u.openApprovalPrompt(*p)
    }
}
```

The envelope also carries `AgentID` (who emitted it) and `ParentID` (empty
for the root agent, set to the root's id for subagent events — route those
into a sub-panel).

### The kinds you'll handle most

| Kind | Payload | Meaning / what to do |
|------|---------|----------------------|
| `KindRunStart` | `RunStartPayload{Prompt}` | a turn began |
| `KindRunEnd` | `RunEndPayload{Iters, Content, Thinking}` | turn finished (win or lose) |
| `KindRunCancelled` | — | ctx cancel tore the run down |
| `KindIterLimit` | `IterLimitPayload{Iters}` | paused at the loop cap; call `Continue` to resume |
| `KindTurnStart`/`KindTurnEnd` | `TurnPayload{Iteration}` | brackets one loop iteration |
| `KindThinking` | `TextPayload{Text}` | full reasoning block (buffered providers) |
| `KindText` | `TextPayload{Text}` | full assistant text (buffered providers) |
| `KindThinkingChunk`/`KindTextChunk` | `TextPayload{Text}` | **streaming deltas** — accumulate consecutive chunks; reset on `KindTurnEnd` |
| `KindToolUseStart` | `ToolUseStartPayload{Name, Input, ToolID}` | a tool dispatched |
| `KindToolUseResult` | `ToolUseResultPayload{ToolID, Content, IsError, Metadata, ContentBlocks}` | a tool returned (pair by `ToolID`) |
| `KindApprovalNeeded` | `ApprovalNeededPayload{RequestID, ToolName, …}` | **must** `RespondPermission` or the tool hangs |
| `KindQuestionNeeded` | `QuestionNeededPayload{RequestID, Questions}` | **must** `RespondQuestion` |
| `KindStoreUpdate` | `StoreUpdatePayload{Domain, Op, ID, Payload}` | a backing store changed (todos, daemons) — re-render the panel |
| `KindUsage` | `UsagePayload{Turn, Cumulative}` | token usage — update the meter |
| `KindModeChanged` | `ModeChangedPayload{PrevMode, Mode}` | permission mode changed (sync the indicator) |
| `KindCompacting`/`KindCompactingEnd` | `CompactingPayload` / `CompactingEndPayload` | session compaction started / ended |
| `KindError` | `ErrorPayload{Stage, Err, Message}` | a Go-level failure aborted the loop |
| `KindBgResult` | `BgResultPayload{TaskID, Status, …}` | a background bash task finished |

**Streaming vs. buffered.** Streaming providers emit `KindTextChunk` /
`KindThinkingChunk` deltas and then *skip* the final full `KindText` /
`KindThinking` to avoid duplication. Buffered providers emit only the full
block. A robust UI handles both: accumulate chunks of the same kind into one
logical block, and treat the full event as a complete block. Reset the
accumulator on `KindTurnEnd`.

**Store updates are a single kind.** Rather than a new event kind per panel,
all backing-store changes flow through `KindStoreUpdate`. Switch on
`StoreUpdatePayload.Domain` (`todo.Domain`, `daemon.Domain`, …) to decide how
to render. The payload is the store's typed snapshot; or just call
`controller.TodoStore()` / `controller.DaemonState()` and read the live
state. Adding a new panel never requires a new event kind.

> Tool errors (`Result.IsError`) flow through `KindToolUseResult`, **not**
> `KindError`. `KindError` is reserved for Go-level failures that abort the
> loop. The model can recover from the former; the latter ends the turn.

---

## 4. The wiring sequence (host responsibility)

The host (`cmd/evva/main.go`, or your own `main`) performs a fixed
four-step dance. This is the contract documented at the top of
`pkg/ui/ui.go`:

```go
// 1. Construct the UI first — it is the agent's event sink.
tui := bubbletea.New(cfg.AppHome)

// 2. Build the agent; route its events into the UI via WithSink.
ag, err := agent.New(agent.Config{AppConfig: cfg},
    agent.WithSink(tui),            // agent → UI
    agent.WithRootContext(ctx),     // signal pump + bg tasks track this ctx
)
if err != nil { /* … */ }
defer ag.Shutdown()

// 3. Hand the UI the controller view of the agent.
tui.Attach(ag.Controller())          // UI → agent

// 4. Run the loop. Blocks until exit.
if err := tui.Run(ctx); err != nil { /* … */ }
```

That's the entire `examples/full-host/main.go` — the canonical embed of the
full experience, in a separate module, ~60 lines. Your UI slots into step 1
and step 3/4 exactly where `bubbletea` does.

Two subtleties worth internalizing:

- **`ag.Controller()` is a *view*, not the agent.** `*agent.Agent` and
  `ui.Controller` share method names with different payload types, so one
  concrete type can't satisfy both. `ag.Controller()` returns the
  `ui.Controller` projection you hand to `Attach`.
- **The UI is the sink.** You pass the same object twice: once as
  `WithSink(tui)` (agent emits into it) and once implicitly as the thing
  that holds `ag.Controller()` after `Attach`. That's the full duplex link.

---

## 5. Build a minimal UI from scratch

The smallest thing that satisfies `ui.UI` is a line-based REPL — no
framework, no goroutines, ~40 lines. Drop this in `pkg/ui/lineui/lineui.go`:

```go
// Package lineui is a bare line-based reference UI: prove out the ui.UI
// contract with stdin/stdout and nothing else.
package lineui

import (
    "bufio"
    "context"
    "fmt"
    "os"

    "github.com/johnny1110/evva/pkg/event"
    "github.com/johnny1110/evva/pkg/ui"
)

type UI struct{ ctrl ui.Controller }

func New() *UI { return &UI{} }

// Emit: agent → UI. Called inline on the agent goroutine during ctrl.Run.
// Because Run below calls ctrl.Run synchronously, there's no second
// render goroutine and therefore no cross-goroutine state to guard.
func (u *UI) Emit(e event.Event) {
    switch p := e.Payload().(type) {
    case *event.TextPayload:
        if e.Kind == event.KindText || e.Kind == event.KindTextChunk {
            fmt.Print(p.Text)
        }
    case *event.ToolUseStartPayload:
        fmt.Printf("\n  → %s\n", p.Name)
    case *event.ToolUseResultPayload:
        mark := "✓"
        if p.IsError {
            mark = "✗"
        }
        fmt.Printf("  %s %s\n", mark, firstLine(p.Content))
    case *event.ApprovalNeededPayload:
        // MUST respond, or the blocked tool goroutine hangs forever.
        // A real UI prompts the user; this demo auto-allows.
        _ = u.ctrl.RespondPermission(p.RequestID,
            ui.PermissionDecision{Behavior: "allow", Reason: "lineui demo"})
    case *event.QuestionNeededPayload:
        _ = u.ctrl.RespondQuestion(p.RequestID, ui.QuestionResponse{})
    }
}

func (u *UI) Attach(c ui.Controller) { u.ctrl = c }

func (u *UI) Run(ctx context.Context) error {
    sc := bufio.NewScanner(os.Stdin)
    fmt.Print("\nevva> ")
    for sc.Scan() {
        line := sc.Text()
        if line == "/exit" || line == "/quit" {
            return nil
        }
        if line != "" {
            if _, err := u.ctrl.Run(ctx, line); err != nil {
                fmt.Fprintln(os.Stderr, "\nerror:", err)
            }
            fmt.Println()
        }
        fmt.Print("evva> ")
    }
    return sc.Err()
}

func firstLine(s string) string {
    for i, r := range s {
        if r == '\n' {
            return s[:i]
        }
    }
    return s
}
```

Wire it in `main` exactly like the bubbletea TUI:

```go
lui := lineui.New()
ag, _ := agent.New(agent.Config{AppConfig: cfg}, agent.WithSink(lui), agent.WithRootContext(ctx))
defer ag.Shutdown()
lui.Attach(ag.Controller())
_ = lui.Run(ctx)
```

This already gives you: streaming text, tool-call traces, and the
permission/question round-trip. What it *doesn't* have — async rendering
while the agent works, panels, overlays, scrollback — is exactly what the
bubbletea reference adds, and why it's more involved. The rest of this guide
is about that gap.

> **The one rule the minimal version sidesteps:** here `ctrl.Run` is
> synchronous, so `Emit` fires on the same goroutine and printing inline is
> safe. The moment you add an async render loop (any real TUI), `Emit` runs
> on the agent's goroutine while your loop runs on another — and you must
> marshal events onto a single goroutine before touching UI state. That's §6.3.

---

## 6. Anatomy of the reference bubbletea TUI

`pkg/ui/bubbletea` is built on [Bubble Tea](https://github.com/charmbracelet/bubbletea),
an Elm-architecture (Model-View-Update) framework. If you build on bubbletea
too, this section is a map. If you build on something else, it still shows
*which problems any evva UI has to solve* — concurrency, turn lifecycle,
broker round-trips, store-driven panels.

### 6.1 Package layout

```
pkg/ui/bubbletea/
├── ui.go                    # the ui.UI adapter — Emit/Attach/Run, ~90 lines
├── app/
│   ├── root.go              # the root tea.Model: Init/Update/View, msg dispatch
│   └── focus.go             # Focusable interface + modal overlay stack
├── events/msgs.go           # the tea.Msg types (AgentEventMsg, RunDoneMsg, …)
├── theme/                   # palette (colors), styles (Theme struct), symbols (glyphs)
├── mouse/                   # wheel detection + clipboard (pbcopy/xclip/OSC52)
└── components/
    ├── transcript/          # the scrollback: blocks, markdown, diff, viewport, cache
    ├── status/              # bottom HUD: run-state machine + token/context meters
    ├── input/               # textarea: paste compaction, history, SubmitMsg
    ├── slash/               # "/"-autocomplete suggestion panel
    ├── overlays/            # modal panels: approval, question, config, model, …
    ├── todos/               # todo panel (reads controller.TodoStore())
    ├── agents/ bgtasks/ monitors/  # daemon chip strips (controller.DaemonState())
    └── diff/                # file-diff renderer for write/edit tool results
```

The root model stays thin: focus stack, layout math, and message dispatch.
Every visual concern lives in a sibling component package. That separation
is what keeps a 957-line `root.go` readable despite the feature count.

### 6.2 The Elm loop: `Init` / `Update` / `View`

The `app.App` struct is the single `tea.Model`. Bubble Tea calls:

- **`Init() tea.Cmd`** — once, at start. Returns initial commands (cursor
  blink + spinner tick).
- **`Update(msg tea.Msg) (tea.Model, tea.Cmd)`** — on every message
  (keystroke, window resize, agent event, async result). Mutates state,
  returns the next command. **All state mutation happens here, on one
  goroutine.**
- **`View() string`** — after each `Update`. Pure function of state →
  string. Composes the frame top-to-bottom: transcript viewport, panels,
  overlay/slash, input box, hint line, status bar. Each layer collapses to
  zero height when empty.

### 6.3 The critical concurrency rule

This is the single most important thing to get right in any async UI.

`ui.go`'s `Emit` runs on the **agent's** goroutine. The bubbletea render
loop runs on **its own** goroutine. You must never touch model state from
`Emit` directly. Instead, forward the event as a message:

```go
// pkg/ui/bubbletea/ui.go
func (u *UI) Emit(e event.Event) {
    if u.program == nil {
        return
    }
    u.program.Send(events.AgentEventMsg{Event: e}) // hand off to the loop
}
```

`tea.Program.Send` is the thread-safe boundary. The event arrives back in
`Update` as an `AgentEventMsg`, on the render goroutine, where mutating state
is safe:

```go
// app/root.go
case events.AgentEventMsg:
    return a.handleAgentEvent(m.Event)
```

Every async producer follows this pattern: the spinner timer, the clipboard
worker, and — crucially — the agent `Run` goroutine (next section) all funnel
back through `program.Send` → `Update`. **One goroutine owns all state.**

### 6.4 Running a turn without freezing the UI

`Controller.Run` blocks until the whole turn finishes (could be minutes). If
you called it from `Update`, the UI would freeze. So the App launches it in a
goroutine and reports completion via a message:

```go
// app/root.go — startRun
func (a *App) startRun(prompt string) {
    ctx, cancel := context.WithCancel(context.Background())
    a.runCancel = cancel          // stash so Esc/Ctrl+C can interrupt
    a.state.OnSubmit()            // status pill → "running"
    p := a.program
    go func() {
        _, err := a.controller.Run(ctx, prompt)
        if p != nil {
            p.Send(events.RunDoneMsg{Err: err}) // back to the loop
        }
    }()
}
```

While that goroutine runs, the agent emits events (text, tool calls, …) that
arrive as `AgentEventMsg` and paint the transcript live. When `Run` returns,
`RunDoneMsg` flips the status pill back to idle (or shows the error / the
iter-limit "press Enter to continue" prompt). The stored `runCancel` lets the
global Esc/Ctrl+C handler interrupt mid-flight.

Mid-run prompts don't start a second `Run` (that errors on every provider).
Instead the App calls `controller.EnqueueUserPrompt`; the agent drains the
queue at the next iteration boundary.

### 6.5 The event → message bridge

`events/msgs.go` declares the `tea.Msg` types, in their own package to avoid
an import cycle between `app` and `components`:

```go
type AgentEventMsg struct{ Event event.Event } // wraps every agent event
type RunDoneMsg struct{ Err error }            // Controller.Run/Continue returned
type QuitMsg struct{}                          // user quit / ctx cancel
type SpinnerTickMsg struct{}                   // drives the spinner animation
type ClipboardMsg struct{ OK bool; … }         // copy-attempt result
```

`handleAgentEvent` (in `root.go`) is the fan-out hub: it feeds the event to
the run-state machine, the status bar, the transcript, and — on
`KindApprovalNeeded` / `KindQuestionNeeded` — pushes an overlay.

### 6.6 The run-state machine

`components/status/state.go` maps the event stream onto a coarse `RunState`
enum that drives the status pill (label + color + spinner):

```
StateIdle → StateRunning → {StateThinking, StateTexting, StateExecuting,
                            StateDraining, StateCompacting} → StateIdle
                          ↘ StateIterLimit / StateError (sticky)
```

`State.Apply(event.Event)` does the transition (`KindThinking` → thinking,
`KindToolUseStart` → executing, `KindRunEnd` → idle, …). Terminal states
(`Error`, `IterLimit`) are *sticky* — a stray mid-run event won't overwrite
them; they clear on the next submit. This is a clean pattern to copy even in a
non-bubbletea UI: derive one display state from the raw stream rather than
tracking a dozen booleans.

### 6.7 The transcript: rendering the conversation

`components/transcript` owns the scrollback. The key entry point is
`Transcript.IngestEvent(e event.Event) bool` — it converts agent events into
renderable *blocks* (user prompt, assistant text, thinking, tool call, tool
result, diff, system lifecycle) and returns whether anything changed (so the
App knows to mark the viewport dirty). Other entry points:

- `AppendUserPrompt(text)` — when the user submits.
- `LoadFromMessages(controller.Messages())` — rebuild scrollback on
  `/resume` or after a profile/model switch.
- `Reset()` — `/clear`.
- A block cache keyed on `theme.Rev` so a theme swap re-renders everything
  and unchanged blocks are never re-rendered per frame.

Rich tool output rides on `ToolUseResultPayload.Metadata` (an opaque `any`).
For example, `write`/`edit` attach a `*fs.FileDiff`; the UI type-asserts and
renders a colored diff. This is how structured payloads reach the UI without
the event layer knowing about every tool.

### 6.8 Panels from stores: todos and daemons

Side panels don't track their own state — they read the agent's backing
stores live and re-render on `KindStoreUpdate`:

- **Todo panel** ← `controller.TodoStore()` (never nil).
- **Subagent / background-task / monitor chip strips** ←
  `controller.DaemonState()` (nil until the first daemon registers — *always
  nil-check*).

One subtlety worth copying: the **auto-fold**. When every todo reaches
`completed`, the App appends a "TASKS COMPLETE" snapshot to the transcript
and clears the store. The clear *must* run off the current goroutine — each
deletion emits a `KindStoreUpdate` that routes back as a message, and calling
`Clear()` inline from `Update` deadlocks bubbletea's unbuffered message
channel. The fix is to return the clear as a `tea.Cmd`:

```go
cmd = func() tea.Msg { store.Clear(); return nil }
```

The general lesson: **don't re-enter the event source from inside the event
handler.** Defer the side effect.

### 6.9 Broker round-trips: approval and questions

This is the one place a UI *owes* the agent a response. When the permission
gate needs a decision, or `ask_user_question` fires, the agent **parks the
tool goroutine** and emits a `*Needed` event carrying a `RequestID`. The tool
stays blocked until you call the matching `Respond*` with that id. If you
never respond, the tool hangs forever.

```
agent: gate blocks tool ──emit KindApprovalNeeded{RequestID}──► UI
UI: push approval overlay, user picks "Allow once"
UI: controller.RespondPermission(RequestID, {Behavior:"allow"}) ──► agent wakes tool
```

The bundled approval overlay (`components/overlays/approval.go`) offers
*Allow once* / *Allow for this session* / *Deny (+reason)*. "Allow for this
session" attaches a `PermissionRuleSeed` so the gate adds an in-memory rule.
The question overlay (`overlays/question.go`) renders single/multi-select
options with optional previews and returns a `QuestionResponse`.

Two non-negotiables:

1. **Always respond.** Even on Ctrl+C, the overlays respond with a deny
   before quitting, so the parked goroutine doesn't leak. A headless sink
   (see `cmd/evva/main.go`'s `cliSink`) auto-denies for the same reason.
2. **The `RequestID` is the correlation key.** Pass back exactly what you
   received.

### 6.10 The focus stack and overlays

Modal panels (`/config`, `/model`, `/profile`, `/compact`, `/effort`,
`/resume`, `/update`, approval, question, yank, search) are managed by a
small `FocusStack` of `Focusable` (`app/focus.go`):

```go
type Focusable interface {
    Update(msg tea.Msg) (close bool, cmd tea.Cmd) // returns close=true to pop
    View(width int, th *theme.Theme) string
    Key() string
    Modal() bool      // true → consumes all keys
    Hint() string     // contributes the contextual hint line
}
```

Key routing precedence in `handleKey` (each layer only sees what higher ones
ignored):

1. Top-of-stack modal overlay — exclusive consumer
2. Global keys: Ctrl+C, Esc, Ctrl+O (fold), Ctrl+Y (yank), Ctrl+F (search),
   Shift+Tab (cycle permission mode), PgUp/PgDn/Home/End (scroll)
3. Slash panel (when visible): Tab completes, Up/Down move selection
4. Input textarea: history nav, paste, plain typing

Adding an overlay = implement `Focusable`, `Push` it on the relevant key or
slash command, and it self-pops by returning `close=true`. The App knows
nothing about a given overlay's internals.

### 6.11 Slash commands and the input box

Slash commands are a **UI-side** convenience, not an agent feature.
`handleSubmit` intercepts text like `/clear`, `/config`, `/model` before any
`Run`. The suggestion catalog (`components/slash/panel.go`) merges static
builtins with `controller.Skills()` — so user-installed skills show up as
`/<name>` automatically. The agent decides if/when to actually invoke a skill
via its `SKILL` tool; the panel just surfaces them.

The input box (`components/input/model.go`) wraps `bubbles/textarea` and adds
bracketed-paste compaction (big pastes become a chip), prompt history
(Up/Down), and a two-form `SubmitMsg`:

```go
type SubmitMsg struct {
    ForAgent string // raw content the agent sees
    ForView  string // transcript form (pastes wrapped in visible chips)
}
```

### 6.12 Keybindings (the reference TUI)

Derived from `app/root.go`'s `handleKey`/`handleSubmit`:

| Key | Action |
|-----|--------|
| `Enter` | send the prompt |
| `Ctrl+J` | newline in the input |
| `Ctrl+O` | toggle tool-result fold |
| `Ctrl+Y` | enter block-yank (clean copy) mode |
| `Ctrl+F` | search the transcript |
| `Shift+Tab` | cycle permission mode |
| `PgUp`/`PgDn`/`Home`/`End` | scroll the transcript |
| `Up`/`Down` | prompt history (or slash selection when the panel is open) |
| `Tab` | complete the highlighted slash command |
| `Esc` | interrupt run · dismiss error · dismiss slash panel · else quit |
| `Ctrl+C` | interrupt run · else quit |
| mouse wheel | scroll the transcript |

---

## 7. Building something stylish: the theme system

The reference TUI's look ("NEON TOKYO" — electric cyan + violet on abyssal
navy, red reserved strictly for faults) lives in `pkg/ui/bubbletea/theme`.
The split is worth copying:

- **`palette.go`** — private `lipgloss.Color` constants. The palette never
  leaves the package; components ask the `Theme` for styles, not raw colors.
- **`styles.go`** — the `Theme` struct: one `lipgloss.Style` field per
  surface (`UserPrompt`, `ToolCall`, `ToolOK`, `ToolErr`, `DiffAdd`,
  `PanelHeader`, `StatusBar`, `Banner`, …). Built once by `Default()` at
  startup and passed by pointer to every renderer. Components read styles off
  the struct rather than rebuilding them per frame.
- **`symbols.go`** — the lifecycle vocabulary: `SpinnerFrames` (braille dots,
  100 ms cadence) and a `Glyph` table mapping status strings
  (`pending`/`in_progress`/`completed`, subagent states) to symbol + color.
  One source of truth so every widget agrees.

```go
// styles.go — a Theme is a bag of pre-built lipgloss styles
type Theme struct {
    Rev        uint64        // bump on swap → invalidates the block cache
    UserPrompt lipgloss.Style
    ToolCall   lipgloss.Style
    DiffAdd    lipgloss.Style
    StatusBar  lipgloss.Style
    // … ~40 surfaces
}
```

The **`Rev` field** is the cache key. Swap the theme, bump `Rev`, and the
transcript block cache re-renders everything with the new styles —
components compare `Rev` values, never `Theme` pointers. To ship a second
theme, build another `*Theme` with a higher `Rev` and a different palette;
no component changes.

`lipgloss` (truecolor, unconditional — evva targets modern terminals),
`bubbles` (textarea, viewport), and `glamour` (markdown) are the styling
stack. Versions are pinned in `go.mod`: bubbletea `v1.3.10`, bubbles
`v1.0.0`, lipgloss `v1.1.x`, glamour `v1.0.0`.

> **Charmbracelet version pinning matters.** `ui.UI.Run` exposes a
> `tea.Program` indirectly. If a downstream UI imports a *different*
> major/minor of bubbletea than evva's pinned `v1.3.10`, Go treats them as
> distinct types and wiring breaks. Match the pin (see `docs/extending.md` §
> "Charmbracelet version pinning").

---

## 8. Switching UIs at startup: `evva -tui <name>`

The bundled binary ships a **UI registry** keyed by name, selected with the
`-tui` flag (default `bubbletea`). `evva -tui <name>` resolves a registered
factory and runs it; an unknown name exits cleanly with the list of
available UIs. `-no-tui` (or a non-TTY stdout) still bypasses the registry
entirely and uses the plain one-shot CLI sink for pipes/CI.

```
$ evva -tui bubbletea     # the default — bundled NEON TOKYO TUI
$ evva -tui lineui        # whatever else is registered
$ evva -tui nope          # → evva: unknown -tui "nope" (available: bubbletea)
$ evva -no-tui "..."      # headless one-shot; -tui ignored
```

### How it works

The registry lives in `pkg/ui/registry.go` — public, so a UI in any module
can register into it:

```go
// pkg/ui/registry.go (shipped)
type Factory func(evvaHome string) UI

func Register(name string, f Factory)        // add/replace a named factory
func Lookup(name string) (Factory, bool)     // resolve at startup
func Names() []string                        // sorted, for help / errors
```

The factory signature is uniform — every UI takes the config dir and
nothing else — so the `-tui` flag can build any of them the same way. A UI
that needs more configuration reads it from its own file under `evvaHome`.

### Add your UI to the bundled binary (three steps)

**1. Implement `ui.UI`** in `pkg/ui/<name>/` (see §5 for the skeleton).

**2. Self-register** with a blank-importable `init()`. The bundled TUI does
exactly this in `pkg/ui/bubbletea/register.go`:

```go
package bubbletea

import "github.com/johnny1110/evva/pkg/ui"

func init() {
    ui.Register("bubbletea", func(evvaHome string) ui.UI {
        return New(evvaHome)
    })
}
```

Your `pkg/ui/lineui/register.go` mirrors it:

```go
package lineui

import "github.com/johnny1110/evva/pkg/ui"

func init() {
    ui.Register("lineui", func(string) ui.UI { return New() })
}
```

(No import cycle: `pkg/ui/<name>` may import `pkg/ui`, but `pkg/ui` never
imports a concrete UI.)

**3. Blank-import it in the host** so the `init()` runs. `cmd/evva/main.go`
imports the bundled TUI purely for its registration side effect — add a line
next to it for yours:

```go
import (
    "github.com/johnny1110/evva/pkg/ui"
    _ "github.com/johnny1110/evva/pkg/ui/bubbletea" // registers "bubbletea"
    _ "github.com/johnny1110/evva/pkg/ui/lineui"    // registers "lineui"
)
```

That's the whole contribution — no change to `runTUI`, the flag, or the
agent wiring. The host already resolves the factory:

```go
// cmd/evva/main.go (shipped) — the -tui flag + lookup
tuiName := flag.String("tui", "bubbletea",
    "interactive UI to use (available: "+strings.Join(ui.Names(), ", ")+")")
// …
func runTUI(ctx context.Context, acfg agent.Config, evvaHome, tuiName string) {
    factory, ok := ui.Lookup(tuiName)
    if !ok {
        exitf(2, "evva: unknown -tui %q (available: %s)", tuiName, strings.Join(ui.Names(), ", "))
    }
    tui := factory(evvaHome)

    ag, err := agent.New(acfg, agent.WithSink(tui), agent.WithRootContext(ctx))
    if err != nil {
        exitf(1, "evva: %v", err)
    }
    defer ag.Shutdown()
    tui.Attach(ag.Controller())
    if err := tui.Run(ctx); err != nil {
        exitf(1, "evva: %v", err)
    }
}
```

> Because `ui.Names()` is read when the flag is defined (inside `main`,
> after every imported package's `init()` has run), the `-tui` help text
> lists every registered UI automatically.

### Alternative: a fully custom host (no registry)

If you're embedding evva in your own app and only ever use one UI, skip the
registry — build your own `main` (the `examples/full-host` pattern) and
construct the UI directly:

```go
tui := myui.New(cfg.AppHome) // instead of bubbletea.New(cfg.AppHome)
```

Everything downstream (`agent.New(..., WithSink(tui))`, `tui.Attach`,
`tui.Run`) is identical because both satisfy `ui.UI`. This is exactly why the
agent never imports a concrete UI.

---

## 9. Checklist and gotchas

Before you call a UI done:

- [ ] **`var _ ui.UI = (*YourUI)(nil)`** — add the compile-time assertion
      (see `pkg/ui/bubbletea/ui_test.go`). It breaks loudly if the contract
      drifts.
- [ ] **`Emit` never blocks.** Forward to a render loop / buffer and return.
      Anything slow (disk, network) goes on its own goroutine.
- [ ] **One goroutine owns UI state.** If `Emit` and your render loop are on
      different goroutines, marshal events onto one (bubbletea:
      `program.Send`).
- [ ] **Always answer brokers.** On every exit path — including Ctrl+C — call
      `RespondPermission` / `RespondQuestion` for any pending `RequestID`, or
      the parked tool goroutine leaks.
- [ ] **Nil-check `DaemonState()`.** It's nil until the first daemon registers.
      `TodoStore()` is never nil.
- [ ] **Handle streaming *and* buffered.** Accumulate `*Chunk` deltas; treat
      full `KindText`/`KindThinking` as complete blocks. Reset on `KindTurnEnd`.
- [ ] **Don't re-enter the event source from a handler.** Defer store
      mutations that themselves emit events (the auto-fold `tea.Cmd` trick).
- [ ] **Run turns off the main loop** and report completion via a message, so
      the UI stays responsive and Esc/Ctrl+C can cancel via the stored
      `context.CancelFunc`.
- [ ] **Match the bubbletea pin** (`v1.3.10`) if you build on bubbletea.
- [ ] **Use only `pkg/*`.** No `internal/*` imports — the
      `controller_compile_test.go` and `examples/full-host` separate module
      are your north stars.

---

## 10. Reference: key files

| File | What it defines |
|------|-----------------|
| `pkg/ui/ui.go` | the `UI` and `Controller` interfaces + payload types |
| `pkg/ui/registry.go` | the `-tui` registry: `Factory`, `Register`/`Lookup`/`Names` |
| `pkg/ui/bubbletea/register.go` | self-registers the bundled TUI as `"bubbletea"` |
| `pkg/event/event.go` | `Event`, all `Kind`s, payload structs, `Payload()` |
| `pkg/event/sink.go` | `Sink`, `Multi`, `Discard`, `BubbleUp`, `SinkFunc` |
| `pkg/ui/controller_compile_test.go` | public-only proof of the `Controller` surface |
| `pkg/ui/bubbletea/ui.go` | the reference `ui.UI` adapter (Emit/Attach/Run) |
| `pkg/ui/bubbletea/app/root.go` | the root `tea.Model`: Update/View, dispatch |
| `pkg/ui/bubbletea/app/focus.go` | `Focusable` + modal overlay stack |
| `pkg/ui/bubbletea/events/msgs.go` | the `tea.Msg` types |
| `pkg/ui/bubbletea/theme/` | palette, styles (`Theme`), symbols/glyphs |
| `pkg/ui/bubbletea/components/status/state.go` | the run-state machine |
| `pkg/ui/bubbletea/components/overlays/approval.go` | a broker round-trip overlay |
| `cmd/evva/main.go` | the host: wiring + the `cliSink` headless fallback |
| `examples/full-host/main.go` | the canonical full embed (separate module) |
| `examples/minimal-host/main.go` | a stdout-sink embed (no TUI) |
| `docs/extending.md` | the short "Custom UI" section + every other extension point |

---

*evva is a ReAct coding agent for the terminal. The UI contract is one of
its core seams: one runtime, many personas, swappable UI. Build a UI under
`pkg/ui/<name>/`, satisfy `ui.UI`, and wire it in — that's the whole job.*
