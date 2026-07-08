# evva Roadmap — Overview & Progress

> **Purpose:** one page that answers "what's shipped, what's in flight, what's
> just a proposal" across everything under `docs/roadmap/`. The individual PRD
> files' own `Status:` headers drift once shipped (see §6) — **this file is
> the source of truth going forward**, refreshed at each release.
> **As of:** 2026-07-08, `dev`/`pre-release` @ `8c873ef` (v1.11.0-beta.1) +
> `feature/output-styles` in flight.

---

## 1. Where we are right now

| Branch | Version | State |
|---|---|---|
| `main` (stable / GitHub "Latest") | **v1.8.4** | promoted 2026-06-26 (Windows CI hotfix) |
| `pre-release` (beta) | **v1.11.0-beta.1** | cut 2026-07-07 — solo dynamic workflow + self-healing edits + structured output (supersedes the unpromoted v1.10.0-beta.1; its content rode along); not yet promoted |
| `dev` (integration) | in sync with `pre-release` | `8c873ef` |
| `feature/output-styles` | — | built 2026-07-08, PR to `dev` pending — the last of the three old small proposals |

**⚠️ v1.9 is claimed but empty.** `docs/roadmap/PRD/swarm-worktree-isolation.md`
claimed the **v1.9** minor in `CLAUDE.md`'s wave→minor map (commit `42ac53f`,
2026-07-02) but no `SWT-1..8` implementation ever landed. The very next cut on
`pre-release` was `v1.8.5-beta.1` (an unrelated within-wave patch), and the one
after that jumped straight to `v1.10.0-beta.1` for dynamic workflow — there is
**no `v1.9.0` tag anywhere in git history**. This is almost certainly the root
of "some PRDs are done, some aren't, hard to tell which" — the wave table
currently overstates what shipped. Two ways to close it:
- **(a)** build `SWT-1..8` now — it would ship under whatever minor is current
  at that time (the CLAUDE.md row needs updating to match), or
- **(b)** formally retire/renumber the v1.9 row as superseded.

Recommend resolving this before the next `pre-release feature` cut so the wave
table stays trustworthy.

---

## 2. Feature PRDs (`docs/roadmap/PRD/`) — 24 tracked

**Tally: 10 stable · 4 in beta (awaiting promotion) · 1 built on a feature branch · 1 claimed-but-unbuilt · 9 proposed.**

