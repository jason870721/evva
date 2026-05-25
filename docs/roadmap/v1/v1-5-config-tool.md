# v1.5 — ConfigTool — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** ready to build.
> **Target release:** `v1.5.0` (additive, minor bump under the Stable-tier promise).
> **Roadmap source:** `CLAUDE.md` → Roadmap → *v1.5 — ConfigTool*.
> **Reference source:** `ref/src/tools/ConfigTool/` (4 files: `ConfigTool.ts`, `prompt.ts`, `supportedSettings.ts`, `constants.ts`).

---

## 1. TL;DR — what this phase actually is

evva today has a fully-typed config surface (`pkg/config.Config` with
twelve+ `Set*` accessors) and an interactive `/config` overlay
(`pkg/ui/bubbletea/components/overlays/config.go:262-346`) that lets the
**user** view and edit every tunable setting. The **model** has no
equivalent handle: when the user says "turn off auto-memory" or
"change my OpenAI key", the model has to instruct them to type
`/config` and walk the overlay by hand.

ConfigTool closes that gap with one focused tool, ported from
`ref/src/tools/ConfigTool/` and shaped to evva's narrower config
surface:

- One tool, `config`, with one input schema:
  `{setting: string, value?: string | bool | number}`.
- Omit `value` → **GET** (auto-allow; read-only).
- Supply `value` → **SET** (`ask` permission with a "Set `<key>` to
  `<value>`" message).
- A `SUPPORTED_SETTINGS` registry — one Go map mapping setting key →
  `{type, description, options, get, set, validate}` — that wraps the
  typed `*config.Config` accessors evva already has. **The map is the
  single source of truth**: both the tool's behaviour and the tool's
  prompt are derived from it.

This is a small focused build phase. Concretely:

1. **Add** `tools.CONFIG` constant in `pkg/tools/name.go`.
2. **Create** `internal/tools/config/` with the `SUPPORTED_SETTINGS`
   table, the `ConfigTool` implementation, and a prompt generator.
3. **Register** the factory in `internal/toolset/builtins.go`.
4. **Activate** the tool on the Main profile in
   `internal/agent/profiles.go`.
5. **Test** the registry, the get/set paths, validation, and the
   permission gate.
6. **Document** + bump version.

Do **not** invent a parallel config system. The `Set*` methods on
`*config.Config` already handle validation, mutex protection, and
SaveFile persistence — the tool's only job is to expose them by name.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 `pkg/config/config.go` (Stable) — the wrappable surface

Every setting the `/config` overlay exposes already has a typed setter
that handles validation + persistence:

| Setting (overlay label) | Config field | Setter | Notes |
| --- | --- | --- | --- |
| `max_iterations` | `DefaultMaxIterations` | `SetMaxIterations(int)` | validates `>0` |
| `max_tokens` | `DefaultMaxTokens` | `SetMaxTokens(int)` | validates `>=0`; 0 = provider default |
| `auto_compact_threshold` | `AutoCompactThreshold` | `SetAutoCompactThreshold(float64)` | validates `(0,1]` |
| `display_thinking` | `DisplayThinking` | `SetDisplayThinking(bool)` | runtime-mutable; under `c.mu` |
| `enable_auto_memory` | `EnableAutoMemory` | `SetEnableAutoMemory(bool)` | takes effect next boot for prompt/toolset |
| `fetch_max_bytes` | `FetchMaxBytes` | `SetFetchMaxBytes(int)` | validates `>0` |
| `tavily_api_key` | `TavilyAPIKey` | `SetTavilyAPIKey(string)` | empty disables `web_search` |
| `<provider>.api_key` | `LLMProviderConfig[name]` | `SetProviderAPIKey(name, key)` | empty removes cloud providers; ollama keeps key="" |
| `<provider>.api_url` | `LLMProviderConfig[name]` | `SetProviderAPIURL(name, url)` | empty resets to constant default |
| `default_effort` | `DefaultEffort` | `SetDefaultEffort(string)` | validates `low|medium|high|ultra` |
| `default_profile` | `DefaultProfile` | `SetDefaultProfile(string)` | no validation here (registry lives in `internal/agent`) |
| `permission_mode` | `PermissionMode` | *— no typed setter today* | see Design Decision §5.4 |
| (provider, model) pair | `DefaultProvider`/`DefaultModel` | `SetDefaultModel(provider, model)` | typed tuple; see Design Decision §5.3 |

Each setter takes `c.mu`, mutates the field, and calls `SaveFile()` to
persist. No new locking, no new SaveFile path, no new YAML schema work
is needed.

Read accessors that exist today:

- `GetDisplayThinking() bool`
- `GetEnableAutoMemory() bool`
- `GetAutoCompactThreshold() float64`
- `Effort() string`

Every other field is read directly off the struct (locking is
implicit because the field is only set under `c.mu`). The ConfigTool's
GET path uses these accessors where they exist and reads the raw field
under `c.mu.RLock` where they don't (see Task 2 — the registry
provides a `get func(*config.Config) any` field so each entry picks
the right read path).

### 2.2 `internal/tools/dev/feedback.go` — the single-file tool template

`FeedbackTool` is the closest existing tool to ConfigTool in shape:

- One Go file under `internal/tools/<family>/`.
- Constructor takes `*config.Config`.
- Implements `tools.Tool`: `Name()` returns `string(tools.FEEDBACK)`,
  `Description()` returns a const string, `Schema()` returns inline
  JSON schema, `Execute(ctx, logger, input)` parses input + does work +
  returns a `tools.Result`.
- A `Names() []tools.ToolName` package-level helper that
  `internal/agent/profiles.go` calls to assemble the active-tools
  slice for an environment-gated tool family.

ConfigTool follows the same layout, splitting into `config.go`
(the tool), `settings.go` (the registry + helpers), and `prompt.go`
(the dynamic prompt generator) only because the registry table is
~150 LOC on its own.

### 2.3 `internal/toolset/builtins.go` — the factory wire-up

Pattern (from `builtins.go:170-172`):

```go
r.MustRegister(tools.FEEDBACK, func(s tools.State) (tools.Tool, error) {
    return dev.NewFeedback(s.Config()), nil
})
```

ConfigTool registers identically:

```go
r.MustRegister(tools.CONFIG, func(s tools.State) (tools.Tool, error) {
    return configtool.New(s.Config()), nil
})
```

`tools.State` already exposes `Config() *config.Config`; no new
state-interface field is needed.

### 2.4 `internal/agent/profiles.go` (Stable) — the activation seam

The Main profile decides which tools land in `ActiveTools` vs.
`DeferredTools`. ConfigTool is **active** (the model needs to surface
it on demand without first calling `tool_search`):

