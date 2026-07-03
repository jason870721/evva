# evva — Conventions & Release Workflow

This file is evva's working agreement: **coding conventions and the release workflow**. It is
loaded as project instructions each session — keep it operational.

`evva` is a ReAct coding agent for the terminal, written in Go: one narrow `llm.Client`
interface across providers (Anthropic, DeepSeek, GLM, OpenAI, Ollama), one `tools.Tool` interface,
one observable store fanning state to any UI, one agent loop. The unifying idea is **one
runtime, many personas, swappable UI**.

> **Full vision and architecture — the `pkg/` SDK surface, `internal/` packages, the
> agent-definition layout, and the key boundaries — live in
> [docs/architecture.md](docs/architecture.md). Read it before changing package structure.**
> Contributor onboarding (build/test/run, PR flow) is in [CONTRIBUTING.md](CONTRIBUTING.md).
> User-facing docs live in [README.md](README.md) and [docs/](docs/README.md).
>
> **Twin file:** [EVVA.md](EVVA.md) holds these same rules for evva — Claude Code reads this
> file, evva reads that one. Keep the two in sync.

The reference TypeScript source lives at `ref/src/`. Treat it as the source of truth for tool
descriptions, harness structure, and agent definitions — port from it, don't reinvent.

---

## Project conventions

- **Public vs. private:** Reusable abstractions live in `pkg/`. evva-runtime-specific implementations live in `internal/`. If a package is useful to downstream embedders, it belongs in `pkg/`.
- **One package per tool family.** Examples: `pkg/tools/fs/`, `pkg/tools/shell/`, `internal/tools/meta/`. A new tool either goes in an existing family or starts a new family package.
- **One package per LLM provider.** `pkg/llm/claude/`, `pkg/llm/deepseek/`, `pkg/llm/ollama/`. Each implements the `llm.Client` interface from `pkg/llm/`. New providers register via `init()` in `pkg/llm/builtins/`.
- **Tests live next to the code they cover** (`*_test.go`). No parallel `tests/` tree.
- **No comments that restate the code.** Only comment WHY when the WHY is non-obvious.
- **Port tool descriptions from `ref/src/tools/*/prompt.ts` verbatim** when reasonable. Diverge only with a clear reason.
- **Minimize external dependencies.** Non-stdlib dependencies: `golang.org/x/sync` (singleflight), the Bubble Tea TUI stack, and `github.com/modelcontextprotocol/go-sdk` (MCP client, added in v1.3.0). Protocol implementations (JSON-RPC, LSP types) are hand-written to avoid dependency chains.

---

## Release workflow

### Branch strategy

Each release branch owns exactly one tag tier: **`main` ships stable tags, `pre-release` ships beta tags.** There is no alpha tier.

```
main  ← production (stable tags only: vX.Y.Z; GitHub "Latest")
  ↑ promote (command: release)        ← hotfix/* cut from main (command: hotfix release)
pre-release  ← staging (beta tags only: vX.Y.Z-beta.N; GitHub pre-release)
  ↑ ship (commands: pre-release feature / hotfix pre-release)
dev  ← integration
  ↑ feature PR, squash/merge after review
feature/*  ← topic branches (cut from dev)
```

This is the seam the `evva update` command rides on: `evva update` resolves GitHub's **Latest** release — the newest stable on `main` — while `evva update v<X>.<Y>.<Z>-beta.<N>` pins to a beta published from `pre-release` (see `pkg/update`). `go install ...@latest` ignores `-beta.N` tags entirely — only stable tags move `@latest`.

**Backflow rule:** after EVERY tag (beta or stable), merge the tagged branch back into `dev` and push. This keeps `pkg/version/version.go` and `CHANGELOG.md` converged across all three branches. Skipping it is exactly why dev's version constant once drifted four releases behind and why every dev → pre-release merge used to hit a CHANGELOG conflict.

### Daily development

1. Branch off `dev`: `git checkout -b feature/<ticket-or-name>` (e.g. `feature/RP-15`, `feature/bundle-skill`).
2. Commit with conventional prefixes: `feat`, `fix`, `chore`, `docs`, `refactor`, `test`.
3. Push to GitHub, open a PR targeting `dev`, merge after review.

### Version numbering

