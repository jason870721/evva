# PRD — StructuredOutput Tool — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build after roadmap slotting.
> **Target release:** TBD (proposed `v1.6+` candidate; small, SDK-facing).
> **Roadmap source:** `CLAUDE.md` → State of v1.0.0 (the SDK v2 surface +
> "separate-module host proof") — this completes the headless/SDK story.
> **Reference source:** `ref/src/tools/SyntheticOutputTool/SyntheticOutputTool.ts`.

---

## 1. TL;DR — what this phase actually is

evva already runs headless: `cmd/evva` has a non-TTY one-shot path
(`runCLI`, `cmd/evva/main.go:138`, selected when
`useTUI := !*noTUI && isTTY(os.Stdout)` is false, `:98`), and the SDK
exposes `agent.New(cfg, opts…)` → `Run(ctx, prompt) (string, error)`
(`pkg/agent/agent.go:83`, `:191`) with `WithHeadlessBypass`
(`pkg/agent/permission_mode.go:84`). What a programmatic caller gets back is
a **prose string** — the model's final free text. If the caller wants
structured data (a list of bugs, a typed verdict, a record to feed the next
pipeline stage), it has to regex-scrape that prose, which is brittle and
defeats the point of embedding an agent.

The **StructuredOutput** tool closes this. The *caller* supplies a JSON
schema; evva registers a one-off tool whose input schema **is** that schema;
the model is instructed to call it exactly once at the end of its work; the
tool's validated input becomes the run's return value as JSON. Ported from
`ref/src/tools/SyntheticOutputTool/`, where the SDK calls
`agent({schema: BUGS_SCHEMA})` and gets typed output back.

Two properties make this small and safe:

- **It's headless-only.** Like ref (`isSyntheticOutputToolEnabled` returns
  `opts.isNonInteractiveSession`), the tool is registered **only** when
  there's no interactive TUI. It never appears in a normal evva session, so
  there's zero interactive-UX surface and zero risk to the TUI path.