```go
// near line 130, alongside ENTER_PLAN_MODE / EXIT_PLAN_MODE
activeTools = append(activeTools, tools.CONFIG)
```

No subagent (`Explore`, `Plan`, `GeneralPurpose`) should get ConfigTool
— they run cold for narrow tasks and have no business mutating user
config. Confirm by inspection of `internal/agent/sysprompt/agent_def.go`.

### 2.5 `pkg/permission` (Stable) — the gate

Permission is already wired into the agent loop and (post-v1.1) into
the PreToolUse hook. The tool itself does **not** call the broker —
it returns its result; the agent's permission gate inspects the call
and decides whether to ask, allow, or deny based on rules + the tool's
own self-classification.

Today the gate consults:

- Tool rules from `<APP_HOME>/permissions.json` and
  `<workdir>/.evva/permissions.json` (`pkg/permission.Store`).
- The active permission mode (`default`/`accept_edits`/`plan`/`bypass`).
- The tool's `isReadOnly` / `isConcurrencySafe` shape *if* the tool
  exposes those classifications.

evva's current tool surface (`pkg/tools.Tool`) does **not** have an
`IsReadOnly(input json.RawMessage) bool` method — that classification
lives in the **permission rules** (`tool=read action=allow` etc.),
not on the tool itself. So ConfigTool cannot self-declare GET as
read-only via a method; instead, the **default permission rules**
need a new entry for `config` distinguishing get-vs-set. See Task 5
for the mechanism — TL;DR: ship a built-in default rule
`config` + `value=undefined` → allow, else → ask.

### 2.6 `pkg/ui/bubbletea/components/overlays/config.go:262-346` — the canonical field catalog

The field-builder function `buildConfigFields(cfg, ctrl)` is the
**definitive list of user-visible settings today**. ConfigTool's
registry must match it 1:1 (plus a small set of model-only-relevant
settings; see Task 2). If the overlay grows a field later, the tool's
registry should grow the same field — these two surfaces describe the
same user-visible setting matrix from two angles (interactive vs.
LLM-callable). The CHANGELOG entry for any future setting addition
will need to mention both.

### 2.7 Reference (`ref/src/tools/ConfigTool/`)

| File | What it does | Port? |
| --- | --- | --- |
| `constants.ts` | One const: `CONFIG_TOOL_NAME = 'Config'` | no — evva's tool name comes from `tools.CONFIG` constant |
| `prompt.ts` | `DESCRIPTION` const + `generatePrompt()` that walks the registry building the "Configurable settings list" markdown body | **yes**, port the structure to `prompt.go` |
| `supportedSettings.ts` | The `SUPPORTED_SETTINGS: Record<string, SettingConfig>` table + helpers (`isSupported`, `getConfig`, `getOptionsForSetting`, `getPath`) | **yes**, port the **shape**; the **content** is evva-specific (drop voice mode, theme, remoteControlAtStartup, taskCompleteNotifEnabled, classifierPermissionsEnabled — none apply to evva) |
| `ConfigTool.ts` | The tool definition: input/output schemas, `checkPermissions`, the `call` body, get/set dispatch, boolean coercion, options validation, async write-validation, error mapping | **yes**, port the dispatch logic — but lean on evva's typed setters for everything `ref` does inline (boolean coerce, options validate, async validate) |
| `UI.tsx` | Claude Code TUI rendering of the tool call/result | **no** — evva's TUI renders via the generic `tools.Result.Content` path |

The ref `ConfigTool.ts` has a lot of voice-mode pre-flight (lines
116-126, 233-308). **Delete it all on port.** evva has no voice mode
and no GrowthBook gates; the port should not carry the dead branches
forward as `if false { … }` comments.

---

## 3. Goal & acceptance criteria

**Goal:** the LLM can call `config({"setting":"<key>"})` to read any
exposed setting and `config({"setting":"<key>", "value":<v>})` to
change it. Reads happen without a permission prompt; writes go
through `ask`. The set of supported settings exactly mirrors the
`/config` overlay, plus a small list of model-relevant settings the
overlay doesn't expose (`default_effort`, `default_profile`).

Ship is complete when **all** of these pass:

- **A1 — Tool registered + active.**
  `toolset.DefaultRegistry().Build("config", state)` returns a non-nil
  `*configtool.Tool`; the Main profile's `ActiveTools` includes
  `tools.CONFIG`; subagent profiles do **not**.
- **A2 — GET, known setting.**
  `config({"setting":"display_thinking"})` returns a `tools.Result`
  whose `Content` reads `display_thinking = true` (or `false`).
- **A3 — GET, unknown setting.**
  `config({"setting":"nope"})` returns
  `tools.Result{IsError:true, Content:"Unknown setting: \"nope\""}`
  with no panic and no log spam.
- **A4 — SET, valid value.**
  `config({"setting":"display_thinking", "value":false})` mutates
  `cfg.DisplayThinking` to `false`, calls `SaveFile()` (verify via
  re-reading the YAML), and returns
  `Set display_thinking to false`.
- **A5 — SET, invalid value (typed).**
  `config({"setting":"max_iterations", "value":-3})` returns
  `tools.Result{IsError:true, Content:"max_iterations must be > 0, got -3"}`
  — the error comes from `cfg.SetMaxIterations`; the tool's job is to
  surface it cleanly.
- **A6 — SET, invalid value (enum).**
  `config({"setting":"default_effort", "value":"insane"})` returns
  `tools.Result{IsError:true, Content:"invalid effort level \"insane\": want low|medium|high|ultra"}`.
- **A7 — Boolean coercion.**
  `config({"setting":"display_thinking", "value":"true"})` works
  (string is coerced to bool); `value:"yes"` does not (returns
  "requires true or false").
- **A8 — Provider settings.**
  `config({"setting":"openai.api_key", "value":"sk-..."})` calls
  `cfg.SetProviderAPIKey("openai", "sk-...")`; the YAML's
  `providers.openai.api_key` reflects the new value.
- **A9 — Permission posture.**
  In `default` permission mode, GET calls do **not** raise a
  `RequestApproval` event; SET calls **do**, with the message
  `Set <key> to <value>`. Verify the rule lands in the agent's
  default rule set.
- **A10 — Prompt generation.** `generatePrompt()` produces a markdown
  body that includes every key in `SUPPORTED_SETTINGS` with its
  description and (when applicable) options enumerated. The body is
  injected as the tool's `Description()` return (or split between
  Description and a long-prompt field — see Task 4).
- **A11 — Tests.** Unit tests cover the registry, GET path, SET path
  (success + each error class), boolean coercion, enum validation,
  and unknown-setting rejection. One integration-style test exercises
  the tool through the toolset registry to prove the wiring lands.