`vX.Y.Z[-beta.N]`:

| Component | Rule |
|---|---|
| **X** (major) | Breaking change to the `pkg/` SDK surface or CLI/config behavior, or a direction-level milestone (v0 → v1). Deliberate and rare. |
| **Y** (minor) | **One roadmap wave = one minor.** A wave claims its minor in its planning doc at planning time; the first release containing that wave's work bumps Y (Z=0). |
| **Z** (patch) | Within-wave increments after the wave's debut: fixes, docs, small follow-ups. |
| **-beta.N** | Only on `pre-release`. N counts cuts of the SAME target base version, starting at 1; it resets when the base changes. A stable tag is ALWAYS a verbatim promotion of the last beta of that base. |

One-line litmus: **does the release contain work from a new roadmap wave → bump Y; otherwise → bump Z.**

**Base-version decision** (run top-down when cutting a new beta):

1. Contains work from a roadmap wave that has never shipped → that wave's claimed minor, `Z=0`. (Any unpromoted older beta is superseded; its content rides along, since dev is cumulative.)
2. Else, the current beta's base is still unpromoted → keep that base; this cut is its `-beta.(N+1)`.
3. Else → newest stable's `Z+1`, `-beta.1`.

Wave → minor map (append a row whenever a new wave is planned):

| Minor | Wave |
|---|---|
| v1.3 | MCP client |
| v1.4 | Typed memory + Veronica Phase 1 (refine waves 1–3, timezone discipline) |
| v1.5 | Veronica wave 4 — operational hardening (RP-13..RP-18) |
| v1.6 | Veronica fifth wave (RP-19..28; RP-24..28 debuted the minor at v1.6.0-beta.1) + EX-6 graduation via RP-26. Remaining EX-1..5 claim future minors as they ship |
| v1.7 | Windows support (WIN-1..9) — native windows binaries, Git-Bash-backed bash tool; PRD: `docs/roadmap/PRD/windows-support.md` |
| v1.7 | Persona members (RP-29) — registry main-tier personas join a swarm as leader/worker with full identity + team protocol |
| v1.8 | Auto-memory consolidation (dream) — a gated, fenced background agent that merges/prunes/re-indexes the global memory store when idle; PRD: `docs/roadmap/PRD/auto-dream.md` |
| v1.9 | Swarm worktree isolation (SWT-1..8) — per-member git worktrees + leader merge-back for coding swarms; PRD: `docs/roadmap/PRD/swarm-worktree-isolation.md` |

### The four release commands

The operator triggers every release with one of four phrases — match on intent, however the sentence is phrased:

| Command | Meaning | Version |
|---|---|---|
| **`pre-release feature`** | ship dev's accumulated work as a new beta | base-version decision above; first cut of a base → `-beta.1` |
| **`hotfix pre-release`** | the current beta broke; re-cut it with fixes | same base, `-beta.(N+1)` |
| **`release`** | promote the newest beta to stable | strip `-beta.N` |
| **`hotfix release`** | critical fix straight onto stable | newest stable `Z+1`, no beta |

**Each phrase IS the full authorization** to execute its playbook end-to-end, including pushing branches and tags — do not ask again. Stop and report instead of pushing only when a precondition fails: dirty tree, failing tests, or the actual dev delta contradicting the command's intent (e.g. `hotfix pre-release` requested but features are present).

#### Playbook: `pre-release feature`

1. Preflight: clean tree; `git fetch origin`; `go test ./...` green on dev.
2. Pick the target version with the base-version decision (check `git tag --sort=-creatordate | head` for current state).
3. `git checkout pre-release && git pull && git merge dev` (`--no-ff` is fine when diverged).
4. `pkg/version/version.go`: set `Version` to the full tag name (e.g. `"v1.5.0-beta.1"`).
5. `CHANGELOG.md`: rename `[Unreleased]` → `[vX.Y.Z-beta.1] — <date>`; insert a fresh `[Unreleased]` on top; update the comparison URLs at the bottom.
6. `git add pkg/version/version.go CHANGELOG.md && git commit -m "chore: changelog and version bump for vX.Y.Z-beta.1"`.
7. `git tag -a vX.Y.Z-beta.1 -m "vX.Y.Z-beta.1 — <one-line summary>"`.
8. `git push origin pre-release vX.Y.Z-beta.1` — the tag push triggers `.github/workflows/release.yml` (tag contains `-` → published as a GitHub pre-release).
9. Backflow: `git checkout dev && git merge pre-release && git push origin dev`.
10. Report: tag, what shipped, release URL.

