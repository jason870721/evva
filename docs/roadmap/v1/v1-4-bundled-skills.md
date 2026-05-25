# v1.4 — Bundled Skills — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** ready to build — **verified against the working tree on branch
> `feature/v1.4` (2026-05-25).** Every inventory line number, symbol, path,
> and `pkg/hooks` field cited below was checked against the live source;
> the corrections from that pass are folded in and itemised in
> §8 (Verification change log) at the end of this doc.
> **Target release:** `v1.4.0` (additive, minor bump under the Stable-tier promise).
> **Roadmap source:** `CLAUDE.md` → Roadmap → *v1.4 — Bundled skills*.
> **Sequencing note:** this phase ships **before** `v1.2` (OpenAI provider) and
> `v1.3` (MCP framework) by an explicit CTO directive — bundled skills deliver
> day-one user value and have **zero dependency** on either of those phases.
> The Roadmap section in `CLAUDE.md` will be updated to reflect the shipping
> order (Task 6).

---

## 1. TL;DR — what this phase actually is

The skill framework (`pkg/skill`) is **already complete** and Stable:

- `skill.Registry` carries both disk-loaded SKILL.md files
  (`<APP_HOME>/skills/<name>/SKILL.md` and
  `<workdir>/.evva/skills/<name>/SKILL.md`) and programmatic skills (a
  `SkillMeta` whose `BodyFunc` returns the body on demand).
- `internal/agent/agent.go:310` auto-loads the disk catalog into the
  per-agent `ToolState.SkillRegistry()` whenever a host has not injected
  one via `WithSkillRegistry`.
- `internal/agent/sysprompt/fragments.go:skillsSection` already advertises
  each skill as **`- <name>: <description>`** in the prompt's `# Skills`
  block — bodies are **never** injected at prompt time, exactly the
  context-economy the directive requires.
- The `SKILL` tool reads `ToolState.SkillRegistry()` at Execute time and
  returns the body wrapped as `"Follow these instructions for skill X:"`
  so the model treats it as guidance, not raw text to summarise.
- Subagent definitions (`ExploreAgent`, `GeneralAgent`, `PlanAgent` in
  `internal/agent/sysprompt/agent_def.go`) declare `AdvertiseSkills: false`
  — only the **Main** agent surfaces the catalog. This is correct and
  must stay (subagents are launched cold for narrow tasks; the catalog
  would bloat their context).

**What is missing:** the framework has **no built-in content.** CLAUDE.md
states that `/commit` "already ships," but that is aspirational. A search
for *committed* skill bodies — `git ls-files '*/SKILL.md'` — returns
**nothing**, and no programmatic skill is registered anywhere in the
binary (`skill.NewRegistry()` appears only in the nil-cfg fallback at
`internal/agent/skills.go:22` and in `pkg/agent/options.go` doc comments;
nothing calls `Registry.Add` outside tests). The dev tree *does* contain
`<repo>/.evva/skills/commit/SKILL.md`, but `.evva/` is gitignored
(`.gitignore:37`) — it is the local runtime `EVVA_HOME`, a *disk-loaded*
skill, **not** shipped or embedded content (and it predates this plan,
with a different Conventional-Commits body that ignores evva's `--author`
policy). A fresh install has an empty `EVVA_HOME`, so today every fresh
install boots with an empty `# Skills` section.

This phase delivers two things:

1. **A new "bundled skills" channel** — Go-side programmatic skills,
   embedded in the binary via `go:embed`, that the `loadDiskSkillRegistry`
   path overlays on top of the disk-loaded catalog. User disk skills with
   the same name **silently override** the bundled body (the user is the
   author of last resort).
2. **Five tier-1 skills authored from scratch or ported from
   `ref/src/skills/bundled` and `ref/src/commands`:**
   `commit`, `review`, `security-review`, `simplify`, and the
   evva-specific `setup-hooks` (the hooks-onboarding skill that completes
   the v1.1 story by teaching users and the model how to author `pkg/hooks`
   configurations).

A tier-2 list of "ship if cheap" skills (`debug`, `remember`, `loop`) is
specified at the end of §4 so the implementing agent can knock them out
in the same release without scope creep — each is a self-contained
SKILL.md addition.

**Do not modify the agent loop, the prompt builder, or the SKILL tool.**
The work is content authoring plus one small additive method on
`pkg/skill.Registry`. Phase deltas are small, but the doc is long because
every skill body is reproduced verbatim — the SKILL.md content is the
deliverable, not the wiring.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 `pkg/skill` (Stable)

| Symbol | Role |
| --- | --- |
| `Registry` | Merged catalog. Methods: `Add`, `Get`, `List`, `Names`, `LoadBody`. Concurrency-safe at construction time. |
| `SkillMeta{Name, Description, Path, Source, BodyFunc}` | Single struct for both disk and programmatic skills. `BodyFunc` non-nil ⇒ programmatic. |
| `SkillSource` constants | `SourceHome`, `SourceWorkDir`, `SourceProgrammatic`. (v1.4 adds `SourceBundled` — Task 1.) |
| `LoadRegistry(homeDir, workdirDir)` | Walks both dirs; `workdir` overrides `home`. Missing dirs are empty (not an error). Returns warnings, not failures. |
| `NewRegistry()` | Empty registry for programmatic-only catalogs. |
| `Registry.Add(SkillMeta)` | Inserts a programmatic skill; rejects duplicates. Forces `Source = SourceProgrammatic`. (v1.4 adds a sibling `AddBundled` — Task 1.) |
| `NewSkill(Lookup) *SkillTool` | Constructs the LLM-facing `SKILL` tool. Reads the registry via late-bound `Lookup` so init order between tool build and registry install is free. |

### 2.2 Wiring sites (cf. `grep -rln 'pkg/skill' --include='*.go'`)

