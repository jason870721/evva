# Agent Runtime — Design

Covers four intertwined pieces:

1. **Agent loop** — the multi-turn dispatch cycle that drives an `*Agent`.
2. **Tool state lifecycle** — when a tool is built, when its schema is sent,
   when it's resolved on demand.
3. **Event emitter** — how the agent streams "what's happening right now"
   (thinking, text, tool calls, errors, task panel, subagent activity) to
   any number of consumers (TUI, log, CLI verbose, future telemetry).
4. **Session persistence & `/resume`** — selecting and reloading a prior
   conversation.

Goal: a clean, testable runtime where:
- the loop has one job (drive the conversation to completion),
- tool packages stay dumb (just `Tool` impls + constructors),
- the UI layer subscribes to events; it does **not** poll agent state,
- subagent activity bubbles up with structure so the TUI can render
  a nested panel (the "fancy UI" point),
- the same event stream that drives the TUI is the API for any future UI
  (web, native, IDE plugin) — UIs are just `Sink` implementations.

---

## 1. Agent Loop

### Public surface

```go
// internal/agent/agent.go

// Run drives the agent to completion for a single user turn. It blocks until
// the model emits a final assistant message (no tool_use), the context is
// cancelled, the iteration cap is hit, or an unrecoverable error occurs.
//
// Events flow to the agent's Sink as side effects (see §3). The returned
// llm.Response is the FINAL assistant turn — the one with text, no tool_use.
// When the iteration cap is hit, Run returns ErrIterLimit and the caller may
// invoke Continue(ctx) to keep going from the same session.
func (a *Agent) Run(ctx context.Context, prompt string) (llm.Response, error)

// Continue resumes a paused agent (e.g. one that hit ErrIterLimit) without
// appending a new user message. The next LLM call starts from the existing
// session history. Used by "press enter to keep going" and by /resume.
func (a *Agent) Continue(ctx context.Context) (llm.Response, error)
```

`Send` (the single-turn primitive) stays for tests / smoke checks; `Run` is
the production entry point. `Continue` shares the loop body with `Run` —
the only difference is whether a new `RoleUser` message is appended first.

### Loop body (pseudocode)

```
runLoop(ctx, startWithUserMsg, prompt):
    if startWithUserMsg:
        append RoleUser{prompt} to session
        emit RunStart{prompt}
        logger.Debug("run.start", "prompt_bytes", len(prompt))
    else:
        emit RunResume{from: lastMessageID}
        logger.Debug("run.resume", "messages", len(session.Messages))

    for iter := 0; iter < a.maxIters; iter++:
        emit TurnStart{iter}
        logger.Debug("turn.start", "iter", iter, "messages", len(session.Messages))

        resp, err := llm.Complete(ctx, session, a.exposedTools())
        if err:
            if errors.Is(err, llm.ErrInterrupted):
                emit RunCancelled
                logger.Info("run.cancelled")
                return Response{}, err
            emit Error{Stage: "llm", Err: err}
            logger.Error("llm.fail", "err", err)
            return Response{}, err

        if resp.Thinking != "":
            emit Thinking{resp.Thinking}
        if resp.Content != "":
            emit Text{resp.Content}

        append RoleAssistant{Content, Thinking, ToolCall, ToolID} to session

        if resp.ToolCall == nil:                  // <-- terminal
            emit RunEnd{Final: resp}
            logger.Debug("run.end", "iter", iter, "content_bytes", len(resp.Content))
            return resp, nil

        emit ToolUseStart{Name, Input, ToolID}
        logger.Debug("tool.dispatch", "name", resp.ToolCall.Name, "tool_id", resp.ToolID)

        tool, err := a.ResolveTool(ToolName(resp.ToolCall.Name))
        if err:
            // Not in active or deferred allowlist — surface to the model
            // and let it recover on the next turn.
            emit ToolUseResult{ToolID, IsError: true, Content: err.Error()}
            append RoleTool{ToolID, IsError: true, Content: err.Error()}
            logger.Warn("tool.reject", "name", resp.ToolCall.Name, "err", err)
            continue

        result, err := tool.Execute(ctx, resp.ToolCall.Input)
        if err:
            emit Error{Stage: "tool:" + name, Err: err}
            logger.Error("tool.exec.fail", "name", name, "err", err)
            // Go-level tool errors abort — they are bugs, not recoverable
            // by the model the way Result.IsError is.
            return Response{}, err

        emit ToolUseResult{ToolID, IsError: result.IsError, Content: result.Content}
        append RoleTool{ToolID, IsError: result.IsError, Content: result.Content}
        logger.Debug("tool.result", "name", name, "is_error", result.IsError, "bytes", len(result.Content))

        emit TurnEnd{iter}

    // Iteration cap — NOT an error. Emit IterLimit so the UI can prompt
    // "press enter to keep going"; the caller calls Continue() to resume.
    emit IterLimit{Reached: a.maxIters}
    logger.Info("run.iter_limit", "reached", a.maxIters)
    return Response{}, ErrIterLimit
```