#### Playbook: `hotfix pre-release`

Premise: the fix is already merged to dev via the normal `feature/*` flow (if not, do that first). Verify the dev → pre-release delta is fixes-only; if features snuck in, report and suggest `pre-release feature` instead.

1. Version = same base as the current beta, `-beta.(N+1)`.
2. Steps 3–10 as in `pre-release feature`, with one CHANGELOG difference: do NOT open a new entry — fold the fix lines into the existing `[vX.Y.Z-beta.N]` entry and rename its heading to `-beta.(N+1)` (one entry per base version; the eventual stable entry is cumulative).

#### Playbook: `release`

1. Identify the newest beta on `pre-release`; report its soak time (days since the tag) for the record.
2. `git checkout main && git pull && git merge pre-release` (`--ff-only` when possible, else `--no-ff`).
3. `pkg/version/version.go`: drop `-beta.N` (e.g. `"v1.5.0"`).
4. `CHANGELOG.md`: rename `[vX.Y.Z-beta.N]` → `[vX.Y.Z] — <date>`; update the comparison URLs.
5. `git add pkg/version/version.go CHANGELOG.md && git commit -m "chore: promote vX.Y.Z-beta.N to stable vX.Y.Z"`.
6. `git tag -a vX.Y.Z -m "vX.Y.Z — <one-line summary>"` then `git push origin main vX.Y.Z` — a bare tag publishes as **Latest** (`evva update` and `@latest` move to it).
7. Backflow: `git checkout dev && git merge main && git push origin dev` (pre-release converges at its next cut from dev).
8. Report: tag, soak time, release URL.

#### Playbook: `hotfix release`

For a critical bug in the current stable while `pre-release` may already carry the next wave.

1. `git checkout main && git pull && git checkout -b hotfix/<name>`; apply the fix (or cherry-pick it from dev); `go test ./...`.
2. Merge `hotfix/<name>` into `main`.
3. Version = newest stable `Z+1`, tagged stable DIRECTLY — the only path that skips a beta. If that number is already claimed by an unpromoted beta, the hotfix still takes it; the superseded beta's content re-ships later under the next free number (never delete or re-point existing tags).
4. `pkg/version/version.go` + a new `[vX.Y.Z] — <date>` CHANGELOG entry (typically just `### Fixed`), committed together.
5. `git tag -a vX.Y.Z -m "vX.Y.Z — <summary>"` then `git push origin main vX.Y.Z`.
6. Backflow: `git checkout dev && git merge main && git push origin dev`. The fix reaches `pre-release` at its next cut from dev.
7. Report.

### CHANGELOG rules

- **One entry per base version.** It is born as `[vX.Y.Z-beta.1]` at the first beta; each later beta of the same base folds its lines in and renames the heading; promotion renames it to `[vX.Y.Z]`. A hotfix-release entry is born stable directly.
- `[Unreleased]` always sits on top between releases; sections are `### Added` / `### Fixed` / `### Changed` / `### Breaking`.
- Update the comparison URLs at the bottom on every rename.
- Merge-conflict rule (legacy drift only): keep `[Unreleased]` from dev on top, released entries below, dedupe lines.

### Key rules

- `pkg/version/version.go`'s `Version` constant carries the FULL tag name including the leading `v` (e.g. `"v1.4.5-beta.2"`). It is the dev-build fallback for `evva update`'s current-version check; release binaries get the real tag injected via ldflags (`pkg/config.Version`). Invariant: tag name == `Version` constant == CHANGELOG heading.
- The four release commands carry push authorization. Any tag or release push OUTSIDE these four playbooks still requires asking first — pushing is a shared-state operation.
- Never skip the backflow merge into `dev` after a tag.
- Releases are published by `.github/workflows/release.yml` on tag push (cross-compiles binaries, attaches them, generates notes): tag containing `-` → `--prerelease`; bare `vX.Y.Z` → Latest. No manual `gh release create`.