- **A12 — Docs + version + changelog.** `docs/sdk-stability.md` notes
  the new tool (no new public package); `docs/user-guide/en/user-guide.md`
  gains a short section under "Configuration" mentioning the model can
  read/write settings; zh-tw mirror updated; `CHANGELOG.md` gains a
  `## [v1.5.0]` block; `pkg/version.Version` → `"1.5.0"`.

---

## 4. Work breakdown (ordered)

### Task 0 — Add the `CONFIG` tool name constant

**File:** `pkg/tools/name.go`.

Find the "Others." block (~line 126) and add:

```go
// CONFIG — get or set evva configuration settings. One tool, one
// {setting, value?} shape: read when value is omitted, write when set.
// Active on the Main profile; subagents don't get it. Permission posture:
// auto-allow on read, ask on write.
CONFIG ToolName = "config"
```

> **Naming note:** ref uses `Config` (CapitalCase user-facing). evva's
> convention is `snake_case_tool_names` everywhere (`read`, `write`,
> `bash`, `web_fetch`, `tool_search`, `update_user_profile`, …). Stick
> with `config` for the wire name; the ref capitalization is a TS-side
> cosmetic the Go port doesn't inherit.

Do this **first** — every subsequent file references `tools.CONFIG`,
so adding the constant up front means no import-cycle dance later.

### Task 1 — Create `internal/tools/config/` package skeleton

Three files (parallel to `internal/tools/dev/` but slightly bigger due
to the registry):

```
internal/tools/config/
├── config.go      # ~150 LOC: Tool, New, Name, Description, Schema, Execute
├── settings.go    # ~250 LOC: SettingConfig, SUPPORTED_SETTINGS, helpers
├── prompt.go      # ~80  LOC: generatePrompt() over the registry
├── settings_test.go
├── config_test.go
└── prompt_test.go
```

**Package clause:** `package configtool` (not `config` — that name
collides with the `pkg/config` import and would force aliases at every
call site).

Add a `Names()` helper for `internal/agent/profiles.go` to call:

```go
// Names is the set of tools this family contributes to a profile's
// ActiveTools. Currently just CONFIG.
func Names() []tools.ToolName {
    return []tools.ToolName{tools.CONFIG}
}
```

### Task 2 — Define `SUPPORTED_SETTINGS`

**File:** `internal/tools/config/settings.go`.

Port the **shape** of `ref/src/tools/ConfigTool/supportedSettings.ts`,
not the content. The Go-side type:

```go
// SettingType discriminates how the value is coerced and rendered.
type SettingType int

const (
    TypeString SettingType = iota
    TypeBool
    TypeInt
    TypeFloat
    TypeSecret // string but masked on read
)

// SettingConfig describes one tunable setting the tool exposes.
//
// Get and Set are deliberately untyped (`any` in / out) so the table
// can mix integers, floats, booleans, strings, and provider tuples in
// one map. Each entry's Type tells the dispatch code how to coerce the
// incoming value before calling Set.
type SettingConfig struct {
    Type        SettingType
    Description string
    // Options, when non-nil, restricts the accepted set of values.
    // Applies to TypeString only.
    Options []string
    // Get reads the current value off cfg. Implementations should use
    // the typed accessor (GetDisplayThinking, Effort, etc.) when one
    // exists, falling back to a direct field read under cfg.mu.RLock
    // for fields without an accessor.
    Get func(cfg *config.Config) any
    // Set persists the new value. Implementations delegate to the typed
    // setter on cfg (SetMaxIterations, SetEnableAutoMemory, …) so
    // validation + locking + SaveFile happen exactly once, in the
    // setter that owns the field.
    Set func(cfg *config.Config, value any) error
    // FormatOnRead, when non-nil, transforms the raw value for display.
    // Used by TypeSecret to mask the value (see maskSecret in settings.go).
    FormatOnRead func(value any) any
}
```

The registry itself — model after `buildConfigFields` in the overlay
file plus the model-only entries called out in §5.3:

```go
// SUPPORTED_SETTINGS is the model-facing setting catalog. Mirror the
// /config overlay's buildConfigFields, plus a small set of model-relevant
// settings the overlay doesn't expose (default_effort, default_profile).
//
// Adding a new setting? Update buildConfigFields too; the two surfaces
// describe the same matrix from different angles.
var SUPPORTED_SETTINGS = map[string]SettingConfig{
    "max_iterations": {
        Type:        TypeInt,
        Description: "Agent loop iteration cap; hitting it pauses for user continue",
        Get:         func(c *config.Config) any { return c.DefaultMaxIterations },
        Set:         func(c *config.Config, v any) error {
            n, err := coerceInt(v)
            if err != nil {
                return err
            }
            return c.SetMaxIterations(n)
        },
    },
    "max_tokens": {
        Type:        TypeInt,
        Description: "Per-completion output token cap; 0 lets the provider apply its default",
        Get:         func(c *config.Config) any { return c.DefaultMaxTokens },
        Set:         func(c *config.Config, v any) error { n, err := coerceInt(v); if err != nil { return err }; return c.SetMaxTokens(n) },
    },
    "auto_compact_threshold": {
        Type:        TypeFloat,
        Description: "Fraction of context (0,1] at which auto-compaction triggers",
        Get:         func(c *config.Config) any { return c.GetAutoCompactThreshold() },
        Set:         func(c *config.Config, v any) error { f, err := coerceFloat(v); if err != nil { return err }; return c.SetAutoCompactThreshold(f) },
    },
    "display_thinking": {
        Type:        TypeBool,
        Description: "Show the model's reasoning trace in the TUI",
        Get:         func(c *config.Config) any { return c.GetDisplayThinking() },
        Set:         func(c *config.Config, v any) error { b, err := coerceBool(v); if err != nil { return err }; return c.SetDisplayThinking(b) },
    },
    "enable_auto_memory": {
        Type:        TypeBool,
        Description: "Enable update_user_profile + update_project_memory tools and the prompt's memory section",
        Get:         func(c *config.Config) any { return c.GetEnableAutoMemory() },
        Set:         func(c *config.Config, v any) error { b, err := coerceBool(v); if err != nil { return err }; return c.SetEnableAutoMemory(b) },
    },
    "fetch_max_bytes": {
        Type:        TypeInt,
        Description: "Cap on the text web_fetch returns from one URL",
        Get:         func(c *config.Config) any { return c.FetchMaxBytes },
        Set:         func(c *config.Config, v any) error { n, err := coerceInt(v); if err != nil { return err }; return c.SetFetchMaxBytes(n) },
    },
    "tavily_api_key": {
        Type:         TypeSecret,
        Description:  "Tavily API key for web_search; empty disables the tool",
        Get:          func(c *config.Config) any { return c.TavilyAPIKey },
        Set:          func(c *config.Config, v any) error { return c.SetTavilyAPIKey(toString(v)) },
        FormatOnRead: maskSecret,
    },
    "default_effort": {
        Type:        TypeString,
        Description: "Thinking effort level used at boot; overridden at runtime by /effort",
        Options:     []string{"low", "medium", "high", "ultra"},
        Get:         func(c *config.Config) any { return c.Effort() },
        Set:         func(c *config.Config, v any) error { return c.SetDefaultEffort(toString(v)) },
    },
    "default_profile": {
        Type:        TypeString,
        Description: "Persona that boots on launch; must match a registered agent name. Empty = evva",
        Get:         func(c *config.Config) any { return c.DefaultProfile },
        Set:         func(c *config.Config, v any) error { return c.SetDefaultProfile(toString(v)) },
    },
    // Provider settings — one entry per (provider, field).
    // Generated programmatically below to avoid duplicating 8 near-identical
    // entries by hand; see registerProviderSettings.
}

func init() {
    registerProviderSettings()
}

// registerProviderSettings adds <provider>.api_key + <provider>.api_url
// entries for every constant.GetAllProviders() entry. Ollama has no api_key
// (local, unauthenticated) and is filtered accordingly.
func registerProviderSettings() {
    for _, p := range constant.GetAllProviders() {
        name := p.Name
        if name != constant.OLLAMA.Name {
            SUPPORTED_SETTINGS[name+".api_key"] = SettingConfig{
                Type:         TypeSecret,
                Description:  fmt.Sprintf("%s API key; empty removes the provider from the active set", name),
                Get:          func(c *config.Config) any { return c.LLMProviderConfig[name].ApiSecret },
                Set:          func(c *config.Config, v any) error { return c.SetProviderAPIKey(name, toString(v)) },
                FormatOnRead: maskSecret,
            }
        }
        SUPPORTED_SETTINGS[name+".api_url"] = SettingConfig{
            Type:        TypeString,
            Description: fmt.Sprintf("Override the %s API base URL; empty resets to the built-in default", name),
            Get:         func(c *config.Config) any { return c.LLMProviderConfig[name].ApiURL },
            Set:         func(c *config.Config, v any) error { return c.SetProviderAPIURL(name, toString(v)) },
        }
    }
}
```