### Invariants

| Invariant | Why |
|---|---|
| `MaxIterations` default = **25**, per-Profile override. | Prevent runaway loops; tunable per agent kind. |
| Hitting `MaxIterations` is **not fatal** — emit `IterLimit` and return `ErrIterLimit`. Caller decides whether to `Continue`. | "Press Enter to keep going" UX; matches user intent. |
| Every LLM turn ending in `tool_use` is followed by exactly one `RoleTool` message before the next LLM turn. | Anthropic / OpenAI-style providers reject mismatched pairings. |
| `ctx` is checked at every LLM call and tool call. | Ctrl+C / TUI ESC must abort within one network round-trip. |
| `result.IsError` is **not** a fatal error — the model sees it and may retry. Only Go-level errors abort the loop. | Lets the model self-correct on bad arguments, missing files, etc. |
| Tool execution runs serially in the loop's goroutine. | Simpler reasoning; concurrent tool calls are a future extension. |
| Every event has a corresponding entry in `session.Messages`. | Replayability, debuggability, `/resume` integrity. |
| Every important transition is `logger.Debug`-logged with structured kv. | Post-mortems read the agent's log, not stdout. |

### Cancellation

`Run` / `Continue` honor `ctx` at three points:
1. Inside `llm.Complete` (provider-side abort).
2. Inside `tool.Execute` (tools must accept `ctx`).
3. Between iterations (`select { case <-ctx.Done(): … }`).

The provider clients already wrap `context.Canceled` into `llm.ErrInterrupted`.
The loop returns this error unchanged so `cmd/evva/main.go` can exit 130.

### Tool dispatch — single entry point

The loop calls `a.ResolveTool(name)`. That method:

- returns the cached instance for an active tool,
- builds-and-caches on first invocation of a deferred tool,
- **errors out** for any name not in the active set OR the deferred
  allowlist — see §2 invariant.

This is the **only** place tools are looked up. No other code in `agent/`
reaches into `a.active` directly.

### Errors taxonomy

| Source | Loop behavior | Returns |
|---|---|---|
| `ctx.Canceled` (anywhere) | Wrap as `llm.ErrInterrupted` if not already; emit `RunCancelled`; return. | `llm.ErrInterrupted` |
| LLM transport / decode error | Emit `Error{Stage:"llm"}`; return. | underlying error |
| `ResolveTool` rejection | Emit `ToolUseResult{IsError:true}`, append as `RoleTool`, continue. Model recovers. | — (loop continues) |
| `tool.Execute` Go error (panic, IO, etc.) | Emit `Error{Stage:"tool:NAME"}`; return. | underlying error |
| `tool.Execute` returns `Result{IsError:true}` | Emit `ToolUseResult{IsError:true}`, append, continue. | — (loop continues) |
| Hit `MaxIterations` | Emit `IterLimit`; return. **Recoverable** via `Continue`. | `ErrIterLimit` |

---

## 2. Tool State Lifecycle

Three phases, decided by the **agent** (profile-level policy), not the tool
package:

| Phase | Built? | Schema seen by LLM? | Trigger |
|---|---|---|---|
| **Active** | Yes, at `agent.New()` | Yes, every `Complete` call | Listed in `Profile.ActiveTools` |
| **Deferred** | No | Name + tags advertised (system prompt); full schema via TOOL_SEARCH | Listed in `Profile.DeferredTools` |
| **Resolved** | Yes, lazily | Yes, from the turn it's built onward | `ResolveTool(name)` invoked — by the loop when the LLM first emits `tool_use` for a deferred name |