| File | Role |
| --- | --- |
| `internal/agent/skills.go:20 loadDiskSkillRegistry(cfg)` | The **single seam** through which all disk skills enter the agent. Returns an empty registry on `nil` cfg. **Task 4 hooks bundled.Register here.** |
| `internal/agent/skills.go:35 refsFromRegistry(r)` | Flattens `*skill.Registry` → `[]sysprompt.SkillRef` for prompt rendering. |
| `internal/agent/agent.go:310-319` | The "no override injected" auto-load branch — calls `loadDiskSkillRegistry`, surfaces warnings to the agent logger, and caches `skillRefs`. |
| `internal/toolset/builtins.go:100` | The `SKILL` factory: `return skill.NewSkill(ts.SkillRegistry), nil`. |
| `internal/agent/profiles.go:127` | The Main profile includes `skill.Names()` in its `ActiveTools` so the `SKILL` tool is always available without a `tool_search` round-trip. |
| `internal/agent/sysprompt/fragments.go:208 skillsSection(skills)` | Renders the `# Skills` block — `- <name>: <description>` per skill, **no bodies**. Adds a hint that the user can author more under `<workdir>/.evva/skills/` or `<APP_HOME>/skills/`. |
| `internal/agent/sysprompt/agent_def.go:90-119` | `MainAgent` has `AdvertiseSkills: true`. `ExploreAgent`, `GeneralAgent`, `PlanAgent` all carry `AdvertiseSkills: false`. **Subagents will not see the bundled catalog — this is intentional and stays.** |
| `pkg/agent/options.go:99 WithSkillRegistry(r)` | SDK opt-in: a downstream host can pre-install its own registry to skip the disk auto-load. |
| `internal/agent/spawn.go:86` | Spawn passes `WithHookRegistry` to subagents (shares the parent's loaded hooks). It does **not** pass `WithSkillRegistry`: subagents (Explore/General/Plan) carry `AdvertiseSkills: false` and lack the `skill` tool in `ActiveTools`, so they never render or dispatch the catalog. (Their nil `SkillRegistry()` makes `agent.go:310` re-run the disk+bundled load per spawn; bodies are lazy so the cost is a few embed reads, never shown to the subagent. Skipping the overlay when `AdvertiseSkills` is false is a possible micro-opt — out of v1.4 scope.) |

### 2.3 `pkg/hooks` (v1.1, Experimental)

The setup-hooks skill body is the **integration point** between this
phase and the v1.1 hooks engine. Reference contract (frozen):

| File | Surface used by setup-hooks |
| --- | --- |
| `pkg/hooks/types.go:Event` | Six constants — `SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`, `Notification`. |
| `pkg/hooks/types.go:Command` | `Type` (`"command"`\|`"http"`), `Command`, `URL`, `Method`, `Headers`, `Timeout`, `Async`. |
| `pkg/hooks/loader.go:Load(workdir, evvaHome)` | Reads `<workdir>/.evva/settings.json` and `<evvaHome>/settings.json`. Project hooks fire before user hooks. |
| `pkg/hooks/decision.go` | Stdout-JSON shape: `{continue, decision, reason, systemMessage, hookSpecificOutput: {permissionDecision, updatedInput, additionalContext, initialUserMessage}}`. |
| Exit-code semantics (`pkg/hooks/runner.go`) | `0` → parse stdout JSON; `1` → log + ignore; `2` → block with stderr as reason; timeout → block. |

The setup-hooks skill must reference these paths and shapes **exactly**
— anything that diverges from `pkg/hooks` is a documentation bug.

---

## 3. Goal & acceptance criteria

**Goal:** every fresh evva install boots with a non-empty `# Skills`
section advertising at least five well-tested bundled skills, each
invocable via `SKILL` and useful out of the box — without expanding any
agent context beyond the skill name + one-line description.

Ship is complete when **all** of these pass:

- **A1 — Bundled catalog appears in the prompt.** A fresh install with
  empty `<APP_HOME>/skills/` and no `<workdir>/.evva/skills/` renders a
  `# Skills` section whose body lists the five tier-1 skills
  (`commit`, `review`, `security-review`, `simplify`, `setup-hooks`),
  each as `- <name>: <description>`, with **no bodies inlined**.
- **A2 — `SKILL` dispatch returns the bundled body.** Calling the
  `SKILL` tool with `{"skill": "commit"}` returns
  `"Follow these instructions for skill `commit`:\n\n<commit body>"`
  where `<commit body>` is the embedded SKILL.md content.
- **A3 — User disk skill silently overrides a bundled.** Authoring
  `<workdir>/.evva/skills/commit/SKILL.md` with a custom body makes the
  registry resolve `commit` to the user's body, **without** any
  shadowing warning (bundled is the lowest-precedence tier). Removing
  the file makes the bundled body win again.
- **A4 — Subagent prompts do not include the catalog.** Build an
  Explore / General / Plan subagent (the same way `agent.Agent.Spawn`
  does); the resulting system prompt contains **no** `# Skills` block.
- **A5 — Zero-cost when not invoked.** Bodies are only read out of the
  embed FS when the model dispatches the `SKILL` tool; the prompt path
  must not touch `BodyFunc` (verified by passing a panicking
  `BodyFunc` and asserting startup succeeds).
- **A6 — `setup-hooks` round-trip.** A user invokes
  `{"skill": "setup-hooks", "args": "format on write"}`; the returned
  body teaches them the `.evva/settings.json` schema, the `PostToolUse`
  matcher, the decision JSON, and the pipe-test verification flow. The
  body **names `pkg/hooks` event constants** (`PreToolUse`,
  `PostToolUse`, ...) and **does not reference `~/.claude/`** or any
  Claude Code-specific path.
- **A7 — `commit` honors evva's authorship policy.** The `commit` body's
  example commit invocation includes
  `--author="evva <frizoevva@gmail.com>"` (matching
  `internal/agent/sysprompt/fragments.go:48`) and **no
  `Co-Authored-By: Claude` trailer**.
- **A8 — `simplify` uses evva tool names.** The body names `agent` (not
  `Task`), `subagent_type: "explore"` or `"plan"` for the parallel
  review agents, and `skill` (not `SKILL_TOOL_NAME`) for any
  cross-reference.
- **A9 — Resilience.** `bundled.Register(nil)` is a no-op (no panic);
  `bundled.Register(reg)` on a registry whose existing entry conflicts
  is a no-op for that name only; loader warnings (e.g. from a malformed
  on-disk SKILL.md) still propagate.
- **A10 — Tests + version.** `go test ./...` is green. `pkg/skill`
  gains tests for `SourceBundled` + `AddBundled`. `internal/skills/bundled`
  has its own unit test (every embedded skill loads cleanly and parses
  its first-line title). `pkg/version.Version` is bumped to `"1.4.0"`,
  `CHANGELOG.md` carries a `## [v1.4.0]` entry, and the Stable-tier
  surface notes in `docs/sdk-stability.md` mention `AddBundled` as
  additive.

---

## 4. Work breakdown (ordered)

### Task 0 — Reorder the roadmap in CLAUDE.md

A two-line edit. The CTO chose to ship v1.4 before v1.2 and v1.3, so the
Roadmap section's ordering note ("ordered by one principle … finish
before expand, integrity before power") must acknowledge the deviation.

**File:** `CLAUDE.md`.

**Edit:** under `## Roadmap (post-v1.0.0)`, immediately before the
`### v1.1` heading, append a paragraph:

> **Shipping order, post-v1.1:** v1.4 (bundled skills) ships **before**
> v1.2 (OpenAI provider) and v1.3 (MCP). Skills deliver day-one user
> value, have no dependency on either provider work or the MCP framework,
> and the harness already supports them — only the content was missing.
> v1.2 and v1.3 then ship in their original order. Phase numbers stay
> as-is so changelog references continue to resolve.

Rationale for landing this first: future cross-references in
`CHANGELOG.md` and the v1.4 doc itself ("ships before v1.2/v1.3") must
have a corresponding statement in the source-of-truth roadmap, or the
docs disagree with the project's stated plan.

### Task 1 — Add `SourceBundled`, `Registry.AddBundled`, and `ParseTitleLine` to `pkg/skill`

Three additions; the existing `SourceProgrammatic` channel cannot
be reused because it rejects duplicates and overrides nothing — bundled
needs the opposite semantic (**lowest-precedence; silent skip when the
user shadowed it**). The third addition is a shared title-line parser
that **both** the disk loader (`parseFirstLine` in `registry.go:180-229`)
**and** the new bundled parser (Task 2) call, so the two paths can't
silently drift on what counts as a valid `# <name> <description>` line.

**File:** `pkg/skill/registry.go`.

**Add the constant** near the existing `Source*` block (~line 43):

```go
// SourceBundled identifies a skill registered by evva itself, embedded in
// the binary via go:embed (internal/skills/bundled). Bundled is the
// lowest-precedence tier: a same-named disk skill (Home or WorkDir) wins
// silently, and a Programmatic skill added by the host wins too. The
// shadowing is NOT recorded as a Warning — the user (or host) is the
// author of last resort, so overriding a bundled body is the documented
// extension point, not a surprise.
SourceBundled SkillSource = "bundled"
```

**Add the method** at the bottom of the file:

```go
// AddBundled inserts a skill at SourceBundled tier. Differs from Add in
// two ways:
//
//  1. If a skill with the same Name already exists in the registry
//     (typically loaded from disk by LoadRegistry), AddBundled silently
//     skips the insert and returns nil. The user's on-disk override wins
//     without a Warning — overriding a bundled body is the documented
//     extension point, not shadowing.
//  2. Source is force-set to SourceBundled regardless of the caller's
//     value, mirroring how Add force-sets SourceProgrammatic.
//
// Validation:
//   - Name must be non-empty.
//   - BodyFunc must be non-nil — bundled skills always carry their body
//     via a closure that reads from the embed FS.
//
// Callers in internal/skills/bundled register each skill via this method.
// External SDK consumers SHOULD NOT call AddBundled — use Add for
// programmatic skills the host ships.
func (r *Registry) AddBundled(m SkillMeta) error {
    if r == nil {
        return fmt.Errorf("skill: nil registry")
    }
    if strings.TrimSpace(m.Name) == "" {
        return fmt.Errorf("skill: name is required")
    }
    if m.BodyFunc == nil {
        return fmt.Errorf("skill: bundled %q has no BodyFunc", m.Name)
    }
    if r.skills == nil {
        r.skills = map[string]SkillMeta{}
    }
    if _, exists := r.skills[m.Name]; exists {
        return nil // user/programmatic override wins; silent skip.
    }
    m.Source = SourceBundled
    r.skills[m.Name] = m
    return nil
}
```

**Add the shared title parser.** Today the disk loader's
`parseFirstLine` (`pkg/skill/registry.go:180-229`) does its own split
on the first non-blank line into `name` + `description`, and a second
parser would be born in `internal/skills/bundled` (Task 2) if we let
it. Both must accept the same shapes (`# <name> <desc>` and
`# <name>`) and reject the same malformed inputs (no `# ` prefix,
empty title). Extract the shared logic so they cannot drift:

```go
// ParseTitleLine parses the first non-blank line of a SKILL.md title
// — the canonical `# <name> [<description>]` shape — into its name and
// optional description components. Returns an error when the line is
// missing the `# ` prefix or carries an empty name.
//
// Callers:
//   - The disk loader's parseFirstLine, after it has scanned the file
//     for the first non-blank line (it additionally compares name
//     against the folder name and emits a warning on mismatch — that
//     check stays in the disk path because bundled skills have no
//     folder concept).
//   - internal/skills/bundled's buildMeta, after reading the embedded
//     file content.
//
// Both paths share the validation rules here, so a shape that loads
// as a disk skill MUST also load as a bundled skill and vice versa.
func ParseTitleLine(line string) (name, description string, err error) {
    trimmed := strings.TrimSpace(line)
    if trimmed == "" {
        return "", "", fmt.Errorf("skill: empty title line")
    }
    if !strings.HasPrefix(trimmed, "# ") {
        return "", "", fmt.Errorf("skill: title line must start with `# `: got %q", trimmed)
    }
    rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
    if rest == "" {
        return "", "", fmt.Errorf("skill: empty title")
    }
    parts := strings.SplitN(rest, " ", 2)
    name = parts[0]
    if len(parts) == 2 {
        description = strings.TrimSpace(parts[1])
    }
    return name, description, nil
}
```

**Refactor `parseFirstLine` to call it.** Edit
`pkg/skill/registry.go:180-229`: keep the file-open + scanner +
"find first non-blank line" logic, then delegate the split to
`ParseTitleLine`. The folder-name mismatch warning stays in this
function because the bundled path has no folder to compare against:

```go
// parseFirstLine (after refactor) — same signature, same warnings,
// title parsing delegated.
func (r *Registry) parseFirstLine(path, folder string) (string, bool) {
    f, err := os.Open(path)
    if err != nil { r.warnf("skill: open %q: %v", path, err); return "", false }
    defer f.Close()

    scanner := bufio.NewScanner(f)
    scanner.Buffer(make([]byte, 0, 4*1024), 1*1024*1024)
    var first string
    for scanner.Scan() {
        if line := strings.TrimSpace(scanner.Text()); line != "" {
            first = line
            break
        }
    }
    if err := scanner.Err(); err != nil { r.warnf("skill: read %q: %v", path, err); return "", false }
    if first == "" { r.warnf("skill: %q has no title line", path); return "", false }

    titleName, desc, err := ParseTitleLine(first)
    if err != nil { r.warnf("skill: %q: %v", path, err); return "", false }
    if titleName != folder {
        r.warnf("skill: %q title names %q but folder is %q; using folder name", path, titleName, folder)
    }
    return desc, true
}
```

**Tests** (`pkg/skill/registry_test.go`):
- `TestAddBundled_Insert` — empty registry → entry present at
  `SourceBundled`.
- `TestAddBundled_SkipsExisting` — pre-seed with a disk skill named
  `commit`; AddBundled with the same name returns nil, the registry
  still holds the disk entry, **no warning** is appended.
- `TestAddBundled_Validates` — empty name → error; nil BodyFunc → error;
  nil receiver → error.
- `TestAddBundled_OverriddenByLoadDir` — call AddBundled first, then
  `loadDir` for a workdir entry with the same name: the loader still
  warns (existing semantics: cross-source override of any kind warns)
  and replaces. (This is fine — the typical flow is the opposite
  order, see Task 4.)
- `TestParseTitleLine` — table-driven cover of every accepted/rejected
  shape: `"# name desc"` → `("name", "desc", nil)`; `"# name"` →
  `("name", "", nil)`; `"#name"` (no space) → error; `"name desc"`
  (no `#`) → error; `""` → error; `"#  "` (empty title) → error;
  leading/trailing whitespace tolerated.
- `TestParseFirstLine_StillWarnsOnFolderMismatch` — regression pin
  that the disk path's folder-vs-title mismatch warning survives the
  refactor.

**SDK stability impact:** `pkg/skill` is Stable. All three additions
are purely additive: a new const value (consumers' switch statements
default through `SourceProgrammatic`), a new method, and a new
exported function. No existing surface moves or changes signature.
Therefore: minor bump compatible. Update `docs/sdk-stability.md`
(Task 6) so the `pkg/skill` row mentions `AddBundled` and
`ParseTitleLine` alongside `Add`.

### Task 2 — Create `internal/skills/bundled` package

New private package — bundled content is evva's product, not part of
the SDK surface. Downstream apps assembling their own skill catalogs
keep using `skill.NewRegistry()` + `Add(...)`.

**Layout:**

```
internal/skills/bundled/
├── bundled.go            # package doc + Register entrypoint
├── embed.go              # go:embed FS + helpers
├── bundled_test.go       # one test per skill + Register integration
└── content/
    ├── commit/
    │   └── SKILL.md
    ├── review/
    │   └── SKILL.md
    ├── security-review/
    │   └── SKILL.md
    ├── simplify/
    │   └── SKILL.md
    └── setup-hooks/
        └── SKILL.md
```

**`bundled.go`:**

```go
// Package bundled registers evva's first-party Markdown skills into a
// skill.Registry. Each SKILL.md file lives under content/<name>/SKILL.md
// and is embedded in the binary via go:embed (embed.go). Register reads
// the first line of each file for the description, wraps the body in a
// lazy BodyFunc, and calls Registry.AddBundled — so any user disk skill
// of the same name silently wins (see pkg/skill.SourceBundled).
//
// The package is private. Downstream SDK consumers that want to ship
// their own skill content build their own skill.Registry via
// skill.NewRegistry()+Add(...) and pass it through agent.WithSkillRegistry;
// see docs/extending.md "Custom skills (Skill SDK)".
package bundled

import (
    "fmt"
    "strings"

    "github.com/johnny1110/evva/pkg/skill"
)

// Register overlays every embedded skill onto reg. Each bundled entry is
// added via Registry.AddBundled, which silently skips names already
// present in reg — so disk-loaded entries (Home or WorkDir) override the
// bundled body without warning. nil reg is a no-op (matches the rest of
// the agent wiring's nil-safety stance).
//
// Returns any non-fatal warnings (currently only "embedded SKILL.md has
// no title line"). Callers surface these on the agent logger the same
// way they surface skill.Registry.Warnings.
func Register(reg *skill.Registry) []string {
    if reg == nil {
        return nil
    }
    var warns []string
    for _, name := range bundledNames {
        meta, err := buildMeta(name)
        if err != nil {
            warns = append(warns, fmt.Sprintf("bundled skill %q: %v", name, err))
            continue
        }
        if err := reg.AddBundled(meta); err != nil {
            warns = append(warns, fmt.Sprintf("bundled skill %q: register: %v", name, err))
        }
    }
    return warns
}

// buildMeta parses the embedded SKILL.md's first non-blank line for the
// description ("# <name> <description>"), and wraps the full file content
// in a BodyFunc closure (lazy: only read when the model dispatches).
//
// Title parsing delegates to skill.ParseTitleLine so the disk loader
// (pkg/skill/registry.go:parseFirstLine) and this path validate exactly
// the same shapes — a SKILL.md that loads as a disk skill MUST also
// load as a bundled skill and vice versa.
func buildMeta(name string) (skill.SkillMeta, error) {
    raw, err := readBundled(name)
    if err != nil {
        return skill.SkillMeta{}, err
    }
    first := firstNonBlankLine(raw)
    if first == "" {
        return skill.SkillMeta{}, fmt.Errorf("bundled %q: no title line", name)
    }
    titleName, desc, err := skill.ParseTitleLine(first)
    if err != nil {
        return skill.SkillMeta{}, fmt.Errorf("bundled %q: %w", name, err)
    }
    if titleName != name {
        // bundledNames is our source of truth; don't warn (we have no
        // log channel here) but the integration test (Task 5) asserts
        // every embedded title matches its bundledNames entry, so a
        // mismatch surfaces in CI rather than at boot.
    }
    bodyCopy := raw // body keeps the title line — the SKILL tool's framing relies on it
    return skill.SkillMeta{
        Name:        name,
        Description: desc,
        BodyFunc: func() (string, error) {
            return bodyCopy, nil
        },
    }, nil
}

// firstNonBlankLine returns the first non-blank line of raw, with
// leading/trailing whitespace preserved on the returned line. The
// scanning rule mirrors the disk loader's bufio.Scanner walk.
func firstNonBlankLine(raw string) string {
    for _, line := range strings.Split(raw, "\n") {
        if strings.TrimSpace(line) != "" {
            return line
        }
    }
    return ""
}
```

**`embed.go`:**

```go
package bundled

import (
    "embed"
    "fmt"
)

//go:embed content/*/SKILL.md
var contentFS embed.FS

// bundledNames is the canonical list of skill names this package owns.
// Order is for stable Register iteration only — the registry sorts
// entries by name for the prompt section, so visible order does not
// depend on this slice.
//
// To add a new bundled skill:
//   1. Create content/<name>/SKILL.md (first line: `# <name> <desc>`).
//   2. Append <name> here.
//   3. Add a unit test asserting the file embeds and parses cleanly.
var bundledNames = []string{
    "commit",
    "review",
    "security-review",
    "simplify",
    "setup-hooks",
    // Tier-2 candidates (see §4 tier-2): "debug", "remember", "loop".
}

// readBundled returns the raw SKILL.md content for a bundled skill.
// Returns an error rather than panicking on missing files so a typo in
// bundledNames surfaces as a Register warning, not a crash.
func readBundled(name string) (string, error) {
    path := "content/" + name + "/SKILL.md"
    b, err := contentFS.ReadFile(path)
    if err != nil {
        return "", fmt.Errorf("embed: %s: %w", path, err)
    }
    return string(b), nil
}
```

**Tests (`bundled_test.go`):**

- `TestRegister_AddsAll` — empty registry → after Register, `reg.Names()`
  contains every entry from `bundledNames`. No warnings.
- `TestRegister_NilSafe` — `Register(nil)` returns nil, no panic.
- `TestRegister_DiskOverridesBundled` — pre-seed a disk skill named
  `commit`; Register skips it silently; `reg.Get("commit").Source ==
  SourceHome`. No warnings about shadowing.
- `TestRegister_AllBodiesLoadable` — for each name in `bundledNames`,
  `reg.LoadBody(name)` returns a non-empty string starting with `"# "`.
- `TestRegister_TitleMatchesBundledName` — for each name in
  `bundledNames`, `skill.ParseTitleLine` on the embedded SKILL.md's
  first non-blank line returns a `titleName` equal to that bundled
  name. Pins the format invariants from §3 against a typo in any
  SKILL.md title.
- `TestRegister_PromptPathDoesNotCallBodyFunc` — the load-bearing
  zero-cost test that **acceptance criterion A5** requires. Build a
  registry programmatically with a `SkillMeta` whose `BodyFunc` is
  `func() (string, error) { panic("body must not be loaded at prompt time") }`.
  Add it via `Registry.Add` (the test reaches past `AddBundled`'s
  silent-skip semantics for clarity). Then exercise every code path
  the agent walks at boot / sysprompt time and assert no panic:
  - `reg.List()`
  - `reg.Names()`
  - `reg.Get("panic-skill")`
  - flatten via `refsFromRegistry(reg)` (the helper sysprompt feeds
    from — `internal/agent/skills.go:35`)
  - render the Main prompt with the resulting `SkillRef` (call into
    `sysprompt.skillsSection` directly — it lives in package
    `sysprompt`, so use the package-private form via a parallel
    `internal/agent/sysprompt/skills_zerocost_test.go` instead if the
    helper isn't exported)
  Finally, call `reg.LoadBody("panic-skill")` inside a `defer
  recover()` and assert the panic DOES fire — the lazy load is the
  one and only place `BodyFunc` may run. Cite A5 in the test comment
  so a future reader knows what it pins.
- `TestEachSkillHasMatchingFolder` — every entry in `bundledNames` has
  a corresponding `content/<name>/SKILL.md` file. (Belt-and-suspenders
  for `embed.FS` mismatches.)

### Task 3 — Author the five tier-1 SKILL.md files

This is the bulk of the work. Each file lives at
`internal/skills/bundled/content/<name>/SKILL.md`. The implementing
agent should treat the bodies below as **canonical** — minor wording
polish is fine, but the structure, tool names, file paths, and
verification flows must land verbatim because they were carefully
adapted to evva's reality (tool names, hooks engine surface, settings
paths).

**Format invariants for every SKILL.md:**

- First non-blank line: `# <name> <one-line description>` — the
  description is what `skillsSection` advertises to the model. Keep it
  under ~120 characters; the line is read by `parseTitleAndBody` in
  Task 2.
- The body that follows is rendered to the model verbatim, prefixed by
  `"Follow these instructions for skill `<name>`:\n\n"` (the SKILL
  tool's wrapping).
- Tool names referenced in skill bodies must match evva's wire names:
  `read`, `write`, `edit`, `bash`, `grep`, `glob`, `tree`, `agent`,
  `tool_search`, `skill`, `web_search`, `web_fetch`, `json_query`,
  `calc`, `todo_write`, `ask_user_question`, `enter_plan_mode`,
  `exit_plan_mode`, `enter_worktree`, `exit_worktree`,
  `update_user_profile`, `update_project_memory`, `daemon_list`,
  `daemon_output`, `daemon_stop`, `monitor`, `cron_create`,
  `cron_list`, `cron_delete`, `lsp_request`. (Source: `pkg/tools/name.go`
  cross-checked by `internal/agent/sysprompt/toolnames_link_test.go`.)
- Subagent kinds: `explore`, `plan`, `general-purpose`. (Source:
  `internal/agent/sysprompt/toolnames.go` constants `subagentExplore`,
  `subagentPlan`, `subagentGeneral`.)
- **Do not** reference Claude Code tool names (`Task`, `Bash`, `Grep`,
  `Glob`, `Read`, `Write`, `Edit`, `WebSearch`, `WebFetch`, `Plan`,
  `EnterPlanMode`, etc.) — these are the ref TS names and would
  confuse the model since they do not exist in evva's tool surface.
- **Do not** reference Claude Code paths (`~/.claude/`,
  `.claude/settings.json`, `~/.claude/keybindings.json`).
- Where the ref source uses `${TOOL_NAME}` template interpolation
  (e.g. `${AGENT_TOOL_NAME}` in `simplify.ts`), inline the literal
  evva tool name in the SKILL.md so there is no runtime templating.

The body specs below show each skill's final content. The implementing
agent copies these verbatim into the respective SKILL.md (preserving
the leading title line — see the format invariants above).

#### 3.1 `commit/SKILL.md`

Adapted from `ref/src/commands/commit.ts`. Two substantive divergences:
the attribution line uses evva's `--author=` policy
(`internal/agent/sysprompt/fragments.go:48`), and the body uses
backticked `bash` (evva's tool name) rather than Claude Code's
camel-case `Bash`. Inline shell-evaluation (`!\`git status\``) is **not
supported** by the evva skill loader — the body instead instructs the
model to *run* the commands itself, which is the same behavior the
model already exhibits.

```markdown
# commit Create a git commit for the staged + relevant unstaged changes

Use this skill when the user asks for a commit ("commit this", "make a commit", "/commit"). Do NOT use it for amending, rebasing, or pushing.

## Context to gather (run these in parallel)

Before drafting the message, run each of the following with `bash`:

1. `git status` — see all changes (untracked + modified). Never pass `-uall`.
2. `git diff HEAD` — staged and unstaged content together so you can judge what to include.
3. `git log --oneline -10` — match this repo's existing commit style.
4. `git branch --show-current` — for context.

## Git safety protocol

- NEVER update `git config`.
- NEVER skip hooks (`--no-verify`, `--no-gpg-sign`) unless the user explicitly asks.
- CRITICAL: always create a NEW commit. Never use `git commit --amend` unless the user explicitly asks.
- Do NOT commit files that may contain secrets (`.env`, `credentials.json`, `*.pem`, `*.key`). Warn the user if they specifically ask for one of these.
- If there are no changes (no untracked files and no modifications), do not create an empty commit — say so and stop.
- Never run interactive git modes (`git rebase -i`, `git add -i`) — they hang on stdin.

## Draft the message

- Match the style of the recent commits you read above.
- Summarize the change in 1–2 sentences focused on the WHY, not the WHAT (the diff already says what).
- Use "add" for net-new features, "update"/"refactor" for changes to existing features, "fix" for bug fixes, "docs" for documentation-only changes.
- If the change spans multiple unrelated concerns, ask the user (via `ask_user_question`) whether to split into separate commits before drafting.

## Stage and commit

Stage the files you intend to include explicitly by name. Do not run `git add -A` or `git add .` because they may sweep in unrelated artifacts or secrets.

Author the commit as evva. Pass the message via a heredoc so multi-line bodies render correctly:

```
git commit --author="evva <frizoevva@gmail.com>" -m "$(cat <<'EOF'
<your commit message>
EOF
)"
```

## After the commit

Run `git status` once more to confirm the commit landed and the working tree is in the state you expect. Do NOT push unless the user explicitly asks — pushing is a shared-state action that needs separate authorization.

If you also touched `pkg/version/version.go` or `CHANGELOG.md` as part of the change, include those in the commit (they belong with the surface they describe).
```

#### 3.2 `review/SKILL.md`

Adapted from `ref/src/commands/review.ts` with evva tooling: `bash` for
`gh` invocations, `agent` for subagent delegation if the reviewer wants
a parallel pass.

```markdown
# review Review a GitHub pull request

Use this skill when the user asks you to review a PR ("review #123", "look at this PR", "/review 42"). The skill expects a PR number in `args`; if `args` is empty, list open PRs first and ask which one to review.

## Workflow

1. Resolve the PR.
   - If `args` is empty or non-numeric, run `gh pr list` (via `bash`) and pause: tell the user which PR you want to review and ask them to confirm.
   - If `args` is a number, run `gh pr view <number>` then `gh pr diff <number>` (via `bash`, in parallel).

2. Read the diff in full. For diffs over ~500 lines, delegate exploration of any unfamiliar files referenced in the diff to a subagent with `subagent_type: "explore"` — its read-only nature is the safest preset and keeps your context clean.

3. Produce the review. Use these sections in order:
   - **Summary** — 1–3 sentences explaining what the PR does and why.
   - **Correctness** — concrete bugs, race conditions, off-by-one errors, missing nil-checks at boundaries, broken invariants. Cite `file:line` for every finding.
   - **Conventions** — places the diff deviates from the repo's existing patterns. Skim 2–3 sibling files before flagging a convention violation.
   - **Performance** — only call out hot-path regressions (request handlers, render loops, startup paths). Skip micro-optimizations.
   - **Tests** — does the PR's test coverage exercise the change? Note untested branches, but do not demand 100% — match the repo's existing test bar.
   - **Security** — input validation, authorization, secrets. For a focused security pass, suggest the user invoke the `security-review` skill afterwards.
   - **Nits** (optional) — small style preferences, gathered under one heading so the substantive findings stay legible.

## Tone

- Concrete and actionable. "Move this validation above the early return" beats "consider validation here".
- Cite `file:line` for every finding so the author can navigate.
- Acknowledge what the PR gets right — a review that is only criticism is incomplete signal.
- If the diff is well-scoped and bug-free, say so plainly. Do not invent issues to pad the review.

## Out of scope for this skill

- Don't run code or tests yourself unless the user asks.
- Don't push commits, leave review comments via `gh pr review`, or merge — those are shared-state actions that need separate authorization.
- Don't run `security-review` automatically; surface it as a suggestion if the diff touches an obvious surface (auth, parsers, deserialization, HTML rendering, SQL).
```

#### 3.3 `security-review/SKILL.md`

Adapted from `ref/src/commands/security-review.ts`. The body is largely
verbatim — security guidance is universal. The two substantive
divergences: `agent` (not `Task`) for the sub-task delegation, and a
note that evva's `bash` tool is used to invoke `git`.

```markdown
# security-review Conduct a focused security review of the branch's pending changes

Use this skill when the user wants a security pass on a branch's pending changes ("security review", "check for vulnerabilities", "/security-review"). This is NOT a general code review — focus only on security implications introduced by the diff.

## Gather the diff

Run these in parallel with `bash`:

- `git status`
- `git diff --name-only origin/HEAD...`
- `git log --no-decorate origin/HEAD...`
- `git diff origin/HEAD...`

If `origin/HEAD` does not exist, fall back to `git diff main...` or `git diff master...`. If neither exists, ask the user which branch the diff should be taken against.

## Objective

Identify HIGH-CONFIDENCE security vulnerabilities with real exploitation potential introduced by THIS diff. Do not flag existing concerns; do not flag theoretical issues.

## Critical instructions

1. **Minimize false positives.** Only flag issues where you are >80% confident of actual exploitability.
2. **Avoid noise.** Skip theoretical issues, style concerns, low-impact findings.
3. **Focus on impact.** Prioritize vulnerabilities that lead to unauthorized access, data breach, or system compromise.
4. **Hard exclusions** — never report:
   - Denial of Service (DOS) vulnerabilities, even if they disrupt service.
   - Secrets or sensitive data stored on disk (handled by other processes).
   - Rate limiting or resource exhaustion.
   - Memory safety issues in memory-safe languages (Go, Rust, Java, Python, ...).
   - Race conditions or timing attacks that are theoretical rather than practical.
   - SSRF that only controls the path (must control host or protocol to be a vulnerability).
   - Findings in test files or test-only code.
   - Log spoofing or non-PII data logging.
   - Vulnerabilities in outdated third-party libraries (managed separately).

## Categories to examine

**Input validation**
- SQL injection via unsanitized user input
- Command injection in system calls or subprocesses
- XXE in XML parsing
- Template injection
- NoSQL injection
- Path traversal in file operations

**Authentication & authorization**
- Authentication bypass logic
- Privilege escalation paths
- Session management flaws
- JWT vulnerabilities
- Authorization logic bypasses

**Crypto & secrets**
- Hardcoded API keys, passwords, tokens
- Weak cryptographic algorithms
- Improper key storage
- Cryptographic randomness issues
- Certificate validation bypasses

**Injection & code execution**
- RCE via deserialization
- Pickle/YAML injection
- Eval injection
- XSS (reflected, stored, DOM-based) in web frontends NOT mediated by an auto-escaping framework

**Data exposure**
- Sensitive data in logs or persistent storage
- PII handling violations
- API endpoint data leakage
- Debug information exposure

## Methodology

### Phase 1 — Repository context
Use `read`, `grep`, `glob` to:
- Identify existing security frameworks/libraries in use.
- Look for established secure-coding patterns elsewhere in the repo.
- Examine existing sanitization and validation patterns.

### Phase 2 — Comparative analysis
- Compare the diff against the established patterns above.
- Identify deviations from secure practice.
- Flag code that introduces new attack surfaces.

### Phase 3 — Vulnerability assessment
- Inspect each modified file for security implications.
- Trace data flow from user inputs to sensitive operations.
- Identify privilege boundaries crossed unsafely.

### Phase 4 — False-positive filtering (parallel subagents)
For each candidate finding, spawn a parallel `agent` with `subagent_type: "explore"` and the false-positive filtering rules below as part of the prompt. Each subagent assigns a confidence score 1–10. Drop any finding with confidence < 8.

## Output format

Markdown. One section per finding, in severity order:

```
# Vuln 1: <category>: `path/to/file.go:42`

* Severity: High | Medium | Low
* Description: <what's wrong>
* Exploit scenario: <how an attacker exploits it>
* Recommendation: <concrete fix>
```

Severity guidelines:
- **High** — directly exploitable, leading to RCE, data breach, or auth bypass.
- **Medium** — vulnerabilities requiring specific conditions but with significant impact.
- **Low** — defense-in-depth issues or low-impact vulnerabilities (only report if obvious and concrete).

Confidence scoring (internal — not reported):
- 0.9–1.0: Certain exploit path identified.
- 0.8–0.9: Clear vulnerability pattern with known exploitation methods.
- 0.7–0.8: Suspicious pattern needing specific conditions.
- < 0.7: Do not report.

## False-positive filtering rules (give these to the per-finding subagents verbatim)

> You do not need to run commands to reproduce — read the code. Do not write files. Do not modify settings.
>
> Auto-exclude:
> 1. Denial of Service or resource exhaustion.
> 2. Secrets or credentials on disk if otherwise secured.
> 3. Rate limiting / service overload.
> 4. Memory / CPU exhaustion.
> 5. Lack of input validation on non-security-critical fields without a proven security impact.
> 6. Input-sanitization concerns in GitHub Action workflows unless clearly triggerable via untrusted input.
> 7. Lack of hardening measures — code is not expected to implement every best practice.
> 8. Theoretical race or timing attacks. Only report concretely problematic ones.
> 9. Outdated third-party library vulnerabilities.
> 10. Memory safety issues in memory-safe languages.
> 11. Files that are only unit tests.
> 12. Log spoofing of un-sanitized user input.
> 13. SSRF that only controls the path.
> 14. User-controlled content in AI system prompts.
> 15. Regex injection / regex DOS.
> 16. Findings in documentation files.
> 17. Lack of audit logs.
>
> Precedents:
> 1. Logging high-value secrets in plaintext IS a vulnerability. Logging URLs is assumed safe.
> 2. UUIDs are unguessable; do not require validation.
> 3. Environment variables and CLI flags are trusted in secure environments.
> 4. Resource leaks (memory, file descriptors) are not vulnerabilities.
> 5. React and Angular are XSS-safe except through `dangerouslySetInnerHTML` / `bypassSecurityTrustHtml` / similar.
> 6. Lack of permission checks in client-side JS/TS is not a vulnerability — the backend is responsible.
> 7. Most GitHub Actions vulnerabilities are not exploitable in practice.
>
> Signal-quality criteria:
> 1. Concrete, exploitable vulnerability with a clear attack path?
> 2. Real security risk vs. theoretical best practice?
> 3. Specific code locations and reproduction steps?
> 4. Actionable for a security team?
>
> Confidence score 1–10:
> - 1–3: low confidence, likely false positive
> - 4–6: medium, needs investigation
> - 7–10: high confidence, likely true vulnerability

## Final reminder

Better to miss some theoretical issues than to flood the report with false positives. Each finding should be something a security engineer would confidently raise in PR review.
```

#### 3.4 `simplify/SKILL.md`

Adapted from `ref/src/skills/bundled/simplify.ts`. Substantive
divergences: `agent` (not `Task`) for the three parallel reviewers,
`subagent_type: "explore"` for read-only inspection, and a note about
evva's `# Doing tasks` policy on minimum-complexity (so the agent does
not undo intentional simplicity in pursuit of an "optimization").

```markdown
# simplify Review changed code for reuse, quality, and efficiency, then fix the issues found

Use this skill when the user asks for a clean-up pass on recent changes ("simplify this", "clean up the diff", "/simplify"). The skill spawns three parallel reviewers, then applies their findings.

## Phase 1 — Identify the change set

Run `git diff` (or `git diff HEAD` if there are staged changes) via `bash`. If there are no git changes, fall back to the files the user most recently mentioned or that you edited in this conversation. Cap the working set: if the diff exceeds ~2000 lines, ask the user (via `ask_user_question`) to scope the review to a subdirectory or file list.

## Phase 2 — Launch three reviewers in parallel

Emit three `agent` tool_use blocks in a SINGLE assistant turn with `subagent_type: "explore"`. Pass each agent the full diff so it has complete context. Give each agent the relevant section below verbatim as its prompt.

### Agent 1 — Code reuse review

For each change:

1. **Search for existing utilities and helpers** that could replace newly written code. Look for similar patterns elsewhere — common locations: utility directories, shared packages, files adjacent to the changed ones.
2. **Flag new functions that duplicate existing functionality.** Suggest the existing function.
3. **Flag inline logic that could use an existing utility** — hand-rolled string manipulation, manual path handling, custom environment checks, ad-hoc type guards.

### Agent 2 — Code quality review

Review the same diff for hacky patterns:

1. **Redundant state** — state that duplicates existing state; cached values that could be derived; observers/effects that could be direct calls.
2. **Parameter sprawl** — new parameters added to a function instead of generalizing or restructuring.
3. **Copy-paste with slight variation** — near-duplicate blocks that should be unified.
4. **Leaky abstractions** — exposing internal details that should be encapsulated; breaking existing boundaries.
5. **Stringly-typed code** — raw strings where constants, enums, or branded types already exist.
6. **Unnecessary comments** — comments explaining WHAT (well-named identifiers already do that), narrating the change, or referencing the task/caller. Keep only non-obvious WHY (hidden constraints, subtle invariants, workarounds).
7. **Speculative abstractions** — helpers, generics, or interfaces introduced for hypothetical future requirements. Three similar lines is better than a premature abstraction.

### Agent 3 — Efficiency review

Review the same diff for efficiency:

1. **Unnecessary work** — redundant computations, repeated file reads, duplicate API calls, N+1 patterns.
2. **Missed concurrency** — independent operations run sequentially when they could run in parallel.
3. **Hot-path bloat** — new blocking work added to startup or per-request/per-render hot paths.
4. **Recurring no-op updates** — state/store updates inside polling loops, intervals, or event handlers that fire unconditionally; add a change-detection guard.
5. **Unnecessary existence checks** — pre-checking file/resource existence before operating (TOCTOU). Operate directly and handle the error.
6. **Memory** — unbounded data structures, missing cleanup, listener leaks.
7. **Overly broad operations** — reading entire files when a portion is needed; loading all items when filtering for one.

## Phase 3 — Apply fixes

Wait for all three agents to complete. Aggregate findings. Fix each issue directly using `edit` / `write`. Rules:

- **Respect the diff's intent.** evva's `# Doing tasks` policy explicitly says "three similar lines is better than a premature abstraction" and "don't add features beyond what was asked". If a reviewer's finding asks for an abstraction the change doesn't need, **skip it** — note the skip in the summary.
- If a finding is a false positive or speculative, note it and move on. Do not argue with the finding; do not file an issue about it.
- If two reviewers contradict each other, pick the path that aligns with evva's `# Doing tasks` policy (minimum complexity wins).

## Phase 4 — Summarize

After fixes, run `git diff` again to confirm only the intended changes landed. Briefly summarize: what was fixed, what was deliberately skipped (with reason), and a confidence note on the code's current state.

Do NOT commit the changes. The user runs the `commit` skill (or asks for a commit) when ready.
```

#### 3.5 `setup-hooks/SKILL.md`

This is the **headline new skill for v1.4**. It teaches the model (and
indirectly the user via the model's responses) how to author entries in
evva's `pkg/hooks` settings format. Adapted from
`ref/src/skills/bundled/updateConfig.ts` (the `HOOKS_DOCS` and
`HOOK_VERIFICATION_FLOW` sections specifically), with all paths,
event names, and decision-JSON fields rewritten against
`pkg/hooks/types.go`, `pkg/hooks/loader.go`, and `pkg/hooks/decision.go`.

Critical: **evva's hook engine defines exactly six events** in
`pkg/hooks/types.go` (the `Event` enum: `SessionStart`, `UserPromptSubmit`,
`PreToolUse`, `PostToolUse`, `Stop`, `Notification`). That enum is the
authoritative list — there is **no** "reserved extra events" set anywhere
in `pkg/hooks`. The body must NOT advertise Claude Code's additional
events (`PreCompact`, `PostCompact`, `SessionEnd`, `SubagentStop`,
`PostToolUseFailure`, `PermissionRequest`); evva does not implement them
(the v1.1 hooks doc lists them as out-of-scope). Note: the only thing
`pkg/hooks/payload.go` *reserves* is the set of SessionStart `source`
values (`"resume"`/`"clear"`/`"compact"`, beyond the active `"startup"`)
— those are payload field values, not event types.

```markdown
# setup-hooks Configure lifecycle hooks in evva's settings.json

Use this skill when the user wants something to happen automatically in response to an EVENT ("after writes, run prettier", "before bash, log the command", "when I send a prompt, prepend X"). Automated behaviors require hooks — memory and prompt preferences cannot trigger automated actions; the harness executes hooks, not the agent.

## Where settings live

evva's hook engine reads two files (project hooks fire before user hooks):

| Scope | Path | Git | Use for |
| --- | --- | --- | --- |
| Project | `<workdir>/.evva/settings.json` | commit (team-shared) or gitignore (personal-per-repo) | Team-wide hooks, repo-specific automation |
| User | `<APP_HOME>/settings.json` (typically `~/.evva/settings.json`) | N/A | Cross-project personal hooks |

Read the existing file FIRST before writing. Merge new hook entries with existing ones — never replace the whole file.

## The six events (and only six)

evva supports exactly these events. Do not advertise others.

| Event | When it fires | Receives | Can block? | Can mutate? | Can inject context? |
| --- | --- | --- | --- | --- | --- |
| `SessionStart` | Once, when a session opens | `source`, `model` | No | No (no tool yet) | Yes — `initialUserMessage` or `additionalContext` prepended to the first turn |
| `UserPromptSubmit` | Each time the user submits a prompt | `prompt` | Yes — dropping the prompt with a `reason` | No | Yes — `additionalContext` appended to the prompt |
| `PreToolUse` | Before the permission gate, for every tool call | `tool_name`, `tool_input`, `tool_use_id` | Yes — short-circuits the tool with `block` | Yes — `updatedInput` replaces the args the tool executes with | Yes — folded into the tool result |
| `PostToolUse` | After the tool returns | `tool_name`, `tool_input`, `tool_response`, `is_error` | No (post-hoc) | No | Yes — `additionalContext` appended to the tool result content |
| `Stop` | When the agent reaches a terminal turn (no more tool calls) | `last_assistant_message`, `stop_hook_active` | Yes — re-enters the loop exactly ONCE (the `stop_hook_active` flag guards a second pass) | No | No |
| `Notification` | Out-of-band side channel — iteration limit, approval needed | `message`, `title`, `notification_type` | No (async fire-and-forget) | No | No (stdout ignored) |

## settings.json schema

```json
{
  "hooks": {
    "<EventName>": [
      {
        "matcher": "<tool-name-glob>",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/hook.sh",
            "timeout": 60
          }
        ]
      }
    ]
  }
}
```

Field rules:
- `matcher` is a doublestar glob on the tool name. Empty = match-all. Required for `PreToolUse`/`PostToolUse`; meaningless for the other four events.
- `type` is `"command"` (shell subprocess) or `"http"` (webhook POST).
- For `type: "command"`: `command` is required (passed to `/bin/sh -c`).
- For `type: "http"`: `url` is required. Optional: `method` (default `POST`), `headers`, `async` (default `true` for http, fire-and-forget).
- `timeout` is seconds in `[1, 600]`. `0` or omit = use the event's default.

## The hook payload (stdin for command, body for http)

Every hook receives a JSON envelope:

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "cwd": "/abs/path/to/workdir",
  "permission_mode": "default" | "accept_edits" | "plan" | "bypass",
  "agent_id": "...",
  "agent_type": "main" | "explore" | "plan" | "general-purpose",
  "hook_event_name": "PreToolUse"
}
```

Per-event fields attach on top — PreToolUse adds `tool_name`, `tool_input` (the JSON the LLM emitted), `tool_use_id`. PostToolUse adds `tool_response`, `is_error`. UserPromptSubmit adds `prompt`. Stop adds `last_assistant_message`, `stop_hook_active`.

## The decision JSON (parse stdout, exit 0)

The hook's stdout (when exit 0) is parsed as a JSON decision object:

```json
{
  "continue": true,
  "decision": "approve" | "block" | "",
  "reason": "why",
  "systemMessage": "shown to user",
  "hookSpecificOutput": {
    "permissionDecision": "allow" | "deny" | "ask",
    "permissionDecisionReason": "why",
    "updatedInput": {"command": "echo replaced"},
    "additionalContext": "appended to result / prompt / session start",
    "initialUserMessage": "prepended to the first turn (SessionStart only)"
  }
}
```

Field semantics (cross-reference: `pkg/hooks/decision.go`):
- `continue: false` OR `decision: "block"` → block the operation (PreToolUse / UserPromptSubmit / Stop). On PreToolUse the tool returns an `is_error` result with `reason` as the content.
- `decision: "approve"` → on PreToolUse, allow the tool unconditionally (overrides any pending permission prompt).
- `hookSpecificOutput.permissionDecision` → on PreToolUse, overrides the permission gate's behavior. `"allow"` skips the gate; `"deny"` blocks without asking; `"ask"` forces a prompt even when a rule would auto-allow.
- `hookSpecificOutput.updatedInput` → on PreToolUse, the tool executes with this JSON instead of the LLM's original `tool_input`. Last-write-wins across multiple hooks in the chain.
- `hookSpecificOutput.additionalContext` → text appended to the tool result (PreToolUse / PostToolUse) or the prompt (UserPromptSubmit) or the first turn (SessionStart). Concatenated across hooks.
- `hookSpecificOutput.initialUserMessage` → SessionStart only. Prepended as a synthetic user message at the start of the first turn.

## Exit codes (matter when stdout is not JSON)

- `0` → parse stdout as the decision JSON above. Empty stdout = no opinion.
- `1` → log the error and continue (non-blocking, treated as pass-through).
- `2` → block. Stderr is used as `reason` if no decision JSON is on stdout.
- Timeout → block. The configured timeout (or 60s default) wins.

## Constructing a hook — the verification flow

Don't just write JSON and hope. Follow this flow — each step catches a different failure class. A hook that silently does nothing is worse than no hook.

### Step 1 — Dedup check

Read the target settings file with `read`. If an entry already exists for the same `event + matcher`, show the user the existing command and ask (via `ask_user_question`) whether to keep it, replace it, or add alongside.

### Step 2 — Construct the command for THIS project

The hook receives a JSON payload on stdin. Build a command that:
- Extracts payload fields safely — use `jq -r` into a quoted variable, or `{ read -r f; ... "$f"; }`, NOT unquoted `| xargs` (splits on spaces).
- Invokes the project's actual tool — check `package.json` scripts, `Makefile`, `go.mod`, etc., before assuming `npx` / `bunx` / `npm` / global install.
- Skips inputs the tool doesn't handle — formatters often have `--ignore-unknown`; if not, guard by extension.
- Stays RAW for now — no `|| true`, no stderr suppression. You'll wrap it after pipe-testing.

### Step 3 — Pipe-test the raw command

Synthesize the stdin payload the hook will receive and pipe it through with `bash`:

For `PreToolUse` / `PostToolUse` on `Write` / `Edit`-like tools:
```
echo '{"tool_name":"edit","tool_input":{"file_path":"<a real file from this repo>"}}' | <cmd>
```

For `PreToolUse` / `PostToolUse` on `bash`:
```
echo '{"tool_name":"bash","tool_input":{"command":"ls"}}' | <cmd>
```

For `SessionStart` / `UserPromptSubmit` / `Stop`: most commands don't read stdin, so `echo '{}' | <cmd>` suffices.

Check the exit code AND the side effect (file actually formatted, log line actually written, etc.). If it fails you get a real error — fix it (wrong package manager? tool not installed? jq path wrong?) and retest. Once it works, wrap with `2>/dev/null || true` UNLESS the user wants the hook to block on failure.

### Step 4 — Write the JSON

Merge the new entry into the target file with `edit`. If you're creating `<workdir>/.evva/settings.json` for the first time, ALSO add it to `.gitignore` if the project doesn't already commit `.evva/` — the `write` tool doesn't auto-gitignore. (Project-shared hooks belong in a committed file; personal-per-repo hooks belong in a gitignored one. Ask if unclear.)

### Step 5 — Validate syntax + schema

Run with `bash`:

```
jq -e '.hooks.<EventName>[] | select(.matcher == "<matcher>") | .hooks[] | select(.type == "command") | .command' <target-file>
```

Exit 0 with your command printed = correct. Exit 4 = matcher doesn't match the path. Exit 5 = malformed JSON or wrong nesting. **A broken settings.json silently disables ALL hooks from that file** — fix any pre-existing malformation too, with the user's permission.

### Step 6 — Prove the hook fires (PreToolUse / PostToolUse only)

For `Pre/PostToolUse` on a matcher you can trigger in-turn (`edit` for write-like tools, `bash` for bash):
- For a formatter on `PostToolUse` for `write|edit`: introduce a detectable violation via `edit` (two consecutive blank lines, bad indentation, missing semicolon — something this formatter corrects; NOT trailing whitespace, `edit` strips that before writing), re-read, confirm the hook **fixed** it.
- For anything else: temporarily prefix the command in settings.json with `echo "$(date) hook fired" >> /tmp/evva-hook-check.txt; `, trigger the matching tool (an `edit` for `write|edit`, a harmless `bash` `true` for `bash`), read the sentinel file.

**Always clean up** — revert the violation, strip the sentinel prefix — whether the proof passed or failed.

For `SessionStart`, `UserPromptSubmit`, `Stop`, `Notification`: those fire outside this turn. Skip the proof; trust the pipe-test from step 3 plus the `jq -e` validation in step 5.

### Step 7 — Handoff

Tell the user the hook is live, point them at the target settings file path so they can edit or disable it later, and remind them that:
- Project hooks (`<workdir>/.evva/settings.json`) fire BEFORE user hooks (`<APP_HOME>/settings.json`) in the dispatcher's sequential walk.
- A `continue: false` from an earlier hook short-circuits later hooks in the chain.

## Common patterns

### Auto-format on write

```json
{
  "hooks": {
    "PostToolUse": [{
      "matcher": "write|edit",
      "hooks": [{
        "type": "command",
        "command": "jq -r '.tool_input.file_path' | { read -r f; gofmt -w \"$f\" 2>/dev/null || prettier --write \"$f\" 2>/dev/null; } || true"
      }]
    }]
  }
}
```

### Block destructive bash commands

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "bash",
      "hooks": [{
        "type": "command",
        "command": "jq -e '.tool_input.command | startswith(\"rm -rf\")' >/dev/null && echo '{\"continue\":false,\"reason\":\"rm -rf blocked by project policy\"}' || echo '{}'"
      }]
    }]
  }
}
```

