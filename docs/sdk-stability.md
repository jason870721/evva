# evva SDK stability

This page declares the stability contract for every `pkg/` package.
Downstream apps consuming evva should read this once, decide which tier
matches their tolerance for churn, and pin accordingly.

The contract follows Go's [semver](https://semver.org/) discipline,
with each package landing in one of three tiers.

## Tiers

### Stable

The surface promise: **breaking changes require a major version bump**.
Field renames, removed exported symbols, signature changes — all
require `v2.0.0` (or later major) at the earliest. Bug fixes,
performance improvements, and additive changes (new exported symbols,
new options) land in minor versions.

| Package | Why it's stable |
| --- | --- |
| `pkg/agent` | The agent constructor and Agent interface — every downstream app touches this. |
| `pkg/config` | `Config`, `Load`, `LoadOptions`, `APIConfig`, the setter helpers. |
| `pkg/event` | `Event`, `Kind` constants, payload structs, `Sink` interface. |
| `pkg/tools` | `Tool`, `Result`, `ToolName` constants, `State` interface. |
| `pkg/llm` | `Client`, `Message`, `Response`, `Option`, `Registry`, `ClientFactory`. |
| `pkg/skill` | `Registry`, `SkillMeta`, `LoadRegistry`, `NewRegistry`, `Registry.Add`, `SkillTool`. Skill SDK landed in the Phase 19 "Out of scope" sweep. |
| `pkg/constant` | `LLMProvider`, `Model` constants, `MODEL_CONTEXT_SIZE`. |
| `pkg/version` | `Version` constant, `BuildStamp`, `String()`. |

A `Kind` constant or payload struct field appearing in `pkg/event` is
load-bearing for every consumer's render layer; we won't rename either
without a major bump. New `Kind`s can land in minor versions
(consumers default-handle unknown kinds; no compile-time impact).

### Experimental

The surface promise: **may break in minor versions; will be documented
in CHANGELOG.md when it does**. Use these packages, but pin to a
specific minor version and watch the changelog before upgrading.

| Package | Why it's experimental |
| --- | --- |
| `pkg/ui` | The Controller surface still returns a couple of internal types in places (`Session()`, `ToolState()`); v1.0 may rework that. |
| `pkg/toolset` | `Registry.Build` signature may grow options. `TagsFor` / `HintFor` are internal-helper-shaped today. |
| `pkg/observable` | The Store / Change framework is the right shape for evva but might tighten its semantics around concurrent emitters. |
| `pkg/tools/kits` | Phase 19d ships four kits (GeneralPurpose / ReadOnly / Coding / Research); the exact membership of each kit may grow as new tool families land. The named-kit pattern itself is stable. |

### Internal helper

The surface promise: **none — these exist because some other public
package needed them, but they aren't part of the SDK contract**. Treat
them as if they were `internal/`.

| Package | What it carries |
| --- | --- |
| `pkg/common` | Misc helpers (UUID, truncate). Use only if you absolutely need; consider duplicating into your own utility package. |
| `pkg/banner` | The "evva online" startup banner; cosmetic only. |
| `pkg/llm/builtins` | Blank-import-only side-effect for provider registration. Safe to import, but `pkg/llm/builtins` itself has no exported symbols you should call. |

## Versioning & deprecations

- The Version constant lives at `pkg/version.Version`. Bumped on every
  tagged release.
- Deprecations appear as `// Deprecated: <reason>; <replacement>; will
  be removed in <phase>` on the symbol's godoc. `go doc` and editors
  surface the warning so consumers see it without reading source.
- No deprecation queue currently — pre-1.0 evva is still in dev mode,
  so the Phase 19 cleanup collapsed every parallel API into its
  canonical form in a single release. See `CHANGELOG.md` under the
  "Breaking" / "Removed" headings for the surface changes.

## How to depend on evva

1. **Pick a major version** that matches your stability appetite. Pre-1.0
   evva carries `0.x.y` versions with breaking changes possible in
   minor bumps; once Phase 19f cuts `v1.0.0`, the stable-tier promise
   above kicks in.
2. **Pin in `go.mod`** to a specific tag rather than `latest`:
   ```
   require github.com/johnny1110/evva v0.2.4-alpha.1
   ```
3. **Read CHANGELOG.md** before upgrading minor versions. Anything in
   the Experimental tier may have changed.
4. **Check `pkg/version.String()` at runtime** for runtime assertions
   (e.g. compatibility shim in your own code).

## Filing an issue

If a symbol in the **Stable** tier needs to break (security fix,
unsalvageable design flaw), file an issue. A breaking change should
land in the next major version with a documented migration path.

A symbol you depend on landed in the **Experimental** tier and you'd
like it promoted? Also file an issue — usage data is what drives
tier promotions.