### Safety invariant

> **`ResolveTool` only resolves names that exist in the active map OR the
> deferred allowlist. Any other name is rejected — even if `toolset.Build`
> would happily construct it.**

This is the trust boundary: the profile declares the agent's authority. A
deferred-not-in-allowlist name reaching the loop is either a bug, a
prompt-injection attempt, or stale state. The loop must never silently
expand the profile's authority.

### Where state lives — `ToolState` (formerly `Builders`)

Rename: `toolset.Builders` → `toolset.ToolState`. The new name describes
what the type *is* (the per-agent state container) rather than what it
*does* (build).

- **Stateless tools** (fs.Read, shell.Bash, …) — package-level singleton
  vars. No `ToolState` involvement.
- **Stateful families** (task today; monitor / cron / skill later) — backing
  state lives on `*toolset.ToolState` with lazy accessors. The `*Agent`
  owns exactly one `*ToolState`. Two agents from the same profile →
  independent state.

```go
type ToolState struct {
    taskStore  *task.Store
    // future: monitorBus, cronSvc, skillLoader, ...
}

func (s *ToolState) TaskStore() *task.Store {
    if s.taskStore == nil { s.taskStore = task.NewStore() }
    return s.taskStore
}
```

`toolset.Build(names, *ToolState)` and `toolset.Describe(name)` keep their
shapes; only the type rename.

### TUI / session access to tool state

```go
a.ToolState().TaskStore().List()        // read tasks for the task panel
a.ToolState().MonitorBus().Subscribe()  // future
```

The single read path for cross-cutting consumers. Tools don't expose state
via getters.

### TOOL_SEARCH — tags for keyword lookup

TOOL_SEARCH lets the LLM find deferred tools without scanning every name.
To make matching cheap and high-signal, every tool carries a small list of
**tags** — short, deliberately chosen keywords the model would naturally
include in a search query.

`Descriptor` gains a `Tags` field:

```go
type Descriptor struct {
    Name        string
    Description string
    Schema      json.RawMessage
    Tags        []string
}
```

Tags are declared centrally in `toolset` (one place, easy to audit):

```go
// internal/toolset/tags.go
var tags = map[tools.ToolName][]string{
    tools.READ_FILE:    {"file", "read", "open", "io", "filesystem"},
    tools.BASH:         {"shell", "command", "exec", "process", "git", "cli"},
    tools.TASK_CREATE:  {"task", "todo", "track", "progress", "plan"},
    tools.WEB_FETCH:    {"http", "url", "web", "fetch", "scrape"},
    tools.WEB_SEARCH:   {"web", "search", "google", "internet", "lookup"},
    tools.CRON_CREATE:  {"schedule", "cron", "recurring", "timer"},
    tools.MONITOR:      {"watch", "tail", "follow", "stream", "stdout"},
    // ...one entry per ToolName
}
```

`toolset.Describe(name)` populates `Tags` from this map.

TOOL_SEARCH's matching algorithm (Wave 2):

1. **`select:` prefix** — `select:Foo,Bar` returns those exact names.
2. **Keyword match** — tokenize the query; rank descriptors by
   `count(tagHits) * 2 + count(nameHits) + count(descHits)`. Return top N.
3. **`+keyword`** — require this token; rank the rest by their hit count.

Tags are not a closed taxonomy — pick the words the LLM would type. Keep
them short (1–2 words) and concrete.

---

## 3. Event Emitter

### Goals

- One unified stream of "what the agent is doing right now."
- Multiple consumers (TUI pane renderer + structured log + CLI verbose mode)
  subscribe without the agent knowing about them.
- Subagent activity is structurally distinguishable so the UI can render
  a nested panel.
- Cheap to emit from inside the loop — no allocations on the hot path
  beyond the event payload itself.
- The same interface is the **UI plugin contract** (see §5).

### Public surface