### Log every bash command

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "bash",
      "hooks": [{
        "type": "command",
        "command": "jq -r '\"\\(now) \\(.tool_input.command)\"' >> ~/.evva/bash-log.txt"
      }]
    }]
  }
}
```

### Inject context at session start

```json
{
  "hooks": {
    "SessionStart": [{
      "hooks": [{
        "type": "command",
        "command": "echo '{\"hookSpecificOutput\":{\"additionalContext\":\"This repo deploys via GitHub Actions on tag push. Do not push to main directly.\"}}'"
      }]
    }]
  }
}
```

## Troubleshooting

If a hook isn't firing:
1. Re-read the settings file with `read`. Confirm the JSON is valid.
2. Run the `jq -e` validation from Step 5.
3. Check the matcher matches the tool's wire name. Tool names are lowercase: `bash`, `edit`, `write`, `read`, `grep`, etc. (see `pkg/tools/name.go`).
4. Pipe-test the command in isolation (Step 3) — if it doesn't work in the shell, it won't work as a hook.
5. Settings files are loaded at session start. If you edited settings mid-session, the user needs to restart evva for the new hooks to take effect.

## Reference

- Engine: `pkg/hooks` (loader, dispatcher, runner, decision).
- Wiring & guarantees: `docs/extending.md` → `## Lifecycle hooks`.
```

### Task 4 — Overlay bundled into the disk auto-load

A four-line addition. After `LoadRegistry` returns, call `bundled.Register`
on the same registry. Bundled silently skips any name the user already
has on disk (the `AddBundled` semantic from Task 1).

**File:** `internal/agent/skills.go`.

**Edit `loadDiskSkillRegistry`:**

```go
func loadDiskSkillRegistry(cfg *config.Config) *skill.Registry {
    if cfg == nil {
        return skill.NewRegistry()
    }
    reg, _ := skill.LoadRegistry(cfg.AppHomeSkillsDir, cfg.WorkDirSkillsDir)
    if warns := bundled.Register(reg); len(warns) > 0 {
        // Bundled warnings are appended to the same slice the caller
        // surfaces on the agent logger via reg.Warnings, so a typo in
        // a bundled SKILL.md's title is visible at boot the same way a
        // malformed disk skill is.
        reg.Warnings = append(reg.Warnings, warns...)
    }
    return reg
}
```

Add the import `"github.com/johnny1110/evva/internal/skills/bundled"`.

**Why this seam (and not `agent.New` directly)?** `loadDiskSkillRegistry`
is the **only** place in the codebase that materialises a registry from
the cfg-derived dirs (cross-checked by `grep -n
'skill.LoadRegistry\\|loadDiskSkillRegistry' --include='*.go'`). Both
`Main` (the profile constructor) and `agent.New` (the agent constructor)
funnel through it. One edit reaches every caller, and the bundled
overlay always sits between the disk catalog and the SKILL tool, never
above the host's `WithSkillRegistry` injection.

**Open question (non-blocking):** should hosts that explicitly inject a
registry via `WithSkillRegistry` also get the bundled overlay? **No.** A
host that injects its own catalog has opted out of disk auto-load and
should explicitly opt into bundled too if they want it. To make that
opt-in possible, expose `bundled.Register` as a public re-export (e.g.
`pkg/skill/bundled.go` → `func WithBundled(r *Registry) []string { ... }`)
**only if a host requests it** — defer until then. Document the current
behavior in `docs/extending.md` (Task 6).

### Task 5 — Tests

Place new tests next to the code they cover. The patterns below mirror
the v1.1 doc's testing approach.

**`pkg/skill/registry_test.go` additions** — Task 1's tests, listed
above. Specifically: `TestAddBundled_Insert`,
`TestAddBundled_SkipsExisting`, `TestAddBundled_Validates`,
`TestAddBundled_OverriddenByLoadDir`.

**`internal/skills/bundled/bundled_test.go`** — Task 2's tests, listed
above.

**`internal/agent/skills_test.go` (new file or addition):** integration
test asserting:
- After `loadDiskSkillRegistry(cfg)` with empty cfg dirs, `reg.Names()`
  includes the five tier-1 bundled names.
- Pre-seed `cfg.WorkDirSkillsDir` with a custom `commit/SKILL.md`; after
  load, `reg.Get("commit").Source == SourceWorkDir`, and bundled
  registration was silent (no extra warnings beyond what the loader
  produces normally).

**`internal/agent/sysprompt/main_agent_test.go` addition:** assert the
rendered Main prompt's `# Skills` section names every tier-1 skill
(plus any tier-2 included) when called with the registry's
`SkillRef` flattening. Use a registry where the bundled overlay has
been applied. **Do not** assert exact descriptions — keep the test
robust to wording polish — only assert that each name appears as a list
item.

