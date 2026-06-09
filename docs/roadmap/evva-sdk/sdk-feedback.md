# evva SDK feedback — from building friday

Notes collected while building friday (Phase 15 of evva's roadmap)
against the evva public surface at `v0.2.4-alpha.1`. The goal is a
concrete, actionable list — every item names the file/line in evva
where the rough edge lives, and what would smooth it.

Loose categories:

- **Naming**: identifier choices that surprised me as a consumer.
- **Discoverability**: things that work fine once you know them but
  aren't documented or hinted at in code.
- **Defaults**: factory defaults that point at evva itself rather than
  the calling AppName.
- **Ergonomics**: missing accessors / helpers that the consumer ends up
  hand-rolling.

---

## Findings

### 1. Naming — `event.ErrorPayload.Err` is `error`, not `string`

`pkg/event/event.go:237`

```go
type ErrorPayload struct {
    Stage string
    Err   error
}
```

Consumer code reads `e.Error.Err.Error()` to stringify. Caught me on
the first render-event pass — I wrote `if e.Error.Err != ""` (treating
it as a string). Either:

- Rename to `ErrMsg string` (matches the rest of the payloads, which
  are all stringy), or
- Document the contract in the doc comment.

### 2. Naming — `IterLimitPayload.Reached` vs `RunEndPayload.Iters`

`pkg/event/event.go:151` and `:142`

```go
type RunEndPayload   struct { Iters int; ... }
type IterLimitPayload struct { Reached int }
```

Same concept (loop iteration count), two field names. Pick one.

### 3. Defaults — first-run YAML always writes `default_profile: evva`

`pkg/config/load.go` → `LoadFileConfig` → seed config

When friday calls `config.Load(LoadOptions{AppName: "friday", ...})`,
the generated `~/.friday/config/friday-config.yml` still contains:

```yaml
default_profile: evva
```

It's harmless (friday calls `NewWithProfile` so the YAML default is
never used), but it's a confusing artefact for a user inspecting their
friday-flavoured config. Suggested fix: `LoadOptions.AppName` propagates
into the seeded YAML's `default_profile`, with a fallback to `evva`
when the field is empty.

### 4. Defaults — `WithPermissionMode("bypass")` is mandatory for a
   non-evva consumer, but the requirement is buried

`pkg/agent/new_with_profile.go:36-42`

The default permission broker auto-DENIES every approval request. For a
TUI like friday that doesn't render an approval overlay, every tool
call needing approval (bash that the classifier doesn't auto-allow,
write/edit in `ModeDefault`, etc.) silently turns into an error.

The minimal-host example does pass `WithPermissionMode("bypass")`, but
this is the kind of footgun that needs a louder pointer:

- An `agent.WithHeadlessBypass()` helper that bundles
  `WithPermissionMode("bypass")` with a comment about "no approval UI
  means tool calls auto-succeed; only use in trusted environments."
- Or `NewWithProfile` could log `slog.Warn` once on first
  `BehaviorDeny` if the caller never installed a real broker.

### 5. Ergonomics — `cfg.LLMProviderConfig` is a public map; expected
   pattern is direct assignment

`pkg/config/config.go` and `examples/minimal-host/main.go:98`

```go
cfg.LLMProviderConfig["deepseek"] = config.APIConfig{
    ApiURL:    "https://api.deepseek.com",
    ApiSecret: apiKey,
    Models:    []constant.Model{constant.DEEPSEEK_V4_PRO},
}
```

This works but feels low-level. Three friction points:

- `Models []constant.Model` is duplicative with `cfg.DefaultModel`
  (friday only uses one model — having to list it in `Models` too is
  bookkeeping with no purpose).
- The map slot doesn't validate on write — a typo in the provider key
  silently registers nothing.
- There's no setter that mutates under `cfg.mu` — concurrent writes
  from two goroutines would race.

Suggested helper:

```go
func (c *Config) SetProviderCredentials(name, apiURL, apiKey string) error
```

### 6. Ergonomics — friday composes the "general-purpose toolkit" by
   hand from per-family `Names()`

`/mnt/friday/internal/bootstrap/bootstrap.go:60-66`

```go
active = append(active, fs.Names()...)
active = append(active, shell.Names()...)
active = append(active, todo.Names()...)
active = append(active, util.Names()...)
active = append(active, tools.TOOL_SEARCH)
```

This works but every downstream consumer wanting a "general coding
agent" duplicates the same boilerplate. A `tools.GeneralPurposeKit()`
helper in `pkg/tools/` (returning the canonical evva general-purpose
active+deferred lists) would let friday write:

```go
active, deferred := tools.GeneralPurposeKit()
```

Document the kit composition so consumers can copy + tweak.

### 7. Ergonomics — `agent.NewProfile` model arg is `string`, not
   `constant.Model`

`pkg/agent/profile.go:62`

```go
func NewProfile(name, systemPrompt string, activeTools []tools.ToolName,
    providerName, model string, opts ProfileOptions) (Profile, error)
```