```go
// internal/agent/event/event.go

type Kind string

const (
    KindRunStart        Kind = "run_start"
    KindRunResume       Kind = "run_resume"
    KindRunEnd          Kind = "run_end"
    KindRunCancelled    Kind = "run_cancelled"
    KindIterLimit       Kind = "iter_limit"      // paused — caller may Continue

    KindTurnStart       Kind = "turn_start"
    KindTurnEnd         Kind = "turn_end"

    KindThinking        Kind = "thinking"        // assistant reasoning text
    KindText            Kind = "text"            // assistant final text

    KindToolUseStart    Kind = "tool_use_start"
    KindToolUseResult   Kind = "tool_use_result"

    KindError           Kind = "error"

    KindTaskUpdate      Kind = "task_update"     // task panel state change
    KindSubagent        Kind = "subagent"        // subagent lifecycle marker
)

// Event is the envelope. Each Kind has a typed payload field; exactly one is
// non-nil per event. Discriminated union — type-safe access, no interface{}.
type Event struct {
    Kind      Kind
    AgentID   string     // who emitted (root agent or subagent UUID)
    ParentID  string     // empty for root; root's AgentID for a subagent
    Time      time.Time

    RunStart       *RunStartPayload
    RunResume      *RunResumePayload
    RunEnd         *RunEndPayload
    IterLimit      *IterLimitPayload
    Turn           *TurnPayload
    Thinking       *TextPayload
    Text           *TextPayload
    ToolUseStart   *ToolUseStartPayload
    ToolUseResult  *ToolUseResultPayload
    Error          *ErrorPayload
    TaskUpdate     *TaskUpdatePayload
    Subagent       *SubagentPayload
}

type RunStartPayload      struct { Prompt string }
type RunResumePayload     struct { FromMessageID string }
type RunEndPayload        struct { Final llm.Response }
type IterLimitPayload     struct { Reached int }   // UI: "press Enter to continue"
type TurnPayload          struct { Iteration int }
type TextPayload          struct { Text string }
type ToolUseStartPayload  struct {
    Name   string
    Input  json.RawMessage
    ToolID string
}
type ToolUseResultPayload struct {
    ToolID  string
    Content string
    IsError bool
}
type ErrorPayload         struct { Stage string; Err error }
type TaskUpdatePayload    struct { TaskID string; Status string; Subject string }
type SubagentPayload      struct {
    SubagentID    string
    AgentType     string         // "explore", "general", etc.
    PromptSummary string
    Phase         SubagentPhase  // started | ended
}
```

No `Depth` field. Subagents are exactly one layer deep (see below).

### Sink interface

```go
// internal/agent/event/sink.go

// Sink consumes events. Implementations: tui.Sink, log.Sink, multi.Sink (fanout).
//
// Emit MUST be non-blocking from the agent's perspective for fast sinks.
// Sinks that need queueing (network, async render) buffer internally.
// The loop calls Emit serially from one goroutine; no concurrent Emit calls
// happen from the same agent.
type Sink interface {
    Emit(Event)
}

// Multi fans out one event to many sinks (synchronous; a slow sink blocks
// the loop — backpressure beats event loss).
type Multi struct{ Sinks []Sink }
func (m Multi) Emit(e Event) { for _, s := range m.Sinks { s.Emit(e) } }

// Discard drops every event. Default for tests / silent CLI.
var Discard Sink = discard{}

// BubbleUp wraps a parent's sink so a subagent's events appear in the
// parent's stream with the right ParentID set.
type BubbleUp struct {
    Parent   Sink
    ParentID string  // = root agent's ID
}
func (b BubbleUp) Emit(e Event) {
    e.ParentID = b.ParentID
    b.Parent.Emit(e)
}
```

### Wiring on the Agent

```go
type Agent struct {
    // ... existing fields ...
    sink     event.Sink   // nil-safe via event.Discard default
    parent   string       // empty for root; root's AgentID for subagents
    maxIters int          // default 25
}

func New(profile Profile, opts ...Option) (*Agent, error)
func WithSink(s event.Sink) Option { return func(a *Agent){ a.sink = s } }
func WithMaxIterations(n int) Option { return func(a *Agent){ a.maxIters = n } }
func asSubagent(parentID string) Option { return func(a *Agent){ a.parent = parentID } }

// emit is the internal hot-path helper:
func (a *Agent) emit(kind event.Kind, payload any) {
    if a.sink == nil { return }
    e := event.Event{Kind: kind, AgentID: a.ID, ParentID: a.parent, Time: time.Now()}
    // assign payload to the appropriate typed field via a small switch
    a.sink.Emit(e)
}
```