**`internal/agent/sysprompt/{explore,general,plan}_agent_test.go`
additions:** assert that even when the same registry is threaded into
the PromptContext, the subagent's rendered prompt **does not contain**
the string `"# Skills"`. This pins the `AdvertiseSkills: false`
invariant against accidental regression.

### Task 6 — Docs + version

**`pkg/version/version.go`** — bump `Version` from `"1.1.0"` to `"1.4.0"`.
**Do not** bump to `"1.2.0"` despite v1.2 not being implemented:
versions follow shipping order, not roadmap-phase order, and the CTO's
directive explicitly skips ahead. The next minor (`"1.5.0"`) lands when
OpenAI ships (the original v1.2 work); the one after lands MCP. Tag
references in CHANGELOG.md, README, and docs stay aligned with the
binary version.

**`CHANGELOG.md`** — add a `## [v1.4.0] — Bundled skills` entry under
`## [Unreleased]`. Suggested content (mirror v1.1's structure):

```markdown
## [v1.4.0] — Bundled skills

Ships evva's first batch of first-party Markdown skills. The skill
framework (`pkg/skill`) was complete and Stable since v1.0; this
release fills the empty `# Skills` section every fresh install shipped
with by overlaying five bundled SKILL.md bodies onto the disk catalog.
User disk skills with the same name silently override the bundled body
— bundled is the lowest-precedence tier.

Sequencing: v1.4 ships BEFORE v1.2 (OpenAI provider) and v1.3 (MCP).
v1.4 has no dependency on either; user value beats roadmap order here.
v1.2 and v1.3 remain on deck and will ship next, in the original order.

### Added

- **Bundled skills** — five tier-1 SKILL.md bodies, embedded in the
  binary via go:embed (`internal/skills/bundled`):
  - `commit` — draft and create a git commit for the current diff,
    authored as evva.
  - `review` — review a GitHub pull request (uses `gh`).
  - `security-review` — focused security pass on the branch's pending
    changes, with parallel subagent false-positive filtering.
  - `simplify` — three-reviewer parallel cleanup pass (reuse / quality
    / efficiency) followed by direct fixes.
  - `setup-hooks` — teach the model (and through it, the user) how to
    author `pkg/hooks` entries in `.evva/settings.json` —
    schema, decision JSON, the six events, and a six-step verification
    flow. Completes the v1.1 hooks story.
- **`skill.SourceBundled`** — new `SkillSource` constant.
- **`skill.Registry.AddBundled`** — inserts a skill at `SourceBundled`
  tier, silently skipping any name already present in the registry
  (user disk override wins without a warning).

### Changed

- `internal/agent/skills.go:loadDiskSkillRegistry` now overlays the
  bundled catalog onto the disk-loaded registry. Hosts that inject
  their own registry via `agent.WithSkillRegistry` are unaffected and
  still skip bundled.
```

**`docs/sdk-stability.md`** — the current `pkg/skill` row (line 27) reads:

> | `pkg/skill` | `Registry`, `SkillMeta`, `LoadRegistry`, `NewRegistry`, `Registry.Add`, `SkillTool`. Skill SDK landed in the Phase 19 "Out of scope" sweep. |

Replace it to add Task 1's three new Stable symbols:

> | `pkg/skill` | `Registry`, `SkillMeta`, `SkillSource` constants (`SourceHome`, `SourceWorkDir`, `SourceProgrammatic`, `SourceBundled`), `LoadRegistry`, `NewRegistry`, `Registry.Add`, `Registry.AddBundled`, `ParseTitleLine`, `SkillTool`. Skill SDK landed in the Phase 19 "Out of scope" sweep; v1.4 added `SourceBundled` + `AddBundled` (evva's bundled-content channel) and exported `ParseTitleLine` (shared title parser for the disk + bundled loaders). |

**`docs/extending.md`** — add a paragraph at the end of the `### Custom
skills (Skill SDK)` section (heading at line 256; the section runs through
the `# Skills` paragraph near line 304):

> evva ships its own bundled SKILL.md catalog (the five tier-1 skills
> listed in `CHANGELOG.md` under v1.4.0), overlaid onto the disk
> catalog automatically by the one-call `agent.New`. A user disk skill
> with the same name silently overrides the bundled body — bundled is
> the lowest-precedence tier (`skill.SourceBundled`). Hosts that
> construct their agent through `agent.NewWithProfile` + an
> explicit `WithSkillRegistry` do NOT pick up the bundled catalog; if
> you want it, build your own programmatic catalog (the SDK pattern
> above) rather than reaching into evva's private bundled package.

**`docs/user-guide/en/user-guide.md`** + **`docs/user-guide/zh-tw/user-guide.md`**:
add a section titled `## Bundled skills` (zh-tw: `## 內建技能`) listing
the five tier-1 skills with their one-line descriptions and a brief
explainer that the model invokes them automatically when appropriate,
and the user can invoke any of them by typing `/<name>` (the slash
shorthand the model already understands per
`internal/agent/sysprompt/fragments.go:190`). Cross-link to
`docs/extending.md#custom-skills-skill-sdk` for authoring custom skills
or overriding bundled bodies.

**`CLAUDE.md`** — Task 0 covered the roadmap reorder paragraph. No
other CLAUDE.md changes needed.

---

## 5. Design decisions & risks (read before coding)

- **Bundled is the LOWEST precedence tier, not the highest.** This is
  the inverse of `SourceProgrammatic` (which represents an explicit
  host choice). Disk skills (Home, WorkDir) and host-injected
  programmatic skills override bundled bodies silently — overriding a
  bundled is the documented extension point, so shadowing it must not
  produce a Warning. `AddBundled`'s skip-if-present is what enforces
  this; `Registry.Add`'s reject-if-present is what prevents accidental
  shadowing of intentional choices.
- **Bundles are private (`internal/skills/bundled`), not part of the
  SDK surface.** Two reasons: the content is evva's product, not an
  extension primitive; downstream SDK consumers who want to ship their
  own SKILL.md catalogs use the existing `skill.NewRegistry()+Add(...)`
  path. If a downstream host genuinely wants to opt INTO evva's
  bundled catalog from a custom registry, we'll expose a thin
  `pkg/skill/bundled.go` wrapper in a future minor — defer until
  requested (see Task 4 "open question").
- **Subagents stay skill-free.** `ExploreAgent`, `GeneralAgent`, and
  `PlanAgent` all carry `AdvertiseSkills: false`. v1.4 does not change
  this. A subagent's context budget should go to its narrow task, not
  to a catalog. The integration test in Task 5 pins this against
  regression.
- **Bodies are lazy.** Every bundled `BodyFunc` is a closure over a
  captured string read from `embed.FS` at `Register` time. The body is
  fetched only when `Registry.LoadBody(name)` is called (i.e. when the
  model dispatches `SKILL`). Prompt assembly never touches `BodyFunc`.
  A1's "zero-cost when not invoked" property is enforced by passing a
  panicking `BodyFunc` in a test and asserting startup still succeeds.
  (Hot-path matters: the prompt is rebuilt on `/profile` switches and
  on every `SwitchProfile` call.)
- **The skill loader is single-pass at boot.** There is no hot-reload
  of bundled or disk skills mid-session. If a user edits
  `<workdir>/.evva/skills/foo/SKILL.md`, they need to restart evva.
  This is consistent with hooks (`pkg/hooks/loader.go`) and permissions
  (`pkg/permission/store.go`) — no v1.4 changes here. Document under
  "Troubleshooting" in the user-guide section.
- **No fork of the SKILL tool.** The `SKILL` tool's signature, schema,
  description, and dispatch behavior do not change. The only thing
  changing is the *content* of `ToolState.SkillRegistry()` for a
  default boot.
- **Authorship policy carries through.** The `commit` skill's
  `--author="evva <frizoevva@gmail.com>"` aligns with
  `coreRulesSection` in `internal/agent/sysprompt/fragments.go:48`. If
  evva's authorship policy changes (e.g. switches to a
  `Co-Authored-By:` trailer), both sites must move in lockstep. There
  is no programmatic guard for this today — the integration test in
  Task 5 only checks that the literal string `--author=` appears in
  the commit skill body. Consider a CI grep against both files as a
  future hardening if drift becomes a real concern.
