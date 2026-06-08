# build-agent Scaffold a Go program that embeds an evva agent with the evva-sdk (pkg/agent)

Use this skill when the user wants to BUILD THEIR OWN agent or app on top of evva — "embed evva in my Go program", "build an agent with the evva-sdk", "scaffold an evva host", "use pkg/agent", "make a custom agent like nono", "how do I host evva". This is about authoring a NEW downstream Go program that imports evva's public `pkg/*` packages. It is NOT about changing the evva repo itself, and NOT about configuring an already-running evva session (for hooks use the `setup-hooks` skill; for MCP servers use `setup-mcp`).

## The mental model: a "host"

A program that embeds evva is a **host**. It owns `main()`, picks a UI (or none), and constructs an agent from evva's public `pkg/*` surface — it never imports `internal/` (Go forbids that across modules anyway). Everything is wired through one of two constructors plus a handful of `Option`s.

**Authoritative API, always:** run `go doc github.com/johnny1110/evva/pkg/agent` (and `.../pkg/config`, `.../pkg/llm`, `.../pkg/tools`, `.../pkg/ui`, `.../pkg/permission`) to read the EXACT API of the evva version the host imports. That godoc beats any snippet in this skill — snippets here may lag the version on disk. The full prose reference is `docs/extending.md` in the evva repo, and two runnable templates are the source of truth — read whichever matches the user's goal before writing code:

- `examples/full-host/main.go` (~60 lines) — the batteries-included path: interactive TUI, persona catalog + `/profile` switching, permission prompts, `/resume`, `/compact`, background tasks. Built on the one-call `agent.New(Config)`. It is a SEPARATE Go module (its own `go.mod` with a `replace`), which proves the public surface is self-sufficient.
- `examples/minimal-host/main.go` (~150 lines) — the à-la-carte path: `agent.NewWithProfile` plus a custom provider, a custom tool, and a custom skill, with a JSON event sink and no TUI.

If the evva source isn't on disk (the user is in their own repo), the patterns below are self-contained — scaffold from them directly, and fetch the example files from the evva repository only if you need more detail.

## Step 1 — Clarify the goal (use ask_user_question if unclear)

Pin down four things before writing any code:

1. **Interactive or headless?** A TUI app a human drives, or a one-shot / automation that returns text and exits.
2. **Which LLM provider?** A built-in (`anthropic`, `deepseek`, `openai`, `ollama`) or a custom one.
3. **Default tools, a kit, or custom?** The standard evva catalog, a named kit from `pkg/tools/kits`, or bespoke tools.
4. **One persona or several?** Just the default `evva`, or a custom persona (the evva→nono pattern), possibly several switchable ones.

The answers decide the constructor (Step 2) and which extension points you wire (Step 4). Don't wire anything the goal doesn't call for.

## Step 2 — Pick the constructor

| Goal | Constructor | Why |
| --- | --- | --- |
| Full app, fastest path | `agent.New(agent.Config{...}, opts...)` | One call wires persona resolution (with an `evva` fallback), memory, the skill catalog, the permission store + mode, and the approval + question brokers — all from `Config`. Mirror `examples/full-host`. |
| Fine-grained / headless / custom wiring | `agent.NewWithProfile(profile, opts...)` | À-la-carte: it wires only what you pass. Mirror `examples/minimal-host`. |

**Default to `agent.New(Config)`** unless the user needs à-la-carte control — it is dramatically less code (a full TUI app is ~40 lines).

## Step 3 — Scaffold the project

1. If it's a new program: `go mod init <module-path>`. If the user already has a module, add to it — don't scaffold a competing one.
2. Add the dependency: `go get github.com/johnny1110/evva@latest` (for local development against an evva checkout, use a `replace` directive instead — see `examples/full-host/go.mod`).
3. Blank-import the providers so the built-ins register: `_ "github.com/johnny1110/evva/pkg/llm/builtins"`.
4. Write `main.go` from the matching template below.

### 3a — Batteries-included host (most users)

```go
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/config"
	_ "github.com/johnny1110/evva/pkg/llm/builtins" // register anthropic/deepseek/openai/ollama
	"github.com/johnny1110/evva/pkg/ui/bubbletea"
)

func main() {
	cfg := config.Get()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	tui := bubbletea.New(cfg.AppHome) // the UI is the agent's event sink

	ag, err := agent.New(agent.Config{AppConfig: cfg},
		agent.WithSink(tui),
		agent.WithRootContext(ctx),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "host:", err)
		os.Exit(1)
	}
	defer ag.Shutdown()

	tui.Attach(ag.Controller()) // ag.Controller() is the ui.Controller view
	if err := tui.Run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "host:", err)
		os.Exit(1)
	}
}
```

### 3b — Headless host (automation, no UI)

A non-interactive host has no UI to resolve approval prompts, so it MUST opt out of the gate — otherwise every tool call that needs approval auto-denies and silently fails.

```go
ag, err := agent.NewWithProfile(profile,
	agent.WithConfig(cfg),
	agent.WithSink(jsonSink{enc: json.NewEncoder(os.Stdout)}), // your event.Sink
	agent.WithHeadlessBypass(), // REQUIRED for non-interactive hosts: auto-allow all tools
)
// ... then ag.Controller().Run(ctx, prompt) and consume events from your sink.
```

