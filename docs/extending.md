# Extending evva

evva's agent runtime is embeddable: a Go program outside this repository
can import `pkg/agent` and assemble its own ReAct agent — with custom
LLM providers, custom tools, custom event sinks, custom UIs, and a
non-default home directory — without forking and without touching the
agent loop.

This page is the reference for downstream developers. The runnable
proof-of-concept lives at [`examples/minimal-host/`](../examples/minimal-host/main.go).

## Public packages

| Package | What's in it | When you need it |
| --- | --- | --- |
| `pkg/agent` | `New`, `NewWithProfile`, `NewProfile`, `Agent` interface, `Option` aliases | Always — the constructor and the controller surface |
| `pkg/config` | `Config`, `Load`, `LoadDefault`, `APIConfig` | Custom `AppHome`, custom per-provider credentials, custom `MaxTokens`/`MaxIterations` defaults |
| `pkg/event` | `Event`, `Sink`, `Kind`, `Multi`, `BubbleUp`, every payload struct | Consuming agent events (custom UI, JSON-over-stdout, telemetry) |
| `pkg/observable` | `Store`, `Observer`, `Change` | Building your own observable backing store; reading state mutations |
| `pkg/ui` | `UI`, `Controller`, `Skill`, `ProfileChoice`, `PermissionDecision`, `QuestionResponse` | Building a custom UI implementation |
| `pkg/llm` | `Client`, `Message`, `Response`, `Option`, `Registry`, `ClientFactory` | Registering a custom LLM provider |
| `pkg/llm/builtins` | side-effect `init()` registering anthropic/deepseek/ollama | Blank-import to get evva's bundled providers |
| `pkg/llm/{claude,deepseek,ollama}` | direct provider client constructors and `Factory` helpers | Reusing one of evva's bundled clients without going through the registry |
| `pkg/toolset` | `Registry`, `ToolFactory`, `DefaultRegistry`, `TagsFor`, `HintFor` | Registering custom tools |
| `pkg/tools` | `Tool` interface, `Result`, `ContentBlock`, `Descriptor`, `Call`, `State`, `ToolName` constants | Authoring custom tools |
| `pkg/tools/{fs,shell,web,util,notebook,monitor,cron,todo}` | Bundled tool family implementations | Reusing the bundled tools directly (rare; most callers use them via the registry) |
| `pkg/constant` | `LLMProvider`, `Model`, `AgentStatus`, `MODEL_CONTEXT_SIZE` | Referencing built-in provider / model identifiers |

Internal packages (`internal/`) remain inaccessible from outside the
module by Go's import-visibility rule. Phase 13 finished that boundary;
everything downstream apps need lives under `pkg/`.

## Extension points

### Custom LLM provider

The `pkg/llm` registry mirrors the tool registry: register a factory by
provider name, then build a profile that targets it.

```go
import (
    "github.com/johnny1110/evva/pkg/llm"
    _ "github.com/johnny1110/evva/pkg/llm/builtins" // optional: pulls in anthropic/deepseek/ollama
)

func init() {
    llm.DefaultRegistry().MustRegister("gemini", func(cfg llm.APIConfig, model string, opts ...llm.Option) (llm.Client, error) {
        return newGeminiClient(cfg, model, opts...), nil
    })
}
```

Your `Client` implementation satisfies four methods: `Name()`, `Model()`,
`Complete(ctx, messages, tools)`, `Stream(ctx, messages, tools, sink)`,
and `Apply(opts...)`. See `pkg/llm/client.go` for the contract.

After registering, install API credentials on the agent's `*config.Config`
under your provider name:

```go
cfg.LLMProviderConfig["gemini"] = config.APIConfig{
    ApiURL: "https://generativelanguage.googleapis.com",
    ApiSecret: os.Getenv("GEMINI_KEY"),
}
```

### Custom tool

A tool satisfies `pkg/tools.Tool`: `Name()`, `Description()`,
`Schema() json.RawMessage`, and `Execute(ctx, logger, input)`. The
factory receives a `pkg/tools.State` so the tool can read the active
`*config.Config` and the agent's workdir.

```go
import (
    "github.com/johnny1110/evva/pkg/agent"
    "github.com/johnny1110/evva/pkg/tools"
)

ag, _ := agent.NewWithProfile(profile,
    agent.WithCustomTool("ping", func(s tools.State) (tools.Tool, error) {
        return pingTool{}, nil
    }),
)
```

For tools that should be available to *every* agent in your process,
register them on the global default registry at startup:

```go
import "github.com/johnny1110/evva/pkg/toolset"

func init() {
    toolset.DefaultRegistry().MustRegister("ping", func(s tools.State) (tools.Tool, error) {
        return pingTool{}, nil
    })
}
```

### Custom config / `AppHome`

`config.LoadDefault()` boots against `~/.evva/` to preserve cmd/evva's
behavior. Downstream apps build their own `Config`:

```go
import "github.com/johnny1110/evva/pkg/config"

cfg, err := config.Load(config.LoadOptions{
    AppName: "myapp",
    AppHome: filepath.Join(home, ".myapp"),
    WorkDir: wd,
})
// Then pass via agent.WithConfig(cfg).
```

Two agents with two different `*config.Config` pointers can coexist in a
single process — there is no global singleton inside the agent loop.

### Custom event sink

Implement `pkg/event.Sink` (one method: `Emit(event.Event)`). Pass via
`agent.WithSink`. Fan out to multiple sinks with `event.Multi{Sinks: [...]}`
or wrap a parent's sink with `event.BubbleUp` to rewrite `ParentID`.

```go
type jsonSink struct{ enc *json.Encoder }

func (s jsonSink) Emit(e event.Event) {
    _ = s.enc.Encode(e)
}

ag, _ := agent.NewWithProfile(prof, agent.WithSink(jsonSink{enc: json.NewEncoder(os.Stdout)}))
```

### Custom UI

`pkg/ui.UI` is the surface a custom UI satisfies. Implement `Emit`,
`Attach`, and `Run`. The agent gives you a `Controller` you drive
commands through (`Run`, `Continue`, `CyclePermissionMode`,
`RespondPermission`, etc.).

`Controller.Session()` and `Controller.ToolState()` currently return
internal types — you can call methods on them but cannot fully implement
`Controller` from outside the module. This is a known follow-up;
downstream UIs that need to render todos / subagents today should
subscribe to `event.KindStoreUpdate` events through the sink instead.

## What you can't change

These are by design — see CLAUDE.md's Phase 13 goals for the rationale.

- **Event kinds.** Downstream apps can subscribe to `pkg/event` but the
  set of `Kind` constants is fixed at the evva-version they import.
  Adding a new kind requires a code change in evva itself.
- **Agent loop logic.** The `iter → LLM call → dispatch tools → fold
  results → repeat` shape lives in `internal/agent/loop.go` and is not
  configurable. Tool dispatch, compaction triggers, plan-mode reminder
  injection — all internal.
- **Sysprompt internals.** Downstream personas inject their full system
  prompt via `NewProfile(..., systemPrompt, ...)`. The sysprompt
  builders that compose evva's bundled main / explore / plan prompts
  are not part of the public surface.

If you need to change one of these, fork or file an issue describing
the use case.

## See also

- [`examples/minimal-host/main.go`](../examples/minimal-host/main.go) — runnable end-to-end downstream consumer.
- [`pkg/agent/downstream_test.go`](../pkg/agent/downstream_test.go) — same shape as a test, useful as a copy-paste template.
- [`CLAUDE.md`](../CLAUDE.md) Phase 13 section — the roadmap that introduced these public packages.