> **Closure-capture trap:** in `registerProviderSettings`, the `name`
> variable inside `Get`/`Set` closures must be the **per-iteration**
> copy, not the loop variable. Go 1.22+ already binds `name` per
> iteration in `for _, p := range …`, so the explicit `name := p.Name`
> shown above is what makes the closure capture the right value. **Do
> not rewrite this as `Get: func(c){ return c.LLMProviderConfig[p.Name]… }`
> using `p` directly** — that captures the loop variable and every
> closure ends up reading the last provider. evva's `go.mod` declares
> `go 1.22+`, so `for _, p := range …` binds `p` per iteration too;
> still, naming the binding explicitly removes the foot-gun.

Helpers (`settings.go`, same file):

```go
// isSupported reports whether key is a recognized setting.
func IsSupported(key string) bool { _, ok := SUPPORTED_SETTINGS[key]; return ok }

// Get returns the config for key, or nil + false.
func Get(key string) (SettingConfig, bool) { c, ok := SUPPORTED_SETTINGS[key]; return c, ok }

// AllKeys returns every supported setting key, sorted, for the prompt
// generator.
func AllKeys() []string {
    keys := make([]string, 0, len(SUPPORTED_SETTINGS))
    for k := range SUPPORTED_SETTINGS {
        keys = append(keys, k)
    }
    sort.Strings(keys)
    return keys
}

// coerceBool accepts true/false, "true"/"false" (case-insensitive),
// and rejects everything else with a clear error. Mirrors the ref
// ConfigTool's boolean coercion at ConfigTool.ts:185-201.
func coerceBool(v any) (bool, error) {
    switch x := v.(type) {
    case bool:
        return x, nil
    case string:
        s := strings.ToLower(strings.TrimSpace(x))
        if s == "true" { return true, nil }
        if s == "false" { return false, nil }
    }
    return false, fmt.Errorf("requires true or false")
}

// coerceInt accepts int, float64 (JSON numbers decode to float64),
// and parseable strings.
func coerceInt(v any) (int, error) {
    switch x := v.(type) {
    case int:
        return x, nil
    case float64:
        if x != float64(int(x)) {
            return 0, fmt.Errorf("requires an integer, got %g", x)
        }
        return int(x), nil
    case string:
        n, err := strconv.Atoi(strings.TrimSpace(x))
        if err != nil {
            return 0, fmt.Errorf("not an integer: %s", x)
        }
        return n, nil
    }
    return 0, fmt.Errorf("requires an integer, got %T", v)
}

// coerceFloat — same shape as coerceInt for floats.
func coerceFloat(v any) (float64, error) { /* same pattern */ }

// toString — coerces any JSON-decoded value to its string representation.
// Used by string + secret settings; rejects compound types.
func toString(v any) string {
    switch x := v.(type) {
    case string:
        return x
    case bool:
        return strconv.FormatBool(x)
    case float64:
        return strconv.FormatFloat(x, 'g', -1, 64)
    case int:
        return strconv.Itoa(x)
    }
    return fmt.Sprint(v)
}

// maskSecret renders a secret value for safe display. Same shape as
// pkg/ui/bubbletea/components/overlays/config.go:maskSecret.
func maskSecret(v any) any {
    s, ok := v.(string)
    if !ok || s == "" { return "(empty)" }
    if len(s) <= 4 { return "****" }
    return "****" + s[len(s)-4:]
}
```

### Task 3 — Implement `ConfigTool` (`config.go`)