### Subagent hierarchy — exactly one layer

> **Only the root (main) agent may spawn subagents.** A subagent cannot
> call the AGENT tool. The hierarchy is always **main → subagent**, never
> deeper.

This keeps event routing trivial (no `Depth` field needed — `ParentID`
empty/non-empty is enough) and makes the UI predictable. If a subagent
needs help, it asks the main agent in its return value.

Implementation: when `meta.Agent.Execute` is called, check the executing
agent's `parent` field. If non-empty (i.e. we're already a subagent),
return `Result{IsError: true, Content: "subagents cannot spawn subagents"}`
and the model adjusts.

### Subagent event bubbling

When the AGENT tool spawns a sub-agent, the parent constructs the child
with a wrapping sink:

```go
// inside the AGENT tool's Execute:
sub, _ := agent.New(profiles.Explore(...),
    agent.WithSink(event.BubbleUp{
        Parent:   parentAgent.Sink(),
        ParentID: parentAgent.ID,
    }),
    agent.asSubagent(parentAgent.ID),
)
sub.emit(KindSubagent, &SubagentPayload{Phase: Started, ...})
resp, err := sub.Run(ctx, prompt)
sub.emit(KindSubagent, &SubagentPayload{Phase: Ended, ...})
```

`BubbleUp` rewrites each child event's `ParentID` so the consumer at the
top of the chain sees a single stream tagged for routing.

TUI rendering with one nested layer:

```
┌─ main agent ────────────────────────────────────────┐
│  user: refactor the tool registry                    │
│  > Thinking...                                       │
│  > Let me explore the current structure first.       │
│  ┌─ subagent (explore) ────────────────────────────┐ │
│  │ > grep "Register" ...                           │ │
│  │ > Found 6 call sites in tools/                  │ │
│  │ > [returns summary]                             │ │
│  └─────────────────────────────────────────────────┘ │
│  > Based on that, I'll rewrite as follows...         │
│  > [tool_use: edit_file] internal/tools/registry.go  │
│  ✓ ok                                                │
└──────────────────────────────────────────────────────┘
```

The TUI keeps `map[agentID]*panel`. `KindSubagent{Phase:Started}` opens a
nested panel; subsequent events with `ParentID == panel.AgentID` route into
it; `KindSubagent{Phase:Ended}` freezes it.

### Task panel events

When task tools mutate the store, the **store itself** emits via a hook the
agent installs at construction:

```go
type Store struct {
    ...
    OnChange func(TaskID, Status, Subject)
}

// in agent.New():
ts := &toolset.ToolState{}
ts.TaskStore().OnChange = func(id, status, subject string) {
    a.emit(event.KindTaskUpdate, &event.TaskUpdatePayload{TaskID:id, Status:status, Subject:subject})
}
```

The TUI's task pane listens for `KindTaskUpdate` and re-renders.

---

## 4. Session Persistence & `/resume`

### What gets persisted

Per-agent state worth resuming:

| Field | Source | Notes |
|---|---|---|
| Agent ID | `Agent.ID` | session filename |
| Profile metadata | `Agent.profile.Type`, system prompt, provider, model | for reconstruction |
| Active + deferred name lists | `Profile.ActiveTools`, `DeferredTools` | snapshot — profile defs can drift, persisted set wins on resume |
| Session messages | `session.Messages` | the full conversation history |
| ToolState snapshot | `*toolset.ToolState` MarshalJSON | task store contents today; monitor/cron stores when they land |
| First-turn timestamp + last-touch timestamp | `time.Time` | for `/resume` listing |

NOT persisted:
- The `llm.Client` (reconstructed from provider + model + options).
- The `Sink` / TUI state (UI is its own concern).
- In-flight tool calls (a session is saved only between turns).

### Storage

```
~/.evva/sessions/
├── 2026-05-13-a1b2c3d4-e5f6.json
├── 2026-05-12-9f8e7d6c-5b4a.json
└── ...
```

- One JSON file per agent run. UTC datestamp prefix for human sortability,
  agent UUID for uniqueness.
