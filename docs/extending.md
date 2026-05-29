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
| `pkg/agent` | `New(Config, ...Option)` one-call constructor, `NewWithProfile`, `NewProfile`, `Agent` interface (incl. `Controller()` / `Shutdown()`), `AgentRegistry`, `AgentDefinition`, `ResolveMainProfile`, `Option` aliases | Always — the constructor, persona catalog, and controller surface |
| `pkg/config` | `Config`, `Load`, `LoadDefault`, `APIConfig` | Custom `AppHome`, custom per-provider credentials, custom `MaxTokens`/`MaxIterations` defaults |
| `pkg/event` | `Event`, `Sink`, `Kind`, `Multi`, `BubbleUp`, every payload struct | Consuming agent events (custom UI, JSON-over-stdout, telemetry) |
| `pkg/observable` | `Store`, `Observer`, `Change` | Building your own observable backing store; reading state mutations |
| `pkg/ui` | `UI`, `Controller`, `Skill`, `ProfileChoice`, `PermissionDecision`, `QuestionResponse`, the read-model accessors | Building a custom UI implementation |
| `pkg/ui/bubbletea` | `New(evvaHome)` — the bundled reference terminal UI | Embedding evva's batteries-included TUI instead of writing your own |
| `pkg/permission` | `Store`, `Rule`, `Mode`, `Decision`, `Broker`, `Load`, `NewBroker`, `SetOnRequest`, `ParseMode` | Custom approval policy / pre-seeded rule store |
| `pkg/llm` | `Client`, `Message`, `Response`, `Option`, `Registry`, `ClientFactory` | Registering a custom LLM provider |
| `pkg/llm/builtins` | side-effect `init()` registering anthropic/deepseek/openai/ollama | Blank-import to get evva's bundled providers |
| `pkg/llm/{claude,deepseek,openai,ollama}` | direct provider client constructors and `Factory` helpers | Reusing one of evva's bundled clients without going through the registry |
| `pkg/toolset` | `Registry`, `ToolFactory`, `DefaultRegistry`, `Describe`, `Build` | Registering custom tools |
| `pkg/tools` | `Tool` interface, `Result`, `ContentBlock`, `Descriptor`, `Call`, `State`, `ToolName` constants | Authoring custom tools |
| `pkg/tools/{fs,shell,web,util,notebook,monitor,cron,todo,daemon}` | Bundled tool family implementations | Reusing the bundled tools directly (rare; most callers use them via the registry) |
| `pkg/tools/lsp` | LSP integration — the deferred `lsp_request` tool (`tools.LSP_REQUEST`) | Reusing evva's Language Server tool; semantic code intelligence |
| `pkg/tools/kits` | `GeneralPurposeKit`, `ReadOnlyKit`, `CodingKit`, `ResearchKit` | Pre-composed tool-name lists for common agent shapes (Phase 19d) |
| `pkg/hooks` | `Registry`, `Dispatcher`, `Load`, `BasePayload`, `Decision`, `WithHookRegistry` | Adding lifecycle hooks (shell commands / HTTP webhooks that fire at SessionStart, UserPromptSubmit, PreToolUse, PostToolUse, Stop, Notification) |
| `pkg/skill` | `Registry`, `SkillMeta`, `LoadRegistry`, `NewRegistry`, `Registry.Add`, `SkillTool` | Building custom skill catalogs (disk-loaded, programmatic, or mixed) |
| `pkg/constant` | `LLMProvider`, `Model`, `AgentStatus`, `MODEL_CONTEXT_SIZE` | Referencing built-in provider / model identifiers |
| `pkg/update` | `evva update` self-update glue (product-specific) | Rarely — most hosts ship their own update path |

Internal packages (`internal/`) remain inaccessible from outside the
module by Go's import-visibility rule. As of SDK v2.5, the flagship
`cmd/evva` itself builds on `pkg/*` alone — zero direct `internal/`
imports — and the separate-module
[`examples/full-host/`](../examples/full-host/main.go) reproduces the
full TUI experience, with Go's internal rule compiler-enforcing that it
touches no internals. If the bundled host can be built on the public
contract, yours can too.

## Extension points

### Custom LLM provider

The `pkg/llm` registry mirrors the tool registry: register a factory by
provider name, then build a profile that targets it.

