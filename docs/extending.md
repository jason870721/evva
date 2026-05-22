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
| `pkg/tools/kits` | `GeneralPurposeKit`, `ReadOnlyKit`, `CodingKit`, `ResearchKit` | Pre-composed tool-name lists for common agent shapes (Phase 19d) |
| `pkg/skill` | `Registry`, `SkillMeta`, `LoadRegistry`, `NewRegistry`, `Registry.Add`, `SkillTool` | Building custom skill catalogs (disk-loaded, programmatic, or mixed) |
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

### Custom skills (Skill SDK)

A **skill** is a Markdown instruction document the model can invoke
through the SKILL tool. evva ships two ways to install skills:

1. **Disk** — drop `SKILL.md` files under
   `<AppHome>/skills/<name>/SKILL.md` (and optionally
   `<workdir>/.evva/skills/<name>/SKILL.md` for workdir-local overrides).
   `agent.New` auto-loads this catalog at boot — downstream apps get it
   for free without any wiring.
2. **Programmatic** — register skills in Go via `skill.NewRegistry()` +
   `Add(...)`. The `BodyFunc` field is called lazily when the model
   dispatches the skill, so the body can come from `embed.FS`, a
   network fetch, or a generator.

```go
import (
    "embed"

    "github.com/johnny1110/evva/pkg/agent"
    "github.com/johnny1110/evva/pkg/skill"
)

//go:embed skills/commit.md
var skills embed.FS

func main() {
    reg := skill.NewRegistry()
    _ = reg.Add(skill.SkillMeta{
        Name:        "commit",
        Description: "Generate a conventional-commits message from the staged diff",
        BodyFunc: func() (string, error) {
            b, err := skills.ReadFile("skills/commit.md")
            return string(b), err
        },
    })

    ag, _ := agent.NewWithProfile(prof,
        agent.WithSkillRegistry(reg),  // skips disk auto-load
        // ...
    )
}
```

Mixed catalogs are supported: start with `skill.LoadRegistry(home, workdir)`
and call `Add(...)` for any programmatic extras. To disable skills
entirely, pass `agent.WithSkillRegistry(skill.NewRegistry())` — an empty
registry suppresses both the SKILL tool's dispatch list and the system
prompt's `# Skills` section.

### Custom AppConfig — `CustomConfig`

`config.Config` carries a generic key/value extension slot for
downstream-private settings that don't fit the typed fields. Values
round-trip through YAML as a `custom:` section under `<AppHome>/config/<app>-config.yml`.

```go
cfg, _ := config.Load(config.LoadOptions{AppName: "friday"})

// Set — thread-safe; persists via SaveFile() automatically.
_ = cfg.SetCustom("broker.url", "https://broker.internal")
_ = cfg.SetCustom("flags", map[string]any{"beta_ui": true, "tier": "pro"})

// Read at use-site. Values come back as `any`; cast at boundary.
if v, ok := cfg.GetCustom("broker.url"); ok {
    brokerURL := v.(string)
    _ = brokerURL
}

// Remove.
_ = cfg.DeleteCustom("flags")
```

evva itself never reads from `CustomConfig`. After a YAML reload, the
concrete value types are whatever `yaml.v3` decoded into (typically
`string`, `int`, `float64`, `bool`, `[]any`, or `map[string]any`) —
hosts that need stable typed structs should layer their own typed
accessors over `Get/SetCustom`.

## SDK polish (Phase 19)

These notes capture the post-Phase-15 polish: friday-driven additions
and the broader "professional SDK" contract.

### Charmbracelet version pinning

The reference TUI in `internal/ui/bubbletea_v2/` uses bubbletea's
`tea.Program` type on `pkg/ui.UI.Run`. If a downstream UI imports a
DIFFERENT major-or-minor version of `github.com/charmbracelet/bubbletea`,
the two `tea.Program` types are distinct and won't unify at the
interface boundary. Match evva's pinned versions to avoid the trap:

| Library | Tested version |
| --- | --- |
| `github.com/charmbracelet/bubbletea` | `v1.3.10` |
| `github.com/charmbracelet/bubbles` | `v1.0.0` |
| `github.com/charmbracelet/lipgloss` | `v1.1.1-0.20250404203927-76690c660834` |

The full required block is in evva's `go.mod`. Downstream `go.mod`
declarations can either pin the same explicit versions or use a
`require` block without explicit versions and trust `go mod tidy` to
resolve to evva's transitive pins (works most of the time, but
explicit pins are safer).

### Headless permission requirement

`NewWithProfile` installs a non-interactive permission broker by
default — it auto-DENIES every approval request. For an interactive
host this is correct (the host wires its own broker via the agent's
internal options); for a non-interactive consumer that has no UI to
display approval prompts, every tool call needing approval would
silently fail.

The fix is **one option call**:

```go
ag, _ := agent.NewWithProfile(prof,
    agent.WithConfig(cfg),
    agent.WithSink(sink),
    agent.WithHeadlessBypass(), // ← required for non-interactive hosts
)
```

