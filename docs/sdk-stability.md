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
| `pkg/config` | `Config`, `Load`, `LoadOptions`, `APIConfig`, the setter/getter helpers (the `config` tool added `Get*` reads in the v1.5 ConfigTool work). |
| `pkg/event` | `Event`, `Kind` constants, payload structs, `Sink` interface. |
| `pkg/tools` | `Tool`, `Result`, `ToolName` constants, `State` interface. |
| `pkg/llm` | `Client`, `Message`, `Response`, `Option`, `Registry`, `ClientFactory`. |
| `pkg/skill` | `Registry`, `SkillMeta`, `SkillSource` constants (`SourceHome`, `SourceWorkDir`, `SourceProgrammatic`, `SourceBundled`), `LoadRegistry`, `NewRegistry`, `Registry.Add`, `Registry.AddBundled`, `ParseTitleLine`, `SkillTool`. Skill SDK landed in the Phase 19 "Out of scope" sweep; v1.4 added `SourceBundled` + `AddBundled` (evva's bundled-content channel) and exported `ParseTitleLine`. |
| `pkg/constant` | `LLMProvider`, `Model` constants, `MODEL_CONTEXT_SIZE`. |
| `pkg/version` | `Version` constant, `BuildStamp`, `String()`. |
| `pkg/ui` | `UI`, `Controller`, `Skill`, `ProfileChoice`, the read-model accessors, and the `PermissionDecision` / `QuestionResponse` payloads. v2.1 removed the internal-type leaks (`Session()` / `ToolState()` → public read-models); v2.5 rebuilt the bundled TUI on this contract, proving it self-sufficient. |
| `pkg/permission` | `Store`, `Rule`, `Mode`, `Decision`, `ApprovalRequest`, `Broker`, `Load`, `NewBroker`, `SetOnRequest`, `ParseMode`, the `Behavior*` / `Source*` constants. Promoted out of `internal/` in SDK v2.2. |
| `pkg/toolset` | `Registry`, `ToolFactory`, `DefaultRegistry`, `Describe`, `Build`. The custom-tool registration seam every downstream tool author touches. |

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
| `pkg/ui/bubbletea` | The bundled reference TUI. Satisfies the Stable `pkg/ui` contract, but its component/layout internals churn freely — depend on `pkg/ui` if you want a stable surface, embed `pkg/ui/bubbletea` if you want evva's batteries-included terminal UI and can tolerate visual/layout changes in minor versions. |
| `pkg/tools/lsp` | Language Server Protocol integration (the deferred `lsp_request` tool). The tool name is stable; the package's exported manager/protocol surface may evolve as more LSP features land. |
| `pkg/observable` | The Store / Change framework is the right shape for evva but might tighten its semantics around concurrent emitters. |
| `pkg/tools/kits` | Phase 19d ships four kits (GeneralPurpose / ReadOnly / Coding / Research); the exact membership of each kit may grow as new tool families land. The named-kit pattern itself is stable. |
| `pkg/hooks` | Lifecycle hook engine (SessionStart / UserPromptSubmit / PreToolUse / PostToolUse / Stop / Notification). The event set and payload shapes follow Claude Code's settings-file contract; the Go surface (`Registry`, `Dispatcher`, `BasePayload`, `WithHookRegistry`) may flex as downstream consumers exercise it. |
| `pkg/mcp` | MCP client (Model Context Protocol) — `Manager`, `ServerConfig`, `Open`/`Load`, the OAuth bridge, and result conversion. Wraps the official `modelcontextprotocol/go-sdk` for the protocol layer. Surface may flex as downstream usage exercises edge cases (transport quirks, OAuth token persistence, result-type expansion). |

### Internal helper

The surface promise: **none — these exist because some other public
package needed them, but they aren't part of the SDK contract**. Treat
them as if they were `internal/`.

| Package | What it carries |
| --- | --- |
| `pkg/common` | Misc helpers (UUID, truncate). Use only if you absolutely need; consider duplicating into your own utility package. |
| `pkg/banner` | The "evva online" startup banner; cosmetic only. |
| `pkg/llm/builtins` | Blank-import-only side-effect for provider registration. Safe to import, but `pkg/llm/builtins` itself has no exported symbols you should call. |
| `pkg/update` | evva's GitHub-release self-update (`evva update`). Product glue, not an SDK primitive — promoted out of `internal/` in v2.5 only so the bundled TUI's update overlay could live under `pkg/`. A downstream app ships its own update mechanism. |

## Versioning & deprecations

- The Version constant lives at `pkg/version.Version`. Bumped on every
  tagged release.
- Deprecations appear as `// Deprecated: <reason>; <replacement>; will
  be removed in <phase>` on the symbol's godoc. `go doc` and editors
  surface the warning so consumers see it without reading source.
- No deprecation queue at the `v1.0.0` cut — the SDK v2 arc collapsed
  every parallel API into its canonical form (converged constructor,
  public permission / persona / read-model surfaces, pkg-only host)
  before tagging. See `CHANGELOG.md` under the "Breaking" / "Removed"
  headings for the surface changes folded into `v1.0.0`.

## How to depend on evva

1. **Pick a major version** that matches your stability appetite. With
   `v1.0.0` cut, the Stable-tier promise above is in force — breaking
   changes to Stable packages require a `v2.0.0`. Experimental packages
   may still change in minor versions (watch the changelog).
2. **Pin in `go.mod`** to a specific tag rather than `latest`:
   ```
   require github.com/johnny1110/evva v1.0.0
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