Friday already has the typed `constant.DEEPSEEK_V4_PRO` constant and
has to call `string(...)` on it:

```go
agent.NewProfile("friday", SystemPrompt, active,
    "deepseek", string(constant.DEEPSEEK_V4_PRO),
    agent.ProfileOptions{...})
```

Accepting either `string` or `constant.Model` (e.g. by accepting a
`constant.Model` and converting `string` callers with `constant.Model(s)`
at the boundary) would be a tiny QoL win.

### 8. Discoverability — bubbletea / bubbles version pinning is implicit

evva's `go.mod` requires:

- `github.com/charmbracelet/bubbletea v1.3.10`
- `github.com/charmbracelet/bubbles v1.0.0`
- `github.com/charmbracelet/lipgloss v1.1.1-...`

Friday pinned the same versions explicitly so its `tea.Program` type
matches the one evva expects on `pkg/ui.UI.Run`. A downstream that
naively `go get`s the latest bubbletea risks a subtle type-mismatch on
the program handle if evva later upgrades. Document the version
contract in `docs/extending.md`.

### 9. Ergonomics — env-var loading is opinionated

`pkg/config/load.go:76` calls `godotenv.Load(appHome + "/.env")` and
then reads a fixed list of canonical names: `APP_ENV`, `LOG_LEVEL`,
`LOG_DIR`, `LOG_FORMAT`, `SKILLS_DIR`, `USER_PROFILE`,
`EVVA_AUTO_MEMORY`.

Friday wanted to accept friendlier aliases (`LOGDIR`, `LOGLEVEL`,
`MAX_ITERS`). The current solution is to translate before/after
`Load`:

```go
// Before Load: promote aliases into canonical names so godotenv sees
// them.
if v := os.Getenv("LOGDIR"); v != "" && os.Getenv("LOG_DIR") == "" {
    os.Setenv("LOG_DIR", v)
}

// After Load: apply config-shaped overrides for vars evva doesn't
// natively read.
if v := os.Getenv("MAX_ITERS"); v != "" { ... cfg.SetMaxIterations(n) ... }
```

A `LoadOptions.EnvAliases map[string]string` field that lets the
consumer map their preferred names to evva's canonicals would replace
the pre-Load shim entirely. And `LoadOptions.EnvOverrides
map[string]func(*Config) error` (or similar) for vars without a YAML
hook would replace the post-Load shim.

### 10. Discoverability — `event.Event` payload field selection
   requires reading source

Twenty `Kind` constants, twenty `*Payload` pointer fields on `Event`,
no comment-table mapping them. Consumers grep `pkg/event/event.go` to
discover that `KindToolUseStart` → `e.ToolUseStart`. A
`func (e Event) Payload() any` switch would be a wrist-saving accessor
for callers who only want the "the thing that goes with this kind":

```go
switch p := e.Payload().(type) {
case *event.TextPayload:    // ...
case *event.ToolUseStartPayload: // ...
}
```

Strictly redundant — the field-pointer pattern is fine once you know
it — but a clear `Payload()` helper in the doc would be a nice
on-ramp.

### 11. Ergonomics — `agent.Agent.Session()` returns `SessionInfo`
   with `MessageCount` but it's not in the public docstring

`pkg/agent/agent.go:118-127` shows the SessionInfo conversion:

```go
return SessionInfo{
    MessageCount:    len(s.GetMessages()),
    InputTokens:     u.InputTokens,
    OutputTokens:    u.OutputTokens,
    LastInputTokens: s.LastTurnInputTokens(),
}
```

Friday's status footer uses `MessageCount` to show "12 msgs" — but the
field has no doc comment in `types.go`. Add a one-line comment per
field so editors surface them.

### 12. Naming — `WithPermissionMode` accepts a string, but a typo
   silently degrades to default

`pkg/agent/agent.go:69-73`:

```go
if cfg.PermissionMode != "" {
    if m, ok := permission.ParseMode(cfg.PermissionMode); ok {
        permMode = m
    }
}
```

If the consumer types `"by-pass"` (with a dash) the agent silently
falls back to `ModeBypass` (the seed default for `agent.New`) or to
whatever the profile carried. No log, no error. A typed parameter
(`agent.PermissionMode` enum exported from `pkg/agent`) would catch
the typo at compile time.

---

## What worked nicely

For balance — these were genuinely smooth:

- The `examples/minimal-host/main.go` template covers ~80% of friday's
  setup. Reading it end-to-end gave me the wiring pattern for sink,
  Profile, agent in about 5 minutes.
- `_ "github.com/johnny1110/evva/pkg/llm/builtins"` for blank-import
  provider registration is exactly the right idiom — clean and
  Go-native.
- `agent.NewWithProfile` separating Profile construction from agent
  construction means tests can build profiles without an LLM client.
- `event.Sink` is a tiny one-method interface — easy to satisfy from a
  bubbletea `tea.Program` holder.
- The published evva tag (`v0.2.4-alpha.1`) resolves cleanly via
  `go mod tidy`; no replace directive needed.