- **The provider already enforces the shape.** evva hands every tool's
  `Schema()` to the LLM as its input schema, and Anthropic/OpenAI tool-use
  constrains the model's tool input to that schema provider-side. So the
  schema *is* the contract; Go-side re-validation is a thin defensive check,
  not the load-bearing mechanism (contrast ref's Ajv compile — see §5.2).

Concretely:

1. `pkg/tools/brief`-sibling package `pkg/tools/structured` (or
   `internal/tools/structured` — see §5.6): the `structured_output` tool,
   built **per-run** from a caller schema.
2. A `WithStructuredOutput(schema)` SDK option + the agent plumbing that
   registers the tool (headless only), captures its validated input, and
   makes `Run` return it as JSON.
3. An optional `cmd/evva --output-schema <file>` flag so the CLI one-shot
   path can use it too.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 Headless detection + SDK entry

- `cmd/evva/main.go:98` — `useTUI := !*noTUI && isTTY(os.Stdout)`;
  `:138 runCLI` is "the headless one-shot path used by `-no-tui` and by
  pipes." **`!useTUI` is the non-interactive signal** the tool gates on. No
  new detection needed — thread this boolean into agent construction (it may
  already be implied by which UI is attached; confirm and reuse, don't add a
  second source of truth).
- `pkg/agent/agent.go:83 New(cfg Config, opts …Option)` — the SDK
  constructor + options pattern. `WithStructuredOutput` is a new `Option`.
- `pkg/agent/agent.go:191 Run(ctx, prompt) (string, error)` — returns the
  final string. In structured mode, that string becomes the captured JSON.

### 2.2 The control-flow tool pattern — `internal/tools/mode`

This is the **template** for a tool that affects the agent's control flow.
`EnterPlanMode`/`ExitPlanMode` don't mutate the agent directly — they call
through a **controller interface** obtained via a late-bound lookup closure:

- `internal/tools/mode/controller.go:16 PlanModeController` — the interface
  the tool calls; `:32 ControllerLookup func() PlanModeController` — the
  closure the tool constructor takes (returning `nil` disables the tool).
- The agent **registers itself** as the controller at startup:
  `internal/agent/agent.go:433 a.toolState.SetPlanController(a)` (and
  `:434` for worktree). The agent implements the controller methods
  (`agent.go:1041` region).

StructuredOutput follows this exactly: a `StructuredOutputSink` interface
with one method, a lookup closure passed to the tool constructor, and the
agent registering itself as the sink. When the model calls the tool, the
tool hands the validated input to the sink (the agent), which captures it
and signals the loop to terminate. **No new control-flow mechanism is
invented — reuse the controller idiom.**

### 2.3 Tool plumbing (shared with every other tool)

The `config`/`feedback` single-file tool shape and its registration are
documented in `docs/roadmap/v1/v1-5-config-tool.md` §2 and apply verbatim:

- `pkg/tools/name.go` — add `STRUCTURED_OUTPUT ToolName = "structured_output"`.
- `internal/toolset/builtins.go` — `r.MustRegister(...)` factory.
- `pkg/tools/tool.go` — the `Tool` interface (`Name`/`Description`/`Schema`/
  `Execute`) and `Result{Content, IsError, Metadata}`.
- Tool registration into a profile happens via the active/deferred lists in
  `internal/agent/profiles.go`. **Crucially, this tool is NOT added to the
  static Main profile** — it's registered dynamically only when both
  (a) the session is non-interactive and (b) a schema was supplied. See
  Task 3 for the conditional-registration seam.

### 2.4 Reference (`ref/src/tools/SyntheticOutputTool/SyntheticOutputTool.ts`)

| Element | What it does | Port? |
| --- | --- | --- |
| `SYNTHETIC_OUTPUT_TOOL_NAME = 'StructuredOutput'` | name | evva → `structured_output` (snake_case house style) |
| `isSyntheticOutputToolEnabled({isNonInteractiveSession})` | gate: headless only | **Yes** — gate on `!useTUI` |
| `createSyntheticOutputTool(jsonSchema)` | builds a tool whose `inputJSONSchema` = caller schema; validates input (Ajv) and returns `{structured_output: input}` | **Yes** — Go equivalent: tool's `Schema()` returns the caller schema; `Execute` captures input |
| `prompt()` "You MUST call this tool exactly once at the end…" | steers the model to the channel | **Yes** — port verbatim |
| `isReadOnly`/`isConcurrencySafe`/`!isOpenWorld`; `maxResultSizeChars 100_000` | tool classification | **Yes** — read-only, concurrency-safe, allow-by-default |
| `checkPermissions → allow` | never prompts | **Yes** — default-allow (it's headless + just returns data) |
| WeakMap identity cache of compiled tools | perf for 30–80-call workflows | **No** — evva builds the tool once per run, not per call; cache is unnecessary |
| Ajv `validateSchema` + `compile` | validate the *schema* and the *input* | **Partial** — see §5.2; rely on provider enforcement + a light parse check, skip a full JSON-Schema engine unless evva already has one |

---

## 3. Goal & acceptance criteria

**Goal:** an SDK/CLI caller running evva headlessly can supply a JSON schema
and receive the agent's final answer as JSON matching that schema, instead
of prose — with the feature completely absent from interactive sessions.

Ship is complete when **all** of these pass:

- **A1 — SDK option.** `agent.New(cfg, WithStructuredOutput(schema))`
  registers the `structured_output` tool **iff** the session is
  non-interactive; in an interactive session the option is a no-op (with a
  one-line debug log) and the tool is absent.
- **A2 — Schema is the tool's input schema.** The registered tool's
  `Schema()` returns the caller-supplied schema verbatim, so the LLM's
  tool-use input is provider-constrained to it.
- **A3 — Capture + return.** When the model calls `structured_output`, its
  input is captured and `Run` returns that input serialized as a JSON
  string (not the model's prose). `error` is nil on success.
- **A4 — Terminal call ends the run.** Calling `structured_output`
  terminates the agent loop after that tool batch — the model does not get
  another turn (port of "call exactly once at the end").
- **A5 — Defensive validation.** If the captured input fails a light
  validation (not valid JSON object / missing a top-level `required` key the
  schema names), `Execute` returns `tools.Result{IsError:true, …}` with a
  clear message and the model gets one chance to correct (the loop does not
  terminate on an errored call). Provider enforcement makes this rare; the
  branch exists for non-enforcing providers (§5.2).
- **A6 — No prompt, no UI.** `checkPermissions`/the permission gate
  auto-allows it (read-only, headless); it never raises an approval event.
  No TUI renderer is required (interactive sessions never see it).
- **A7 — Absent by default.** With no `WithStructuredOutput`, no
  `structured_output` tool is registered in any profile, interactive or
  headless. The default tool catalog is unchanged (snapshot/registration
  test).
- **A8 — Invalid caller schema.** `WithStructuredOutput(badSchema)` surfaces
  a construction-time error (or a logged warning + no-op) — a malformed
  schema never half-registers a broken tool.
- **A9 — CLI flag (optional).** `evva -no-tui --output-schema bugs.json -p
  "find bugs"` prints the structured JSON to stdout (A3 via the CLI path).
- **A10 — Tests.** Option gating (interactive vs headless), schema-as-input,
  capture-and-return, terminal-call loop exit, defensive-validation error
  path, absent-by-default. A headless run test with a **scripted/fake
  `llm.Client`** that emits a `structured_output` tool call asserts the
  returned JSON.
- **A11 — Docs + version + changelog.** SDK doc (`docs/evva-sdk/`) gains a
  structured-output example; `docs/sdk-stability.md` notes the new option +
  tool (and its tier); `CHANGELOG.md` + version bump.

---

## 4. Work breakdown (ordered)

### Task 0 — Tool name constant

`pkg/tools/name.go`: add `STRUCTURED_OUTPUT ToolName = "structured_output"`
in the "Others" block. Do first — everything references it.

### Task 1 — The tool (`pkg/tools/structured`)

```
pkg/tools/structured/
├── structured.go      # Tool, New(schema, sinkLookup), Name/Description/Schema/Execute
└── structured_test.go
```

```go
// Sink is the seam back to the owning agent — the analog of
// mode.PlanModeController. The tool hands the validated structured payload
// to the sink, which captures it and ends the run.
type Sink interface {
    CaptureStructuredOutput(payload json.RawMessage)
}
type SinkLookup func() Sink // nil → tool disabled (mirrors ControllerLookup)

type Tool struct {
    schema json.RawMessage
    lookup SinkLookup
}

func New(schema json.RawMessage, lookup SinkLookup) *Tool { … }

func (t *Tool) Name() string            { return string(tools.STRUCTURED_OUTPUT) }
func (t *Tool) Schema() json.RawMessage { return t.schema } // caller schema verbatim (A2)
func (t *Tool) Description() string     { return structuredOutputPrompt } // ported verbatim

func (t *Tool) Execute(_ ctx, logger, raw json.RawMessage) (tools.Result, error) {
    if err := lightValidate(raw, t.schema); err != nil {     // A5, defensive
        return tools.Result{IsError: true, Content: "structured_output: " + err.Error()}, nil
    }
    if sink := t.lookup(); sink != nil {
        sink.CaptureStructuredOutput(raw)                    // A3 capture
    }
    return tools.Result{Content: "Structured output recorded."}, nil
}
```

`structuredOutputPrompt` ports `SyntheticOutputTool.ts:51` verbatim:
*"Use this tool to return your final response in the requested structured
format. You MUST call this tool exactly once at the end of your response to
provide the structured output."*

`lightValidate` (§5.2): assert `raw` parses as a JSON object; if the schema
declares top-level `required`, assert those keys are present. Do **not**
implement a full JSON-Schema validator unless evva already vendors one.

The tool is read-only + concurrency-safe + default-allow. If evva's `Tool`
interface carries those classifications, set them; otherwise the
default-allow comes from a permission default rule (Task 3.3).

### Task 2 — SDK option + agent capture/terminate

**`pkg/agent` option:**

```go
// WithStructuredOutput makes the agent expose a one-off structured_output
// tool whose input schema is `schema`, and makes Run return the model's
// structured payload as JSON instead of prose. No-op (logged) in an
// interactive session — the tool is headless-only.
func WithStructuredOutput(schema json.RawMessage) Option { … }
```

**`internal/agent/agent.go`:**

- Hold `structuredSchema json.RawMessage`, `structuredResult json.RawMessage`,
  and a `structuredDone bool`.
- Implement `Sink`: `CaptureStructuredOutput(p)` stores `p` and sets
  `structuredDone` under the agent's existing lock.
- Register the agent as the structured sink at the same place it registers
  the plan/worktree controllers (`agent.go:433-434`):
  `a.toolState.SetStructuredSink(a)`.
- **Loop termination:** in the agent loop, after a tool batch executes,
  if `structuredDone` → break the loop and have `Run` return
  `string(a.structuredResult)` (A3/A4). This is the structured analog of how
  the loop already ends on `end_turn`; reuse that exit, just with the JSON
  payload as the return string.

### Task 3 — Conditional registration (headless + schema present)

This is the one non-boilerplate seam: the tool must be registered **only
when** (a) non-interactive and (b) a schema was supplied.

**3.1 Thread the non-interactive flag.** `cmd/evva/main.go:98` knows
`!useTUI`. Pass it into agent construction (an existing `Config`/option
field if one exists, else add `Config.NonInteractive bool`). The SDK's
headless callers set it (or it's inferred from no UI attached — pick one
source of truth, §2.3).

**3.2 Register dynamically.** In the agent's tool-assembly path (where
`profiles.go` active/deferred tools become live `tools.Tool` instances),
after building the profile's tools, if `nonInteractive && structuredSchema
!= nil`, append a live `structured.New(schema, lookup)` to the active set.
Do **not** add `STRUCTURED_OUTPUT` to any `Profile.ActiveTools`/`DeferredTools`
in `profiles.go` — those are static and would leak the tool into interactive
sessions. The dynamic append keeps A7 true.

**3.3 Permission default.** Add a default rule `structured_output → allow`
(mirrors `read`), or rely on the tool's read-only self-classification if the
interface supports it. It must never prompt (A6).

### Task 4 — CLI flag (optional, secondary)

`cmd/evva`: a `--output-schema <path>` flag (paired with `-no-tui -p`).
Read the file, `json.Valid` it, pass via `WithStructuredOutput`, and have
`runCLI` print the returned JSON to stdout. Keep it thin — the SDK is the
primary consumer (ref's primary consumer is the SDK `agent({schema})` call).
If scoping tight, ship Tasks 0–3 (SDK) and fast-follow the CLI flag.

### Task 5 — Docs + version

- `docs/evva-sdk/sdk-v2.md` (or a new snippet) — a worked example:
  `WithStructuredOutput` + a schema + the typed result.
- `docs/sdk-stability.md` — record the new option + tool and its stability
  tier (§5.6).
- `CHANGELOG.md` `### Added`; `pkg/version/version.go` bump.

---

## 5. Design decisions & risks

### 5.1 — Capture-and-terminate via the controller idiom

The model signals "I'm done, here's the structured answer" *by calling the
tool*. The tool can't return the value to the caller directly (tools return
`tools.Result` to the loop, not to `Run`), so it hands the payload to the
agent through the `Sink` interface — exactly how `EnterPlanMode` reaches the
agent through `PlanModeController` (`internal/tools/mode/controller.go`). The
agent captures it and ends the loop. This reuses an established, tested
pattern rather than inventing a "tool that returns up the stack."

### 5.2 — Lean on provider schema enforcement; validate defensively

ref compiles the schema with Ajv and validates both the schema and every
input. evva doesn't need the heavy path: it sends `Schema()` to the provider
as the tool's input schema, and Anthropic/OpenAI tool-use **already**
constrain the model's tool input to that schema. So in practice the captured
input matches by construction. The `lightValidate` check (valid JSON object
+ top-level `required` keys present) catches the edge cases — a provider
that doesn't enforce tool schemas (some Ollama models; evva supports Ollama),
or a hand-rolled fake client in tests. If evva later needs strict
validation, add a JSON-Schema dep then — don't pull one in now for a branch
that fires only on non-enforcing providers. (Document this clearly: the
guarantee is "provider-enforced where the provider supports it, best-effort
otherwise.")

### 5.3 — Headless-only is a hard invariant

The tool must never appear in an interactive session — that's what keeps the
TUI path untouched and the blast radius near zero. The gate is checked in
**two** places that must agree: the SDK option is a no-op when interactive
(A1), and the dynamic registration requires `nonInteractive` (Task 3.2). A7
(absent by default) + an interactive-session test (A10) lock it. The risk is
a future refactor that "helpfully" adds `STRUCTURED_OUTPUT` to the static
Main profile — call that out in the code comment on the constant and in the
registration site.

### 5.4 — Steering the model to actually use the channel

Registering the tool isn't enough; the model must *call* it instead of
emitting prose. The tool description ("You MUST call this tool exactly once
at the end") is the primary steer and is usually sufficient in a headless
single-shot. If testing shows the model sometimes answers in prose, add a
one-line system-prompt addendum **only in structured mode** (e.g. "Return
your final answer by calling `structured_output`; do not answer in plain
text"). Keep that addendum behind the same headless+schema gate so it never
touches interactive prompts. Treat it as a fallback, not a default.

### 5.5 — What `Run` returns when the model never calls the tool

Defensive: if the loop ends (end_turn / max iterations) without a
`structured_output` call, `Run` returns the model's prose as today, plus a
non-nil `error` (`ErrNoStructuredOutput`) so the caller can distinguish
"got JSON" from "model declined the channel." Don't silently return prose as
if it were structured — the caller asked for a contract; tell them it wasn't
met.

### 5.6 — `pkg/tools/` vs `internal/tools/`

Unlike `config`/`memory` (evva-runtime-specific), structured output is a
**generic, downstream-valuable** capability — any SDK host wants typed
output. That argues for `pkg/tools/structured` (Stable-candidate,
Experimental tier at first), consistent with the roadmap's reasoning for
putting `BriefTool` in `pkg/tools/brief` (v1.7). The `Sink` interface is the
only evva-coupling, and it's a narrow seam the host implements — same shape
as the existing public tool seams. **Recommended:** `pkg/tools/structured`,
Experimental tier, promote later. (If the team prefers to keep new tools
internal until proven, `internal/tools/structured` is the fallback — note
the choice in the PR.)

### 5.7 — One schema per run, not per call

ref caches per-call because its workflow API builds the tool 30–80×. evva
builds it once at `New` and reuses it for the whole run — no cache needed. If
a future "workflow" API runs many short structured agents, revisit; for now,
one schema per agent instance is the model.

---

## 6. Out of scope

- **Strict full JSON-Schema validation** (§5.2) — provider enforcement +
  light check; add an engine only if a non-enforcing provider becomes a
  first-class structured-output target.
- **Interactive structured output** — the tool is headless-only by design.
- **Multiple/streamed structured outputs per run** (§5.7).
- **Auto-deriving a schema from a Go struct** — a nice SDK ergonomic
  (reflect a struct → JSON schema) but a separate, optional helper; ship the
  raw-schema option first.
- **`BriefTool`/`send_user_message`** — a different channel (user-facing
  messages), already slated as roadmap v1.7; don't conflate.

---

## 7. Verification checklist (PR gate)

- [ ] **Task 0:** `STRUCTURED_OUTPUT` constant added with a comment warning
      against static-profile registration.
- [ ] **Task 1:** tool returns caller schema from `Schema()`; `Execute`
      captures via `Sink` and light-validates; prompt ported verbatim.
- [ ] **Task 2:** `WithStructuredOutput` option; agent implements `Sink`,
      registers at the controller-registration site; loop terminates on
      capture and `Run` returns the JSON.
- [ ] **Task 3:** dynamic registration only when `nonInteractive && schema`;
      **not** in any static profile (A7); default-allow permission.
- [ ] **A1/A7:** interactive session → option no-op, tool absent (test).
- [ ] **A3/A4:** headless run with a scripted client emitting a
      `structured_output` call → `Run` returns the JSON, loop exits (test).
- [ ] **A5:** malformed payload → errored Result, loop continues (test).
- [ ] **A8:** malformed caller schema → construction error / logged no-op.
- [ ] **Task 4 (if shipped):** `--output-schema` prints JSON to stdout.
- [ ] `go build/vet/test ./...` green.
- [ ] **Docs:** SDK example + stability note + CHANGELOG + version bump.
- [ ] **Manual:** `echo '{"type":"object","required":["summary"],
      "properties":{"summary":{"type":"string"}}}' > /tmp/s.json` then a
      headless run with that schema → stdout is JSON with a `summary` key,
      not prose.

---

## 8. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `pkg/tools/name.go` | Edit — add `STRUCTURED_OUTPUT` | Task 0 |
| `pkg/tools/structured/structured.go` | **New** — tool + `Sink`/`SinkLookup` | Task 1 |
| `pkg/tools/structured/structured_test.go` | **New** | Task 1, 10 |
| `pkg/agent/options.go` (or where `Option`s live) | Edit — `WithStructuredOutput` | Task 2 |
| `internal/agent/agent.go` | Edit — schema/result fields, `Sink` impl, `SetStructuredSink`, loop-terminate, `Run` return | Task 2 |
| `internal/agent/toolstate` / `internal/toolset` sink registration | Edit — `SetStructuredSink` seam (mirror `SetPlanController`) | Task 2/3 |
| tool-assembly path (dynamic register) | Edit — append tool iff `nonInteractive && schema` | Task 3.2 |
| `pkg/permission` defaults | Edit — `structured_output → allow` (or rely on read-only class) | Task 3.3 |
| `cmd/evva/main.go` | Edit — thread `!useTUI`; optional `--output-schema` flag | Task 3.1, 4 |
| `internal/toolset/builtins.go` | Edit (only if a static factory is needed for tests) | Task 1 |
| `pkg/version/version.go`, `CHANGELOG.md`, `docs/evva-sdk/*`, `docs/sdk-stability.md` | Edit | Task 5 |

---

## 9. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 0 — constant | ~5 | 5 min |
| Task 1 — tool + light validate | ~120 | 1.5 h |
| Task 2 — option + sink + capture/terminate | ~120 | 2.5 h |
| Task 3 — conditional registration + permission | ~60 | 1.5 h |
| Task 4 — CLI flag (optional) | ~50 | 1 h |
| Task 5 — docs + changelog + version | ~50 | 45 min |
| Tests | ~250 | 2.5 h |

Total: ~650–700 LOC, ~9–10 hours focused. The smallest of the three
PRDs: no new data layer (vs. memory), no prompt-composition refactor (vs.
output styles). The only real design work is the capture/terminate seam
(Task 2), and that reuses the existing plan-mode controller idiom.