```go
package configtool

import (
    "context"
    "encoding/json"
    "fmt"
    "log/slog"

    "github.com/johnny1110/evva/pkg/config"
    "github.com/johnny1110/evva/pkg/tools"
)

// Tool implements the `config` tool: one input shape {setting, value?},
// dispatched to either a get (value omitted) or set (value supplied).
type Tool struct {
    cfg *config.Config
}

func New(cfg *config.Config) *Tool { return &Tool{cfg: cfg} }

func (t *Tool) Name() string { return string(tools.CONFIG) }

func (t *Tool) Description() string { return generatePrompt() }

func (t *Tool) Schema() json.RawMessage {
    return json.RawMessage(`{
        "type":"object",
        "additionalProperties":false,
        "required":["setting"],
        "properties":{
            "setting":{"type":"string","description":"The setting key (e.g., \"display_thinking\", \"max_iterations\", \"openai.api_key\")"},
            "value":{"description":"The new value. Omit to read the current value. May be string, boolean, or number depending on the setting."}
        }
    }`)
}

type input struct {
    Setting string          `json:"setting"`
    // RawMessage so we can distinguish absent (GET) from present-but-falsy
    // (SET to false / 0 / ""). A pointer to `any` would conflate
    // {value: null} with absence; RawMessage with len==0 is unambiguous.
    Value   json.RawMessage `json:"value"`
}

func (t *Tool) Execute(_ context.Context, logger *slog.Logger, raw json.RawMessage) (tools.Result, error) {
    var in input
    if err := json.Unmarshal(raw, &in); err != nil {
        return errResult("config: bad input: %v", err), nil
    }
    if t.cfg == nil {
        return errResult("config: no config installed"), nil
    }

    sc, ok := Get(in.Setting)
    if !ok {
        return errResult("Unknown setting: %q", in.Setting), nil
    }

    // GET — value absent.
    if len(in.Value) == 0 {
        v := sc.Get(t.cfg)
        if sc.FormatOnRead != nil {
            v = sc.FormatOnRead(v)
        }
        return tools.Result{Content: fmt.Sprintf("%s = %v", in.Setting, v)}, nil
    }

    // SET — decode value into the most permissive container, then let
    // the setter coerce to its native type.
    var rawValue any
    if err := json.Unmarshal(in.Value, &rawValue); err != nil {
        return errResult("config: bad value: %v", err), nil
    }

    // Options validation (string-typed settings only).
    if len(sc.Options) > 0 {
        s, _ := rawValue.(string)
        if !contains(sc.Options, s) {
            return errResult("Invalid value %q. Options: %s",
                fmt.Sprint(rawValue), strings.Join(sc.Options, ", ")), nil
        }
    }

    if err := sc.Set(t.cfg, rawValue); err != nil {
        return errResult("%s: %s", in.Setting, err.Error()), nil
    }

    logger.Debug("config.set", "key", in.Setting, "value", rawValue)
    return tools.Result{Content: fmt.Sprintf("Set %s to %v", in.Setting, rawValue)}, nil
}

func errResult(format string, args ...any) tools.Result {
    return tools.Result{IsError: true, Content: fmt.Sprintf(format, args...)}
}

func contains(s []string, v string) bool {
    for _, x := range s { if x == v { return true } }
    return false
}
```

The constant `DESCRIPTION = "Get or set evva configuration settings."`
from ref's `prompt.ts` becomes the **leader** line of
`generatePrompt()`; the full body the tool returns from `Description()`
includes both the short summary and the dynamic settings list.

### Task 4 — Generate the prompt body (`prompt.go`)

Port `ref/src/tools/ConfigTool/prompt.ts:generatePrompt()` to Go.
Evva-specific deviations: no `globalSettings` vs `projectSettings`
split (evva has one store — `evva-config.yml`); no GrowthBook feature
gates; no special-cased model section (model swaps go through `/model`,
not `config`).

```go
package configtool

import (
    "fmt"
    "sort"
    "strings"
)

const description = "Get or set evva configuration settings."

const usage = `## Usage
- **Get current value:** omit the "value" parameter.
- **Set new value:** include the "value" parameter.

## Examples
- Get: {"setting":"display_thinking"}
- Set bool: {"setting":"display_thinking","value":false}
- Set int: {"setting":"max_iterations","value":40}
- Set string: {"setting":"default_effort","value":"high"}
- Set provider key: {"setting":"openai.api_key","value":"sk-..."}
`

// generatePrompt returns the body the tool exposes via Description().
// Walks the registry in sorted key order so the prompt is stable across
// invocations (deterministic prompt caching).
func generatePrompt() string {
    var b strings.Builder
    b.WriteString(description)
    b.WriteString("\n\nUse when the user requests a configuration change, asks about a current setting, or when changing a setting would benefit them.\n\n")
    b.WriteString(usage)
    b.WriteString("\n## Configurable settings\n\n")

    for _, key := range AllKeys() {
        sc := SUPPORTED_SETTINGS[key]
        line := "- " + key
        if len(sc.Options) > 0 {
            opts := make([]string, len(sc.Options))
            for i, o := range sc.Options { opts[i] = `"` + o + `"` }
            line += ": " + strings.Join(opts, ", ")
        } else {
            switch sc.Type {
            case TypeBool:    line += ": true/false"
            case TypeInt:     line += ": <integer>"
            case TypeFloat:   line += ": <float>"
            case TypeSecret:  line += ": <secret string>"
            case TypeString:  line += ": <string>"
            }
        }
        line += " — " + sc.Description
        b.WriteString(line)
        b.WriteByte('\n')
    }
    return b.String()
}
```

> **Prompt-caching note:** `generatePrompt()` runs once per tool
> construction (the `Tool` is built per-agent in
> `internal/toolset/builtins.go`). The output is deterministic — every
> identical `Config` produces an identical body — so the Anthropic /
> OpenAI prompt-prefix cache will keep hits across turns. Adding a
> setting with a non-deterministic description (e.g. embedding a
> timestamp) would break this; don't.

### Task 5 — Permission wiring

ConfigTool's input has a runtime-dependent classification (GET ≠ SET,
but both call the same tool name `config`). evva's permission model
needs a way to express that. Two paths:

**5.1 — Recommended: PreToolUse hook composition (post-v1.1).**

Now that v1.1's hook engine is wired, the cleanest expression of
"GET is safe, SET requires ask" is a **default PreToolUse hook** that
peeks at `tool_input.value` and short-circuits to `allow` when it's
absent. But shipping a default hook bundle is out of scope here —
hooks are user-configured.

**5.2 — Recommended for v1.5: add `config` to the default rule set
with a value-aware matcher.**

`pkg/permission/store.go` (or wherever the default rules are seeded —
check `pkg/permission` for the seed path) already ships defaults for
`read` (allow), `write` (ask), `bash` (ask), etc. Add an entry that
matches `config` with **two rules in order of precedence**:

```yaml
# pseudo — actual format follows the existing default-rule mechanism
- tool: config
  match:
    value_absent: true   # GET
  action: allow

- tool: config
  match: {}              # any other (SET)
  action: ask
  message: "Set {{.Input.setting}} to {{.Input.value}}"
```

> **Implementation note:** if the existing rule-matcher does not
> support "value absent" matching, **extend the matcher first** (a
> two-line predicate addition) before declaring the rule. Do not
> ship the tool with an "always-ask" rule and a TODO comment — the
> UX would be miserable (the user gets prompted to "Get display_thinking"
> on every read).

If extending the matcher is contentious, the alternative is **5.3**.

**5.3 — Fallback: split into two tool names.**