```go
import (
    "github.com/johnny1110/evva/pkg/llm"
    _ "github.com/johnny1110/evva/pkg/llm/builtins" // optional: pulls in anthropic/deepseek/openai/ollama
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

As of SDK v2.1 the entire `Controller` surface is public — its read-models
return only `pkg/*` types (`Messages() []llm.Message`, `Usage() llm.Usage`,
`TodoStore() *todo.TodoStore`, `DaemonState() *daemon.DaemonState`), so a
UI in a separate module can fully implement and drive it without importing
any evva internals. `pkg/ui/controller_compile_test.go` is the compile-time
proof of that. You can render todos / subagents either by reading those
accessors each frame or by subscribing to `event.KindStoreUpdate` through
the sink — both work.

If you don't want to write a UI at all, embed the bundled reference TUI:
`pkg/ui/bubbletea.New(evvaHome)` returns a `ui.UI`. Hand it to the agent as
the sink and hand the agent back as its controller — see the one-call
constructor below.

### One-call constructor — `agent.New(Config)`

`NewWithProfile` is the à-la-carte constructor: it wires only what you
pass. `New(Config, ...Option)` is the batteries-included one — it absorbs
the whole bootstrap a host used to hand-wire, driven by a declarative
`Config` plus a few options. From `Config` alone it resolves the persona
(with an `evva` fallback), auto-loads `EVVA.md` / `USER_PROFILE.md` memory
and the skill catalog, loads the permission store, resolves the mode, and
installs the approval + question brokers.

```go
cfg := config.Get() // or config.Load(LoadOptions{...})

tui := bubbletea.New(cfg.AppHome)      // pkg/ui/bubbletea
ag, _ := agent.New(agent.Config{
    AppConfig:      cfg,
    PermissionMode: "default",          // "" → YAML → "default"
    // Persona, Personas, PermissionStore, Provider, Model, MaxIters,
    // LLMOptions are all optional.
}, agent.WithSink(tui), agent.WithRootContext(ctx))

tui.Attach(ag.Controller())            // ag.Controller() is the ui.Controller view
defer ag.Shutdown()
tui.Run(ctx)
```

`agent.Agent` and `ui.Controller` share method names with different payload
types, so one concrete type can't satisfy both — `ag.Controller()` returns
the `ui.Controller` view to hand to `UI.Attach`. The runnable end-to-end
version of the above is [`examples/full-host/`](../examples/full-host/main.go).

### Personas (the `evva → nono` pattern)

A **persona** is a main-tier agent definition — its own system prompt,
tool lists, and model preference. `pkg/agent` exposes the catalog so a host
can register its own personas in code, load them from disk, drive the
`/profile` picker, and spawn them as subagents.

```go
reg, _ := agent.BuildAgentRegistry(cfg.AppHome) // built-ins + <AppHome>/agents/*
reg.Register(agent.AgentDefinition{
    Name:         "nono",
    WhenToUse:    "financial questions",
    As:           []string{"main", "subagent"},
    InjectMemory: true,
    SystemPrompt: "You are nono, a financial manager.",
})

ag, _ := agent.New(agent.Config{
    AppConfig: cfg,
    Personas:  reg,     // omit → built from <AppHome>/agents/ automatically
    Persona:   "nono",  // omit → cfg.DefaultProfile → "evva"
}, agent.WithSink(sink))
```

On-disk personas live under `<AppHome>/agents/{name}/` as
`system_prompt.md` + `tools.yml` + `meta.yml` (the `as:` field controls
whether a persona shows in the `/profile` picker, the subagent catalog, or
both). The `Controller`'s `ListMainProfiles()` / `SwitchProfile(name)`
light up off whichever registry you install. `agent.LoadDiskAgents(home)`
returns the disk catalog without the built-ins.

### Permissions

`pkg/permission` is the approval system: a rule `Store`, a `Mode` (the
Shift-Tab stance), and a `Broker` back-channel. `New(Config)` wires sane
defaults — but a host can override either piece.

```go
store, _ := permission.Load(cfg.WorkDir, cfg.AppHome) // project + user rules

ag, _ := agent.New(agent.Config{
    AppConfig:       cfg,
    PermissionStore: store, // omit → loaded from disk automatically
    PermissionMode:  "accept_edits",
}, agent.WithSink(tui))
```

For a non-interactive policy (no UI), install a custom broker via
`agent.WithPermissionBroker(b)`: build one with `permission.NewBroker()`
and register a callback with `permission.SetOnRequest` that inspects each
`permission.ApprovalRequest` and replies with a `permission.Decision`. With
a sink installed and no custom broker, the agent emits
`event.KindApprovalNeeded` for an interactive UI to resolve via
`Agent.RespondPermission`; with no sink it auto-denies (so a request never
parks a goroutine). For trusted/headless runs, `agent.WithHeadlessBypass()`
or `PermissionMode: "bypass"` auto-allows everything.

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

**Bundled skills.** evva ships its own first-party SKILL.md catalog
(`commit`, `review`, `security-review`, `simplify`, `setup-hooks` — see the
v1.4.0 `CHANGELOG.md` entry), overlaid onto the disk catalog automatically by
the one-call `agent.New`. Bundled is the **lowest-precedence** tier
(`skill.SourceBundled`): a user disk skill with the same name silently
overrides the bundled body — no shadowing warning. Hosts that construct their
agent through `agent.NewWithProfile` + an explicit `WithSkillRegistry` do
**not** pick up the bundled catalog; to ship your own content, build a
programmatic catalog (the pattern above) rather than reaching into evva's
private `internal/skills/bundled` package.

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

The reference TUI in `pkg/ui/bubbletea/` uses bubbletea's
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

## Lifecycle hooks

evva supports six lifecycle hook events: **SessionStart**, **UserPromptSubmit**,
**PreToolUse**, **PostToolUse**, **Stop**, and **Notification**. Hooks are
configured in `.evva/settings.json` (project scope) or
`<APP_HOME>/settings.json` (user scope). Project hooks fire before user
hooks.

### settings.json shape

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "bash",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/my-hook.sh",
            "timeout": 60
          }
        ]
      }
    ]
  }
}
```

Each event maps to an array of matchers. Each matcher carries a `matcher`
glob (doublestar syntax; empty matches all tools) and a list of hooks.
A hook is either `type: "command"` (shell subprocess, exit code 0 →
parse stdout as JSON decision, exit 2 → block) or `type: "http"` (async
by default, POSTs the payload to `url`).

### Hook payload shape

Every hook receives a JSON payload on stdin (command) or as the POST body
(http). The base envelope carries `session_id`, `cwd`, `permission_mode`,
`agent_id`, `agent_type`, and `hook_event_name`. Per-event fields attach
below (e.g. PreToolUse adds `tool_name`, `tool_input`, `tool_use_id`).

### Decision JSON

Hook stdout (exit 0) is parsed as a JSON decision object:

```json
{
  "continue": true,
  "decision": "approve",
  "reason": "looks safe",
  "hookSpecificOutput": {
    "permissionDecision": "allow",
    "updatedInput": {"command": "echo hello"},
    "additionalContext": "[extra info for the LLM]"
  }
}
```

Key fields:

| Field | Effect |
| --- | --- |
| `continue: false` | Block the operation (PreToolUse, UserPromptSubmit, Stop) |
| `decision: "block"` | Same as continue=false |
| `decision: "approve"` | For PreToolUse: allow the tool unconditionally |
| `hookSpecificOutput.permissionDecision` | One of `allow`, `deny`, `ask` — overrides the permission gate |
| `hookSpecificOutput.updatedInput` | New tool input JSON (PreToolUse only) — the tool executes with this instead of the model's args |
| `hookSpecificOutput.additionalContext` | Text appended to the tool result / prompt / session start, visible to the model next turn |
| `hookSpecificOutput.initialUserMessage` | Synthetic user message prepended at session start |

### SDK usage

Downstream hosts that construct agents via `pkg/agent.NewWithProfile` can
opt into hooks with `WithHookRegistry`:

```go
hookReg, _ := hooks.Load(workdir, appHome)
ag, _ := agent.NewWithProfile(prof,
    agent.WithHookRegistry(hookReg),
)
```

The one-call `pkg/agent.New` loads hooks from disk automatically alongside
`permission.Load`. A nil registry is safe — the dispatcher noops.

## MCP servers

evva consumes Model Context Protocol servers as a source of tools and
resources. Configure them under `mcpServers` in the same settings.json
files that hold hooks:

- Project: `<workdir>/.evva/settings.json`
- User: `<APP_HOME>/settings.json` (typically `~/.evva/settings.json`)

### settings.json shape

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${HOME}/work"],
      "env": {"NODE_OPTIONS": "--enable-source-maps"}
    },
    "github": {
      "type": "http",
      "url": "https://api.example.com/mcp",
      "headers": {"X-Custom-Header": "${MY_HEADER_VAR:-default}"}
    }
  }
}
```

Per-server fields:
- `type`: `"stdio"` or `"http"`. Inferred from `command` (→stdio) or `url`
  (→http) when omitted.
- `command`, `args`, `env`: stdio only. `${VAR}` and `${VAR:-default}`
  expansion happens at load time (process environment).
- `url`, `headers`: http only (Streamable HTTP, 2025-03-26 spec).
- `timeout`: connect timeout in seconds; default 30, max 600.
- `disabled`: skip this server entirely (no subprocess, no HTTP).

A misconfigured server never blocks startup: the failure is logged, the
server shows `failed`/`needs-auth` in `Manager.Status()`, and every other
configured server still connects (concurrently, no head-of-line blocking).

### Tool naming

Every discovered tool becomes `mcp__<server>__<tool>` in evva's tool
catalog (lowercased and char-sanitized to `^[a-zA-Z0-9_-]{1,64}$`). The
names are deferred — they appear in `<available-deferred-tools>` and load
on demand via `tool_search`. Permission rules and hooks target this
fully-qualified name:

```json
{
  "permissions": {
    "alwaysAllow": ["mcp__filesystem__read_file"],
    "deny": ["mcp__filesystem__delete_file"]
  },
  "hooks": {
    "PreToolUse": [{"matcher": "mcp__**__write_*", "hooks": [{"type":"command","command":"./audit.sh"}]}]
  }
}
```

The default stance for an unknown tool is `ask`, so the first MCP write
prompts; add a rule to make it permanent. A glob hook like
`mcp__**__write_*` is a one-rule way to audit or redact every MCP write
across every server.

### Resources

`list_mcp_resources` returns the resource catalog across connected servers
(each entry tagged with its `server`); `read_mcp_resource` takes
`{server, uri}` and returns text inline. Binary blobs are persisted under
`<APP_HOME>/mcp-blobs/` and the path is returned in place of the bytes —
read it back with the `read` tool if needed.

### OAuth-protected HTTP servers

When an HTTP server returns 401 on initial connection, evva flags it
`needs-auth` and registers a one-off `mcp__<server>__authenticate` tool.
Invoking it surfaces the authorization URL to the user (via the question
broker — the bundled TUI's `ask_user_question` overlay), waits for them to
finish the in-browser flow, then reconnects and makes the server's real
tools available. Tokens live in SDK-managed memory only in this release —
re-auth on session restart.

### SDK usage

Downstream hosts that construct agents via `pkg/agent.NewWithProfile` opt
into MCP with `WithMcpManager`:

```go
cfg, warns := mcp.Load(workdir, evvaHome)
mgr, openWarns := mcp.Open(ctx, cfg, mcp.OpenOptions{
    Logger:   logger,
    EvvaHome: evvaHome,
    // OAuthPrompt: yourPromptFn,  // optional; nil disables the OAuth flow.
    //                              // Hosts that want the bundled
    //                              // ask_user_question flow should build an
    //                              // adapter bridging OAuthPromptFn to their UI.
})
mgr.RegisterFactories(toolset.DefaultRegistry())

ag, _ := agent.NewWithProfile(prof,
    agent.WithMcpManager(mgr),
)
```

The one-call `pkg/agent.New` loads + opens the manager automatically,
including wiring `OAuthPrompt` to the bundled `ask_user_question` flow. A
nil manager is safe — the resource tools and dynamic factories just have
nothing to surface.

### Out of scope (v1.6 first cut)

Sampling, prompts, roots, elicitation, SSE-IDE / WebSocket / SDK
transports, plugin-provided servers, hot-reload, and disk-persisted OAuth
tokens are deliberately omitted. See `docs/roadmap/v1/v1-3-mcp.md` §6 for
the full list and follow-up candidates.

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

- [`examples/full-host/main.go`](../examples/full-host/main.go) — the canonical full host: TUI + personas + permissions via the one-call constructor, in a separate module (compiler-enforced pkg-only).
- [`examples/minimal-host/main.go`](../examples/minimal-host/main.go) — the tiny host: `NewWithProfile` + a custom provider, tool, and skill.
- [`pkg/agent/downstream_test.go`](../pkg/agent/downstream_test.go) + [`converged_downstream_test.go`](../pkg/agent/converged_downstream_test.go) — public-only test templates for the à-la-carte and one-call constructors.
- [`docs/sdk-stability.md`](sdk-stability.md) — the per-package stability tiers.