- **Shell quoting in SKILL.md bodies is literal — no Go-string or
  Markdown escape layer.** The implementer copies the bytes between
  the doc's code fences into the .md file as-is; go:embed reads them
  byte-for-byte; the LLM sees them raw (no Markdown rendering, no
  pre-processing). So every `"` in a SKILL.md shell example must be a
  literal `"` — NOT `\"` (which would land in the file as
  backslash-quote and either confuse the model or pass through to the
  shell as a malformed command). The §3.1 commit body uses bare `"`
  inside the heredoc invocation for this exact reason — the closest
  analog in ref TS (`ref/src/commands/commit.ts:48-52`) wraps the
  example in a TypeScript template literal delimited by backticks, so
  it also writes bare `"` even though the surrounding TS code
  routinely escapes quotes. Manual verification of the `commit` skill
  end-to-end (see §7's manual checklist) is the safety net: stage a
  trivial change, run `/commit`, confirm the model emits a
  syntactically valid `git commit --author="..." -m "$(cat <<'EOF'
  ... EOF)"` invocation that the shell accepts on the first try. JSON
  bodies in `setup-hooks/SKILL.md` are the one exception — they
  legitimately need `\"` (single backslash + quote) for inner JSON
  string escapes; those are documented and pin-tested by `TestRegister_AllBodiesLoadable`
  loading every body without error.
- **Security review's hard-exclusions list is opinionated.** The list
  is copied verbatim from `ref/src/commands/security-review.ts`
  because the policy was designed by Anthropic's security team and is
  the same one Claude Code ships. Reviewers may push back on specific
  items (e.g. "we DO want DOS findings here"); if accepted, both the
  bundled SKILL.md and any related docs must change. For v1.4 keep it
  verbatim — divergence is a follow-up PR's call.
- **`setup-hooks` is a documentation surface.** Any change to
  `pkg/hooks/types.go`'s event set, `pkg/hooks/decision.go`'s field
  set, or `pkg/hooks/loader.go`'s settings.json shape **must** update
  `setup-hooks/SKILL.md` in lockstep. The skill body cites these
  packages by path so future contributors notice the link; consider a
  CI test that greps the skill body for `pkg/hooks` and at least one
  occurrence of every `EventXxx` constant.

---

## 6. Out of scope for v1.4

Listed so contributors don't propose them as phase additions.

- **Tier-2 skills** beyond the five listed. The package layout makes
  adding `debug`, `remember`, `loop`, `skillify`, `batch`, etc. trivial
  — append to `bundledNames` and drop a SKILL.md under `content/`. The
  implementing agent MAY include them if scope allows; treat each as an
  additive task with its own integration test. **Recommended next set:**
  - `debug` — port adapted from `ref/src/skills/bundled/debug.ts`.
    Reads evva's per-agent log files (`cfg.LogDir`, see
    `pkg/config/config.go`); explains how to enable debug logging
    mid-session (the user toggles `LogLevel` via `/config` or a
    settings.json edit). Skip the "tail the last N lines" auto-prefix
    behavior — evva's logger format differs from Claude Code's.
  - `remember` — port adapted from `ref/src/skills/bundled/remember.ts`.
    Reviews evva's memory layers: workdir `EVVA.md` (project rules),
    `<APP_HOME>/USER_PROFILE.md` (cross-project preferences), and the
    per-project `<APP_HOME>/projects/<repo-slug>/MEMORY.md` (auto
    memory). Proposes promotions/cleanups without applying — calls
    `update_user_profile` / `update_project_memory` only after the user
    approves each proposal.
  - `loop` — port adapted from `ref/src/skills/bundled/loop.ts`. Uses
    evva's `cron_create` tool (`pkg/tools/cron`) instead of Claude
    Code's `CronCreateTool`. Default interval `10m`. The tier-1
    `setup-hooks` skill does NOT cover periodic prompts — that is
    `loop`'s territory.

  None of these have new infrastructure requirements; they're pure
  SKILL.md additions.