Register two tools, `config_get` and `config_set`. The first is
`allow` by default; the second is `ask`. Same backing implementation
(an internal `dispatch(get|set, …)` function), but two `tools.Tool`
faces. This costs one extra entry in the permission registry but
needs **zero** matcher changes.

The tradeoff: the model sees two tools and one extra line of prompt
content. For a tool the model uses occasionally (not every turn), the
prompt cost is negligible. Pick this if extending the matcher would
ripple into more places than v1.5 should touch.

**Choose 5.2** (recommended). If the matcher extension is more than
~20 LOC, **fall back to 5.3** and note the choice in the PR.

### Task 6 — Register + activate

**6.1 — Factory.** `internal/toolset/builtins.go`, near line 170 (next
to FEEDBACK):

```go
// --- config (let the model read/write evva settings) ---
r.MustRegister(tools.CONFIG, func(s tools.State) (tools.Tool, error) {
    return configtool.New(s.Config()), nil
})
```

Add the import: `"github.com/johnny1110/evva/internal/tools/config"`.
**Use a package alias** to avoid shadowing the `config` parameter name
that appears throughout this file:

```go
import (
    ...
    configtool "github.com/johnny1110/evva/internal/tools/config"
    ...
)
```

**6.2 — Main profile.** `internal/agent/profiles.go`, where
`ENTER_PLAN_MODE` / `EXIT_PLAN_MODE` get appended to `activeTools`
(~line 131):

```go
activeTools = append(activeTools, tools.ENTER_PLAN_MODE, tools.EXIT_PLAN_MODE, tools.CONFIG)
```

Or, if you prefer the `Names()` pattern used by `dev`, `memory`:

```go
activeTools = append(activeTools, configtool.Names()...)
```

**6.3 — Tag table.** `pkg/toolset/tags.go` — the tool-tag map used by
`tool_search` for scoring. Add:

```go
tools.CONFIG: "Get or set evva configuration settings (theme, model, memory toggles, provider keys).",
```

Mirror the FEEDBACK row's style.

**6.4 — Subagent exclusion confirmation.** Read
`internal/agent/sysprompt/agent_def.go` for `ExploreAgent`,
`GeneralAgent`, `PlanAgent`. Confirm none reach into the Main profile's
active-tools mechanism — subagents build their own `ActiveTools` list
from their definitions, so as long as Task 6.2 only edits the Main
profile path, subagents stay clean. No further change needed; just
verify by inspection (and assert with the test in Task 7).

### Task 7 — Tests

**7.1 — Registry tests (`settings_test.go`).**

```go
func TestRegistryCovers(...) {
    // Every Set* method on *config.Config corresponds to at least one
    // SUPPORTED_SETTINGS entry that wraps it. Reflective check: list all
    // Set* methods on *config.Config (skipping SetDefaultModel — that's
    // tuple-typed and out of scope; see §5.3); for each one, assert at
    // least one entry's Set field calls it.
    // Implementation: scan SUPPORTED_SETTINGS, build a set of all
    // Set funcs by name (via runtime.FuncForPC), then diff against the
    // expected list.
}

func TestCoerceBool(...)    { /* true, "true", "TRUE", "false", "yes" (rejects), 1 (rejects) */ }
func TestCoerceInt(...)     { /* 42, 42.0, "42", 42.5 (rejects), "abc" (rejects) */ }
func TestCoerceFloat(...)   { /* 0.5, 1, "0.5", "abc" (rejects) */ }
func TestMaskSecret(...)    { /* "", "abc", "abcdef" → "****cdef", "abcde" → "****bcde" */ }

func TestSupportedSettingsKeys(...) {
    // Pin the exact key set so an accidental addition/removal is
    // visible in a code review. Includes provider settings.
    want := []string{
        "anthropic.api_key", "anthropic.api_url",
        "auto_compact_threshold",
        "default_effort", "default_profile",
        "deepseek.api_key", "deepseek.api_url",
        "display_thinking",
        "enable_auto_memory",
        "fetch_max_bytes",
        "max_iterations", "max_tokens",
        "ollama.api_url",
        "openai.api_key", "openai.api_url",
        "tavily_api_key",
    }
    if got := AllKeys(); !reflect.DeepEqual(got, want) {
        t.Errorf("AllKeys: got %v, want %v", got, want)
    }
}
```

> **Why pin the key set:** the registry is the contract between the
> tool and every downstream consumer. A drift-by-accident here would
> silently change the prompt body and the LLM's view of what it can
> tune. Make additions explicit.

**7.2 — Tool tests (`config_test.go`).** Build a `*config.Config` via
the test-only `config.Load(...)` helper (or a hand-constructed one
under `t.TempDir()`); exercise each acceptance criterion:

```go
func TestConfigGet(...)         { /* A2 */ }
func TestConfigGetUnknown(...)  { /* A3 */ }
func TestConfigSetValid(...)    { /* A4 — assert YAML re-read */ }
func TestConfigSetInvalidTyped(...) { /* A5 */ }
func TestConfigSetInvalidEnum(...)  { /* A6 */ }
func TestConfigBoolCoercion(...) { /* A7 — string "true" works, "yes" rejected */ }
func TestConfigProviderKey(...)  { /* A8 */ }
func TestConfigSecretMaskedOnRead(...) {
    // Set tavily_api_key to "abcd1234"; GET returns "****1234" not the raw key.
}
```

**7.3 — Prompt test (`prompt_test.go`).**

```go
func TestGeneratePromptContainsEveryKey(...) {
    body := generatePrompt()
    for _, k := range AllKeys() {
        if !strings.Contains(body, "- "+k) {
            t.Errorf("prompt missing setting %q", k)
        }
    }
    if !strings.Contains(body, description) {
        t.Errorf("prompt missing leader description")
    }
}

func TestGeneratePromptStable(...) {
    // Run twice; bytes must match. Catches non-determinism (random map
    // order, embedded timestamps) that would break prompt caching.
    a := generatePrompt()
    b := generatePrompt()
    if a != b { t.Errorf("non-deterministic prompt body") }
}
```

**7.4 — Registry integration test (toolset-level).** In
`internal/toolset/builtins_test.go` (or wherever the integration
fixture lives), add a single test:

```go
func TestConfigToolWiring(t *testing.T) {
    state := /* build a minimal ToolState with a *config.Config */
    tool, err := DefaultRegistry().Build("config", state)
    if err != nil { t.Fatal(err) }
    if tool.Name() != "config" { t.Errorf("tool name = %q, want config", tool.Name()) }
    // Probe one GET to prove Execute works end-to-end through the registry.
    res, _ := tool.Execute(ctx, slog.Default(),
        json.RawMessage(`{"setting":"display_thinking"}`))
    if res.IsError { t.Errorf("GET errored: %s", res.Content) }
}
```