`WithHeadlessBypass()` auto-allows every tool — use ONLY in trusted environments (CI runners, sandboxes, ephemeral VMs). If the host has any approval UI, omit it and wire real approvals via `Agent.RespondPermission`.

## Step 4 — Wire only the extension points the goal calls for

Each bullet is one section of `docs/extending.md` — read that section (or `go doc` the package) for the exact contract.

- **Custom LLM provider** — implement `pkg/llm.Client` (`Name`, `Model`, `Complete`, `Stream`, `Apply`), register a factory on `llm.DefaultRegistry().MustRegister(name, factory)`, then install creds: `cfg.LLMProviderConfig[name] = config.APIConfig{ApiURL: ..., ApiSecret: os.Getenv(...)}`.
- **Custom tool** — implement `pkg/tools.Tool` (`Name`, `Description`, `Schema`, `Execute`). For one agent: `agent.WithCustomTool(name, func(s tools.State) (tools.Tool, error) { ... })`. Process-wide: `toolset.DefaultRegistry().MustRegister(name, factory)` in an `init()`.
- **Tool kits** — instead of hand-assembling tool-name lists, use `kits.GeneralPurposeKit()` / `ReadOnlyKit()` / `CodingKit()` / `ResearchKit()` from `pkg/tools/kits`; each returns `(active, deferred)` name slices you can `append` to.
- **Custom persona (evva→nono)** — `reg, _ := agent.BuildAgentRegistry(cfg.AppHome)`, then `reg.Register(agent.AgentDefinition{Name: "nono", WhenToUse: "...", As: []string{"main", "subagent"}, InjectMemory: true, SystemPrompt: "..."})`. Pass `Personas: reg, Persona: "nono"` in `agent.Config`. Or drop `<AppHome>/agents/<name>/{system_prompt.md,tools.yml,meta.yml}` on disk — `New(Config)` loads them automatically.
- **Custom event sink** — implement `pkg/event.Sink` (one method, `Emit(event.Event)`); pass `agent.WithSink(s)`. Fan out with `event.Multi{Sinks: [...]}`. Type-switch on a payload with `e.Payload()`.
- **Custom UI** — implement `pkg/ui.UI` (`Emit`, `Attach`, `Run`) and drive the `Controller`; or just embed `pkg/ui/bubbletea`.
- **Permissions** — `permission.Load(cfg.WorkDir, cfg.AppHome)` for disk rules (`New(Config)` does this for you). For a programmatic allow/deny policy: build `permission.NewBroker()`, register `permission.SetOnRequest(func(req) Decision { ... })`, pass `agent.WithPermissionBroker(b)`.
- **Custom skills** — `reg := skill.NewRegistry(); reg.Add(skill.SkillMeta{Name, Description, BodyFunc})`; pass `agent.WithSkillRegistry(reg)`. NOTE: an explicit registry SKIPS the disk + bundled auto-load, so include any built-ins you still want.
- **Custom AppHome / config** — `config.Load(config.LoadOptions{AppName, AppHome, WorkDir, ProviderCredentials, EnvAliases, SeedEnvTemplate})` for a fully branded app with its own home dir and declarative env wiring.
- **Hooks / MCP** — config-driven via `settings.json`; `agent.New(Config)` auto-loads both. To CONFIGURE them in a running session, the user wants the `setup-hooks` / `setup-mcp` skills, not this one.

## Step 5 — What you CANNOT change (say so up front)

These are fixed by design (see `extending.md` "What you can't change"):

- The set of `pkg/event.Kind` constants — fixed at the evva version you import; a new kind needs a change in evva itself.
- The agent loop shape — `iter → LLM call → dispatch tools → fold results → repeat` is internal and not configurable.
- The bundled sysprompt builders — a custom persona injects its OWN full system prompt (via the `AgentDefinition` / `NewProfile`), it doesn't edit evva's.

If the user needs one of these, the answer is "fork or file an issue", not "import internal/".

## Step 6 — Verify it compiles and runs

Never hand the user code you haven't checked:

1. `go build ./...` — fix every error before continuing.
2. If the host is its own module AND embeds the bundled TUI (`pkg/ui/bubbletea`), pin the charmbracelet versions to evva's (a different bubbletea major/minor makes the two `tea.Program` types fail to unify at the `UI.Run` boundary — see `extending.md` "Charmbracelet version pinning"), or run `go mod tidy` to resolve to evva's transitive pins.
3. Confirm a provider credential is set (the relevant `*_API_KEY` env var or `.env`) before the first run, or the first LLM call fails.
4. Run it: `go run .` (TUI) or pipe a prompt to the headless binary. Confirm one full ReAct turn end to end.

## Guardrails

- NEVER import `internal/` — it won't compile across modules and isn't the supported surface. If a capability looks internal-only, it's almost certainly a public symbol you haven't found yet: check the `extending.md` package table or `go doc`, don't reach in.
- Keep the host SMALL. The point of `agent.New(Config)` is that ~40 lines buys a full app — don't re-hand-wire what the constructor already does.
- Match the user's existing module and Go toolchain; add to their `go.mod` rather than creating a parallel module.
- This skill scaffolds a NEW host program. It does not modify the evva repository, and it is not how you configure a running evva session.
