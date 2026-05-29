# PRD — Output Styles — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build after roadmap slotting.
> **Target release:** TBD (proposed `v1.6+` candidate; small, self-contained).
> **Roadmap source:** `CLAUDE.md` → Vision (*"one runtime, many personas,
> swappable UI"*) — output styles are the lightweight, stackable
> complement to personas.
> **Reference source:** `ref/src/constants/outputStyles.ts`,
> `ref/src/outputStyles/loadOutputStylesDir.ts`,
> `ref/src/components/OutputStylePicker.tsx`.

---

## 1. TL;DR — what this phase actually is

A **persona** in evva is a heavyweight definition — its own tools, model,
system prompt, `meta.yml` (`CLAUDE.md` → Agent definitions). Switching
persona is `/profile <name>`. That's the right unit for "who the agent is"
(`evva` the engineer vs. `nono` the financial manager).

It is the **wrong** unit for "how the agent *talks*." If a user wants evva
to explain its reasoning as it codes, or to pause and hand them small
exercises, they shouldn't have to author a whole persona — duplicate the
tools, pick a model, re-write the harness prompt. They want a thin overlay
on the persona they already have.

**Output styles are that overlay.** A style is a Markdown file whose body is
a prompt fragment layered onto the active persona's system prompt. Ported
from Claude Code:

- **Built-in styles** ship as Go-defined configs: `default` (no overlay),
  `Explanatory` (explains choices + codebase insights), `Learning` (asks
  the user to write small pieces for hands-on practice) — lifted from
  `ref/src/constants/outputStyles.ts`.
- **User / project styles** load from disk: `<appHome>/output-styles/*.md`
  and `<workdir>/.evva/output-styles/*.md`, each with frontmatter
  (`name`, `description`, `keep-coding-instructions`) and a Markdown body
  that *is* the style prompt. Project overrides user overrides built-in,
  on name clash.
- A `keep-coding-instructions` flag decides whether the style **adds to**
  evva's coding harness (the default — `Explanatory`/`Learning` keep it) or
  **replaces** it (a fully custom voice — e.g. a non-coding assistant).
- The active style is one config value (`output_style`, default
  `"default"`), switchable via a `/output-style` picker and persisted.

Concretely:

1. `internal/outputstyle` — the `OutputStyle` type, the built-in registry,
   and the disk loader (Markdown + frontmatter, layered/precedence).
2. A `PromptContext.OutputStyle` field + an injection point in
   `buildMainPrompt` that either appends the style prompt (keep-coding) or
   substitutes it for the coding-doctrine sections (replace).
3. `output_style` config setting + a `/output-style` picker command + TUI
   surfacing of the active style.

Output styles are **orthogonal to personas**: any style stacks on any
persona. They apply to the **Main user-facing agent only** — subagents run
cold for narrow tasks and don't get a style (§5.4).

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 System-prompt composition — `internal/agent/sysprompt`

`buildMainPrompt(ctx)` (`main_agent.go:50`) is an ordered `joinSections(…)`
of ~17 fragments. The injection design hinges on splitting these into two
groups:

**Infrastructure (always present — a style never drops these):**

| Fragment | File:line | Why it always stays |
| --- | --- | --- |
| `identitySection` | `fragments.go:23` | who the agent is |
| `coreRulesSection` | `fragments.go:41` | honesty / redirect doctrine |
| `systemSection` | `fragments.go:54` | permission flow, hooks, injection caveat, compression |
| `environmentSection` | `fragments.go:144` | OS/shell/workdir/today/model |
| memory sections | `main_agent.go:61-64` | EVVA.md / profile / index |
| `sessionSpecificGuidanceSection` | `fragments.go:182` | `!`-prefix, denied-tool behavior |
| `skillsSection` | `fragments.go:209` | installed skills |
| `mainDeferredToolsSection` | `main_agent.go:81` | deferred-tool catalog |
| `devSectionIfEnabled` | `main_agent.go:98` | dev-only feedback |

**Coding doctrine (dropped when `keep-coding-instructions: false`):**

| Fragment | File:line |
| --- | --- |
| `doingTasksSection` | `fragments.go:69` |
| `actionsSection` | `fragments.go:97` |
| `mainToolsGuideSection` | `main_agent.go:119` |
| `toneAndStyleSection` | `fragments.go:114` |
| `outputEfficiencySection` | `fragments.go:126` |
| `summarizeToolResultsSection` | `fragments.go:198` |
| `mainTodoSection` | `main_agent.go:294` |

`sysprompt` imports **only stdlib** (`sysprompt.go:9`) and renders strings
from a `PromptContext`. So the style — already resolved to `{prompt,
keepCodingInstructions}` strings by the caller — arrives as plain
`PromptContext` fields; the loader/IO lives in the caller. Same one-way
arrow as memory and skills.

### 2.2 The caller — `internal/agent/profiles.go`

`mainProfile` (`profiles.go:129`) builds the `PromptContext`, sets
`ctx.Skills`, the memory fields, `ctx.DeferredTools`, `ctx.Model`, then
calls `sysprompt.MainAgent.BuildSystemPrompt(ctx)`. This is where the
resolved `OutputStyle` gets attached (Task 4). The disk-persona path
(`mainProfileFromDiskAgent`, `profiles.go:279`) is the second seam — a disk
persona should honor the active style too (and may declare a default style;
§5.5).

### 2.3 The disk-markdown loader template — `pkg/skill`

`pkg/skill/registry.go` already implements "walk `<root>/skills/<name>/`,
load Markdown, build a registry, AppHome-then-WorkDir precedence,
programmatic override" (`registry.go:1-25`, `LoadRegistry`). The output-
style loader is the **same ergonomics** but:

- one flat `output-styles/*.md` dir (no per-style subdir);
- parses **YAML frontmatter** (`name` / `description` /
  `keep-coding-instructions`), not skill's first-line `# name description`
  (`registry.go:14-18`) — so the frontmatter reader is new (or shared with
  the memory PRD's `frontmatter.go` if that lands first — §5.6).

Mirror skill's precedence + warning ergonomics; don't reuse its parser.

### 2.4 Config + ConfigTool

- `pkg/config.Config` has typed setters + a `/config` overlay
  (`pkg/ui/bubbletea/components/overlays/config.go`). Add an `OutputStyle`
  field + `SetOutputStyle` + `GetOutputStyle`, following the
  `DefaultProfile` pattern (`profiles.go` references `cfg.DefaultProfile`).
- If the **ConfigTool** phase (`docs/roadmap/v1/v1-5-config-tool.md`) has
  shipped, add an `output_style` entry to its `SUPPORTED_SETTINGS`
  registry so the model can read/set it too. Validate against the resolved
  style-name set.

### 2.5 Skills / commands surface — how `/output-style` plugs in

`/commit` ships today as a bundled skill; the framework (`pkg/skill`) loads
`SKILL.md` files. `/output-style` is **not** a skill (it doesn't dispatch a
prompt to the model) — it's an **interactive picker** that mutates config,
exactly like `/config`, `/model`, `/profile`. Implement it as a TUI command
that opens a picker overlay (mirror `/profile`'s persona picker), not as a
skill. Locate the slash-command registry the TUI uses for `/config` /
`/model` / `/profile` and add `/output-style` beside it (Task 5).

### 2.6 Reference (`ref/src/`)

| File | What it does | Port? |
| --- | --- | --- |
| `constants/outputStyles.ts` | `OutputStyleConfig` type; built-in `default`/`Explanatory`/`Learning`; `getAllOutputStyles` (layered merge); `getOutputStyleConfig` (resolve active) | **Yes** — port the type, the two built-ins (drop the `figures` glyph deps → plain ASCII), and the layered-merge + resolve logic |
| `outputStyles/loadOutputStylesDir.ts` | walk `.claude/output-styles/*.md`, parse frontmatter (`name`/`description`/`keep-coding-instructions`), body = prompt, tag `source` | **Yes** — direct port to the Go loader |
| `components/OutputStylePicker.tsx` | the `/output-style` picker UI | **Adapt** — to evva's bubbletea overlay idiom (model after the existing config/profile overlay) |
| plugin / `forceForPlugin` / `policySettings` (managed) tiers | enterprise-managed + plugin-forced styles | **No** — evva has no plugin or managed-policy layer (out of scope) |

The built-in prompts in `outputStyles.ts:43-134` reference `figures.star` /
`figures.bullet` (a Node glyph lib). Replace with literal characters; don't
add a Go glyph dep.

---

## 3. Goal & acceptance criteria

**Goal:** a user can switch how the active persona communicates — picking a
built-in or a custom Markdown-defined style — without authoring a persona.
The style layers onto (or, when declared, replaces) the coding harness, is
persisted in config, switchable via `/output-style`, and visible in the UI.

Ship is complete when **all** of these pass:

- **A1 — Built-ins present.** `default`, `Explanatory`, `Learning` resolve
  from the built-in registry with no disk files. `default` yields a nil/
  empty overlay (prompt byte-identical to today's Main prompt).
- **A2 — Disk load + precedence.** A `<appHome>/output-styles/foo.md` and a
  `<workdir>/.evva/output-styles/foo.md` both named `foo` resolve to the
  **workdir** one (project overrides user overrides built-in). Frontmatter
  `name`/`description` parsed; body becomes the prompt.
- **A3 — Append mode.** With `Explanatory` active
  (`keep-coding-instructions: true`), the system prompt contains **both** the
  full coding-doctrine sections **and** the style prompt (appended after the
  tone/output sections).
- **A4 — Replace mode.** With a style whose `keep-coding-instructions:
  false` is active, the coding-doctrine fragments (§2.1 second table) are
  **absent** and the style prompt appears in their place; the
  infrastructure fragments (identity, system, environment, memory, skills,
  deferred tools) remain.
- **A5 — Config round-trip.** `SetOutputStyle("Explanatory")` persists to
  the config file; on next boot the active style is `Explanatory`.
  `SetOutputStyle` rejects a name that resolves to no style.
- **A6 — Picker.** `/output-style` opens a picker listing built-in + disk
  styles (name + description), selecting one calls `SetOutputStyle` and the
  **next turn's** prompt reflects it (re-resolved at profile build).
- **A7 — Default is a true no-op.** With `output_style = "default"` (or
  unset), the Main system prompt is **byte-identical** to the pre-feature
  prompt (snapshot test). Output styles cost nothing until used.
- **A8 — Subagent isolation.** Spawned subagents (Explore/Plan/General) and
  their prompts are unaffected by the active output style (§5.4).
- **A9 — Disk persona honors style.** A disk-loaded main persona
  (`mainProfileFromDiskAgent`) also gets the active style applied, unless it
  declares `output_style` in its own `meta.yml` (§5.5), which wins.
- **A10 — Malformed style is safe.** A style file with no frontmatter / no
  body is skipped with a warning (not a crash); an unknown active
  `output_style` name falls back to `default` with a warning.
- **A11 — ConfigTool integration (if present).** When the `config` tool
  exists, `config({"setting":"output_style","value":"Learning"})` switches
  the style; an invalid value is rejected with the allowed set listed.
- **A12 — Tests.** Loader precedence, frontmatter parse, append-vs-replace
  composition, default no-op snapshot, config persistence, picker→config
  path, subagent isolation.
- **A13 — Docs + version + changelog.**

---

## 4. Work breakdown (ordered)

### Task 1 — `internal/outputstyle` package

```
internal/outputstyle/
├── style.go        # OutputStyle type, built-in registry, Resolve
├── load.go         # disk loader (frontmatter + body), layered precedence
├── style_test.go
└── load_test.go
```

```go
type OutputStyle struct {
    Name                  string
    Description           string
    Prompt                string // "" for default
    KeepCodingInstructions bool  // default true when omitted (append mode)
    Source                string // "built-in" | "user" | "project"
}

const DefaultStyleName = "default"

// BuiltIns returns default (empty Prompt), Explanatory, Learning —
// ported from ref/src/constants/outputStyles.ts (glyphs → ASCII).
func BuiltIns() map[string]OutputStyle

// LoadAll merges built-ins with disk styles from appHome then workdir
// (workdir wins). Returns the merged map + non-fatal warnings.
func LoadAll(appHome, workdir string) (map[string]OutputStyle, []string)

// Resolve returns the active style by name, or the default (nil overlay)
// when name is "default"/unknown (with a warning for unknown).
func Resolve(all map[string]OutputStyle, name string) (OutputStyle, string)
```

`KeepCodingInstructions` defaults to **true** when the frontmatter key is
absent — matching ref's built-ins and the safe default (a style *adds* to
the harness unless it explicitly opts out). Port the frontmatter tri-state
parse from `loadOutputStylesDir.ts:58-72` (true/"true" → true, false/"false"
→ false, absent → default).

### Task 2 — `PromptContext` field + injection

**`sysprompt/sysprompt.go`** — add to `PromptContext`:

```go
// OutputStyle, when non-empty Prompt, overlays the active output style.
// KeepCoding decides append (true) vs. replace-coding-doctrine (false).
OutputStylePrompt string
OutputStyleKeepCoding bool
```

**`sysprompt/main_agent.go`** — refactor `buildMainPrompt` to name the two
groups, then branch:

```go
func buildMainPrompt(ctx PromptContext) string {
    infra := []string{
        identitySection(ctx), coreRulesSection(), systemSection(),
        environmentSection(ctx),
        memorySection("Project memory (from EVVA.md)", ctx.WorkdirMemory),
        memorySection("User profile (from USER_PROFILE.md)", ctx.UserProfile),
        autoMemoryGuidanceSection(ctx), projectMemoryIndexSection(ctx),
        sessionSpecificGuidanceSection(), skillsSection(ctx.Skills),
        summarizeToolResultsSection(), // stays — it's infra, not doctrine
        mainDeferredToolsSection(ctx.DeferredTools), devSectionIfEnabled(ctx),
    }
    coding := []string{
        doingTasksSection(), actionsSection(), mainToolsGuideSection(),
        toneAndStyleSection(), outputEfficiencySection(), mainTodoSection(),
    }
    style := outputStyleSection(ctx) // "" when default

    if ctx.OutputStylePrompt != "" && !ctx.OutputStyleKeepCoding {
        // replace mode: infra + style (drop coding doctrine)
        return joinSections(spliceStyle(infra, style)...)
    }
    // append mode (and default): infra + coding + style
    return joinSections(append(append(infraThenCoding(infra, coding)), style)...)
}
```

(The exact ordering helper is an implementation detail — the **invariant**
is: default → identical to today (A7); append → coding doctrine present +
style after tone/output; replace → coding doctrine absent, style present,
infra intact. Pin all three with snapshot tests, A3/A4/A7.)

`outputStyleSection(ctx)` returns `""` when `OutputStylePrompt` is empty, so
the default path is a literal no-op and `joinSections` drops the empty entry
exactly as it does for empty memory/skills today.

### Task 3 — Config

`pkg/config/config.go`: add `OutputStyle string` field (YAML
`output_style`), `GetOutputStyle()` (returns `"default"` when empty),
`SetOutputStyle(name)` under `c.mu` + `SaveFile()`, validating the name
against `outputstyle.LoadAll(...)` keys. Mirror the `DefaultProfile`
accessor shape. Add the field to the `/config` overlay's
`buildConfigFields` so the interactive overlay lists it too.

### Task 4 — Wire into profile build

`internal/agent/profiles.go`:

- In `mainProfile` (`:129`), after the existing `ctx.*` assignments:

```go
all, warns := outputstyle.LoadAll(cfg.AppHome, cfg.WorkDir) // log warns
st, w := outputstyle.Resolve(all, cfg.GetOutputStyle())     // log w
ctx.OutputStylePrompt = st.Prompt
ctx.OutputStyleKeepCoding = st.KeepCodingInstructions
```

- In `mainProfileFromDiskAgent` (`:279`), do the same, but let a
  persona-declared style (`def.OutputStyle` from `meta.yml`, §5.5) take
  precedence over `cfg.GetOutputStyle()` when set.

Because styles re-resolve at **profile build** time and the TUI rebuilds the
Main profile on `/output-style` (same path as `/profile` / `/model`
switches), A6 falls out for free — no live prompt mutation needed.

### Task 5 — `/output-style` picker + UI surfacing

- Add `/output-style` to the slash-command set the TUI registers alongside
  `/config`, `/model`, `/profile`. It opens a picker overlay listing
  `outputstyle.LoadAll(...)` entries (name + description, active one
  marked), and on select calls `cfg.SetOutputStyle(name)` + triggers the
  same profile-rebuild the `/model` switch uses.
- Surface the active style somewhere persistent in the TUI (footer/status
  line or the session header) when it isn't `default`, mirroring ref's
  `StatusLine.tsx` output-style indicator. Minor; not load-bearing.

### Task 6 — ConfigTool entry (conditional)

If `config` tool exists, add to its `SUPPORTED_SETTINGS`:

```go
"output_style": {
    Type: TypeString,
    Description: "Communication style overlaid on the active persona (default, Explanatory, Learning, or a custom style)",
    Options: <resolved style names>,           // or validate dynamically
    Get: func(c) any { return c.GetOutputStyle() },
    Set: func(c, v) error { return c.SetOutputStyle(toString(v)) },
},
```

Note: options are dynamic (depend on disk styles). Either compute at
registry build or skip the static `Options` and rely on `SetOutputStyle`'s
validation for the error path (A11).

### Task 7 — Docs + version + changelog

- `docs/user-guide/{en,zh-tw}/user-guide.md` — a "Output styles" section:
  built-ins, how to write a custom one (frontmatter + body example), the
  `keep-coding-instructions` flag, `/output-style`.
- `docs/extending.md` — note the new disk extension point
  (`output-styles/*.md`) beside skills.
- `CHANGELOG.md` + `pkg/version/version.go`.

---

## 5. Design decisions & risks

### 5.1 — Append vs. replace is the whole feature

The single behavioral decision is `keep-coding-instructions`. **Append**
(default) keeps evva a coding agent and layers tone/teaching behavior on top
— this is `Explanatory`/`Learning` and the common case. **Replace** lets a
power user turn evva into a different kind of assistant (the tools are still
present, but the coding doctrine prompt is swapped for the style's). The
infrastructure sections (permission flow, environment, memory, skills,
deferred tools) are **never** dropped — dropping them would break the
runtime, not just the voice. Getting the infra/doctrine split right (§2.1)
is the core implementation risk; the three snapshot tests (A3/A4/A7) are the
guardrail.

### 5.2 — Styles are orthogonal to personas (not redundant)

A reviewer will ask "why not just make Explanatory a persona?" Because a
persona is who + what-tools + which-model; a style is *only* how-it-talks,
and must compose with **every** persona. A user running the `nono` financial
persona can still pick `Explanatory`. Encoding that as personas would force
an N×M explosion (every persona × every voice). The overlay is the right
factoring — and it's exactly the Vision's "swappable" axis distinct from the
"many personas" axis.

### 5.3 — Re-resolve at profile build, never mutate a live prompt

The active style is read at `mainProfile` build time. Switching style
rebuilds the profile (the existing `/model` / `/profile` machinery), which
re-runs `BuildSystemPrompt`. There is no path that edits an in-flight system
prompt. This keeps the prompt immutable per profile instance and avoids a
cache-thrash mid-turn. (It does mean the prompt prefix changes when the
style changes — that's correct and expected, same as a model/profile
switch.)

### 5.4 — Main agent only

Subagents (Explore/Plan/General) have hand-written, task-narrow prompts
(`explore_agent.go`, etc.) and run cold for delegated work. An output style
is a *user-facing communication* preference; it has no meaning for a
subagent returning a structured result to the main agent. Do not thread the
style into spawn. A8 asserts this.

### 5.5 — Persona-declared default style (small, optional)

A disk persona may want a default voice (a teaching persona that defaults to
`Learning`). Add an optional `output_style:` key to `meta.yml`; when set, it
seeds the style for that persona unless the user has explicitly overridden
via `/output-style` this session. **Recommended** as a 10-line addition (it
makes the persona/style composition feel complete), but droppable if
scoping tight — the core feature stands without it. If dropped, note it as a
fast-follow.

### 5.6 — Frontmatter parser sharing

The disk loader needs a flat-frontmatter reader. If the **typed-memory
PRD** has shipped, its `internal/memdir/frontmatter.go` already provides
one — extract it to a tiny shared `internal/mdfront` (or `pkg/common`) and
reuse. If output styles ship first, put the parser here and let memory reuse
it later. Either way, **one** frontmatter parser in the codebase, not two.
No YAML dependency for flat `key: value` frontmatter.

### 5.7 — `default` must be free

A7 is non-negotiable: with no style chosen, the prompt is byte-identical to
today. The whole feature must be invisible until opted into — no reordering
of existing sections, no stray newline. The snapshot test guards this; if it
fails, the refactor in Task 2 changed the default output and must be fixed
before merge.

---

## 6. Out of scope

- **Plugin / managed-policy style tiers** (`forceForPlugin`,
  `policySettings`) — evva has no plugin or enterprise-policy layer.
- **Per-output-style model overrides** — a style changes voice, not model;
  model is the persona/`/model`'s job.
- **Output styles for subagents** (§5.4).
- **A `/output-style:new` scaffolder** (ref has a creation flow) — users
  hand-author the Markdown file; a generator is a nice-to-have follow-up.
- **Statusline framework changes** — surface the active style in the
  existing TUI footer; don't build new statusline plumbing.

---

## 7. Verification checklist (PR gate)

- [ ] **Task 1:** built-ins resolve with no disk files; disk loader honors
      workdir-over-appHome precedence; frontmatter tri-state for
      `keep-coding-instructions`.
- [ ] **Task 2:** three snapshot tests — default (byte-identical to
      pre-feature, A7), append (doctrine + style, A3), replace (no doctrine,
      infra intact, A4).
- [ ] **Task 3:** `SetOutputStyle` persists + validates; `/config` overlay
      lists the field.
- [ ] **Task 4:** Main + disk-persona paths apply the style; persona
      `meta.yml` `output_style` wins when set (A9).
- [ ] **Task 5:** `/output-style` picker switches style; next turn's prompt
      reflects it (A6); active style visible in TUI.
- [ ] **Task 6:** `config` tool switches `output_style` when present (A11).
- [ ] **A8:** subagent prompts unchanged by active style.
- [ ] **A10:** malformed style skipped w/ warning; unknown active name →
      default + warning.
- [ ] `go build/vet/test ./...` green.
- [ ] **Manual (TTY):** `/output-style` → `Explanatory`; ask for a small
      code change; confirm the model adds insight blocks. Switch to
      `default`; confirm normal behavior returns. Drop a custom
      `keep-coding-instructions: false` style in `.evva/output-styles/`;
      confirm `/output-style` lists it and it replaces the harness voice.

---

## 8. File-by-file change list (cheat sheet)

| File | Action | Why |
| --- | --- | --- |
| `internal/outputstyle/style.go` | **New** — type, built-ins, resolve | Task 1 |
| `internal/outputstyle/load.go` | **New** — disk loader + precedence | Task 1 |
| `internal/outputstyle/*_test.go` | **New** | Task 1 |
| `internal/agent/sysprompt/sysprompt.go` | Edit — `OutputStyle*` `PromptContext` fields | Task 2 |
| `internal/agent/sysprompt/main_agent.go` | Edit — infra/doctrine split + `outputStyleSection` | Task 2 |
| `internal/agent/profiles.go` | Edit — resolve + attach style (Main + disk persona) | Task 4 |
| `pkg/config/config.go` | Edit — `OutputStyle` field + Get/Set | Task 3 |
| `pkg/ui/bubbletea/components/overlays/config.go` | Edit — list `output_style` field | Task 3 |
| TUI slash-command registry + new picker overlay | Edit/New — `/output-style` | Task 5 |
| `internal/agent/sysprompt/agent_def.go` (+ `meta.yml` schema) | Edit (optional) — persona `output_style` | §5.5 |
| `internal/tools/config/settings.go` | Edit (if ConfigTool exists) — `output_style` entry | Task 6 |
| `pkg/version/version.go`, `CHANGELOG.md`, user-guide en/zh-tw, `docs/extending.md` | Edit | Task 7 |

---

## 9. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
| --- | --- | --- |
| Task 1 — package + built-ins + loader | ~250 | 2.5 h |
| Task 2 — PromptContext + injection refactor + snapshots | ~120 | 2.5 h |
| Task 3 — config field + overlay | ~60 | 1 h |
| Task 4 — profile wiring | ~40 | 45 min |
| Task 5 — `/output-style` picker + UI surfacing | ~150 | 2.5 h |
| Task 6 — ConfigTool entry (conditional) | ~20 | 20 min |
| Task 7 — docs + changelog + version | ~70 | 1 h |
| Tests | ~250 | 2.5 h |

Total: ~900–1,000 LOC, ~12–14 hours focused. Smaller than the memory phase
(no new data layer, no LLM side-query). The only subtle work is the
infra/doctrine split in Task 2 — everything else is a well-trodden
load-Markdown-from-disk + config-setting + picker pattern evva already has
three instances of (skills, profiles, models).
