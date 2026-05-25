# v1.2 — OpenAI Provider — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** ready to build.
> **Target release:** `v1.2.0` (additive, minor bump under the Stable-tier promise).
> **Roadmap source:** `CLAUDE.md` → Roadmap → *v1.2 — OpenAI provider*.

---

## 1. TL;DR — what this phase actually is

This phase **closes a crash path the constant table already promises.**
`pkg/constant/llm.go:15` declares `OPENAI = LLMProvider{Name: "openai", …, Models: []Model{GPT_5_5}}` and the config / UI layers act as if it works:

- `pkg/config/from_home.go:39` registers OpenAI credentials.
- `pkg/config/file_config.go:92` writes an `openai:` block into a fresh `evva-config.yml`.
- `pkg/ui/bubbletea/components/overlays/config.go:343` shows `openai.api_key` / `openai.api_url` editable fields.
- `pkg/ui/bubbletea/components/overlays/model.go:57` enumerates OpenAI models in the `/model` picker.

But `pkg/llm/builtins/builtins.go` only registers `claude`, `deepseek`, `ollama` — selecting any OpenAI model lands in `llm.DefaultRegistry().Build("openai", …)` → `"unknown provider"`, surfaced by `internal/agent/llm.go:25`. The user-guide already pictures `openai / gpt-5.5  (current)` in the picker (`docs/user-guide/en/user-guide.md:146`), so the integrity defect is the same shape as v1.1's hooks gap: the surface promises something the runtime doesn't deliver.

**This is a small, additive build phase**, not an integration phase like v1.1. Concretely:

