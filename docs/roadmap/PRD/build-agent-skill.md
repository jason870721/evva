# PRD — `build-agent` Bundled Skill — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build (content authoring + one registration line).
> **Target release:** TBD — additive bundled-skill content, no framework change.
> Rides any minor (or a content patch) under the Stable-tier promise, exactly
> like the v1.4 skills it joins.
> **Roadmap source:** `CLAUDE.md` → Roadmap → *v1.4 — Bundled skills* (this
> extends that catalog with a seventh skill); the SDK surface it teaches is
> the `v1.0.0` Stable `pkg/*` contract.
> **Reference source (content):** `docs/extending.md` (the authoritative SDK
> reference — every section of the skill distills one of its extension
> points); `examples/full-host/main.go` + `examples/minimal-host/main.go` (the
> two runnable host templates); `docs/evva-sdk/sdk-v2.md` (the completeness
> arc that produced the public surface); `docs/sdk-stability.md` (tiers).
> **Live-source verification (2026-05-30):** the bundled-skills wiring,
> `bundledNames`, the title-token invariant, the SKILL-tool framing, and the
> example paths/line-counts cited below were checked against the working tree
> on `dev`. Itemised in §2.

---

## 1. TL;DR — what this phase actually is

evva can already *do* everything needed to help a user embed it in their own
Go program — the knowledge lives in `docs/extending.md`, the public `pkg/*`
surface is complete and Stable (`v1.0.0`, proved by `examples/full-host`
building on `pkg/*` alone in a separate module), and the bundled-skills
framework is done (`internal/skills/bundled`, six skills shipping today:
`commit`, `review`, `security-review`, `simplify`, `setup-hooks`,
`setup-mcp` — `embed.go:19-26`).

**What is missing is a *skill*.** When a user asks evva *"help me build my own
agent with the evva-sdk"* / *"embed evva in my Go app"* / *"scaffold a host
like nono"*, evva answers from whatever fragments of `extending.md` happen to
be in context — ad hoc, version-drifting, and with no structured playbook. The
`# Skills` catalog advertises onboarding skills for hooks (`setup-hooks`) and
MCP (`setup-mcp`) — the two *config-in-an-existing-session* tasks — but nothing
for the *author-a-new-host* task, which is the single biggest "how do I use
this?" question the SDK exists to answer.

This phase adds **one new bundled skill, `build-agent`**: a model-facing,
action-oriented distillation of `docs/extending.md` into a decision tree
("which constructor? which extension points?") plus a scaffolding workflow
that points at the two canonical example templates. It teaches evva to *walk a
user through building a downstream host* — clarify the goal, pick
`agent.New(Config)` vs `agent.NewWithProfile`, scaffold `main.go`, wire only
the extension points the goal calls for, and verify it compiles and runs.

The deliverable is almost entirely **content**:

1. **One new file:** `internal/skills/bundled/content/build-agent/SKILL.md`
   (the full body is reproduced verbatim in Task 1 — it *is* the deliverable).
2. **One registration line:** append `"build-agent"` to `bundledNames`
   (`internal/skills/bundled/embed.go`).
3. **Test + docs + version:** the existing `bundled_test.go` table tests
   extend automatically off `bundledNames`; add the cross-link in
   `extending.md`'s bundled-skills paragraph; `CHANGELOG.md` + version bump.

**Do not modify** the skill framework, the SKILL tool, the agent loop, the
prompt builder, or any `pkg/*` API. There is zero code change beyond one slice
entry. The doc is long only because the SKILL.md body is reproduced in full —
the body is the work, the wiring is a one-liner.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 The bundled-skills channel (`internal/skills/bundled`, done in v1.4)

