# PRD — Onboarding & Doctor (first-run setup + environment diagnostics) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W20**, suggested horizon
> H1+ per [../long-range.md](../long-range.md) §3b — small, high-
> leverage, schedulable early (it makes every *other* wave's features
> discoverable and correctly configured).
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> evva's surface has grown enormous — five providers, LSP servers,
> MCP, hooks, skills, memory, swarm, and (per this roadmap) soon DAP,
> sandboxing, tree-sitter, bridges, more. Each has config, prereqs,
> and failure modes. A new user's first five minutes decide adoption,
> and an existing user hitting a misconfiguration burns an hour. There
> is a `swarm-doctor` PRD for the swarm; this wave is its generalization
> to the whole product plus the missing front door.
> **Reference source:** none in `ref/src` — evva-native; prior art:
> `brew doctor`, `flutter doctor`, `npm doctor`.

---

## 1. TL;DR

Two related surfaces, one wave:

1. **`evva doctor`** — a comprehensive environment diagnostic:
   provider keys present + reachable (a cheap ping per configured
   provider), LSP servers on PATH for the repo's languages, container
   runtime for sandboxing (W3), debug adapters (W30), tree-sitter
   grammars (W28), git config + version, MCP server reachability,
   hook script validity, config file well-formedness, version/update
   status, disk space for sessions/audit. Each check: ✓ / ⚠ / ✗ with
   a **specific, actionable fix line** ("delve not found — `go install
   github.com/go-delve/delve/cmd/dlv@latest` to enable debugging").
   Subsumes and reuses `swarm-doctor`'s checks as one section.

2. **First-run onboarding** — on a fresh `<EVVA_HOME>`, an interactive
   setup: pick a provider + validate the key live, choose a default
   persona, offer to detect + note available LSP/debug tools, explain
   the three things a new user actually needs (how to talk to it, how
   permissions work, where memory lives), and write a starter config.
   Skippable (`--no-onboard`), re-runnable (`evva onboard`), and
   honest about what it changed.

The theory: a product this capable is only as good as a user's ability
to *reach* the capability. Doctor is the ongoing answer; onboarding is
the first-contact answer. Both are cheap to build (checks over existing
subsystems) and disproportionately valuable to adoption.

## 2. Goals / non-goals

### Goals

- Check framework: a `doctor.Check` contract (`{name, category, run()
  → {status, detail, fix?}}`) each subsystem registers — so every
  future wave adds its own check (the sandbox wave registers
  "container runtime present", DAP registers "adapters found", etc.).
  The framework is the durable deliverable; the initial checks are
  the seed.
- `evva doctor [--category X] [--json]`: grouped output, exit code
  reflecting worst status (green/warn/fail), JSON for scripting/CI.
- Provider reachability: a minimal, cheap validation call per
  configured provider (cost-negligible, clearly labeled), distinguishing
  "no key" / "bad key" / "reachable" / "network".
- Onboarding flow: fresh-home detection, provider setup + live
  validation, persona choice, tool detection summary, the 3-concept
  orientation, starter config write, provenance summary.
- Fix affordance: where a fix is a single safe command, offer to run
  it (`doctor --fix` for the safe subset — installing nothing without
  consent; mostly config repairs and directory creation).
- Doctor as a check other things call: pre-swarm-up, pre-sandbox-
  session, CI-profile startup can invoke relevant check subsets and
  refuse-with-guidance instead of failing cryptically mid-run.

### Non-goals (this wave)

- Installing third-party tools automatically (LSP servers, delve,
  Docker — doctor *detects and instructs*; it never silently installs
  heavyweight external software; the `--fix` subset is config/dirs
  only).
- A GUI/TUI dashboard (doctor is a report; onboarding is a linear
  interview — no persistent UI).
- Telemetry/phone-home (all checks are local or against the operator's
  own configured endpoints; nothing reports anywhere).
- Auto-updating evva itself (the update mechanism exists; doctor
  reports status and points to it).

## 3. Design sketch

- **Check registry:** subsystems register checks at init (same
  builtin-registration idiom as tools/providers); `doctor` runs the
  registry, grouped by category, concurrently where checks are I/O
  (provider pings, PATH lookups) with a bounded pool + per-check
  timeout (a hung MCP server must not hang doctor). The registry
  completeness is the architectural win: adding a subsystem *without*
  a health check becomes the exception, visible in review.
- **Fix lines are first-class:** a check that can't pass must, where
  possible, carry the exact remediation — the difference between
  `doctor` being useful and being a wall of red. Fix text is reviewed
  content, per-platform where it matters (the install command differs
  on Windows).
- **Onboarding is doctor + interview + writes:** it runs the relevant
  checks, but framed as setup steps with live validation (the provider
  key check *is* the "validate your key" step). Reuse, not
  reimplementation.
- **Refuse-with-guidance hook:** subsystems that need prerequisites
  (sandbox needs Docker, swarm needs its config) call
  `doctor.Require(categories…)` at entry and surface the failing
  check's fix line instead of a raw error — turning every "it broke
  mysteriously" into "here's exactly what's missing".

## 4. Work items

- **DOC-1 — Check framework + registry.** Contract, registration,
  concurrent run, timeouts, categories, exit codes, `--json`.
  *Accept:* fixture checks (passing/warn/failing) group and
  aggregate correctly; a hung check times out without hanging the
  run.
- **DOC-2 — Seed checks.** Providers (reachability), LSP/PATH, git,
  MCP, hooks, config well-formedness, version/update, disk. Reuse
  swarm-doctor's checks as a category. *Accept:* each check produces
  correct status + a fix line on a seeded-broken fixture environment.
- **DOC-3 — `--fix` safe subset.** Config repairs + directory
  creation with consent; never external installs. *Accept:* a
  malformed-config fixture is repaired with confirmation; `--fix`
  refuses anything outside the safe set.
- **DOC-4 — Onboarding flow.** Fresh-home detection, provider setup +
  live validation, persona choice, tool-detection summary, 3-concept
  orientation, starter config, provenance. *Accept:* fresh-home run
  produces a working single-provider config and the user can complete
  a first turn immediately after.
- **DOC-5 — Refuse-with-guidance hook.** `doctor.Require` entry point;
  wire sandbox/swarm/CI-profile startups to it. *Accept:* starting a
  sandbox session without a container runtime refuses with the exact
  fix line, not a raw exec error.
- **DOC-6 — Docs + changelog.** User-guide (en + zh-tw): doctor as
  the first troubleshooting step, onboarding walkthrough, how to add
  a check (contributor note — every future wave should).

## 5. Risks

| Risk | Mitigation |
|---|---|
| Provider ping costs money / rate-limit | the cheapest possible call (token-count or a 1-token request), clearly labeled, skippable (`--no-net`); results cached briefly |
| Fix lines rot as tools change | fix text is reviewed content with the checks; per-platform where needed; a stale fix is a doc bug caught like any other |
| Onboarding annoys power users | `--no-onboard` + it only triggers on a genuinely fresh home; fully skippable, never blocks |
| Check sprawl / slow doctor | concurrent + timed + categorized (`--category` for targeted runs); the completeness expectation is "consequential subsystems", not "everything" |

## 6. Open questions

1. Should `doctor` run automatically (quietly) at session start and
   surface only ✗/⚠, or stay purely on-demand? Leaning a one-line
   startup summary only when something's wrong, full report on
   demand.
2. `--fix` scope creep pressure: where's the hard line? Proposed:
   config + directories + owned-file repairs only; anything touching
   $PATH or installing software is instruction-only, forever.
3. Does onboarding write a starter `EVVA.md`/agent-instruction file
   for the repo, or leave that to `/init`? Leaning point-to-`/init`
   to avoid overlap.