1. **Build** `pkg/llm/openai/` — `Client`, `Factory`, `ProviderName`. DeepSeek is the template (OpenAI-compatible Chat Completions API); the bulk of the work is a focused port with a handful of OpenAI-specific deviations called out in §5.
2. **Register** the factory in `pkg/llm/builtins`.
3. **Reconcile** the placeholder `GPT_5_5` model id with real OpenAI model ids in `pkg/constant/llm.go`.
4. **Test** the package (parity with DeepSeek's stream test, plus OpenAI-specific deviations).
5. **Document** + bump version.

Do **not** invent a new provider abstraction or refactor `pkg/llm`. The provider seam (`llm.Client` + `llm.ClientFactory` + `Registry.Build`) is Stable and unchanged by this phase.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 `pkg/constant/llm.go` (Stable) — the placeholder

```go
OPENAI    = LLMProvider{Name: "openai", ApiUrl: "https://api.openai.com", Models: []Model{GPT_5_5}}

// OPENAI
GPT_5_5 Model = "gpt-5.5"

MODEL_CONTEXT_SIZE = map[Model]int{
    …
    GPT_5_5:           500_000,
}
```

`GPT_5_5 = "gpt-5.5"` is **bogus** — there is no OpenAI model with that id. Reconciliation is part of this phase (Task 0). The constant slot, name, default URL, and the `GetAllProviders()` enumeration are all fine as-is.

### 2.2 `pkg/llm/deepseek/` (Stable) — the template

DeepSeek is the closest existing implementation:

- DeepSeek's chat endpoint is the OpenAI Chat Completions API (`POST /chat/completions`), nearly byte-compatible.
- Tool calling wire shape is identical: `tools: [{type:"function", function:{name, description, parameters}}]` request side, `tool_calls: [{id, type:"function", function:{name, arguments}}]` response side, role `"tool"` for results.
- SSE streaming framing is identical: `data: <json>\n` per frame, `[DONE]` terminator, choice-keyed deltas.
- Usage carries `prompt_tokens` / `completion_tokens` / `total_tokens`; cache stats live in `prompt_cache_hit_tokens` (DeepSeek's name) vs `prompt_tokens_details.cached_tokens` (OpenAI's name) — the **one usage shape divergence**.

| File | What to port | Lines |
| --- | --- | --- |
| `pkg/llm/deepseek/client.go` | All of it; rename, swap headers, swap effort map, swap usage parsing | 528 |
| `pkg/llm/deepseek/factory.go` | Direct port — change `ProviderName`, `New` reference | 14 |
| `pkg/llm/deepseek/client_test.go` | Adapt `TestDeepseekEffort` → `TestOpenAIEffort` | 35 |
| `pkg/llm/deepseek/stream_test.go` | Adapt fixture to OpenAI usage shape | 109 |

Total expected new code in `pkg/llm/openai/`: ~600 LOC (client + factory + ~200 LOC tests). Roughly the size of DeepSeek's whole package.

### 2.3 `pkg/llm/builtins/builtins.go` (Internal helper)

```go
func init() {
    r := llm.DefaultRegistry()
    r.MustRegister(claude.ProviderName,   claude.Factory)
    r.MustRegister(deepseek.ProviderName, deepseek.Factory)
    r.MustRegister(ollama.ProviderName,   ollama.Factory)
}
```

One-line edit: add `r.MustRegister(openai.ProviderName, openai.Factory)`. The accompanying test (`builtins_test.go:17`) gains one entry in its `for _, name := range […]` loop.

### 2.4 `pkg/llm/registry.go` + `pkg/llm/client.go` (Stable)

The `Client` interface (`pkg/llm/client.go:15`) is the contract — six methods:

```go
type Client interface {
    Name() string
    Model() string
    SupportsDeferLoading() bool
    Complete(ctx, messages, tools) (Response, error)
    Stream(ctx, messages, tools, sink) (Response, error)
    Apply(opts ...Option)
}
```

OpenAI's client satisfies all six. The cancellation contract (return an error matching `llm.ErrInterrupted` via `errors.Is`) is the same as every other provider — port the `llm.NormalizeErr(err)` pattern verbatim.

### 2.5 `pkg/config` + `pkg/agent` (Stable) — already OpenAI-aware

- `pkg/config/from_home.go:39` wires `cfg.LLMProviderConfig["openai"]` from `providers.openai` in the YAML.
- `pkg/config/file_config.go:92` writes an empty `openai: {}` stanza on first-launch.
- `internal/agent/llm.go:25` calls `llm.DefaultRegistry().Build(provider.Name, …)` — provider-agnostic. Once the factory is registered, this path works.
- `pkg/agent/agent.go:30`'s godoc lists `"openai"` as a valid `Config.Provider` value (it has all along; only the runtime caught up here).

### 2.6 Reference source (`ref/src/`)

The reference TypeScript source has **no OpenAI implementation** — Claude Code targets Anthropic only. There is nothing to port from `ref/`. DeepSeek's package is the substitute reference, as called out in `CLAUDE.md`'s v1.2 entry.

---

## 3. Goal & acceptance criteria

**Goal:** a user with an OpenAI API key in `~/.evva/config/evva-config.yml` (or `OPENAI_API_KEY` via the config UI) can select an OpenAI model in `/model`, send a prompt, and get a full ReAct turn back — including tool calls, streaming, and the effort knob — with the same fidelity DeepSeek delivers today.

Ship is complete when **all** of these pass:

- **A1 — Factory wired.** `llm.DefaultRegistry().Has("openai")` returns true after importing `pkg/llm/builtins`. Selecting the OpenAI provider via `/model` no longer errors with `"unknown provider"`.
- **A2 — Complete.** A non-streaming `Complete` call against a real OpenAI key returns assistant text, tool calls (when the model emits them), and a populated `llm.Usage` (`InputTokens`, `OutputTokens`, `CacheReadTokens` from `prompt_tokens_details.cached_tokens`, and `ReasoningTokens` from `completion_tokens_details.reasoning_tokens` when present).
- **A3 — Stream.** A streaming `Stream` call surfaces text deltas as `llm.ChunkText` chunks in arrival order, accumulates tool-call argument fragments by index into the final `Response.ToolCalls`, parses the terminal usage frame, and returns on `data: [DONE]`. Note: OpenAI Chat Completions does **not** stream reasoning content, so `ChunkThinking` is not emitted (see §5).
- **A4 — Effort mapping.** `WithEffort(level)` translates to OpenAI's `reasoning_effort` parameter per the table in §4 Task 2.4; level 0 omits the parameter entirely; unknown levels also omit it (no error).
- **A5 — Cancellation.** Cancelling the request context aborts both `Complete` and `Stream` promptly and returns an error matching `errors.Is(err, llm.ErrInterrupted)`. The streaming scanner honors `ctx.Err()` between SSE frames like DeepSeek's does.
- **A6 — Defer-loading honesty.** `SupportsDeferLoading()` returns **false**. OpenAI has no defer-loading-equivalent server-side mechanism; mutating the tools array between turns would invalidate automatic prompt caching. (Same posture as DeepSeek and Ollama; same justification.)
- **A7 — Models reconciled.** `pkg/constant/llm.go` lists real OpenAI model ids — at minimum a "fast" / "pro" pair so `LLMProvider.ModelForLevel(1|2)` resolves to sensible models. `GPT_5_5` is gone. `MODEL_CONTEXT_SIZE` entries match (the documented context window of) each declared model.
- **A8 — Builtins membership.** `pkg/llm/builtins.TestBuiltinsRegistered` (already an existence-style test) is extended to include `openai.ProviderName`.
- **A9 — Stream parity test.** `pkg/llm/openai/stream_test.go` exercises a canned SSE byte stream covering: text deltas, multi-fragment tool-call arguments, terminal usage frame, `[DONE]` terminator, ctx-cancel returning `ErrInterrupted`. Same shape as `pkg/llm/deepseek/stream_test.go` and `pkg/llm/claude/stream_test.go`.
- **A10 — Effort unit test.** `pkg/llm/openai/client_test.go` exercises the effort-level → API string mapping (mirrors `TestDeepseekEffort` / `TestAnthropicEffort`).
- **A11 — Docs + version + changelog.** `docs/sdk-stability.md` mentions `pkg/llm/openai` in the same row as the other provider packages. `docs/extending.md` (line 24) updates "anthropic/deepseek/ollama" → "anthropic/deepseek/openai/ollama". `docs/user-guide/en/user-guide.md` line 786's import comment is updated. The zh-tw mirror gets the same update. `CHANGELOG.md` gains a `## [v1.2.0]` entry. `pkg/version.Version` → `"1.2.0"`.
- **A12 — No constant promises an unimplemented provider** (the CLAUDE.md goal sentence, verbatim). After this PR, every name in `constant.GetAllProviders()` resolves through `llm.DefaultRegistry().Build` without "unknown provider" errors.

---

## 4. Work breakdown (ordered)

### Task 0 — Reconcile placeholder model ids

**File:** `pkg/constant/llm.go`.

**First action — verify current OpenAI model list.** Before writing any code, run `web_search` for "OpenAI API model list 2025 2026" and `web_fetch` the OpenAI platform docs at `platform.openai.com/docs/models`. Model id strings are the single brittle thing in this phase — do not guess. Pin the fast/pro pair from the live model list and note their documented context windows.

Recommended candidate pair (confirm against live docs at implementation time):

```go
var (
    …
    OPENAI = LLMProvider{
        Name:   "openai",
        ApiUrl: "https://api.openai.com",
        Models: []Model{GPT_5_MINI, GPT_5},   // ordered fast → pro
    }
)

const (
    …
    // OPENAI
    GPT_5_MINI Model = "gpt-5-mini"
    GPT_5      Model = "gpt-5"
)

var MODEL_CONTEXT_SIZE = map[Model]int{
    …
    GPT_5_MINI: 400_000,   // confirm at impl time
    GPT_5:      400_000,   // confirm at impl time
}
```

Notes:

- Delete `GPT_5_5` entirely. It was never referenced outside this file (`grep -rn "GPT_5_5"` confirms — only `pkg/constant/llm.go` mentions it).
- The user-guide picker mockup at `docs/user-guide/en/user-guide.md:146` (`│   openai / gpt-5.5                                           │`) and the zh-tw mirror at `:153` will need their ASCII art updated to the new ids in Task 5.
- `LLMProvider.ModelForLevel(1)` returns `Models[0]` (`gpt-5-mini`) — confirm `pkg/constant/llm.go:71` still does the right thing with the new pair (it will: zero behavioral change to the helper itself).
- After settling on the model ids, add a comment above the `OPENAI` var block categorizing each model as reasoning or non-reasoning. The `isReasoningModel` stub in Task 2.3 depends on this. Example:
  ```go
  // OPENAI — all currently listed models are reasoning-class (gpt-5 / o-series).
  // If a non-reasoning model (gpt-4*, gpt-3.5*) is added here later, update
  // pkg/llm/openai/client.go isReasoningModel to match.
  ```

**Do this first.** Every subsequent edit in `pkg/llm/openai/` and the tests will reference the real constants — so reconciling them up front avoids a second pass on imports.

### Task 1 — Create the `pkg/llm/openai/` package skeleton

Create three files mirroring DeepSeek's layout:

```
pkg/llm/openai/
├── client.go      # ~340 LOC: New, Complete, wire types, conversion helpers
├── factory.go     # ~15  LOC: ProviderName + Factory adapter
├── stream.go      # ~190 LOC: Stream + consumeStream (split out of client.go
│                  #            so client.go matches DeepSeek's eventual split
│                  #            target; DeepSeek currently bundles them but
│                  #            Claude already splits)
├── client_test.go # effort-mapping unit test + any toAPI* tests
└── stream_test.go # canned SSE fixture exercising consumeStream
```

> **Split rationale:** Claude already keeps `client.go` (buffered Complete + types + converters) separate from `stream.go` (SSE consumer). DeepSeek bundles both in one file; OpenAI is a new package — start with the Claude layout so the package doesn't outgrow a single file later. ~340 + ~190 LOC is a healthier split than 528 in one file.

**Package clause:** `package openai`. Imports mirror DeepSeek's: `pkg/constant`, `pkg/llm`, `pkg/tools`, plus stdlib `bufio`, `bytes`, `context`, `encoding/json`, `errors`, `fmt`, `io`, `net/http`, `strings`.

### Task 2 — Port the client, with five OpenAI-specific deviations

Port from `pkg/llm/deepseek/client.go` verbatim except for the items below. Anywhere the DeepSeek code references `constant.DEEPSEEK` / `DefaultModel = "deepseek-v4-flash"` / `chatPath = "/chat/completions"` / `"deepseek:"` error prefixes, swap to OpenAI equivalents.

**2.1 — Constants.**

```go
const (
    DefaultModel = "gpt-5-mini"           // matches constant.GPT_5_MINI
    chatPath     = "/v1/chat/completions" // OpenAI uses /v1/ prefix; DeepSeek omits it
)
```

> **Path note:** OpenAI's chat endpoint is `/v1/chat/completions`. DeepSeek's effective endpoint is also `/v1/chat/completions`, but DeepSeek's `ApiUrl = "https://api.deepseek.com"` and `chatPath = "/chat/completions"` — DeepSeek collapses the `/v1/` because their base URL already includes it for some routes. **Do not** copy DeepSeek's `chatPath = "/chat/completions"` literally; OpenAI requires `/v1/chat/completions`.

**2.2 — Authentication header.** OpenAI uses the same `Authorization: Bearer <key>` pattern DeepSeek uses, plus an optional `OpenAI-Organization` / `OpenAI-Project` header. v1.2 sends only `Authorization` — org/project headers stay out of scope (`APIConfig` has no field for them and the YAML doesn't expose one).

```go
req.Header.Set("content-type", "application/json")
req.Header.Set("authorization", "Bearer "+c.apiKey)
```

**2.3 — Sampling-parameter omission for reasoning models.** OpenAI's gpt-5 family rejects requests that set `temperature`, `top_p`, or `top_k` to non-default values — the documented behavior is "fixed at 1, do not send." DeepSeek accepts every sampling knob.

The safest behavior in v1.2: **honor `c.params.Temperature` / `c.params.TopP` only when explicitly set** (they already are pointer-typed in `llm.LLMParams`, so `nil` means unset). Then omit them in the request body for reasoning-class models. The simplest discriminator: maintain a small allowlist of non-reasoning model prefixes — `gpt-4*`, `gpt-3.5*`, `text-*` — and **send sampling params only when the model matches that allowlist; otherwise unconditionally drop them.**

```go
// stripSamplingForReasoning returns a copy of params with Temperature / TopP
// nil-ed out for reasoning-class models. OpenAI's gpt-5 / o-series reject
// non-default sampling; the older gpt-4 family accepts them.
func stripSamplingForReasoning(p llm.LLMParams, model string) llm.LLMParams {
    if isReasoningModel(model) {
        p.Temperature = nil
        p.TopP = nil
        // TopK is OpenAI-unrecognized regardless, but evva omits it via omitempty.
    }
    return p
}

func isReasoningModel(model string) bool {
    // TODO(isReasoningModel): when a non-reasoning model (gpt-4*, gpt-3.5*,
    // text-*) is added to constant.OPENAI.Models, grow this allowlist.
    // The corresponding constant block in pkg/constant/llm.go carries a
    // matching comment — update both together.
    //
    // Conservative: every model evva currently ships in constant.OPENAI is a
    // reasoning model (gpt-5 / o-series). Returning true unconditionally is
    // correct for v1.2.
    return true
}
```

> **Decision rationale:** keeping the allowlist trivially-true today (since `Models: []Model{GPT_5_MINI, GPT_5}` is reasoning-only) makes the wiring obvious and the future extension point clear. The `TODO(isReasoningModel)` marker links the stub to the companion comment in `pkg/constant/llm.go` (Task 0) so a future engineer adding a non-reasoning model is reminded to update both sites. Don't over-engineer; the moment a non-reasoning model is added, that's when `isReasoningModel` grows real logic.

**2.4 — Effort mapping.** OpenAI's `reasoning_effort` enum is **`"low" | "medium" | "high"`** (gpt-5 added `"minimal"` as a fourth tier; the o-series originally had only the three). DeepSeek's `"medium" | "high" | "xhigh" | "max"` mapping does **not** translate. The replacement:

```go
// openaiEffort maps evva effort levels to OpenAI's reasoning_effort.
//
//   0 → ""        (parameter omitted; OpenAI chooses the default)
//   1 → "low"     (evva "low")
//   2 → "medium"  (evva "medium", default)
//   3 → "high"    (evva "high")
//   4 → "high"    (evva "ultra" caps at OpenAI's top tier)
//
// Sending an out-of-range value would 400 from OpenAI; "high" is the
// honest cap. Note this differs from Anthropic's mapping where ultra → "max".
func openaiEffort(effort int) string {
    switch effort {
    case 1:
        return "low"
    case 2:
        return "medium"
    case 3, 4:
        return "high"
    default:
        return ""
    }
}
```

In the request body, set `ReasoningEffort: openaiEffort(c.params.Effort)`. DeepSeek's `Thinking *apiThinking` field has **no OpenAI counterpart** in the Chat Completions wire shape — drop the struct field entirely. OpenAI's reasoning mode is implicit in the model id (you don't toggle it on per-request via Chat Completions; you toggle it by choosing a reasoning-capable model).

**2.5 — Usage-shape divergence.** DeepSeek's `apiResponse.Usage` carries `prompt_cache_hit_tokens` / `prompt_cache_miss_tokens` at the top level. OpenAI nests cache stats under `prompt_tokens_details`:

```go
type apiResponse struct {
    Choices []struct {
        Message      apiMessage `json:"message"`
        FinishReason string     `json:"finish_reason"`
    } `json:"choices"`
    Usage *struct {
        PromptTokens            int `json:"prompt_tokens"`
        CompletionTokens        int `json:"completion_tokens"`
        TotalTokens             int `json:"total_tokens"`
        PromptTokensDetails     *struct {
            CachedTokens int `json:"cached_tokens"`
        } `json:"prompt_tokens_details,omitempty"`
        CompletionTokensDetails *struct {
            ReasoningTokens int `json:"reasoning_tokens"`
        } `json:"completion_tokens_details,omitempty"`
    } `json:"usage,omitempty"`
    Error *struct {
        Type    string `json:"type"`
        Message string `json:"message"`
    } `json:"error,omitempty"`
}
```

Map to `llm.Usage`:

```go
if parsed.Usage != nil {
    out.Usage = llm.Usage{
        InputTokens:  parsed.Usage.PromptTokens,
        OutputTokens: parsed.Usage.CompletionTokens,
    }
    if d := parsed.Usage.PromptTokensDetails; d != nil {
        out.Usage.CacheReadTokens = d.CachedTokens
    }
    if d := parsed.Usage.CompletionTokensDetails; d != nil {
        out.Usage.ReasoningTokens = d.ReasoningTokens
    }
}
```

Apply the same shape to `streamChunk.Usage` in Task 3.

**Everything else mirrors DeepSeek byte-for-byte**: `apiMessage` / `apiToolCall` / `apiTool` / `apiRequest` shapes; `toAPIMessages` / `toAPITools` converters; the `apiMessage.Content` "always present, never omitempty" rule (DeepSeek's comment about strict deserialization at `pkg/llm/deepseek/client.go:74-78` applies verbatim to OpenAI — assistant messages carrying only `tool_calls` still need `content: ""`); the `reasoning_content` echo-back is **dropped** (OpenAI Chat Completions does not surface it; see 2.4 / §5).

**Drop these DeepSeek fields entirely:**

- `apiMessage.ReasoningContent` — no OpenAI counterpart in Chat Completions.
- `apiRequest.Thinking *apiThinking` — no OpenAI counterpart.

### Task 3 — Port the SSE stream consumer

Port `pkg/llm/deepseek/client.go`'s `Stream` + `consumeStream` + `streamChunk` + `streamToolCallDelta` + `streamingToolCall` into `pkg/llm/openai/stream.go`. Adjustments:

- **Headers, URL, error prefix** — swap as in Task 2.
- **`streamChunk.Choices[].Delta`** — drop `ReasoningContent` (OpenAI Chat Completions does not emit it). `ChunkThinking` is therefore never emitted by this client. The `Thinking strings.Builder` accumulator and the assembly into `out.Thinking` can be dropped too. (Don't keep dead code; if the executor finds a use case for an empty `Thinking` value later, re-add it then.)
- **`streamChunk.Usage`** — port the `PromptTokensDetails.CachedTokens` / `CompletionTokensDetails.ReasoningTokens` shape from Task 2.5.
- **Streaming inclusion of usage** — same `stream_options.include_usage: true` knob DeepSeek uses; the field name and behavior are identical.
- **Cancellation** — same `errors.Is(ctx.Err(), context.Canceled)` → `llm.ErrInterrupted` path. Same `bufio.Scanner` 1 MB buffer headroom.

The SSE parsing loop itself is **identical** to DeepSeek's — same `data: ` prefix, same `[DONE]` terminator, same per-index tool-call accumulator pattern. The reasoning_content branch in DeepSeek's loop simply doesn't exist for OpenAI.

### Task 4 — Register the factory + tests

**4.1 — Factory.** Copy `pkg/llm/deepseek/factory.go` and adjust:

```go
package openai

import "github.com/johnny1110/evva/pkg/llm"

const ProviderName = "openai"

func Factory(cfg llm.APIConfig, model string, opts ...llm.Option) (llm.Client, error) {
    return New(cfg, model, opts...), nil
}
```

**4.2 — Register in builtins.** `pkg/llm/builtins/builtins.go`:

```go
import (
    "github.com/johnny1110/evva/pkg/llm"
    "github.com/johnny1110/evva/pkg/llm/claude"
    "github.com/johnny1110/evva/pkg/llm/deepseek"
    "github.com/johnny1110/evva/pkg/llm/ollama"
    "github.com/johnny1110/evva/pkg/llm/openai"   // ← new
)

func init() {
    r := llm.DefaultRegistry()
    r.MustRegister(claude.ProviderName,   claude.Factory)
    r.MustRegister(deepseek.ProviderName, deepseek.Factory)
    r.MustRegister(ollama.ProviderName,   ollama.Factory)
    r.MustRegister(openai.ProviderName,   openai.Factory)   // ← new
}
```

The package's godoc comment ("registers evva's bundled LLM providers (Anthropic, DeepSeek, Ollama)") gains "OpenAI". Update both occurrences in `pkg/llm/builtins/builtins.go:1-10`.

**4.3 — Extend the existence test.** `pkg/llm/builtins/builtins_test.go:17` — add `openai.ProviderName` to the slice:

```go
for _, name := range []string{
    claude.ProviderName,
    deepseek.ProviderName,
    ollama.ProviderName,
    openai.ProviderName,   // ← new
} {
```

Add the `openai` import to the test file.

**4.4 — Effort unit test.** `pkg/llm/openai/client_test.go` — mirror `pkg/llm/deepseek/client_test.go`'s `TestDeepseekEffort`:

```go
func TestOpenAIEffort(t *testing.T) {
    tests := []struct {
        level int
        want  string
    }{
        {0, ""},
        {1, "low"},
        {2, "medium"},
        {3, "high"},
        {4, "high"},   // ultra caps at OpenAI's top tier
        {5, ""},       // out-of-range
    }
    for _, tt := range tests {
        if got := openaiEffort(tt.level); got != tt.want {
            t.Errorf("openaiEffort(%d) = %q, want %q", tt.level, got, tt.want)
        }
    }
}
```

If the executor adds the `isReasoningModel` allowlist (Task 2.3), include a small unit test that pins the current model set as reasoning-only — so the day a non-reasoning model lands the test reminds the engineer to update the allowlist.

**4.5 — Stream parity test.** `pkg/llm/openai/stream_test.go` — adapt `pkg/llm/deepseek/stream_test.go`'s `TestConsumeStream`:

- **Drop the `reasoning_content` deltas** from the fixture; they don't exist on OpenAI.
- **Change the terminal usage frame** to OpenAI shape:

  ```
  data: {"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":{"reasoning_tokens":7}}}
  ```

- **Drop the `wantChunks` thinking entries.** OpenAI streams text and tool-call deltas; no `ChunkThinking` is expected.
- **Keep** the multi-fragment tool-call accumulation case (`{"msg"` then `:"hi"}` in two fragments) — it's the most load-bearing scenario in the parser.
- **Keep** `TestConsumeStreamCancel`. Same shape; pre-cancelled ctx must return `llm.ErrInterrupted`.

The expected final `Response` for the adapted test:

```go
resp.Content == "Hello world"
resp.Thinking == ""                        // OpenAI does not stream reasoning
len(resp.ToolCalls) == 1
resp.ToolCalls[0].ID == "tc_1"
resp.ToolCalls[0].Name == "echo"
string(resp.ToolCalls[0].Input) == `{"msg":"hi"}`
resp.Usage.InputTokens == 12
resp.Usage.OutputTokens == 34
resp.Usage.CacheReadTokens == 5            // from prompt_tokens_details
resp.Usage.ReasoningTokens == 7            // from completion_tokens_details
```

**4.6 — `TestComplete` round-trip test against `httptest.Server` (required).** DeepSeek and Claude don't have one; OpenAI is a fresh wire path so an `httptest`-backed `TestComplete` that asserts the request body shape (Authorization header, JSON body, model field, sampling params dropped for reasoning models) catches regressions that an effort-mapping unit test can't. Worth ~50 LOC.

### Task 5 — Docs + version

**5.1 — `docs/sdk-stability.md`.** The provider sub-packages aren't individually tabled there today (only `pkg/llm/builtins` is). Add no new row; but update the godoc on `pkg/llm/builtins/builtins.go:1-10` (the canonical mention list) and `docs/extending.md:24`:

```
| `pkg/llm/builtins` | side-effect `init()` registering anthropic/deepseek/openai/ollama | Blank-import to get evva's bundled providers |
| `pkg/llm/{claude,deepseek,openai,ollama}` | direct provider client constructors and `Factory` helpers | Reusing one of evva's bundled clients without going through the registry |
```

**5.2 — `docs/user-guide/en/user-guide.md`.**

- Line 786: `_ "github.com/johnny1110/evva/pkg/llm/builtins" // register anthropic/deepseek/ollama` → `… // register anthropic/deepseek/openai/ollama`.
- Line 146 (the picker mockup): `│   openai / gpt-5.5                                           │` → `│   openai / gpt-5                                             │` (and add a second line for `gpt-5-mini`, preserving column alignment).
- Lines 702 (the YAML stanza) — `openai:    { api_key: "", api_url: "" }` is already correct.

**5.3 — `docs/user-guide/zh-tw/user-guide.md`.** Mirror lines 153 and the import comment. The bilingual convention is established by the v1.1 doc updates; keep the translation surface-only.

**5.4 — `CHANGELOG.md`.** Add a `## [v1.2.0]` block above the current `## [v1.1.0]` entry:

```markdown
## [v1.2.0] — OpenAI provider

Closes the OpenAI integrity gap. The `constant.OPENAI` provider, the
`openai.api_key` / `openai.api_url` config fields, and the `/model` picker
already promised OpenAI as a bundled provider, but `pkg/llm/builtins` only
registered Anthropic / DeepSeek / Ollama — selecting OpenAI failed with
`"unknown provider"`. This release ships `pkg/llm/openai` (a focused
Chat-Completions port of `pkg/llm/deepseek` with the OpenAI-specific
deviations called out) and registers it via the builtins side-effect, so
every name in `constant.GetAllProviders()` now resolves through the
factory.

### Added

- **`pkg/llm/openai`** — new bundled provider implementing the full
  `llm.Client` contract over OpenAI's Chat Completions API. Supports
  streaming, tool calling, automatic prompt caching (server-side; reported
  via `Usage.CacheReadTokens`), and reasoning-effort levels mapped onto
  OpenAI's `reasoning_effort` enum (`low` / `medium` / `high`).
- **OpenAI factory registered in `pkg/llm/builtins`** — blank-importing
  `pkg/llm/builtins` now wires anthropic, deepseek, openai, **and** ollama.

### Changed

- **`pkg/constant/llm.go`** — replaced the `GPT_5_5` placeholder with the
  real OpenAI model ids (`GPT_5`, `GPT_5_MINI`). `MODEL_CONTEXT_SIZE`
  updated to match.

### Notes

- `openai.Client.SupportsDeferLoading()` returns `false`. OpenAI relies on
  automatic prefix-prompt caching; the agent must therefore keep the
  `tools` array stable across turns — same posture as DeepSeek and Ollama.
- Sampling parameters (`temperature`, `top_p`) are silently dropped for
  reasoning-class OpenAI models (the gpt-5 / o-series fix these at 1).
  The non-reasoning allowlist is empty in this release; revisit when the
  first non-reasoning OpenAI model is added to `constant.OPENAI.Models`.
- Reasoning content is **not** streamed (OpenAI Chat Completions does not
  surface it). For reasoning visibility, use the Anthropic or DeepSeek
  providers, both of which emit `llm.ChunkThinking` deltas.
```

**5.5 — `pkg/version/version.go`.** `const Version = "1.1.0"` → `const Version = "1.2.0"`.

---

## 5. Design decisions & risks (read before coding)

- **DeepSeek is the structural template; Claude is the per-package layout template.** Port the wire types and conversion logic from DeepSeek (it's already an OpenAI-compatible client); use Claude's `client.go` / `stream.go` split for file organization. The hybrid is intentional — DeepSeek's package grew under one file because it was the second provider; OpenAI joins as the fourth, so start with the split.
- **`SupportsDeferLoading()` returns `false`.** This may look surprising given the `pkg/llm/client.go:21-23` docstring lists "(Anthropic, OpenAI)" as the providers that natively support it. The truth is **no provider in evva returns true today** — Anthropic's `defer_loading` + `tool_reference` pipeline isn't wired in the Anthropic client either (see `pkg/llm/claude/client.go:86-91`). Until the agent layer learns to dynamically promote deferred tools and providers learn to send `defer_loading: true`, every client returns `false`. OpenAI in v1.2 follows that convention. A future phase will flip both Anthropic and OpenAI together when the agent-side machinery lands; this is **not** a v1.2 concern.
- **No thinking-content stream.** OpenAI's Chat Completions API does not surface model reasoning even for the gpt-5 / o-series; you get a `reasoning_tokens` count in usage and nothing else. The Responses API (`/v1/responses`) does surface reasoning summaries, but it has incompatible streaming semantics and would be a non-trivial second wire format. v1.2 uses Chat Completions exclusively. Users who want visible thinking should select Anthropic or DeepSeek; the `/effort` knob still works on OpenAI but its visible effect is opaque (the model thinks longer or harder, but evva sees only the answer).
- **Sampling-knob omission is provider-correct, not evva-eccentric.** OpenAI's gpt-5 / o-series **reject** non-default `temperature` / `top_p` / `top_k`. Stripping them is the documented contract; sending them is the bug. The DeepSeek client passes them through because DeepSeek's API tolerates them. This is the one place the executor will be tempted to "just port DeepSeek verbatim" and produce a 400-rejecting client.
- **OpenAI org / project headers are out of scope.** `OpenAI-Organization` and `OpenAI-Project` headers would let a user route requests under a specific org/project; v1.2 doesn't expose them. The config schema (`APIConfig{ApiURL, ApiSecret, Models}`) has no slot, and adding one is a config-schema change that bleeds into `pkg/config`, the YAML loader, the file_config defaults, the config overlay, and the docs. If demand surfaces, add a v1.2.x point release; do not bundle it here.
- **Model id strings are the only brittle thing.** `gpt-5` / `gpt-5-mini` reflect OpenAI's then-current naming as of the planning date; the executor must reconfirm at implementation time. The `MODEL_CONTEXT_SIZE` value (~400K input tokens, model-dependent) should also be reconfirmed against OpenAI's documentation. Constants are cheap to fix in a follow-up patch if a model id rotates.
- **Test fidelity vs. live API.** The unit tests use canned SSE fixtures. They prove the parser works against the documented wire shape; they don't prove the live API still emits that shape. Manual smoke (see §7 verification checklist) is required for any release.
- **The "OpenAI" namespace covers Azure OpenAI compatibility for free.** Azure's `/openai/deployments/<name>/chat/completions?api-version=…` endpoint is wire-compatible after a base-URL swap. v1.2 ships only the public OpenAI client; Azure users can register their own factory under a different name (`"azure-openai"`) using `pkg/llm/openai` internals directly. **Do not** add Azure-specific code paths to the OpenAI client — they don't belong inside this package and would muddle the contract.

---

## 6. Out of scope for v1.2

- **OpenAI Responses API** (`/v1/responses`) — separate endpoint, separate streaming protocol, surfaces reasoning summaries. Add only when a feature requires it (e.g. visible thinking on OpenAI).
- **Native OpenAI defer_loading / prompt-prefix tools API** — the agent-side machinery doesn't exist yet; flipping `SupportsDeferLoading()` to `true` without that machinery would invalidate caching, not improve it.
- **Azure OpenAI as a bundled provider.** Wire-compatible but a separate config story; register externally.
- **OpenAI org / project headers** (`OpenAI-Organization`, `OpenAI-Project`) — config schema change deferred.
- **Image / audio inputs to OpenAI** (vision content blocks in user messages). evva's `tools.ContentBlock` machinery exists, but only Claude consumes it today (`pkg/llm/claude/client.go:336-358`). Wiring OpenAI's `image_url` content blocks is a separate, larger task; tool-result image blocks fall back to `llm.RenderContentBlocksAsText` for v1.2.
- **Tool-result image attachments** to OpenAI in particular. Same reason — falls back to text.
- **Function-calling parallelism overrides** (`parallel_tool_calls: false`). OpenAI accepts the flag, but evva's agent loop assumes parallel tool calling everywhere; there's no current need to expose the knob.
- **Logprobs / log_probs** request parameter and response parsing. Not in the `llm.LLMParams` surface today; add only when a downstream needs it.

---

## 7. Verification checklist (PR gate)

- [ ] **Task 0:** `GPT_5_5` removed from `pkg/constant/llm.go`; `GPT_5_MINI` / `GPT_5` constants added; `OPENAI.Models` updated; `MODEL_CONTEXT_SIZE` entries match; `grep -rn "GPT_5_5"` returns nothing.
- [ ] **Task 1–3:** `pkg/llm/openai/{client,factory,stream}.go` compile; `client.go` ≈ 340 LOC; `stream.go` ≈ 190 LOC; no `reasoning_content` field, no `Thinking apiThinking` field in the OpenAI wire types.
- [ ] **Task 2.3:** `temperature` / `top_p` are dropped for every model listed in `constant.OPENAI.Models` (verified by the `httptest`-backed `TestComplete` round-trip test from Task 4.6).
- [ ] **Task 2.4:** `openaiEffort(0..5)` returns `""`, `"low"`, `"medium"`, `"high"`, `"high"`, `""` respectively (covered by `TestOpenAIEffort`).
- [ ] **Task 2.5:** A successful response with `usage.prompt_tokens_details.cached_tokens=N` populates `Response.Usage.CacheReadTokens=N`; reasoning tokens land in `Usage.ReasoningTokens`.
- [ ] **Task 4.2:** `pkg/llm/builtins.TestBuiltinsRegistered` covers `openai`; `go test ./pkg/llm/...` green.
- [ ] **Task 4.5:** SSE stream test covers text deltas, multi-fragment tool-call argument accumulation, terminal usage frame, `[DONE]` terminator, ctx-cancel returning `ErrInterrupted`. No `ChunkThinking` is emitted (assert the chunk count matches the text-only expectation).
- [ ] **A1 (registry):** `llm.DefaultRegistry().Has("openai")` returns true after `_ "github.com/johnny1110/evva/pkg/llm/builtins"` is imported.
- [ ] **A12 (no unimplemented promise):** for `p := range constant.GetAllProviders()`, `llm.DefaultRegistry().Has(p.Name)` is true.
- [ ] **A11 (docs):** `docs/extending.md:24-25` updated; `docs/user-guide/en/user-guide.md:786` import comment updated; the picker mockup at `:146` shows the real model ids; zh-tw mirror updated; `CHANGELOG.md` has a `[v1.2.0]` block; `pkg/version.Version` is `"1.2.0"`.
- [ ] `go build ./...` and `go vet ./...` clean.
- [ ] `go test ./...` green (including the new openai unit + stream tests).
- [ ] `go test -race ./...` clean. The SSE stream parser maintains per-index `streamingToolCall` buffers in a map — the race detector validates that no concurrent access slips through.
- [ ] **Manual (needs a real OpenAI key — flag for a human reviewer):**
  - [ ] Configure `openai.api_key` in `/config`.
  - [ ] `/model` → select `openai / gpt-5` (or `gpt-5-mini`).
  - [ ] Send a prompt that requires a tool call (e.g. *"list files in /tmp"*). Verify the tool call dispatches, the result is echoed back, and the model produces a coherent reply.
  - [ ] Toggle effort via `/effort high` and `/effort low`; confirm the reply differs in depth and that no 400 lands.
  - [ ] Press ESC mid-stream; confirm the request aborts cleanly and the TUI shows the interrupt.
  - [ ] Verify the usage panel shows non-zero `cache_read` after the second turn on the same prompt prefix (OpenAI's automatic prompt caching).

---

## 8. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `pkg/constant/llm.go` | Edit: replace `GPT_5_5` with `GPT_5_MINI` + `GPT_5`; update `OPENAI.Models` and `MODEL_CONTEXT_SIZE` | Task 0 |
| `pkg/llm/openai/client.go` | **New** — buffered Complete, wire types, converters | Task 1, Task 2 |
| `pkg/llm/openai/stream.go` | **New** — Stream + consumeStream + stream types | Task 1, Task 3 |
| `pkg/llm/openai/factory.go` | **New** — `ProviderName` + `Factory` | Task 4.1 |
| `pkg/llm/openai/client_test.go` | **New** — `TestOpenAIEffort` + `TestComplete` (round-trip against `httptest.Server`) | Task 4.4, 4.6 |
| `pkg/llm/openai/stream_test.go` | **New** — `TestConsumeStream` + `TestConsumeStreamCancel` | Task 4.5 |
| `pkg/llm/builtins/builtins.go` | Edit: add `openai` import + `r.MustRegister(openai.ProviderName, openai.Factory)`; update godoc list | Task 4.2 |
| `pkg/llm/builtins/builtins_test.go` | Edit: add `openai.ProviderName` to existence check | Task 4.3 |
| `pkg/version/version.go` | Edit: `1.1.0` → `1.2.0` | Task 5.5 |
| `CHANGELOG.md` | Edit: add `## [v1.2.0]` block | Task 5.4 |
| `docs/extending.md` | Edit: provider list at line 24-25 | Task 5.1 |
| `docs/user-guide/en/user-guide.md` | Edit: line 146 picker mockup, line 786 import comment | Task 5.2 |
| `docs/user-guide/zh-tw/user-guide.md` | Edit: line 153 picker mockup, equivalent import comment | Task 5.3 |
| `docs/sdk-stability.md` | **No change** — no new public package row needed; the openai provider is bundled like the other three under `pkg/llm/builtins`'s side-effect import | — |

---

## 9. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 0 — reconcile constants | ~10 LOC delta | 15 min |
| Task 1 — package skeleton | ~30 LOC scaffolding | 30 min |
| Task 2 — port client.go w/ five deviations | ~340 LOC | 2–3 h |
| Task 3 — port stream.go | ~190 LOC | 1–2 h |
| Task 4 — factory, builtins, tests | ~250 LOC | 2 h |
| Task 5 — docs + version + changelog | ~80 LOC across files | 45 min |
| Manual smoke (§7 last block) | — | 20 min (needs key) |

Total: ~900 LOC new + edited, ~7–9 hours of focused engineering. Smaller than v1.1 (which was integration-heavy) and an order of magnitude smaller than v1.6 (MCP).