| Symbol / file | Role | This phase |
| --- | --- | --- |
| `embed.go:8` `//go:embed content/*/SKILL.md` + `embed.go:19 bundledNames` | The canonical name list; the embed glob already captures any new `content/<name>/SKILL.md`. | **Append `"build-agent"`** (Task 2). The glob needs no change. |
| `bundled.go:26 Register(reg)` | Iterates `bundledNames`, `buildMeta` → `reg.AddBundled`. nil-safe; returns warnings. | **No change** — picks up the new name for free. |
| `bundled.go:50 buildMeta(name)` | Reads the embedded file, parses the first non-blank line via `skill.ParseTitleLine`, wraps the full body (title line included) in a lazy `BodyFunc`. | **No change**, but note the invariant in §2.4. |
| `pkg/skill.Registry.AddBundled` | Lowest-precedence insert: a same-named disk skill (`SourceHome`/`SourceWorkDir`) **silently wins**, no shadowing warning. | **No change** — gives A8 for free. |
| `pkg/skill.SourceBundled` | The tier constant marking first-party embedded content. | **No change.** |

### 2.2 The SKILL tool framing (`pkg/skill/skill.go`, done)

`SkillTool.Execute` (`skill.go:72-113`) resolves the skill by name and returns
the body wrapped as **`"Follow these instructions for skill `build-agent`:\n\n"`
+ body** (`:107`), trimming a trailing newline and appending **`"\n\nargs: " +
args`** when the model passed `args` (`:109-112`). So the body must read as
*standalone instructions* (the model treats it as guidance to follow, not text
to summarise), and it may consume a free-form `args` string (e.g. the target
module path, or "headless"). The advertised one-liner is the SKILL.md's first
line; the model sees only that line in the `# Skills` block until it dispatches
(`fragments.go:skillsSection`) — **bodies are never inlined at prompt time**.

### 2.3 The SDK surface the skill teaches (`docs/extending.md`, `v1.0.0` Stable)

The skill is a distillation of `extending.md`; each step maps to one of its
sections. The public packages the body names (all verified present in the
`extending.md` package table, `extending.md:13-34`):

| Package | What the skill points at |
| --- | --- |
| `pkg/agent` | `New(Config, ...Option)`, `NewWithProfile`, `NewProfile`, `Agent`, `Controller()`, `Shutdown()`, `AgentDefinition`, `BuildAgentRegistry`, `WithSink`, `WithRootContext`, `WithCustomTool`, `WithSkillRegistry`, `WithPermissionStore/Broker`, `WithHeadlessBypass`, `PermissionMode` |
| `pkg/config` | `Get`, `Load`, `LoadDefault`, `LoadOptions`, `APIConfig`, `LLMProviderConfig` |
| `pkg/llm` (+ `pkg/llm/builtins`) | `Client`, `DefaultRegistry().MustRegister`, the blank-import for built-ins |
| `pkg/tools` (+ `pkg/toolset`, `pkg/tools/kits`) | `Tool`, `Result`, `State`; `toolset.DefaultRegistry().MustRegister`; `kits.GeneralPurposeKit/ReadOnlyKit/CodingKit/ResearchKit` |
| `pkg/event` | `Sink`, `Emit`, `Multi`, `Payload()` |
| `pkg/ui` (+ `pkg/ui/bubbletea`) | `UI`, `Controller`, `bubbletea.New(evvaHome)` |
| `pkg/permission` | `Load`, `NewBroker`, `SetOnRequest` |
| `pkg/skill` | `NewRegistry`, `Add`, `SkillMeta`, `BodyFunc` |
| `pkg/hooks`, `pkg/mcp` | config-driven; `agent.New` auto-loads — the skill defers detail to `setup-hooks` / `setup-mcp` |

### 2.4 Format invariants (live code, stricter than the v1.4 doc)

- **First non-blank line:** `# build-agent <one-line description>`. The first
  whitespace-delimited token after `# ` is the name; the rest is the
  description advertised in `# Skills` (`skill.ParseTitleLine`).
- **The title token MUST equal the folder/`bundledNames` name.** `buildMeta`
  (`bundled.go:63-65`) **returns an error** — not a warning — when
  `titleName != name`. (The v1.4 PRD described a warn-and-continue; the
  shipped code is strict. The embedded title's first token must be exactly
  `build-agent`, or `Register` drops the skill with a warning and A1 fails.)