- **Skill arguments contract.** evva's `SKILL` tool already accepts an
  optional `args` string parameter (the `Args` field at `pkg/skill/skill.go:67`, appended to the body at `:107`) and
  appends it at the end of the returned body as `\\n\\nargs: <value>`.
  This is sufficient for the tier-1 skills' needs. Argument templating
  (`{{args}}` interpolation, multi-arg parsing) and a typed
  `argument-hint` frontmatter field — present in
  `ref/src/skills/bundled/loop.ts` and `bundledSkills.ts` — are NOT in
  v1.4.

- **`isEnabled` gating** (skills hidden behind a feature flag, like
  `ref/src/skills/bundled/loop.ts:isKairosCronEnabled`). evva's tier-1
  set is always-on. If a future bundled skill needs gating (e.g. a
  remote-only skill), add an `IsEnabled func() bool` field to the
  bundled meta and let `Register` filter — but defer the SDK addition
  to `pkg/skill` until a real need surfaces.

- **`disableModelInvocation`** (a skill the user can run but the model
  cannot auto-invoke; `ref/src/skills/bundled/debug.ts` sets this).
  evva's `skillsSection` advertises every skill in the registry; there
  is no current distinction between "user-invocable only" and "model-
  invocable too". Adding the distinction means a new `SkillMeta` field
  plus a filter on the prompt rendering path. Defer.