| # | PRD | Status | Shipped in | Notes |
|---|---|---|---|---|
| 1 | [windows-support.md](PRD/windows-support.md) | ✅ Stable | v1.7.0 | WIN-1..9; WIN-9 real-hardware validation was still "pending" in the PRD text — worth a final check |
| 2 | [memory-typed-directory.md](PRD/memory-typed-directory.md) | ✅ Stable | v1.4.0-beta.1 | foreground half of the memory roadmap; background half is auto-dream (#4) |
| 3 | [build-agent-skill.md](PRD/build-agent-skill.md) | ✅ Stable | v1.4.0-beta.1 | rode along as a content patch, no framework change |
| 4 | [auto-dream.md](PRD/auto-dream.md) | ✅ Stable | v1.8.0 | background memory consolidation; debuted the v1.8 wave |
| 5 | [alarm-tool.md](PRD/alarm-tool.md) | ✅ Stable | v1.8.0 | own PRD header still says "Target release: next beta" — stale, ignore it |
| 6 | [cron-scheduling.md](PRD/cron-scheduling.md) | ✅ Stable | v1.8.0 | reuses the alarm `Scheduler`, shipped alongside it |
| 7 | [resilient-edit.md](PRD/resilient-edit.md) | ✅ Stable | v1.8.2 (beta.1) | whitespace/indentation-tolerant edit fallback |
| 8 | [checkpoint-rewind.md](PRD/checkpoint-rewind.md) | ✅ Stable | v1.8.2 (beta.2) | `/rewind`, opt-in, solo main-agent only |
| 9 | [lsp-repo-map.md](PRD/lsp-repo-map.md) | ✅ Stable | v1.8.2 (beta.4) | opt-in, built on the shipped LSP module |
| 10 | [parallel-fanout-reconcile.md](PRD/parallel-fanout-reconcile.md) | ✅ Stable | v1.8.2 | `exit_worktree action:"merge"` + `worktree_list` |
| 11 | [swarm-dynamic-workflow.md](PRD/swarm-dynamic-workflow.md) | 🟡 **Beta — awaiting promotion** | v1.10.0-beta.1 | DWF-1..8: task graph auto-dispatch, `task_done`, ephemeral `member_spawn` clones |
| 12 | [swarm-worktree-isolation.md](PRD/swarm-worktree-isolation.md) | ⚠️ **Claimed, not built** | — (v1.9 row exists, no code) | see the callout above — needs an operator call |
| 13 | [edit-diagnostics-sync.md](PRD/edit-diagnostics-sync.md) | 🟡 **Beta — awaiting promotion** | v1.11.0-beta.1 | self-healing edit→LSP diagnostics sync; merged as johnny1110/evva#52 |
| 14 | [output-styles.md](PRD/output-styles.md) | ⏳ **Built on `feature/output-styles`** — PR to dev pending | next cut (no wave claim) | built 2026-07-08; `/output-style` picker, built-in Explanatory/Learning + disk styles, `keep-coding-instructions` |
| 15 | [structured-output-tool.md](PRD/structured-output-tool.md) | 🟡 **Beta — awaiting promotion** | v1.11.0-beta.1 | headless typed final answers via caller schema; merged as johnny1110/evva#54 |
| 16 | [swarm-blackboard.md](PRD/swarm-blackboard.md) | 📝 Proposed | — | v1.11+ candidate, 2026-07-04/05 swarm design review |
| 17 | [swarm-cost-accounting.md](PRD/swarm-cost-accounting.md) | 📝 Proposed | — | v1.11+ candidate, same review |
| 18 | [swarm-doctor.md](PRD/swarm-doctor.md) | 📝 Proposed | — | v1.11+ candidate, same review |
| 19 | [swarm-outbound-notifications.md](PRD/swarm-outbound-notifications.md) | 📝 Proposed | — | v1.11+ candidate, same review |
| 20 | [swarm-tui-attach.md](PRD/swarm-tui-attach.md) | 📝 Proposed | — | v1.11+ candidate, same review |
| 21 | [swarm-verify-checks.md](PRD/swarm-verify-checks.md) | 📝 Proposed | — | v1.11+ candidate, same review |
| 22 | [sandbox-isolation.md](PRD/sandbox-isolation.md) | 📝 Proposed — **new** | — | added 2026-07-06 (this session) — OS-level sandboxing for bash/fan-out/swarm clones |
| 23 | [mcp-server-mode.md](PRD/mcp-server-mode.md) | 📝 Proposed — **new** | — | added 2026-07-06 (this session) — expose evva as an MCP server |
| 24 | [agent-eval-harness.md](PRD/agent-eval-harness.md) | 📝 Proposed — **new** | — | added 2026-07-06 (this session) — transcript replay + regression scoring |
| 25 | [solo-dynamic-workflow.md](PRD/solo-dynamic-workflow.md) | 🟡 **Beta — awaiting promotion** | v1.11.0-beta.1 (claimed v1.11) | SDW-1..8: DWF execution model for solo TUI — `wf_task_*` board, engine auto-dispatch of subagent workers, `enable_dynamic_workflow` flag |

**Status legend:** ✅ Stable (in a promoted `vX.Y.Z` on `main`) · 🟡 Beta (built,
in a `-beta.N` on `pre-release`, not yet promoted) · ⚠️ Anomaly (wave claimed
in CLAUDE.md, no implementation) · 📝 Proposed (PRD written, no wave claimed,
no code).

**Plus 32 long-range concept PRDs** (added 2026-07-06 by the
[long-range.md](long-range.md) planning pass, in two batches, not
duplicated as rows here until they claim waves):

- *Backbone (W1–W19):* secret-redaction, context-engine,
  memory-semantic-recall, session-tree, steering-v2, model-routing,
  vision-completion, acp-editor-integration, ci-headless-runner,
  browser-tools, swarm-federation, swarm-leader-takeover, workflow-scripts,
  gardener, persona-ecosystem, arch-v2.
- *Batch 2 (W20–W35):* onboarding-doctor, swarm-templates, diff-review-ui,
  plan-mode-v2, test-watch-loop, git-intelligence, provider-expansion,
  batch-api-background, treesitter-code-intel, multi-root-workspaces,
  dap-debugger, replay-lab, human-in-the-swarm, swarm-retrospective,
  audit-trail, chat-bridges.

All carry `Status: long-range concept draft` — **not audited against live
source**; per the concept → build gate in long-range §8, each needs an
audit pass before implementation. Full sequencing lives in long-range §3
(backbone) and §3b (batch 2).

---

## 3. Veronica — multi-agent swarm project (`docs/roadmap/veronica/`)

Veronica has its own planning tree with decent self-tracking
([veronica/README.md](veronica/README.md),
[refine-plan/README.md](veronica/refine-plan/README.md)); this is a rollup, not
a replacement.

| Track | Status |
|---|---|
| **Phase 1 — swarm infrastructure** (SPRD-1-1..1-13) | ✅ **DONE** — certified 2026-06-04, all DoD boxes green ([checklist](veronica/phase-1-dod-checklist.md)) |
| **Refine wave 1** (RP-1..4 — smoke-test fixes) | ✅ RP-1/2/3 done (2026-06-05, messaging reliability + approval routing + run-phase states). RP-4 (Web UX critique) was direction-only and got superseded by the FE v2 rewrite rather than implemented standalone |
| **Refine wave 2** (RP-5..10 — scaling/scheduling/skills) | ✅ done — shipped incrementally as v1.4.x/v1.5.x patches |
| **Refine wave 3** (FE-1..8 — Web UI 2.0 "web2") | ✅ done — NEON TOKYO Vue3/TS/Pinia rewrite cut over at v1.4.1-beta.1, extended continuously as later RP tickets added backend surfaces |
| **RP-11, RP-12** (independent refines between waves 3–4) | ✅ done |
| **Refine wave 4** (RP-13..18 — operational hardening: cost metering, stall watchdog, webapi auth, ledger retention, event log, ops polish) | ✅ **DONE** 2026-06-10 — ships in v1.5 |
| **Refine wave 5** (RP-19..28 — framework maturity) + **RP-29** (persona members) | ✅ **DONE** — RP-19..28 ship in v1.6, RP-29 ships in v1.7 |
| **Explore spikes** (EX-1..6) | EX-1 (member-native memory) ✅ graduated → RP-25. EX-6 (skill sharing) ✅ graduated → RP-26. **EX-2 (remote persona), EX-3 (leader takeover), EX-4 (replay/eval harness), EX-5 (wake jitter) are still open, unclaimed spikes — no RP ticket yet** |
| **Phase 2 — trader-team validation** ([prd-phase2-trader-team.md](veronica/prd-phase2-trader-team.md)) | ⏸ **Superseded** — the crypto-trading-swarm direction was pivoted away from; validation now happens in the separate **Sunday** project (a token-free Binance proxy, different repo), not as evva's in-repo Phase 2 |

**Net: Phase 1 done, all 5 refine waves done, 2/6 explore spikes graduated, Phase 2 pivoted out of this repo.** The swarm track is in unusually good shape — essentially everything *committed to* has shipped; the open surface is the six `v1.11+` candidate PRDs in §2 plus the four un-graduated EX spikes.

---

## 4. Foundational / historical (no longer actively tracked)

These are all from before the current v1.8–v1.10 window and are done — kept for reference, not because anything is pending:

| Folder | What | Status |
|---|---|---|
| [v1/](v1/) | v1.1 hooks, v1.2 OpenAI provider, v1.3 MCP client, v1.4 bundled skills, v1.5 config tool | ✅ all shipped, ancient history |
| [evva-sdk/](evva-sdk/) | SDK v2 hardening ("harden to stable") | ✅ shipped — CHANGELOG `v1.0.0` "SDK v2 complete + LSP: the pkg-only milestone" |
| [design/](design/) | agent-runtime, daemon-design, fs-edit-gate-parity, lsp, lsp-feedback, task-design | living architecture references, not features — no done/not-done state |
| [roadmap_healthcheck.md](roadmap_healthcheck.md) | 2026-06 audit of `ref/src` gaps (M1–M9 must-haves, N1–N15 nice-to-haves) + a codebase health check | Largely actioned: compaction (M1), LSP (M2), notebook edit (M8) all shipped since. **Output styles (M6) and a dedicated cost tracker (M5) are the still-open items** — both now tracked as real PRDs (#14 output-styles is open; cost tracking is partially covered by RP-13's per-member metering plus the still-proposed #17 swarm-cost-accounting) |

---

## 5. Backlog at a glance

Everything with no code yet, grouped by what it needs from the operator:

- **Needs a decision, not just a slot:** `swarm-worktree-isolation.md` (§1 anomaly — build it or retire the v1.9 row).
- ~~**Old, non-swarm, never slotted**~~ — **cleared.** All three old small proposals are now built: `edit-diagnostics-sync.md` + `structured-output-tool.md` shipped in v1.11.0-beta.1, and `output-styles.md` (the last one) is on `feature/output-styles` awaiting merge.
- **Swarm v1.11+ candidates** (all from the 2026-07-04/05 design review, none claim a minor yet): `swarm-blackboard.md`, `swarm-cost-accounting.md`, `swarm-doctor.md`, `swarm-outbound-notifications.md`, `swarm-tui-attach.md`, `swarm-verify-checks.md`.
- **New this session** (2026-07-06, researched against current industry trends — see each PRD's header for prior-art citations): `sandbox-isolation.md`, `mcp-server-mode.md`, `agent-eval-harness.md`.
- **Un-graduated explore spikes** (no PRD at all yet, just a hypothesis): EX-2 (remote persona — graduation path now drafted as `swarm-federation.md`), EX-3 (leader takeover — graduation path now drafted as `swarm-leader-takeover.md`), EX-4 (replay/eval harness — note the new `agent-eval-harness.md` generalizes this; see its own header for the boundary), EX-5 (wake jitter).
- **Long-range concept PRDs** (2026-07-06, 16 files — see the note under §2 and [long-range.md](long-range.md) §3 for the full sequenced list): concept-grade drafts for horizons W3–W19; each requires a live-source audit pass before build.

None of the above has claimed a wave/minor. Per `CLAUDE.md`, that happens at
planning time when an operator picks it up — this document doesn't presume
sequencing.

---

## 6. Keeping this doc honest

Individual PRD files' `Status:` headers are **not** reliably updated after
shipping — of the 11 shipped PRDs in §2, only `windows-support.md` and (partly)
`alarm-tool.md` got their header updated post-ship; the other 9 still read
"proposed" despite being live in `main` for weeks. Chasing 20+ files on every
release isn't worth it. Going forward:

- Treat **this file** as the single source of truth for "is X done".
- Refresh it as part of the release playbooks in `CLAUDE.md` (`release` /
  `pre-release feature`) — a two-minute diff against the CHANGELOG entry being
  promoted/cut, not a re-audit.
- Don't bother hand-syncing each PRD's own header when it ships; it's not
  worth the churn and this file is the thing people should actually read.
