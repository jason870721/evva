# evva Roadmap — Overview & Progress

> **Purpose:** one page that answers "what's shipped, what's in flight, what's
> just a proposal" across everything under `docs/roadmap/`. The individual PRD
> files' own `Status:` headers drift once shipped (see §6) — **this file is
> the source of truth going forward**, refreshed at each release.
> **As of:** 2026-08-01, `main` @ **v1.14.0 stable**; `dev` carries five
> unreleased waves (SBX → v1.15, EVAL → v1.16, CTX → v1.17, MEM → v1.18,
> SES → v1.19).

---

## 1. Where we are right now

| Branch | Version | State |
|---|---|---|
| `main` (stable / GitHub "Latest") | **v1.14.0** | promoted 2026-08-01 at `7ae8d7a`. Carries everything through secret redaction: the v1.11 swarm batch, SWT worktree isolation (v1.12), MCP server mode (v1.13), SEC redaction (v1.14) |
| `pre-release` (beta) | **v1.14.0-beta.1** | promoted verbatim to v1.14.0; converges at its next cut from dev |
| `dev` (integration) | unreleased | `main` + **SBX-1..7** (claims v1.15, merged 2026-08-01 at `023dc87`, PR #66) + **EVAL-1..7** (claims v1.16, merged 2026-08-01 at `e8f8089`, PR #67) + **CTX-1..7** (claims v1.17, merged 2026-08-01 at `c02eb8e`, PR #68) + **MEM-1..7** (claims v1.18, merged 2026-08-01 at `db47629`, PR #69) + **SES-1..7** (claims v1.19, PR open) |

**Five unreleased waves are stacked on `dev`.** Per the base-version decision
in `CLAUDE.md`, a single `pre-release feature` cut would ship them together
and take the newest never-shipped wave's minor — **v1.19.0-beta.1**. The
v1.15–v1.18 rows stay in the wave map regardless: they record which wave the
work belongs to, not which tag carried it.

**✅ Horizon 1 is closed.** With SBX and EVAL built, every wave the 2026-07
design reviews put on the table has been implemented — W1 swarm operations
(v1.11), W2 swarm trust (v1.12), W3 safety (SEC v1.14 + SBX v1.15), W4 quality
loop (EVAL v1.16), plus MCP server mode pulled forward from W11 into v1.13.
There is no remaining *designed-but-unbuilt* surface; what is left is
long-range concept drafts (§5), each of which still needs its audit pass.

**▶ Horizon 2 has started.** W5 — the context engine (CTX-1..7, claims v1.17)
— is the first long-range concept PRD taken through the full
audit-then-build gate. It is also the strongest evidence yet for why that
gate is non-negotiable: **two of its seven work items were already shipped**
(CTX-3 read dedup, and the status-bar half of CTX-5), and the draft's central
term "microcompaction" named the exact opposite of what evva's shipped
`microCompact` did. Building to the draft as written would have re-implemented
solved problems and redefined live vocabulary. W5 unblocked **W6 — semantic memory recall (MEM-1..7, claims
v1.18)**, which followed immediately and produced the most expensive audit so
far: the draft's central factual claim about how evva recalls memories was
simply **wrong**, not stale. **W7 — session tree (SES-1..7, claims v1.19)**
followed, and added a different lesson: its catalog work item was not wrong,
it was **unjustified**, and only a measurement showed it (§2's audit table).

**✅ The v1.9 anomaly is closed.** `swarm-worktree-isolation.md` claimed the
**v1.9** minor (commit `42ac53f`, 2026-07-02) but was never built, and no
`v1.9.0` tag exists anywhere in git history — meanwhile `v1.10.0-beta.1` and
then v1.11.0 shipped past it. Closed by **option (a): build it.** SWT-1..8
landed at `acba56a` (2026-07-29) and shipped in v1.12.0; because v1.9 was no
longer reachable the CLAUDE.md wave→minor map re-slots the wave to **v1.12**,
leaving a struck-through v1.9 row so the gap in the version line is documented
rather than mysterious.

---

## 2. Feature PRDs (`docs/roadmap/PRD/`) — 29 tracked

**Tally: 24 stable · 5 built-but-unreleased (sandbox isolation → v1.15, agent eval harness → v1.16, context engine → v1.17, memory semantic recall → v1.18, session tree → v1.19) · 0 proposed.**

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
| 11 | [swarm-dynamic-workflow.md](PRD/swarm-dynamic-workflow.md) | ✅ Stable | v1.11.0 | DWF-1..8: task graph auto-dispatch, `task_done`, ephemeral `member_spawn` clones |
| 12 | [swarm-worktree-isolation.md](PRD/swarm-worktree-isolation.md) | ✅ Stable | v1.12.0 | SWT-1..8, 2026-07-29: `settings.worktree_isolation`, per-member worktrees, leader `worktree_merge`, root-state pinning (`SessionWorkdir`), lifecycle + roster column. Re-slotted from the never-built v1.9 claim |
| 13 | [edit-diagnostics-sync.md](PRD/edit-diagnostics-sync.md) | ✅ Stable | v1.11.0 | self-healing edit→LSP diagnostics sync; merged as johnny1110/evva#52 |
| 14 | [output-styles.md](PRD/output-styles.md) | ✅ Stable | v1.11.0 | `/output-style` picker, built-in Explanatory/Learning + disk styles, `keep-coding-instructions`; no wave claim (within-wave) |
| 15 | [structured-output-tool.md](PRD/structured-output-tool.md) | ✅ Stable | v1.11.0 | headless typed final answers via caller schema; merged as johnny1110/evva#54 |
| 16 | [swarm-blackboard.md](PRD/swarm-blackboard.md) | ✅ Stable | v1.11.0 | v1.11+ candidate, 2026-07-04/05 swarm design review |
| 17 | [swarm-cost-accounting.md](PRD/swarm-cost-accounting.md) | ✅ Stable | v1.11.0 | v1.11+ candidate, same review |
| 18 | [swarm-doctor.md](PRD/swarm-doctor.md) | ✅ Stable | v1.11.0 | v1.11+ candidate, same review |
| 19 | [swarm-outbound-notifications.md](PRD/swarm-outbound-notifications.md) | ✅ Stable | v1.11.0 | v1.11+ candidate, same review |
| 20 | [swarm-tui-attach.md](PRD/swarm-tui-attach.md) | ✅ Stable | v1.11.0 | v1.11+ candidate, same review |
| 21 | [swarm-verify-checks.md](PRD/swarm-verify-checks.md) | ✅ Stable | v1.11.0 | CHK-1..6 implemented 2026-07-10 (`feature/swarm-verify-checks`); minor unclaimed — operator assigns at wave confirmation |
| 22 | [sandbox-isolation.md](PRD/sandbox-isolation.md) | 🟡 **Built — merged to `dev`, untagged** | — (claims **v1.15**) | SBX-1..7, 2026-08-01: `isolation:"sandbox"` = worktree + bind-mounted container, `bash` via `docker`/`podman exec`, devcontainer.json image resolution, `sandbox_runtime`/`sandbox_image`/`sandbox_network`, swarm `settings.sandbox` + per-member `sandbox:`. **Was blocked, not merely unstarted** — its acceptance criteria need a container runtime, and there now is one (Docker 28.1.1), so they were met against real containers. Six audit corrections in its header; two changed the design. Completes W3 "Safety" |
| 23 | [mcp-server-mode.md](PRD/mcp-server-mode.md) | ✅ Stable | v1.13.0 | MCP-1..5, 2026-07-30: `evva mcp-serve` over stdio / streamable HTTP, `mcpServe` allowlist (startup-validated, read-only tools only), whole-persona invocation with `<external-request>` trust framing, RP-15-style bearer auth. Two PRD corrections recorded in its header: the persona adapter's placement was an import cycle, and the RP-21 envelope was the wrong framing |
| 24 | [agent-eval-harness.md](PRD/agent-eval-harness.md) | 🟡 **Built — merged to `dev`, untagged** | — (claims **v1.16**) | EVAL-1..7, 2026-08-01: `evva eval capture/run/list`, `pkg/evalharness` fixtures + structural tool-call diff (hard gate, non-zero exit) + opt-in LLM judge (advisory). Six audit corrections; fixtures deliberately do NOT embed `session.Snapshot` (machine-specific paths), and driving is a `Runner` interface so the scoring layer stays free of the agent loop. **Closes Horizon 1** |
| 25 | [solo-dynamic-workflow.md](PRD/solo-dynamic-workflow.md) | ✅ Stable | v1.11.0 | SDW-1..8: DWF execution model for solo TUI — `wf_task_*` board, engine auto-dispatch of subagent workers, `enable_dynamic_workflow` flag |
| 26 | [secret-redaction.md](PRD/secret-redaction.md) | ✅ Stable | v1.14.0 | SEC-1..6, 2026-07-30: `pkg/redact` credential detector + stable content-derived placeholders, masking at the `execTool` choke point (covers provider payload, snapshot and TUI in one insertion), `/redactions` panel, `redaction` config defaulting **ON** — evva's only opt-OUT gate. First long-range **concept draft** to go through the audit pass; five corrections recorded in its header, incl. SEC-4 collapsing into SEC-2 and operator input being scoped out. Half of W3 — its sibling SBX stays blocked on a container runtime |
| 27 | [context-engine.md](PRD/context-engine.md) | 🟡 **Built — merged to `dev`, untagged** | — (claims **v1.17**) | CTX-1..7, 2026-08-01: block ledger (`internal/session/ledger.go`) + a three-rung ladder — prune with recovery tombstones → span compaction → full compaction — plus `/context` overlay and pinning. **First Horizon 2 wave.** Seven audit corrections; the two expensive ones are that **CTX-3 and half of CTX-5 were already shipped**, and that the draft's "microcompaction" named the opposite of evva's shipped `microCompact`. Also repaired two live defects the draft's own safety rules exposed: auto-compaction was destroying error text, and the cheap tier could only run once per session |
| 28 | [memory-semantic-recall.md](PRD/memory-semantic-recall.md) | 🟡 **Built — merged to `dev`, untagged** | — (claims **v1.18**) | MEM-1..7, 2026-08-01: `memory_search` tool (closes push-only recall), optional `llm.Embedder` capability with Ollama + OpenAI-compatible backends, hash-diffed vector sidecar, embedding pre-filter for the per-turn selector, `origin` provenance. **The most expensive audit yet — the draft's premise was false, not stale:** recall was never "load the index into the prompt", and MEM-6 proposed adding a global scope to a store that is already global. MEM-4 as written would have shipped less than what existed |
| 29 | [session-tree.md](PRD/session-tree.md) | 🟡 **Built — PR open against `dev`, unreleased** | — (claims **v1.19**) | SES-1..7, 2026-08-01: `evva resume` / `-c` / `resume <id>` (unique-prefix ids), `evva sessions list|prune` (dry-run by default, pin-exempt), `evva export` (self-contained HTML, secrets scrubbed unconditionally), `/fork`, `/title`, and `/resume` grown into the curation surface (pin, delete-with-confirm, all-workdirs, fork-tree indentation). Nine audit corrections. **The instructive one is SES-1: its catalog was cut on measured evidence** — a machine-wide scan of 93 sessions / 14 MB takes 110 ms, so the index bought ~100 ms for a second store; the fields it would have held moved into the snapshot envelope instead. Half of SES-2/SES-4 was already shipped |

**Status legend:** ✅ Stable (in a promoted `vX.Y.Z` on `main`) · 🟡 Beta (built,
in a `-beta.N` on `pre-release`, not yet promoted) · ⚠️ Anomaly (wave claimed
in CLAUDE.md, no implementation) · 📝 Proposed (PRD written, no wave claimed,
no code).

**Plus 29 long-range concept PRDs** (32 were added 2026-07-06 by the
[long-range.md](long-range.md) planning pass, in two batches; they are not
duplicated as rows here until they claim waves, and three — secret-redaction,
context-engine and memory-semantic-recall — have since done so and moved into
the table above):

- *Backbone (W1–W19):* session-tree, steering-v2, model-routing,
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
audit pass before implementation. Five have now been through that gate,
and together they calibrate what the pass costs — it is never zero, and it
has changed the design every time:

| Wave | Corrections | The one that mattered |
|---|---|---|
| SEC (2026-07-30) | 5 | deleted a whole work item (SEC-4 collapsed into SEC-2) |
| SBX (2026-08-01) | 6 | a *shipped* wave (SWT) had closed half the gap the PRD was written against, which changed where the remaining work belonged |
| EVAL (2026-08-01) | 6 | the prescribed fixture format embedded machine-specific paths, so it could not be committed |
| CTX (2026-08-01) | 7 | **two of seven work items were already shipped**, and the draft's central term named the opposite of the live code |
| MEM (2026-08-01) | 9 | **the draft's central factual claim was wrong** — it described a subsystem evva does not have |
| SES (2026-08-01) | 9 | **a work item was deleted by measurement, not by reading** — the catalog solved a real problem that turned out to cost 110 ms |

Four cases are instructive for anyone picking up a later wave. Read as a
sequence they describe an escalating failure mode: a claim that went stale, a
claim that was overtaken, a claim that was never true, and a claim that was
true but did not matter.

**SBX** — the PRD's central claim ("swarm clones are the least-supervised
bash path, because `constructMember` never reassigns `WorkDir`") was simply
true when written and simply false four minors later. Nothing about reading
the PRD alone would reveal that.

**CTX** — the sharpest illustration so far, because the audit *subtracted*
work rather than adjusting it. CTX-3 (read dedup) and the status-bar half of
CTX-5 were already in the tree, so building to the draft would have
re-implemented solved problems. Worse, the draft's headline feature name —
"microcompaction", defined as *summarize the oldest span with an LLM call* —
was already taken by a shipped `microCompact` that makes **no LLM call** and
does something else entirely; adopting the draft's vocabulary would have
silently redefined a `/compact` menu entry, an event payload, a log line and
a persisted snapshot field. The audit also surfaced two live defects that the
draft's own safety rules exposed but nobody had noticed: auto-compaction was
erasing error *text* while keeping the error *flag*, and the cheap tier could
only run once per session. Neither was in scope as written; both were
repaired.

**MEM** — the one that should change how these drafts are read. Its premise
was not stale, it was **false at the time of writing**: the draft describes
recall as "loading the index into the prompt … blind to phrasing", but
`internal/memdir/recall` has run a model-driven semantic selection since v1.4,
and the draft's own worked example ("the deploy pipeline" never matching "CI
release flow") is exactly what an LLM reading descriptions gets right. MEM-6
was specified backwards — it proposed adding a global memory scope to a store
that `memdirpaths.go` explicitly documents as *already* global — and MEM-4
would have shipped strictly less than what already existed, narrowing per-turn
recall to session-open. Roughly half the wave, as drafted, was solving
problems evva did not have.

**SES** — the cheapest lesson to act on. Half its premise was already
shipped (`/resume` has had a paginated picker for minors; `ResumeSnapshot`
already rebuilds persona, provider, model, tools, the checkpoint namespace and
the workflow board), so SES-2 and SES-4 became extensions rather than
construction, and the genuine gap turned out to be the *pre-TUI* entry — from
a fresh terminal there was no way in at all. But the item worth remembering is
SES-1's catalog: a coherent, buildable design solving a real problem
(enumeration cost) that **no amount of code-reading would have refuted**.
Parsing every snapshot on a real machine — 93 sessions, 14 MB, 14 workdirs —
measures 110 ms, so the index would have bought ~100 ms in exchange for a
second store and the drift class the PRD's own risk table listed. It was cut,
and the fields it would have held moved into the snapshot envelope.
**Audit passes should measure, not only read.**

The lesson is not "the drafts decay". It is that these 32 concept PRDs were
written from the **roadmap's model of evva**, in one pass, without reading the
code — so a wave can be wrong on arrival, not merely out of date, and a wave
can be right on arrival and still not worth building. The audit gate is what
catches both; MEM is the case to cite for the first and SES for the second.

Full sequencing lives in long-range §3 (backbone) and §3b (batch 2).

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

- ~~**Needs a decision:** `swarm-worktree-isolation.md`~~ — **resolved 2026-07-29.** Built (SWT-1..8), re-slotted v1.9 → **v1.12**; shipped stable in v1.12.0.
- ~~**`mcp-server-mode.md`**~~ — **resolved 2026-07-30.** Built (MCP-1..5); shipped stable in v1.13.0.
- ~~**`secret-redaction.md`**~~ — **resolved 2026-07-30.** Built (SEC-1..6); shipped stable in v1.14.0. First long-range concept PRD to clear the audit gate.
- ~~**`sandbox-isolation.md`**~~ — **resolved 2026-08-01.** Was **blocked, not merely unstarted**: its rollout (§8) needs a working `docker`/`podman` and the build machine had none, which is why W3 "Safety" shipped half. Docker **28.1.1** is now installed and verified running containers, so the wave was picked up: SBX-1..7 built and merged to `dev` at `023dc87` (PR #66), acceptance criteria met against real containers, claims **v1.15**. W3 is complete.
- ~~**`agent-eval-harness.md`**~~ — **resolved 2026-08-01.** Built (EVAL-1..7) and merged to `dev` at `e8f8089` (PR #67), claims **v1.16**. This was W4 and the last never-started wave from the 2026-07 design reviews.
- ~~**`memory-semantic-recall.md`**~~ — **resolved 2026-08-01.** Built (MEM-1..7), claims **v1.18**; merged to `dev` at `db47629` (PR #69). W6, gated on W5 by long-range §7 and picked up the moment it landed.

- ~~**`session-tree.md`**~~ — **resolved 2026-08-01.** Built (SES-1..7), claims **v1.19**; PR open against `dev`. W7, no unmet dependency. Its audit cut SES-1's catalog on measured evidence and found SES-2/SES-4 half-shipped; long-range §5's "session store backend interface" rider was parked for v2.0 because its premise (a jsonl store) was wrong and the catalog it was meant to ride no longer exists.
- ~~**`context-engine.md`**~~ — **resolved 2026-08-01.** Built (CTX-1..7) and merged to `dev` at `c02eb8e` (PR #68), claims **v1.17**. **W5 — the first Horizon 2 wave**, and the first pickup where the audit gate deleted work rather than merely adjusting it: CTX-3 and half of CTX-5 were already shipped. Unblocks W6 (memory semantic recall).
- ~~**Old, non-swarm, never slotted**~~ — **cleared.** All three shipped stable in v1.11.0.
- ~~**Swarm v1.11+ candidates**~~ — **cleared.** All six from the 2026-07-04/05 design review shipped stable in v1.11.0 (blackboard, cost accounting, doctor, outbound notifications, TUI attach, verify checks).
- **Nothing designed remains unbuilt.** Every PRD in §2 is now stable or built-and-awaiting-a-cut. Every further wave comes from the long-range set below, which means every further piece of work starts with an audit pass.
- **Un-graduated explore spikes** (no PRD at all yet, just a hypothesis): EX-2 (remote persona — graduation path drafted as `swarm-federation.md`), EX-3 (leader takeover — drafted as `swarm-leader-takeover.md`), EX-5 (wake jitter). **EX-4 (replay/eval harness) is now partly answered:** the shipped `pkg/evalharness` provides the scoring layer EX-4 would otherwise have had to invent, so if EX-4 is ever built its remaining scope is swarm event-log *capture* only — see the boundary in `agent-eval-harness.md` §3.3.
- **Long-range concept PRDs** (2026-07-06, **29 still unbuilt** of the original 32 — see the note under §2 and [long-range.md](long-range.md) §3 for the full sequenced list): concept-grade drafts for horizons W7–W35; each requires a live-source audit pass before build. The nearest are the rest of H2's TUI quartet — session-tree (W7) and steering-v2 (W8) — which long-range §4 argues should be batched with W5/W6 (now built), since they share the status bar, overlays and session store. **W7 is the natural next pickup**; it has no unmet dependency in long-range §7.

Nothing in the remaining backlog has claimed a wave/minor. Per `CLAUDE.md`,
that happens at planning time when an operator picks it up — this document
doesn't presume sequencing.

---

## 6. Keeping this doc honest

Individual PRD files' `Status:` headers are **not** reliably updated after
shipping — most of the 24 stable PRDs in §2 still read "proposed" despite
having been live in `main` for weeks or months. Chasing 20+ files on every
release isn't worth it. The exception worth keeping is the *audit-pass
record*: waves that went through the concept → build gate (SEC, SBX, EVAL, CTX, MEM, SES)
carry their corrections in their own headers, because that delta is the only
place the reasoning for a design change survives. Going forward:

- Treat **this file** as the single source of truth for "is X done".
- Refresh it as part of the release playbooks in `CLAUDE.md` (`release` /
  `pre-release feature`) — a two-minute diff against the CHANGELOG entry being
  promoted/cut, not a re-audit.
- Don't bother hand-syncing each PRD's own header when it ships; it's not
  worth the churn and this file is the thing people should actually read.