**7.5 — Permission default rule test.** If §5.2 is taken, add a test
that:

- Loads the default permission rules.
- Builds a `permission.ToolCall{Name:"config", Input: ...}` for both
  GET (no value) and SET (value present).
- Asserts the broker resolves GET → `BehaviorAllow`, SET → `BehaviorAsk`.

If §5.3 is taken, test that `config_get` resolves allow and
`config_set` resolves ask.

### Task 8 — Docs + version

**8.1 — `docs/extending.md`.** Tool families table (around line 25 or
wherever the tool list lives). Add no row — ConfigTool is `internal/`,
not a public extension point.

**8.2 — `docs/user-guide/en/user-guide.md`.** Under §8 *Configuration*
(after the YAML stanza around line 700), add a short paragraph:

```markdown
### Setting changes from chat

You can also ask evva to read or change settings without typing
`/config`. The `config` tool exposes the same keys the overlay does
(`max_iterations`, `display_thinking`, `default_effort`,
`<provider>.api_key`, …). Reads land directly; writes go through the
permission prompt (the model's request shows up as
"Set <key> to <value>"). Type `/permissions config` to add a
permanent allow-rule for a specific setting.
```

**8.3 — `docs/user-guide/zh-tw/user-guide.md`.** Mirror with the
translated paragraph.

**8.4 — `CHANGELOG.md`.** Add above the v1.4 entry:

```markdown
## [v1.5.0] — ConfigTool

The model can now read and change evva's configuration directly via
a new `config` tool, instead of asking the user to type `/config`.

### Added

- **`config` tool** — one input `{setting, value?}`. Omitting `value`
  reads the current value (auto-allowed); supplying it writes
  (gated by an `ask` permission prompt). Mirrors the `/config`
  overlay's setting catalog plus a small set of model-relevant
  extras (`default_effort`, `default_profile`).
- **`internal/tools/config`** — `SUPPORTED_SETTINGS` registry that
  wraps the typed `*config.Config` setters. Adding a new setting in
  one place (this table) automatically grows the tool's prompt,
  schema, and permission default rule.
- **Default permission rule** for `config`: GET → allow, SET → ask
  with a "Set <key> to <value>" message.

### Notes

- The registry is the single source of truth — if `/config` overlay
  (`pkg/ui/bubbletea/components/overlays/config.go`) gains a field,
  add it to `SUPPORTED_SETTINGS` too. A test pins the key set so
  drift is caught in CI.
- Subagents (`Explore`, `Plan`, `GeneralPurpose`) do **not** get the
  tool; only the Main persona does. Subagents run cold for narrow
  tasks and have no business mutating user config.
```

**8.5 — `pkg/version/version.go`.**
`const Version = "1.4.0"` → `const Version = "1.5.0"` (adjust from
whatever the current shipping value is at execution time — v1.4 lands
first per the v1.4 doc's sequencing note).

---

## 5. Design decisions & risks (read before coding)

### 5.1 — Registry is the only source of truth

Every other layer (prompt body, JSON schema's narrative, tool tag,
permission default rule, future docs page) derives from
`SUPPORTED_SETTINGS`. Resist the urge to duplicate the key list
anywhere. The one place a duplication is unavoidable is the
`/config` overlay's `buildConfigFields`, which is interactive (with
input widgets per field type) and can't be auto-generated; the
**test in §4 Task 7.1** is the canary that catches drift between the
two surfaces.

### 5.2 — Lean on existing setters; do not re-implement validation

`SetMaxIterations(n)` already validates `>0`. The tool's
`Set: func(c, v) error { n,err := coerceInt(v); if err != nil { return err }; return c.SetMaxIterations(n) }`
does **coercion then delegation**. It does not re-validate. If the
setter's error message ("max_iterations must be > 0, got -3") changes
or gets nicer, the tool benefits automatically. The temptation to
inline the validation here for "better error messages" is the
beginning of two-sources-of-truth — don't.

### 5.3 — `default_model` is intentionally out of v1.5

`SetDefaultModel(provider, model)` takes a **tuple** of typed
constants — there's no clean single-value JSON shape for the tool to
accept. Two viable extensions exist:

- A single key `default_model` whose value is the model string,
  with the provider inferred from `MODEL_CONTEXT_SIZE` keys.
- Two separate keys `default_provider` + `default_model` with
  cross-validation on write.

Both are non-trivial and **the `/model` picker already does this
job well**. ConfigTool v1.5 ships without model swap; a follow-up
can add it once the right shape is settled. Document this in the
prompt body's intro: *"To change the model, ask the user to type
/model — that's the supported swap path."*

### 5.4 — `permission_mode` is also out