- **evva wire tool names only.** Tools referenced in the body must match
  evva's names: `read`, `write`, `edit`, `bash`, `grep`, `glob`, `tree`,
  `agent`, `tool_search`, `skill`, `web_search`, `web_fetch`,
  `ask_user_question`, etc. **Never** Claude Code's camel-case names (`Task`,
  `Bash`, `Read`, …) or Claude Code paths (`~/.claude/`).
- **Subagent kinds:** `explore`, `plan`, `general-purpose`.

### 2.5 The two canonical example templates (the body points here)

| File | Lines | Shape | Skill uses it as |
| --- | --- | --- | --- |
| `examples/full-host/main.go` | ~62 | One-call `agent.New(agent.Config{AppConfig: cfg}, WithSink(tui), WithRootContext(ctx))` + `pkg/ui/bubbletea`; a **separate Go module** (own `go.mod` with a `replace`) so any `internal/` import is a compile error — the proof the public surface is self-sufficient. | The **batteries-included** template (Step 3a). |
| `examples/minimal-host/main.go` | ~156 | À-la-carte `agent.NewWithProfile` + a custom provider, a custom tool, a custom skill, a JSON event sink, no TUI. | The **à-la-carte / headless** template (Step 3b). |

Both are stable anchors; the skill points at the *paths* (drift-resistant)
rather than copying their bodies (which would rot). Line counts are
approximate hints, not assertions.

### 2.6 What this skill is NOT (boundary against existing skills)

- **Not `setup-hooks` / `setup-mcp`.** Those configure an *already-running*
  evva session via `settings.json`. `build-agent` authors a *new downstream
  program*. The body cross-references them for the in-session config tasks.
- **Not about editing the evva repo itself.** It scaffolds a host that
  *imports* evva; it never tells the model to modify `internal/` or `pkg/*`.

---

## 3. Goal & acceptance criteria

**Goal:** a fresh evva install advertises a `build-agent` skill that, when
dispatched, gives the model a complete, version-accurate, self-contained
playbook for scaffolding a downstream Go host on evva's public `pkg/*`
surface — so a user asking "help me build an agent with the evva-sdk" gets a
working `main.go` that compiles, not a hand-wave.

Ship is complete when **all** of these pass:

- **A1 — Catalog appears.** A fresh install renders `build-agent` in the
  `# Skills` block as `- build-agent: <description>`, with **no body inlined**.
  `reg.Names()` (after `Register`) contains `"build-agent"`; `Register`
  returns **no warning** for it.
- **A2 — Dispatch returns the body.** `{"skill": "build-agent"}` returns
  `"Follow these instructions for skill `build-agent`:\n\n# build-agent …"`
  — the embedded SKILL.md content, title line included.
- **A3 — Title invariant.** The embedded file's first non-blank line is
  `# build-agent <description>`; `skill.ParseTitleLine` yields
  `titleName == "build-agent"`, so `buildMeta` does **not** error
  (`bundled.go:63-65`).
- **A4 — Tool-name + path hygiene.** The body uses only evva wire tool names
  and `pkg/*` symbols that exist in `extending.md`'s package table; it
  contains **no** Claude Code tool names, **no** `~/.claude*` path, and
  references real example paths (`examples/full-host/main.go`,
  `examples/minimal-host/main.go`).
- **A5 — Self-contained.** The body scaffolds a working host from **its own
  inlined patterns alone** — the references to `docs/extending.md`, the
  examples, and `go doc` are *enrichment for when the evva source is reachable*
  (the realistic case is the user is in their *own* repo, without evva's
  `docs/` on disk). A model with only the skill body can produce a compiling
  `main.go`.
- **A6 — Constructor decision tree.** The body gives an explicit
  `agent.New(Config)` vs `agent.NewWithProfile` table with the recommendation
  to default to `New(Config)`, **and** the load-bearing headless warning:
  a non-interactive host MUST set `WithHeadlessBypass()` (or
  `PermissionMode: "bypass"`) or every approval auto-denies
  (`extending.md:372-393`).
- **A7 — `internal/` guardrail.** The body states, as a hard rule, never to
  import `internal/` (won't compile across modules; not the supported
  surface), and tells the model to check the `extending.md` package table /
  `go doc` for the public symbol instead.