- **Bundled skill `files` (extra reference docs)**. Some ref skills
  (`verify`, `claude-api`) embed additional files alongside SKILL.md so
  the model can `Read` them from disk during execution. evva's v1.4
  bundled skills are single-file by design — if a skill body grows past
  ~600 lines, that's the signal to split it into per-step subagent
  prompts, not to attach reference files. Defer the multi-file pattern
  until v1.5+ or until a contributor justifies it.

- **A `/skills` slash-command picker** (a TUI panel that lists every
  registered skill with its source). The data is already there
  (`Registry.List()` returns `Source`), but the UI surface is out of
  scope. The user can already invoke any skill via `/<name>`.

- **Hot-reload of bundled or disk skills.** A user editing a SKILL.md
  mid-session needs to restart evva, same as for permissions and
  hooks. Adding fsnotify would touch `pkg/skill`, the agent boot
  path, and the runtime registry mutex — all out of v1.4 scope.

- **Bundled MCP skills** (`ref/src/skills/mcpSkillBuilders.ts`). MCP
  arrives in v1.3. Bundled skills that wrap MCP tools wait for that
  phase to land, then can be added in v1.6+ following the same
  pattern as v1.4.

---

## 7. Verification checklist (PR gate)

- [ ] **Task 0** — `CLAUDE.md` roadmap section carries the v1.4-before-v1.2/v1.3 ordering note.
- [ ] **Task 1** — `pkg/skill` exports `SourceBundled`,
      `Registry.AddBundled`, and `ParseTitleLine`. The disk loader's
      `parseFirstLine` is refactored to call `ParseTitleLine` and the
      folder-name mismatch warning still fires
      (`TestParseFirstLine_StillWarnsOnFolderMismatch`). Existing tests
      still pass. New tests cover insert, skip-if-existing, validation,
      the loader-wins-on-cross-source-add case, and every accepted /
      rejected title shape via `TestParseTitleLine`.