- `~/.evva/` is the canonical store; respect `EVVA_HOME` env override.
- Writes happen **on `TurnEnd`** (atomic write + rename) and on graceful
  shutdown. Crash recovery loses at most one in-flight turn.

### `/resume` command (CLI for now, TUI later)

```
$ evva /resume
Recent sessions:
  1. 2026-05-13 14:22  (main, deepseek-v4-flash)  "refactor the tool registry"
  2. 2026-05-13 11:08  (explore, claude-sonnet-4-6) "where is auth handled"
  3. 2026-05-12 22:01  (main, deepseek-v4-pro)   "fix the cron parser bug"
Select [1-3] or q: 1
Resuming session a1b2c3d4-e5f6...
```

Implementation sketch (`cmd/evva/resume.go`):

```go
func resumeCmd(ctx context.Context) error {
    sessions, _ := sessionstore.List()  // sorted newest-first
    pick, err := promptPick(sessions)
    if err != nil { return err }

    snap, _ := sessionstore.Load(pick.ID)
    ag, err := agent.FromSnapshot(snap, agent.WithSink(/* CLI sink */))
    if err != nil { return err }

    resp, err := ag.Continue(ctx)
    // ... render resp, handle ErrIterLimit, etc.
}
```

`agent.FromSnapshot(snap, opts...)` is the only new constructor — it:
1. allocates a new `Agent` with the same ID from snap,
2. rebuilds active tools and rehydrates the ToolState (task store, etc.),
3. reconstructs the `llm.Client` from `snap.Profile` via `llmfactory.Of`,
4. seeds `session.Messages` from the snapshot.

### Continue vs Run on resume

After loading a snapshot:

- If the last message is `RoleAssistant{ToolCall: nil}` → conversation is
  at a clean turn boundary. The natural next call is `Run(ctx, newPrompt)`
  with whatever the user types.
- If the last message is `RoleAssistant{ToolCall: …}` → the previous run
  ended mid-loop (probably crash). The session is malformed for any
  provider that demands pairing. `FromSnapshot` should detect this and
  truncate back to the prior clean boundary, logging a warning.

### Out of scope for v1

- Cross-machine resume (the snapshot includes absolute paths in tool
  history). Mark this in the snapshot version field.
- Snapshot encryption. Sessions may contain secrets pasted by the user;
  the user owns their `~/.evva/`. Document this.

---

## 5. UI Architecture — Bubble Tea Now, Plugin Surface Always

### Phase 1: Bubble Tea TUI