- **A8 — User override still silent.** Authoring
  `<workdir>/.evva/skills/build-agent/SKILL.md` makes the registry resolve
  `build-agent` to the user body with **no shadowing warning** (inherited from
  `AddBundled`; pin it in a test).
- **A9 — Drift-resistance mechanism present.** The body names
  `go doc github.com/johnny1110/evva/pkg/agent` (and siblings) as the
  *authoritative* way to read the exact API of the evva version the host
  imports — explicitly above any snippet in the skill, which may lag.
- **A10 — Tests + docs + version.** `go test ./...` green;
  `internal/skills/bundled` table tests cover `build-agent` (embeds, parses,
  title matches, body loadable, starts with `# `); `extending.md`'s
  bundled-skills list names it; `CHANGELOG.md` `### Added`; `pkg/version`
  bump. No `docs/sdk-stability.md` change (no API surface moves).

---

## 4. Work breakdown (ordered)

### Task 1 — Author `content/build-agent/SKILL.md` (the deliverable)

Create `internal/skills/bundled/content/build-agent/SKILL.md` with the body
below **verbatim** (preserving the leading title line — §2.4). Minor wording
polish is fine; the structure, the decision tree, the code snippets, the
guardrails, and the `go doc` drift mechanism must land as written — they were
adapted to evva's real public surface (`extending.md`) and tool names.

````markdown
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
- **Custom persona (evva→nono)** — `reg, _ := agent.BuildAgentRegistry(cfg.AppHome)`, then `reg.Register(agent.AgentDefinition{Name: "nono", WhenToUse: "...", As: []string{"main","subagent"}, InjectMemory: true, SystemPrompt: "..."})`. Pass `Personas: reg, Persona: "nono"` in `agent.Config`. Or drop `<AppHome>/agents/<name>/{system_prompt.md,tools.yml,meta.yml}` on disk — `New(Config)` loads them automatically.
- **Custom event sink** — implement `pkg/event.Sink` (one method, `Emit(event.Event)`); pass `agent.WithSink(s)`. Fan out with `event.Multi{Sinks: [...]}`. Type-switch with `e.Payload()`.
- **Custom UI** — implement `pkg/ui.UI` (`Emit`, `Attach`, `Run`) and drive the `Controller`; or embed `pkg/ui/bubbletea`.
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
````