`PermissionMode` has no typed setter (it's set at boot from CLI flag
or YAML and toggled at runtime via the TUI's Shift+Tab cycle). Adding
it to the tool would mean either (a) building a `SetPermissionMode`
plumbing path through the agent and broker — significant scope — or
(b) just mutating the field, which races with the live broker and
could put the agent in an inconsistent state mid-turn. Skip for
v1.5. If the user asks the model to change permission mode, the
model can suggest `Shift+Tab` (and the system prompt already
mentions this affordance).

### 5.5 — JSON-decoded numbers are float64

evva's input arrives as `json.RawMessage`; decoding into `any` makes
every number a `float64`. `coerceInt` must reject non-integer floats
(`{"value": 3.7}` → "requires an integer, got 3.7"). The test cases
in §4 Task 7.1 nail this — without the float-rejection branch the
tool would silently truncate `3.7` to `3`, which is a subtle data
loss.

### 5.6 — Secrets are masked on read, not on write

`maskSecret` is applied via `FormatOnRead` on the secret-typed
entries. The model **does** receive the masked value on GET — that's
intentional; the model has no need for the raw key in normal
operation. On SET, the model passes the raw value through `Value`
and it lands in the YAML as-is. The TUI's transcript should redact
the displayed value of SET calls too (`Set openai.api_key to
sk-****1234` not the full key) — this is a TUI concern, not a tool
concern, but it's worth flagging because today's transcript renderer
shows raw input. **Out of scope for v1.5**, but file an issue.

### 5.7 — Concurrency

ConfigTool can be called in parallel with other tools in one turn.
Every typed setter on `*config.Config` already holds `c.mu`; the tool
adds no new lock surface. The only place the tool reads-without-the-
typed-accessor is for fields like `FetchMaxBytes` and
`TavilyAPIKey` (no `Get*` exists). For those, the registry's `Get`
closure reads the field directly — and a concurrent `Set*` could
race the read. Add the missing `Get*` accessors on `*config.Config`
or accept the data race for read-only display. **Recommended:** add
the accessors (each is 4 lines) in the same PR; cleaner and removes
the race.

### 5.8 — Why this isn't a `pkg/tools/` family

ConfigTool is **evva-runtime-specific**: it knows about evva's
`*config.Config` shape and the names of evva-specific settings. The
project convention (CLAUDE.md → Project conventions) is that
runtime-specific tools stay under `internal/tools/`. Downstream apps
building on the pkg-only surface get their own config story; they
won't import ConfigTool.

---

## 6. Out of scope for v1.5

- **Model swap via the tool** — use `/model` (see §5.3).
- **Permission mode swap via the tool** — use Shift+Tab (see §5.4).
- **Adding new settings to `*config.Config`** — this phase only
  exposes settings that already exist. The `EnableAutoMemory` /
  `auto_dream_enabled` distinction in `ref/`'s registry doesn't
  apply: evva has no `auto_dream` background-consolidation feature.
- **Editing `<workdir>/.evva/settings.json`** — ref splits global
  vs. project settings; evva has one config file in `AppHome`. The
  `permissions.json` file in either location is **separate** and is
  edited via `/permissions`, not `/config` or this tool.
- **Async write-validation** (ref does this for `model` to check
  the API). Every evva setter is synchronous; no need.
- **Transcript redaction of SET inputs for secrets** — TUI concern,
  not tool concern (see §5.6).
- **`<APP_HOME>/permissions.json` editing.** That's the
  `/permissions` tool's territory; cross-tool overlap would be
  confusing. If a user asks the model to add a permission rule,
  the model should suggest `/permissions`.

---

## 7. Verification checklist (PR gate)

- [ ] **Task 0:** `tools.CONFIG` constant added to `pkg/tools/name.go`;
      `grep -rn "tools.CONFIG"` finds the registration in
      `internal/toolset/builtins.go` and the activation in
      `internal/agent/profiles.go`.
- [ ] **Task 1–4:** `internal/tools/config/` compiles; `config.go` ≈
      150 LOC; `settings.go` ≈ 250 LOC; `prompt.go` ≈ 80 LOC.
- [ ] **Task 2:** `AllKeys()` returns the exact slice pinned in
      `TestSupportedSettingsKeys`.
- [ ] **Task 4:** `generatePrompt()` output is byte-stable across two
      consecutive calls (`TestGeneratePromptStable`).
- [ ] **Task 5:** §5.2 chosen → default rule loaded for `config`
      with value-absent matcher → allow, else ask. §5.3 chosen → two
      tool names `config_get` / `config_set` with separate default
      rules. PR explicitly states which path was taken.
- [ ] **Task 6:** Main profile's `ActiveTools` includes `tools.CONFIG`;
      subagent profiles do not. Verify by reading
      `internal/agent/sysprompt/agent_def.go` and the relevant
      `Profile` definitions in `profiles.go`.
- [ ] **Task 7:** `go test ./internal/tools/config/...` green;
      `TestConfigToolWiring` in the toolset test suite green;
      permission default-rule test green.
- [ ] **A1–A12:** all acceptance criteria demonstrably pass (A11 via
      the unit tests; A9 via the permission rule test or by manual
      check in the TUI).
- [ ] `go build ./...` and `go vet ./...` clean.
- [ ] `go test ./...` green.
- [ ] **A12 (docs):** `CHANGELOG.md` updated; `pkg/version.Version`
      bumped; user-guide en + zh-tw updated.
- [ ] **Manual (TTY-only):** in a real evva session, ask the model
      *"what is my display_thinking setting?"* — confirm the model
      calls `config({"setting":"display_thinking"})` and no
      permission prompt appears. Then ask *"turn auto-memory off"* —
      confirm the permission prompt shows the message
      `Set enable_auto_memory to false` (not generic), and after
      approval the YAML reflects the change. Then ask
      *"what's my OpenAI key?"* — confirm the response shows the
      masked form (`****1234`), not the raw key.

---

## 8. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `pkg/tools/name.go` | Edit: add `CONFIG ToolName = "config"` | Task 0 |
| `internal/tools/config/config.go` | **New** — tool implementation | Task 3 |
| `internal/tools/config/settings.go` | **New** — registry + coercion helpers | Task 2 |
| `internal/tools/config/prompt.go` | **New** — dynamic prompt body | Task 4 |
| `internal/tools/config/config_test.go` | **New** — Execute path tests | Task 7.2 |
| `internal/tools/config/settings_test.go` | **New** — registry + coercion tests | Task 7.1 |
| `internal/tools/config/prompt_test.go` | **New** — prompt stability + coverage | Task 7.3 |
| `internal/toolset/builtins.go` | Edit: factory registration + alias import | Task 6.1 |
| `internal/toolset/builtins_test.go` | Edit: `TestConfigToolWiring` | Task 7.4 |
| `internal/agent/profiles.go` | Edit: append to Main `activeTools` | Task 6.2 |
| `pkg/toolset/tags.go` | Edit: `tools.CONFIG` row | Task 6.3 |
| `pkg/permission/*` | Edit: extend matcher (§5.2) or no edit (§5.3) | Task 5 |
| `pkg/config/config.go` | Edit (optional): add `GetTavilyAPIKey`, `GetFetchMaxBytes`, `GetDefaultProfile`, `GetMaxIterations`, `GetMaxTokens` accessors | §5.7 |
| `pkg/version/version.go` | Edit: bump to `1.5.0` | Task 8.5 |
| `CHANGELOG.md` | Edit: `## [v1.5.0]` block | Task 8.4 |
| `docs/user-guide/en/user-guide.md` | Edit: §8 paragraph | Task 8.2 |
| `docs/user-guide/zh-tw/user-guide.md` | Edit: mirror | Task 8.3 |

---

## 9. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 0 — tool name constant | ~5 LOC | 5 min |
| Task 1 — package skeleton | ~30 LOC | 15 min |
| Task 2 — SUPPORTED_SETTINGS + helpers | ~250 LOC | 1.5 h |
| Task 3 — Tool implementation | ~150 LOC | 1 h |
| Task 4 — prompt generator | ~80 LOC | 30 min |
| Task 5 — permission wiring (§5.2 path) | ~30 LOC + tests | 45 min |
| Task 6 — factory + activation + tag | ~15 LOC | 15 min |
| Task 7 — tests | ~400 LOC across files | 2 h |
| §5.7 missing accessors | ~30 LOC | 15 min |
| Task 8 — docs + changelog + version | ~70 LOC across files | 45 min |
| Manual smoke (§7 last block) | — | 15 min |

Total: ~1,000 LOC new + edited, ~7–8 hours of focused engineering.
Comparable in size to v1.2 (OpenAI provider); smaller than v1.1
(hooks integration) because no engine-level work is needed —
everything plumbs through existing setters.