`WithHeadlessBypass()` bundles `WithPermissionMode("bypass")` with a
strong docstring spelling out the security trade-off. With bypass
active, every tool call auto-succeeds. Use only in trusted
environments (CI runners, sandboxed containers, ephemeral VMs).

If your host has an approval UI, omit `WithHeadlessBypass()` and wire
real approval flows via `agent.RespondPermission`.

### Typed PermissionMode

Phase 19c exports a typed `agent.PermissionMode` enum so a typo
becomes a compile error instead of a silent fall-through:

```go
agent.WithPermissionMode(agent.PermissionBypass) // typed — only signature
agent.WithHeadlessBypass()                       // convenience for the bypass case
```

If your config layer reads a mode from a YAML / CLI string, convert
at the boundary: `agent.PermissionMode(s)`.

### `LoadOptions` — the declarative host surface

`LoadOptions` is the single config object every downstream app fills
in. Each field handles one class of runtime customisation:

| Field | Purpose |
| --- | --- |
| `AppName` | Brand identifier; drives the AppHome layout. |
| `AppHome` | Absolute path to the per-user dir (`~/.<AppName>/`). |
| `WorkDir` | Process cwd. Defaults to `os.Getwd()`. |
| `AppVersion` | Version string for diagnostics. |
| `EnvAliases` | Promote friendlier env-var names → evva canonicals before godotenv runs. |
| `ProviderCredentials` | Declaratively wire LLM provider creds from env vars. |
| `EnvOverrides` | Named post-Load mutations for env vars without a YAML hook. |
| `SeedEnvTemplate` | First-run `.env` body — written next to the YAML if missing. |

Together they let a host declare every "what does this app want from
its environment" detail in one block, no pre/post-Load shim functions.

```go
cfg, _ := config.Load(config.LoadOptions{
    AppName: "friday",
    AppHome: "/home/me/.friday",

    // Promote alias → canonical BEFORE godotenv.Load runs.
    EnvAliases: map[string]string{
        "LOGDIR":   "LOG_DIR",
        "LOGLEVEL": "LOG_LEVEL",
        "APIKEY":   "DEEPSEEK_API_KEY",
    },

    // Declaratively wire provider credentials from env vars.
    // Replaces hand-rolled "read env + call SetProviderCredentials" code.
    ProviderCredentials: map[string]config.ProviderCredsFromEnv{
        "deepseek": {
            APIKeyEnv:     "DEEPSEEK_API_KEY",
            APIURLDefault: constant.DEEPSEEK.ApiUrl,
        },
    },

    // Named overrides for vars without a YAML hook. The Name field
    // surfaces in the wrapped error: `config: EnvOverrides[<name>]: ...`
    EnvOverrides: []config.EnvOverride{
        {Name: "max_iters_from_env", Fn: func(c *config.Config) error {
            if v := os.Getenv("MAX_ITERS"); v != "" {
                if n, err := strconv.Atoi(v); err == nil && n > 0 {
                    return c.SetMaxIterations(n)
                }
            }
            return nil
        }},
    },

    // First-run `.env` seed. Written to <AppHome>/.env when missing;
    // never overwrites an existing file.
    SeedEnvTemplate: "DEEPSEEK_API_KEY=\nLOG_LEVEL=info\n",
})
```

Behaviour notes:

- `EnvAliases` is non-overriding: an existing canonical export wins.
- `ProviderCredentials` runs after the YAML loader populates
  `LLMProviderConfig` but before `EnvOverrides`, so an override can
  still mutate the installed creds.
- `EnvOverrides` short-circuits on the first error and wraps it with
  the failing override's Name. Nameless entries are rejected at
  validation time.
- `SeedEnvTemplate` writes once on first launch; never overwrites.

### Tool kits

Instead of `append(fs.Names(), shell.Names()...)` chains, use the
named kits from `pkg/tools/kits`:

```go
import "github.com/johnny1110/evva/pkg/tools/kits"

active, deferred := kits.GeneralPurposeKit() // fs + shell + todo + util + tool_search active; web deferred
// or: ReadOnlyKit / CodingKit / ResearchKit
```

Each kit's godoc lists every tool it includes — copy the kit you
want and tweak with `append`.

### `Event.Payload()` for ergonomic switching

Phase 19a added a `Payload() any` helper that returns the pointer
matching `e.Kind`. Lets consumers type-switch instead of grepping
which of the 20 pointer fields goes with which Kind:

```go
switch p := e.Payload().(type) {
case *event.TextPayload:
    render(p.Text)
case *event.ToolUseStartPayload:
    renderToolCall(p.Name, p.Input)
case *event.ErrorPayload:
    render(p.Message) // Phase 19a: stringified field, populated at emit time
}
```

The direct field access (`e.Text`, `e.ToolUseStart`, …) stays
available — `Payload()` is purely an ergonomics layer.

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