> **Authoring notes for the implementer.**
> - The body is fenced above with `````` so the inner ```` ```go ```` blocks
>   render; in the actual `SKILL.md` they are ordinary triple-backtick blocks.
> - The 3a snippet is a faithful trim of `examples/full-host/main.go` — keep it
>   in sync if that example changes signature, but prefer pointing at the file
>   over expanding the snippet (drift-resistance, §5.3).
> - Keep the title line ≤ ~120 chars; it is the advertised description.

### Task 2 — Register the skill

**File:** `internal/skills/bundled/embed.go`. Append `"build-agent"` to
`bundledNames` (one line). Order is cosmetic (the registry sorts by name for
display); put it after `setup-mcp` to keep the slice readable.

```go
var bundledNames = []string{
	"commit",
	"review",
	"security-review",
	"simplify",
	"setup-hooks",
	"setup-mcp",
	"build-agent",
}
```

No other code change. `Register` (`bundled.go:26`) and the `//go:embed
content/*/SKILL.md` glob (`embed.go:8`) pick it up automatically.

### Task 3 — Tests

`internal/skills/bundled/bundled_test.go` already drives its core assertions
off `bundledNames` (the v1.4 design: per-name "embeds, parses, title matches
name, body loadable, starts with `# `"). Confirm those table tests iterate
`bundledNames` so `build-agent` is covered without new code; if any test
hard-codes the six current names, extend it. Add **one** content-specific
regression test for the invariants that matter for this skill:

```go
// TestBuildAgentSkill_Content pins the hygiene rules from the PRD (A4/A6/A7)
// so a future edit can't silently regress them.
func TestBuildAgentSkill_Content(t *testing.T) {
	body, err := readBundled("build-agent")
	if err != nil { t.Fatal(err) }

	// A4: no Claude Code tool names / paths leaked in.
	for _, bad := range []string{"~/.claude", "`Task`", "`Bash`", "`Read`", "`Grep`"} {
		if strings.Contains(body, bad) {
			t.Errorf("build-agent body contains forbidden token %q", bad)
		}
	}
	// A6/A7/A9: the load-bearing instructions are present.
	for _, must := range []string{
		"agent.New(",            // constructor decision tree
		"agent.NewWithProfile",  // the à-la-carte alternative
		"WithHeadlessBypass",    // the headless warning (A6)
		"internal/",             // the guardrail mentions it (A7)
		"go doc",                // drift-resistance (A9)
		"examples/full-host/main.go",
		"examples/minimal-host/main.go",
	} {
		if !strings.Contains(body, must) {
			t.Errorf("build-agent body missing required reference %q", must)
		}
	}
}
```

(If the existing `TestRegister_DiskOverridesBundled` is name-parameterised it
already covers A8 for `build-agent`; otherwise the generic override test on any
bundled name is sufficient — A8 is an `AddBundled` property, not skill-specific.)

### Task 4 — Docs + changelog + version

- **`docs/extending.md`** — in the **Bundled skills** paragraph
  (`extending.md:306-315`), add `build-agent` to the catalog list and one
  clause describing it ("`build-agent` — scaffold a downstream host on
  `pkg/*`"). This is the only `extending.md` edit; do **not** duplicate the
  skill body there.
- **`CHANGELOG.md`** — under `## [Unreleased]` → `### Added`: "Bundled
  `build-agent` skill: walks the user through scaffolding a downstream Go host
  on the evva-sdk (`pkg/agent`), with a constructor decision tree and the two
  example templates."
- **`pkg/version/version.go`** — bump (additive content; a patch or minor per
  the release in flight).
- **`docs/sdk-stability.md`** — **no change**: no public API surface moves
  (`pkg/skill` already documents `AddBundled`/`SourceBundled` from v1.4).
- **`CLAUDE.md`** (optional) — the Roadmap's v1.4 line still says only
  `/commit` ships; if touching it, refresh the bundled list to the live seven.
  Out of scope strictly, noted so it isn't a surprise.

---

## 5. Design decisions & risks (read before authoring)

### 5.1 — Why a *skill*, not (just) a doc

`docs/extending.md` already documents the SDK for *humans reading the repo*. A
skill is the *model-facing, on-demand, in-session* form: it loads into context
only when the user actually asks to build a host, it's framed as imperative
instructions the model follows (not prose it summarises), and it survives the
realistic case where the user is in **their own** repo with no evva `docs/` on
disk. The doc and the skill are complements: the doc is the exhaustive
reference; the skill is the action playbook that *points back at* the doc and
the godoc. Authoring the skill does not make the doc redundant, and vice versa.

### 5.2 — Self-contained body, pointer-rich references

The body must scaffold a compiling host from its **own inlined snippets**
(A5) — because the most common invocation is from a user's own project where
evva's `docs/` and `examples/` are not on disk. But it also *points at* the
example file paths and `go doc` for the full contract when they ARE reachable
(the user is in the evva repo, or evva is in their module cache). This dual
mode is deliberate: inline enough to work cold, reference enough to stay
authoritative warm.

### 5.3 — Drift is the central risk; mitigate by pointing, not copying

A skill embedded in the binary describes an evolving API. Three mitigations,
in order of strength:

1. **`go doc` is named as authoritative** (A9). The model is told, up front,
   that `go doc github.com/johnny1110/evva/pkg/agent` on the imported version
   beats any snippet here. This makes the skill *self-correcting* against
   version skew — the single most important design choice in the phase.
2. **Point at file paths, not file contents.** The body references
   `examples/full-host/main.go` by path; it inlines only a minimal trim. When
   the example evolves, the path still resolves; the trim is a starting point,
   not a contract.
3. **The content regression test** (Task 3) pins the *invariant references*
   (constructor names, the headless warning, the guardrail) so a careless edit
   can't gut them — but it deliberately does **not** assert exact prose, so
   normal wording polish stays cheap.

The snippets WILL eventually lag a signature change. That's acceptable because
of (1): a lagging snippet plus a `go doc` instruction degrades to "the model
reads the real API", not "the model writes broken code with confidence".

### 5.4 — The headless-bypass warning is load-bearing (A6)

The single highest-value line in the body is the Step 3b warning that a
non-interactive host MUST set `WithHeadlessBypass()`. This is the exact trap
`extending.md:372-393` calls out as the friday-driven gotcha: without it, a
headless host's every approval-needing tool call silently auto-denies and the
agent appears broken. A skill that scaffolds a headless host without that
warning would actively produce the bug. Keep it prominent.

### 5.5 — Title-token strictness (A3)

Unlike the v1.4 PRD's described warn-and-continue, the shipped `buildMeta`
(`bundled.go:63-65`) *errors* when the title token ≠ `bundledNames` entry, so
`Register` drops the skill and the catalog silently loses it. The first line
MUST be exactly `# build-agent <description>`. The table test (Task 3) catches
a typo here at CI time, not boot time.

### 5.6 — Scope boundary against `setup-hooks` / `setup-mcp`

There is a real risk of the model reaching for `build-agent` when the user just
wants to add a hook to their *running* session (a `setup-hooks` job), or vice
versa. The body's first paragraph and its closing guardrail both draw the line
explicitly: `build-agent` = author a new host program; `setup-*` = configure
an existing session. The advertised one-liner leads with "Scaffold a Go
program … with the evva-sdk" to bias the model correctly from the catalog line
alone.

### 5.7 — No framework change, deliberately

This phase touches one slice entry and one Markdown file. It does **not** add a
`SkillMeta` field, a new tier, or a tool. If a future need arises for
generated/parameterised skill bodies (e.g. injecting the live `go.mod` module
path into the scaffold), that's a `BodyFunc` enhancement on the bundled
channel — out of scope here; the static body plus the `args` string the SKILL
tool already forwards (`skill.go:109-112`) is sufficient for v1.

---

## 6. What "done" feels like (worked example)

1. User, in their own empty Go project: *"I want to build a financial-advisor
   agent in Go using evva — like the nono persona, headless, talking to
   DeepSeek."*
2. evva, seeing the `build-agent` catalog line, dispatches
   `{"skill": "build-agent", "args": "headless, deepseek, custom persona"}`.
   The body loads as instructions.
3. evva runs Step 1 mentally (headless ✓, deepseek ✓, default tools ✓, one
   custom persona ✓), picks **`agent.New(Config)` headless** (Step 2 + 3b),
   and scaffolds:
   - `go mod init` (or adds to the user's module) + `go get
     github.com/johnny1110/evva@latest`;
   - a `main.go` from the 3b template with `agent.WithHeadlessBypass()`, a
     custom persona registered via `agent.BuildAgentRegistry` +
     `reg.Register(agent.AgentDefinition{Name: "nono", ...})`, and
     `cfg.LLMProviderConfig["deepseek"]` creds from `DEEPSEEK_API_KEY`;
   - the `_ "…/pkg/llm/builtins"` blank import.
4. evva runs `go doc github.com/johnny1110/evva/pkg/agent` to confirm
   `BuildAgentRegistry` / `AgentDefinition` field names on the imported
   version (A9), then `go build ./...` (Step 6) — fixing one import — and
   reports the host compiles and what env var to set before the first run.
5. The user has a ~50-line working host they can `go run`. No `internal/`
   import anywhere; nothing in the evva repo was touched.

---

## 7. Out of scope (revisit later)

- **A parameterised / generated body** (e.g. injecting the user's module path
  or detected provider into the scaffold via a `BodyFunc`) — the static body +
  forwarded `args` is enough for v1 (§5.7).
- **A `/new-tool`, `/new-provider`, or `/new-persona` companion skill** — each
  extension point *could* get its own focused skill; this phase ships the
  umbrella playbook first. Split later if the umbrella proves too broad.
- **Editing the evva repo itself** — `build-agent` only ever scaffolds a host
  that imports evva; contributing to evva is a different (un-skilled) task.
- **Non-Go hosts / FFI / HTTP-gRPC adapter** — `extending.md` and the SDK are
  in-process Go only (`sdk-v2.md` out-of-scope); the skill stays Go-only.
- **Bundling `docs/extending.md` or the example files into the binary** so the
  model can always read them — rejected; the `go doc` + module-cache path
  (A9) is the lighter, version-accurate alternative.
- **A `setup-hooks` / `setup-mcp` rewrite** — unchanged; `build-agent`
  cross-references them, it doesn't subsume them.

---

## 8. Verification checklist (PR gate)

- [ ] **Task 1:** `content/build-agent/SKILL.md` exists; first line is
      `# build-agent <description>`; body uses only evva wire tool names and
      `pkg/*` symbols; no `~/.claude*`, no Claude Code tool names (A3/A4).
- [ ] **Task 1:** body contains the constructor table (`agent.New` vs
      `NewWithProfile`), the `WithHeadlessBypass` warning (A6), the
      `internal/` guardrail (A7), the `go doc` drift line (A9), and both
      example paths (A4).
- [ ] **Task 2:** `"build-agent"` is in `bundledNames`; `go build ./...` green;
      `Register` on an empty registry adds it with **no warning** (A1).
- [ ] **A2:** `SKILL` dispatch with `{"skill":"build-agent"}` returns
      `"Follow these instructions for skill `build-agent`:\n\n# build-agent …"`.
- [ ] **A5:** the body's inlined 3a/3b snippets are complete enough to compile
      a host without reading any external file (spot-check by eye against the
      `pkg/agent` API).
- [ ] **A8:** a `<workdir>/.evva/skills/build-agent/SKILL.md` override wins
      with no shadowing warning (existing `AddBundled` test covers; confirm it
      parameterises over names or add a case).
- [ ] **Task 3:** `bundled_test.go` table tests cover `build-agent`;
      `TestBuildAgentSkill_Content` passes; `go test ./internal/skills/...`
      and `go test ./...` green.
- [ ] **Task 4:** `extending.md` bundled-skills list names `build-agent`;
      `CHANGELOG.md` `### Added`; `pkg/version` bumped; no
      `docs/sdk-stability.md` change needed.
- [ ] **Manual (TTY):** fresh `EVVA_HOME`; start evva; confirm `build-agent`
      shows in the `# Skills` block (via logs / prompt dump); dispatch it; ask
      it to scaffold a headless host; confirm the produced `main.go` includes
      `WithHeadlessBypass()` and builds.

---

## 9. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `internal/skills/bundled/content/build-agent/SKILL.md` | **New** — the skill body | Task 1 |
| `internal/skills/bundled/embed.go` | Edit — append `"build-agent"` to `bundledNames` | Task 2 |
| `internal/skills/bundled/bundled_test.go` | Edit — confirm table coverage; add `TestBuildAgentSkill_Content` | Task 3 |
| `docs/extending.md` | Edit — add `build-agent` to the bundled-skills paragraph | Task 4 |
| `CHANGELOG.md` | Edit — `### Added` | Task 4 |
| `pkg/version/version.go` | Edit — bump | Task 4 |
| `CLAUDE.md` | Edit (optional) — refresh the stale v1.4 bundled-skills list | Task 4 |

---

## 10. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 1 — author `build-agent/SKILL.md` | ~180 prose | 2.5 h |
| Task 2 — register (one slice entry) | ~1 | 5 min |
| Task 3 — content regression test (+ confirm table coverage) | ~40 | 45 min |
| Task 4 — extending.md + changelog + version | ~15 | 30 min |

By far the cheapest of the PRDs: no new Go package, no API surface, no agent-
loop or prompt change — one Markdown file, one slice entry, one test. The
entire risk and value live in the **quality of the SKILL.md body** (§5.1–5.6):
the `go doc` drift line (A9) and the headless-bypass warning (A6) are the two
highest-leverage sentences in it.