The first UI is a [Bubble Tea](https://github.com/charmbracelet/bubbletea)
program living in `internal/tui/`. It implements `event.Sink` — every
incoming event becomes a Bubble Tea message:

```go
// internal/tui/sink.go
type Sink struct{ p *tea.Program }
func (s Sink) Emit(e event.Event) { s.p.Send(eventMsg{e}) }
```

The TUI's `Update(msg)` handles `eventMsg` by mutating its view model
(append text, open/close subagent panel, update task list, show iter-limit
prompt, etc.). Bubble Tea handles redraws.

Initial layout (kept simple — production polish is its own phase):

```
┌─ evva ───────────────────────────────────────────────┐
│ > user prompt input ▌                                │
├──────────────────────────────────────────────────────┤
│                                                      │
│   main agent conversation pane                       │
│   (nested subagent panel inline when one is running) │
│                                                      │
├──────────────── tasks ───────────────────────────────┤
│ ☐ refactor registry      in_progress                 │
│ ☑ flatten tool dirs      completed                   │
└─ esc cancel · ? help ────────────────────────────────┘
```

### Phase 2+: Plugin-Style UI

The `event.Sink` interface IS the UI plugin contract. Anyone can build
their own renderer — a web UI, an IDE plugin, a native macOS app — by:

1. Implementing `event.Sink` (one method: `Emit(Event)`).
2. Constructing the agent with `agent.WithSink(yourSink)`.
3. Optionally calling `agent.Run` / `Continue` from their own dispatcher.

No UI-specific code lives in the agent. The agent emits; consumers render.

Future helpers we may add to make plugin authoring easier (not v1):

- A WebSocket sink (`event.WSSink`) that streams events over a socket —
  the canonical "remote UI" plumbing.
- A JSON event format (already trivial via the discriminated payload
  fields; standardize the schema).
- A registry for swapping sinks at runtime (e.g. multi-window UIs).

---

## 6. File Structure

```
internal/agent/
├── agent.go              (existing — adds Run, Continue, emit helper, FromSnapshot)
├── types.go              (existing — Profile unchanged)
├── options.go            (NEW — agent.Option, WithSink, WithMaxIterations, asSubagent)
├── loop.go               (NEW — runLoop body shared by Run + Continue)
├── snapshot.go           (NEW — agent.Snapshot + FromSnapshot)
├── event/
│   ├── event.go          (NEW — Event, Kind constants, payload types)
│   └── sink.go           (NEW — Sink, Multi, Discard, BubbleUp)
└── profiles/
    └── profiles.go       (existing)

internal/sessionstore/
├── store.go              (NEW — List, Load, Save against ~/.evva/sessions)
└── snapshot_codec.go     (NEW — JSON encode/decode)

internal/toolset/
├── toolset.go            (existing — rename Builders → ToolState)
└── tags.go               (NEW — toolName → keyword tags map)

internal/tools/task/
├── store.go              (existing — add OnChange hook + MarshalJSON)
└── ...

internal/tui/
├── tui.go                (NEW — Bubble Tea program)
├── sink.go               (NEW — event.Sink → tea messages)
├── view.go               (NEW — rendering)
└── update.go             (NEW — message handlers)
```

No changes to `internal/tools` (except `task/store.go`), `internal/llm`,
`internal/llmfactory`. The runtime and UI live in the agent / sessionstore
/ tui layers.

---

## 7. Implementation Order

1. **event/** package (types + Sink + Multi + Discard + BubbleUp). Pure
   types, easy to land.
2. **Rename `Builders` → `ToolState`** across `internal/toolset`,
   `internal/agent`. Pure mechanical rename.
3. **Tags map** in `internal/toolset/tags.go`; extend `Descriptor.Tags`.
4. **agent options + emit helper** + `MaxIterations`. No behavior change
   yet — just plumb the Sink and limit through New().
5. **loop.go** — implement `Run(ctx, prompt)` + `Continue(ctx)` with the
   loop sketched above, including the `IterLimit` path.
6. **Bash tool** — first real tool. Verifies the loop end-to-end against
   a CLI sink that prints events.
7. **Task tools** — exercises stateful ToolState + the `OnChange` task-
   update event hook.
8. **AGENT tool / subagent bubbling** — exercises `BubbleUp` and the
   "one layer deep" guardrail.
9. **TOOL_SEARCH** — uses `toolset.Describe` + `agent.DeferredNames()` +
   the tag-based matcher.
10. **sessionstore + `/resume`** — snapshot encoding, `FromSnapshot`, CLI
    subcommand.
11. **Bubble Tea TUI** — minimal layout, subscribes to events.
12. **Remaining tools** in any order — each is independent given the
    loop, events, and ToolState.

Each step is independently buildable / testable.

---

## 8. Open Questions / Future

- **Streaming completion**: today `llm.Client.Complete` returns one big
  response. When we add SSE streaming, the loop will emit incremental
  `KindThinking` / `KindText` events as deltas arrive. The Event types
  support this (one event per chunk) without changing the consumer side.
- **Concurrent tool calls**: Anthropic's API now supports parallel
  `tool_use` blocks in one response. Out of scope for v1 — pick one or
  reject the rest. Revisit before TUI polish.
- **Session compaction**: when the message count or token estimate exceeds
  N, trigger `microCompact` (drop tool-result payloads, keep summaries)
  or `fullCompact` (LLM-driven summarization). Scaffolded in `Session`;
  hook into `TurnEnd`.
- **Cross-machine resume**: snapshot format includes absolute paths.
  Either rewrite on load (best-effort) or mark host-bound in v1 and
  defer.
- **Snapshot encryption**: sessions may contain secrets. Document that
  `~/.evva/sessions/` should be treated as sensitive; consider an
  optional age-encrypted store later.
- **Multi-consumer ordering across subagents**: with parallel AGENT calls
  (future), the parent sink sees interleaved events from sibling
  subagents. `AgentID` + `ParentID` is enough to demux; the TUI needs an
  explicit reorder/group pass.
