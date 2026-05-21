# How to integrate EVVA agent in your Go project?

<br>

---

<br>

evva's agent runtime is embeddable. A Go program outside this repo can import `pkg/agent` and assemble its own ReAct agent — with a custom LLM provider, custom tools, a custom event sink, and a non-default home directory — without forking evva and without touching the agent loop.

Everything below uses **only** `pkg/*` imports. Go's `internal/` rule enforces this at compile time: a downstream module that accidentally reaches into `evva/internal/...` won't build.

### 1. Add evva to your module

```bash
go get github.com/johnny1110/evva
```

### 2. Pick your providers

Blank-import `pkg/llm/builtins` to register Anthropic / DeepSeek / Ollama on the default registry; or register a custom provider yourself.

```go
import (
    _ "github.com/johnny1110/evva/pkg/llm/builtins" // anthropic/deepseek/ollama
    "github.com/johnny1110/evva/pkg/llm"
)

// Optional: register your own LLM client.
func registerGemini() {
    llm.DefaultRegistry().MustRegister("gemini",
        func(cfg llm.APIConfig, model string, opts ...llm.Option) (llm.Client, error) {
        return newGeminiClient(cfg, model, opts...), nil
    })
}
```

Your `llm.Client` implementation satisfies five methods: `Name()`, `Model()`, `Complete(ctx, msgs, tools)`, `Stream(ctx, msgs, tools, sink)`, and `Apply(opts...)`. See `pkg/llm/client.go` for the contract.

### 3. Load a Config with your own AppHome

`config.LoadDefault()` boots against `~/.evva/` for compatibility with the bundled CLI. Downstream apps build their own:

```go
import "github.com/johnny1110/evva/pkg/config"

cfg, err := config.Load(config.LoadOptions{
    AppName: "myapp",
    AppHome: filepath.Join(home, ".myapp"),
})

// Install the API key for whichever provider you picked.
cfg.LLMProviderConfig["anthropic"] = config.APIConfig{
    ApiURL:    "https://api.anthropic.com",
    ApiSecret: os.Getenv("ANTHROPIC_API_KEY"),
}
```

Two agents with different `*config.Config` pointers coexist in one process — there is no global singleton inside the agent loop.

### 4. Author a custom tool (optional)

A tool satisfies `pkg/tools.Tool`: `Name()`, `Description()`, `Schema()`, and `Execute(ctx, logger, input)`. The factory receives a `pkg/tools.State` so the tool can read the active `*config.Config` and the agent's workdir.

```go
import (
    "github.com/johnny1110/evva/pkg/tools"
)

type pingTool struct{}

func (pingTool) Name() string            { return "ping" }
func (pingTool) Description() string     { return "respond with pong" }
func (pingTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (pingTool) Execute(_ context.Context, _ *slog.Logger, _ json.RawMessage) (tools.Result, error) {
    return tools.Result{Content: "pong"}, nil
}
```

### 5. Consume events with your own sink

A `pkg/event.Sink` is one method: `Emit(event.Event)`. Fan out to multiple sinks with `event.Multi{Sinks: [...]}` or wrap a parent's sink with `event.BubbleUp` to rewrite `ParentID`.

```go
import "github.com/johnny1110/evva/pkg/event"

type stdoutSink struct{}

func (stdoutSink) Emit(e event.Event) {
    switch e.Kind {
    case event.KindText:
        if e.Text != nil {
            fmt.Println(e.Text.Text)
        }
    case event.KindToolUseStart:
        if e.ToolUseStart != nil {
            fmt.Printf("→ %s\n", e.ToolUseStart.Name)
        }
    }
}
```

### 6. Build the agent

`pkg/agent.NewProfile` constructs a downstream-friendly profile (system prompt + active tool names + provider + model). `pkg/agent.NewWithProfile` assembles the agent against your config, sink, and custom tools.

```go
import "github.com/johnny1110/evva/pkg/agent"

prof, _ := agent.NewProfile(
    "myapp",
    "you are a concise assistant",
    []tools.ToolName{tools.READ_FILE, tools.BASH},
    "anthropic", "claude-sonnet-4-6",
    agent.ProfileOptions{},
)

ag, _ := agent.NewWithProfile(prof,
agent.WithConfig(cfg),
agent.WithSink(stdoutSink{}),
agent.WithMaxIterations(20),
agent.WithPermissionMode("bypass"),       // string-based: "default"|"accept_edits"|"plan"|"bypass"|"auto"
agent.WithCustomTool("ping", func(tools.State) (tools.Tool, error) {
    return pingTool{}, nil
    }),
)

resp, err := ag.Run(context.Background(), "list files under /tmp")
```

### Full working example

A runnable end-to-end downstream consumer lives at [`examples/minimal-host/`](examples/minimal-host/main.go). It registers a custom LLM provider, a custom tool, a custom sink, loads its own config, and runs one turn — in ~110 lines, with **zero `internal/*` imports**.

```bash
go run ./examples/minimal-host
```

### What you can't change

These are deliberately not part of the public surface:

- **Event kinds.** `pkg/event.Kind` constants are fixed at the evva version you import. Adding a new kind requires a code change in evva.
- **Agent loop logic.** The `LLM call → dispatch tools → fold results → repeat` shape lives in `internal/agent/loop.go` and is not configurable.
- **Sysprompt internals.** Inject your own full system prompt via `NewProfile(..., systemPrompt, ...)`; evva's bundled prompt builders are not exported.

If one of these is blocking your use case, fork or file an issue.

### See also

- [`docs/extending.md`](../../../docs/extending.md) — full reference covering every public package, every extension point, and the things you can't override.
- [`examples/minimal-host/main.go`](../../../examples/minimal-host/main.go) — runnable downstream consumer.
- [`pkg/agent/downstream_test.go`](../../../pkg/agent/downstream_test.go) — the same shape as a test, useful as a copy-paste template.