- [ ] **Task 2** — `internal/skills/bundled` compiles. Every entry in
      `bundledNames` has a matching `content/<name>/SKILL.md`. Unit
      tests assert all bodies parse via `skill.ParseTitleLine`, the
      title-vs-name match holds (no drift between embedded title and
      `bundledNames` entry), and the package is nil-safe.
- [ ] **Task 3** — All five tier-1 SKILL.md files land verbatim per the
      bodies above, including the format invariants (first line `# <name>
      <desc>`, evva tool names, no Claude Code paths). Spot-check each
      file against §3 invariants:
      - [ ] No `~/.claude/` or `.claude/` references anywhere.
      - [ ] No `Task`, `Bash`, `Edit`, `Write`, `Read`, `Grep`, `Glob`,
            `WebSearch`, `WebFetch`, `Plan`, `EnterPlanMode`,
            `ExitPlanMode` as tool names (lowercase evva forms only).
      - [ ] `commit/SKILL.md` references `--author="evva <frizoevva@gmail.com>"`.
      - [ ] `setup-hooks/SKILL.md` lists exactly the six evva events
            (no `PreCompact`/`PostCompact`/`SessionEnd`/`PermissionRequest`/`PostToolUseFailure`).
- [ ] **Task 4** — `loadDiskSkillRegistry` calls `bundled.Register` and
      surfaces its warnings on `reg.Warnings`. No new public API in
      `internal/agent`.
- [ ] **Task 5** — `go test ./...` green; `go vet ./...` clean.
      Subagent prompts contain no `# Skills` block (regression pin).
      Main prompt with a fresh registry lists every tier-1 skill.
      `TestRegister_PromptPathDoesNotCallBodyFunc` is present and
      passes — pins acceptance criterion **A5** (a panicking
      `BodyFunc` survives every prompt-time call path; only
      `LoadBody` invokes it).
- [ ] **Task 6** — `pkg/version.Version = "1.4.0"`. `CHANGELOG.md` has
      a `## [v1.4.0]` entry. `docs/sdk-stability.md` `pkg/skill` row
      mentions `SourceBundled` and `AddBundled`. `docs/extending.md`
      has the bundled-overlay paragraph. `docs/user-guide/en` and
      `docs/user-guide/zh-tw` both have a `## Bundled skills` section.
- [ ] **Manual (needs a TTY — flag for a human):**
      1. Run `evva` with no disk skills, type `/commit` after staging
         a change. Confirm the bundled body fires.
      2. **Watch the assistant's `bash` invocation carefully** — the
         emitted `git commit --author="..." -m "$(cat <<'EOF'...EOF)"`
         must be syntactically valid shell. The shell must accept it
         on the first try with no quote-escape or heredoc errors. This
         is the manual safety net §5 calls out for the
         "Shell quoting in SKILL.md bodies" risk: if the model emits
         literal `\"` characters in the command, the SKILL.md bytes
         leaked an unwanted backslash escape and the body must be
         re-checked. Confirm the resulting commit's author shows up
         as `evva <frizoevva@gmail.com>` in `git log -1 --format="%an <%ae>"`.
      3. Author `<workdir>/.evva/skills/commit/SKILL.md` with a one-
         line override, restart, run `/commit`, confirm the override
         wins; remove the override file, restart, confirm bundled is
         back.
      4. Repeat for `/setup-hooks`: invoke once with no args
         (confirm the schema + verification flow renders); invoke once
         with an `args` describing a real hook the user wants
         (e.g. "format Go files with gofmt on write"), confirm the
         model walks the 6-step verification flow and produces a
         syntactically valid `.evva/settings.json` entry that
         `jq -e .hooks` accepts.

---

## 8. Verification change log (2026-05-25, branch `feature/v1.4`)

This plan was first drafted against an earlier sandbox (it referenced
`/mnt/evva`). Before marking it ready-to-build, every load-bearing claim
was re-checked against the live working tree.

**Confirmed accurate (no change needed):** the `pkg/skill` surface and
line numbers (`registry.go`: `SkillMeta`/`Source*`@41-46/`Add`@100/
`LoadRegistry`@128/`NewRegistry`@86/`Get`@236/`List`@246/`Names`@260/
`LoadBody`@273/`parseFirstLine`@180/`Warnings` field@77; `skill.go`:
`Lookup`@24/`NewSkill`@37/framing string@105); the wiring sites
(`skills.go:20/35`, `agent.go:310-319`, `toolset/builtins.go:100`,
`profiles.go:127`, `options.go:99`, `fragments.go:48` author line /
`:190` slash hint / `:208` skillsSection; `agent_def.go` `AdvertiseSkills`
Main=true & Explore/General/Plan=false); the six-event `pkg/hooks` surface
and decision-JSON shape; the tool wire-names (`pkg/tools/name.go`) and
subagent kinds (`toolnames.go`); `cfg.AppHomeSkillsDir`/`cfg.WorkDirSkillsDir`;
`Version == "1.1.0"` (so the bump to `1.4.0` is correct);
`ref/src/commands/{commit,review,security-review}.ts` and
`ref/src/skills/bundled/simplify.ts` all exist; author policy
`--author="evva <frizoevva@gmail.com>"` (A7).

**Corrections folded in:**

| # | Where | Was | Now (verified) |
| --- | --- | --- | --- |
| 1 | §1 premise | `find /mnt/evva …` returns nothing | `git ls-files '*/SKILL.md'` returns nothing; clarified the gitignored `.evva/` local `EVVA_HOME` artifact (`.gitignore:37`) vs shipped/embedded content. Premise (empty `# Skills` on fresh install) still holds. |
| 2 | §2.2 wiring table | `spawn.go:80-87` passes `WithSkillRegistry` **and** `WithHookRegistry` | `spawn.go:86` passes `WithHookRegistry` **only**; subagents have no `skill` tool and `AdvertiseSkills:false`, so the catalog is never surfaced. |
| 3 | §3.5 preamble | extra events "reserved in `pkg/hooks/payload.go`" | No reserved-events set exists; the `Event` enum in `pkg/hooks/types.go` is authoritative (six events). `payload.go` only reserves SessionStart `source` values. |
| 4 | `setup-hooks` event table + payload prose | Stop receives `last_message` | `last_assistant_message` (`pkg/hooks/payload.go:61`). |
| 5 | `setup-hooks` event table | Notification receives `ntype` | `notification_type` (JSON tag; Go field is `NType`). |
| 6 | `setup-hooks` payload JSON | `permission_mode: … "acceptEdits" \| "dontAsk"` | `"default" \| "accept_edits" \| "plan" \| "bypass"` (`pkg/permission/types.go:82-87`). |
| 7 | `setup-hooks` payload JSON | `agent_type: … "general"` | `"general-purpose"` (`toolnames.go:60`). |
| 8 | `setup-hooks` auto-format pattern | `jq -r '.tool_response.filePath // .tool_input.file_path'` | `jq -r '.tool_input.file_path'` — evva's `tool_response` is a plain string (`payload.go:49`), not an object, so `.filePath` was inert; `file_path` is the confirmed `edit`/`write` input key (`pkg/tools/fs`). |
| 9 | §6 args contract | `pkg/skill/skill.go:65` | `Args` field at `:67`, appended at `:107`. |
| 10 | Task 6 `sdk-stability.md` snippet | mis-quoted the existing row | quoted the real row (line 27) and added `ParseTitleLine` to the proposed Stable surface. |
| 11 | Task 6 `extending.md` ref | "~ line 304" | heading at line 256 (section runs to ~304). |

Nothing in the work breakdown's *shape* changed — Tasks 0–6 and the five
tier-1 bodies stand. The corrections are factual-fidelity fixes,
concentrated (by design) in the `setup-hooks` body, which §5 flags as a
"documentation surface" that must track `pkg/hooks` exactly.
