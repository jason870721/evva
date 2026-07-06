# PRD — CI & Headless Runner (GitHub Action, @evva PR bot, machine-readable findings) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W12 / v1.22** per
> [../long-range.md](../long-range.md). Depends on structured output
> (shipped, `dev`) and secret redaction (W3) for log hygiene.
> **Roadmap source:** 2026-07-06 long-range planning pass. "Agent in CI"
> went mainstream in 2025-26 (Claude Code GitHub Action, Gemini CLI
> workflows); evva has the harder half already — headless mode with typed
> final answers (`--output-schema`) — but no packaging, no review
> playbook, and no CI-safe cost rails.
> **Reference source:** the Claude Code GitHub Action's interaction shape
> (mention-triggered, comment-replying) as prior art; implementation is
> evva-native.

---

## 1. TL;DR

evva can already run headless and emit exactly one JSON document on stdout
(structured output, shipped on dev). This wave packages that primitive
into the three things a team actually wants:

1. **A GitHub Action** (`evva-agent/action`): installs a pinned release
   binary (the release workflow already cross-compiles), restores config
   from repo secrets/vars, runs a prompt or a named playbook against the
   checkout, and uploads outputs — comment, artifact, or SARIF.
2. **A PR review playbook**: `evva review --pr <n>` (or in-action) reads
   the diff via `gh`, reviews with a findings schema (file, line,
   severity, summary, fix suggestion), and posts inline comments and/or
   emits SARIF so findings land in GitHub code scanning natively.
3. **A mention bot recipe**: a documented workflow where `@evva` in a PR
   comment triggers the action with that comment as the prompt, replying
   in-thread — the async-teammate pattern.

Everything runs under hard CI rails: token budget cap, wall-clock cap,
tool allowlist profile (no interactive tools, no permission prompts —
deny-by-default with an explicit CI profile), and redacted logs.

## 2. Goals / non-goals

### Goals

- Action published from the evva repo (`action.yml`, composite):
  version-pinned binary install with checksum, config via env
  (`EVVA_*` secrets), workdir = checkout, outputs = `{json, exit_code,
  report_path}`.
- **CI profile**: a built-in agent profile for unattended runs —
  headless-safe toolset (no `ask_user_question`, no plan mode),
  permission mode preconfigured (no prompts: auto-deny outside the
  allowlist), budget + iteration caps mandatory (refuse to start
  uncapped).
- `evva review`: diff ingestion (unified diff or `gh pr diff`), findings
  schema built on structured output, renderers: GitHub inline comments
  (via `gh api`), SARIF 2.1.0 file, markdown summary.
- Exit-code contract: distinct codes for clean / findings-above-threshold
  / budget-exhausted / error — so workflows can gate merges.
- Mention-bot recipe as a documented, copy-pastable workflow file (the
  action does the work; the recipe is wiring + minimal-permissions
  guidance incl. `pull-requests: write` only).
- Cost visibility: the run's spend printed in the job summary (rate card
  already exists; RTE budget rails apply when W9 has shipped, hard caps
  regardless).

### Non-goals (this wave)

- A hosted evva service, GitHub App with server-side webhooks, or
  cross-repo fleet orchestration (Actions-native only; the App is a
  possible follow-on once demand is real — §6).
- Auto-merge / auto-push authority — the CI profile can propose branches
  and comments; pushing to protected branches stays human.
- GitLab/Gitea parity in v1 (SARIF + exit codes are portable; runners for
  other forges follow demand).
- Swarm-in-CI (a full swarm inside a runner is EX-9-adjacent; solo evva
  only).

## 3. Design sketch

- **Binary distribution:** the action downloads the tagged release asset
  (already produced by `.github/workflows/release.yml`), verifies
  checksum, caches via tool-cache. No container image required (keeps
  cold-start ~seconds; a container variant is an open question).
- **Auth model:** LLM keys via repo secrets → env (config load already
  reads env); `GITHUB_TOKEN` with least privilege for `gh` calls; docs
  are explicit that logs pass through the SEC redactor.
- **Review flow:** diff → chunked review turns (respecting the context
  engine when available) → findings JSON (schema versioned, checked into
  the repo) → renderers. Inline-comment placement uses the diff anchors;
  unplaceable findings fall back to the summary comment.
- **Determinism posture:** CI runs pin model + provider explicitly in the
  workflow (no route ambiguity in CI); the action fails loud if the model
  is unavailable rather than failing over silently (overrides W9 chains
  by default — reproducibility beats availability in CI).

## 4. Work items

- **CIH-1 — CI profile.** Unattended toolset, mandatory caps,
  deny-by-default permissions, refuse-uncapped-start. *Accept:* the
  profile cannot emit a permission prompt under any tool call in a test
  sweep; uncapped invocation exits with the config-error code.
- **CIH-2 — `evva review` + findings schema.** Diff ingestion, schema,
  markdown renderer. *Accept:* a fixture PR diff yields valid
  schema-conformant findings JSON and a readable summary.
- **CIH-3 — SARIF + inline comments.** SARIF 2.1.0 emitter + `gh`-based
  inline placement with fallback. *Accept:* SARIF validates against the
  schema; unplaceable findings appear in the summary instead of being
  dropped.
- **CIH-4 — The Action.** `action.yml`, pinned install, outputs, job
  summary with spend. *Accept:* a real workflow run on the evva repo
  itself reviews a test PR end-to-end (dogfood gate).
- **CIH-5 — Mention-bot recipe.** Documented workflow, permission
  guidance, reply-in-thread. *Accept:* recipe works on a sandbox repo;
  docs include the security caveats (prompt injection via comments —
  mention runs use the CI profile's deny-by-default rails).
- **CIH-6 — Exit-code contract + threshold gating.** Codes, severity
  threshold input. *Accept:* findings ≥ threshold flips the job red;
  budget exhaustion is distinguishable from crash.
- **CIH-7 — Docs + changelog.** User-guide (en + zh-tw) + `examples/ci/`
  with the review and mention workflows.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Prompt injection from PR content/comments drives tool abuse | CI profile: deny-by-default permissions, no network-mutating tools in the allowlist, budget/iteration hard caps; docs treat mention-bot as the highest-risk recipe |
| Secrets in CI logs | SEC redactor is a W3 dependency, applied to all streamed output; docs mandate secret-based key injection |
| Cost runaway on large diffs | mandatory caps + chunked review + spend in job summary; threshold-gated early exit |
| Review quality noise annoys teams | severity threshold default posts only high-confidence findings inline; everything else lives in the artifact |

## 6. Open questions

1. Container-image variant of the action (slower cold start, better
   hermeticity + SBX synergies) — ship binary-only first?
2. Findings schema: adopt/borrow SARIF's taxonomy wholesale internally,
   or keep a minimal evva schema + SARIF as a renderer? Leaning minimal +
   renderer.
3. GitHub App follow-on (webhook-driven, no Actions minutes): what demand
   signal justifies it?
