# evva Long-Range Roadmap — 2026 H2 → 2028

> **Purpose:** the direction document. Where [overview.md](overview.md) answers
> *"what shipped?"*, this file answers *"what's next, in what order, and why"* —
> a 1–2 year horizon plan covering both flagships (**evva tui**, **evva swarm**),
> the interop surface, and the architecture-optimization track that ends in
> **v2.0**.
> **Drafted:** 2026-07-06, `dev @ 8348ba7` (v1.10.0-beta.1 + structured output
> + edit-diagnostics sync already merged to dev).
> **Authority split:** ship-status truth stays in [overview.md](overview.md);
> the wave → minor map in `CLAUDE.md` remains the only place a minor is
> *claimed* (at planning time, per the release workflow). This document
> *proposes* sequencing; it never claims versions by itself.

---

## 1. How to consume this document

Every wave below follows the same lifecycle:

1. **Concept PRD** (linked in §3) — written now, design-complete but **not
   audited** against live source. Headers carry
   `Status: proposed — long-range concept draft`.
2. **Audit pass** — when an operator picks the wave up, run a live-source
   audit at the then-current `dev` commit, pin `file:line` references, adjust
   the design to what the code has become. This is non-negotiable: the
   roadmap-provenance rule exists because line numbers drift and features
   land in between (three concept-stage assumptions in this very planning
   pass were already stale — see the "verified current state" notes inside
   the PRDs).
3. **Claim the minor** — append the wave → minor row to `CLAUDE.md`.
4. Build on `feature/*` → `dev`, ship via `pre-release feature`, promote.

Tentative minors below are **ordinals, not promises** — the demonstrated pace
(7 minors between 2026-05 and 2026-07) means waves may pull forward, and an
operator may reorder freely. Only dependency edges (§7) are real constraints.

---

## 2. North-star themes

Five themes explain every wave. When in doubt about scope, cut toward the
theme.

| # | Theme | One-liner | Flagship served |
|---|---|---|---|
| **T1** | **Never lose the thread** | Context, memory, and sessions survive anything — long runs, restarts, weeks-later resumption | tui |
| **T2** | **Run unattended** | The swarm earns the right to work while the operator sleeps: verification, isolation, resilience, self-repair | swarm |
| **T3** | **Open at every seam** | evva is a good citizen of every ecosystem: MCP in both directions, editors (ACP), CI, the browser | both |
| **T4** | **Spend wisely, prove it works** | Cost intelligence, model routing, and behavioral regression evals — the boring trust infrastructure | both |
| **T5** | **One runtime, many personas → an ecosystem** | Personas/skills become shareable artifacts; the SDK freezes into a platform others build on | SDK |

---

## 3. Horizon plan

Status legend: 📄 **existing PRD** (already in `PRD/`, some audit-grade) ·
🆕 **concept PRD** (written 2026-07-06 in this planning pass, needs audit
before build).

### Horizon 1 — 2026 Q3: close the committed surface

Finish what the 2026-07 design reviews already put on the table before
opening new fronts.

> **✅ Horizon 1 closed 2026-08-01.** All four waves are built. The minors
> drifted from the tentative column — MCP server mode was pulled forward from
> W11 and took v1.13, pushing SEC to v1.14, SBX to v1.15 and EVAL to v1.16 —
> but nothing was dropped. Every wave from here comes from H2 or the batch-2
> set, which means every wave from here starts with an audit pass (§1 step 2);
> see overview.md §2 for what the four completed audits actually cost. The
> first post-H1 pickup was **W5 (context engine, v1.17)**, and its audit
> deleted two of seven work items outright.

| Wave | Tentative minor | Scope (tickets) | PRDs | Theme |
|---|---|---|---|---|
| **W1 — Swarm operations** ✅ | v1.11 | swarm-doctor, swarm-cost-accounting, swarm-outbound-notifications, swarm-tui-attach | 📄 [swarm-doctor](PRD/swarm-doctor.md) · 📄 [swarm-cost-accounting](PRD/swarm-cost-accounting.md) · 📄 [swarm-outbound-notifications](PRD/swarm-outbound-notifications.md) · 📄 [swarm-tui-attach](PRD/swarm-tui-attach.md) | T2, T4 |
| **W2 — Swarm trust** ✅ | v1.12 | SWT-1..8 (worktree isolation — **resolves the empty-v1.9 anomaly**, see §9.1), swarm-verify-checks, swarm-blackboard | 📄 [swarm-worktree-isolation](PRD/swarm-worktree-isolation.md) · 📄 [swarm-verify-checks](PRD/swarm-verify-checks.md) · 📄 [swarm-blackboard](PRD/swarm-blackboard.md) | T2 |
| **W3 — Safety** ✅ | v1.14 (SEC) + v1.15 (SBX) | SBX-1..7 (sandboxed execution), SEC-1..6 (secret redaction at the LLM egress boundary) | 📄 [sandbox-isolation](PRD/sandbox-isolation.md) · 🆕 [secret-redaction](PRD/secret-redaction.md) | T2, T4 |
| **W4 — Quality loop** ✅ | v1.16 | EVAL-1..7 (transcript replay + regression scoring); shipped as a tool, NOT wired into the release playbooks — that remains the operator's call per the PRD's §7 open question 3 | 📄 [agent-eval-harness](PRD/agent-eval-harness.md) | T4 |

### Horizon 2 — 2026 Q4: TUI excellence (flagship #1 deepens)

The solo terminal experience becomes the best-in-class reason to choose evva.

> **✅ Horizon 2 closed 2026-08-04.** All four waves are built (v1.17–v1.20).
> The tentative minors drifted by two — H1 ran long (MCP pulled forward, then
> SBX and EVAL) — but the W5 → W6 dependency edge in §7 held: W6 was picked up
> the moment W5 landed. Two of these audits are worth reading before costing
> any later wave, because they fail in opposite directions. **W7** deleted a
> work item by *measuring* it rather than by reading the code (§0.4 of that
> PRD). **W8** did the reverse: it found that the two pieces of infrastructure
> the design planned to build *on* — a per-phase cancellation seam, and a free
> keybinding for the new gesture — did not exist, so the audit **added** the
> largest item in the wave rather than removing one.

| Wave | Tentative minor | Scope (tickets) | PRDs | Theme |
|---|---|---|---|---|
| **W5 — Context engine** ✅ | v1.17 (was v1.15) | CTX-1..7: prune-with-tombstones, span compaction, `/context` overlay, pinning. Read dedup and the status-bar gauge turned out to be **already shipped** — see the PRD's §0 audit record | 🆕 [context-engine](PRD/context-engine.md) | T1 |
| **W6 — Memory intelligence** ✅ | v1.18 | MEM-1..7: `memory_search` (the real gap — recall was push-only), optional `llm.Embedder` + Ollama/OpenAI backends, hash-diffed vector sidecar, pre-filter for the per-turn selector, `origin` provenance. Cross-project scope turned out to be **already shipped** — the store was always global; see the PRD's §0 | 🆕 [memory-semantic-recall](PRD/memory-semantic-recall.md) | T1 |
| **W7 — Session tree** ✅ | v1.19 | SES-1..7: `evva resume` / `-c`, fork, `/title`, pin/delete/all-workdirs in `/resume`, `evva sessions prune`, self-contained HTML export. The catalog was **measured out of the design** and the in-TUI picker turned out to be **already shipped** — the gap was the pre-TUI entry; see the PRD's §0 | 🆕 [session-tree](PRD/session-tree.md) | T1 |
| **W8 — Steering v2** ✅ | v1.20 | STE-1..6: interrupt-grade steering — a per-phase cancel-with-cause seam (which **had to be built**; the loop owned one context per run), `Ctrl+G` interject, partial-answer capture, paired interrupted tool results, `/queue`, swarm `urgency:"interject"`. The draft's "double-Esc abort" did not exist — a single Esc has always aborted; see the PRD's §0 | 🆕 [steering-v2](PRD/steering-v2.md) | T1 |

### Horizon 3 — 2027 H1: model & modality intelligence + interop

| Wave | Tentative minor | Scope (tickets) | PRDs | Theme |
|---|---|---|---|---|
| **W9 — Model intelligence** | v1.21 (next) | RTE-1..8: provider failover chains, role-tier routing, budget enforcement, cache parity beyond Anthropic | 🆕 [model-routing](PRD/model-routing.md) | T4 |
| **W10 — Vision completion** | v1.22 | VIS-1..7: TUI image paste/attach, screenshot tool, provider vision parity, see-then-verify flows | 🆕 [vision-completion](PRD/vision-completion.md) | T1, T3 |
| **W11 — Interop A: protocols** | v1.21 | MCP server mode + ACP-1..6 (Agent Client Protocol — evva inside Zed and other ACP editors) | 📄 [mcp-server-mode](PRD/mcp-server-mode.md) · 🆕 [acp-editor-integration](PRD/acp-editor-integration.md) | T3 |
| **W12 — Interop B: CI** | v1.22 | CIH-1..7: GitHub Action, `@evva` PR review bot, SARIF/JSON findings, budget-capped headless runs | 🆕 [ci-headless-runner](PRD/ci-headless-runner.md) | T3 |
| **W13 — Browser tools** | v1.23 | BRW-1..7: CDP-driven `browser_*` tool family (navigate/read/screenshot/interact/console) | 🆕 [browser-tools](PRD/browser-tools.md) | T3 |

### Horizon 4 — 2027 H2: Swarm 2.0 — distributed, resilient, self-directing

| Wave | Tentative minor | Scope (tickets) | PRDs | Theme |
|---|---|---|---|---|
| **W14 — Federation** | v1.24 | FED-1..8: remote members over the bus (WS transport, join tokens, partition handling) — **EX-2 graduates** | 🆕 [swarm-federation](PRD/swarm-federation.md) | T2 |
| **W15 — Resilience** | v1.25 | RES-1..6: leader health, deputy promotion, takeover protocol, handback — **EX-3 graduates** | 🆕 [swarm-leader-takeover](PRD/swarm-leader-takeover.md) | T2 |
| **W16 — Workflow scripts** | v1.26 | WFS-1..8: deterministic orchestration — the swarm's task graph (DWF) exposed to solo evva as declarative `workflow.yml` runs | 🆕 [workflow-scripts](PRD/workflow-scripts.md) | T2, T5 |
| **W17 — Gardener** | v1.27 | GRD-1..6: idle-time repo gardening on the dream gate — chores run in sandboxed worktrees, morning digest, never touches shared branches | 🆕 [gardener](PRD/gardener.md) | T2 |

### Horizon 5 — 2028: platform

| Wave | Tentative minor | Scope (tickets) | PRDs | Theme |
|---|---|---|---|---|
| **W18 — Persona ecosystem** | v1.28 | PER-1..7: persona/skill packs, git-based index, `evva persona install`, hash-pinned trust | 🆕 [persona-ecosystem](PRD/persona-ecosystem.md) | T5 |
| **W19 — v2.0: SDK freeze + runtime hardening** | **v2.0** | ARC-1..10: typed events, middleware turn pipeline, claude/glm engine dedupe, typed tool metadata, config layering, observability export, API freeze + migration guide | 🆕 [arch-v2](PRD/arch-v2.md) | T5 |

---

## 3b. Batch-2 waves (W20–W35) — the second planning pass

The 19 waves above (W1–W19) are the **backbone** — one coherent spine from
"close the committed surface" to v2.0. This second batch (16 more concept
PRDs, drafted 2026-07-06) **interleaves** with that spine rather than
extending past it: these are waves the operator slots into the horizons
where their dependencies are met, not a rigid W20-then-W21 sequence. Think
of W1–W19 as the load-bearing structure and W20–W35 as the rooms — each
tagged with the horizon whose infrastructure it needs.

A sixth theme emerges from this batch and is worth naming:

| # | Theme | One-liner | Flagship served |
|---|---|---|---|
| **T6** | **Reach & accountability** | evva meets people where they are (chat, editors, phones) and can prove what it did (audit, retro, replay) | both |

### Developer-tool depth (the IDE-grade trio + git)

| Wave | Suggested horizon | Scope | PRD | Theme |
|---|---|---|---|---|
| **W20 — Onboarding & Doctor** | H1+ (early — makes everything else reachable) | DOC-1..6: `evva doctor` check registry + first-run onboarding + refuse-with-guidance | 🆕 [onboarding-doctor](PRD/onboarding-doctor.md) | T4 |
| **W22 — Diff review UI** | H2 (TUI) | DRV-1..6: hunk-level edit review + propose mode (virtual changeset) | 🆕 [diff-review-ui](PRD/diff-review-ui.md) | T1 |
| **W23 — Plan mode v2** | H2 (TUI) | PLN-1..7: researched plans, plan artifacts, plan→workflow/swarm handoff | 🆕 [plan-mode-v2](PRD/plan-mode-v2.md) | T1 |
| **W24 — Test watch loop** | H2 (TUI) | TWL-1..6: edit-triggered continuous verification, drain/inline/badge delivery | 🆕 [test-watch-loop](PRD/test-watch-loop.md) | T1 |
| **W25 — Git intelligence** | H2/H3 | GIT-1..7: history-as-context, commit craft, conflict assistance | 🆕 [git-intelligence](PRD/git-intelligence.md) | T1 |
| **W28 — Tree-sitter code intel** | H3 | TSI-1..6: server-less structure tier, repo-map fallback, `code_outline` | 🆕 [treesitter-code-intel](PRD/treesitter-code-intel.md) | T1 |
| **W29 — Multi-root workspaces** | H3 | WSP-1..7: one session, several repos, per-root config/LSP/git/permissions | 🆕 [multi-root-workspaces](PRD/multi-root-workspaces.md) | T1 |
| **W30 — DAP debugger** | H3 | DBG-1..7: Debug Adapter Protocol client, breakpoints/step/inspect | 🆕 [dap-debugger](PRD/dap-debugger.md) | T1 |

### Economics & model reach

| Wave | Suggested horizon | Scope | PRD | Theme |
|---|---|---|---|---|
| **W26 — Provider expansion** | H3 (with W9 routing) | PRV-1..7: conformance kit + Gemini/Bedrock/Vertex/OpenRouter | 🆕 [provider-expansion](PRD/provider-expansion.md) | T4 |
| **W27 — Batch API lane** | H3 (after W9) | BAT-1..6: half-price async lane for dream/evals/gardener/titles | 🆕 [batch-api-background](PRD/batch-api-background.md) | T4 |

### Reach & accountability (T6)

| Wave | Suggested horizon | Scope | PRD | Theme |
|---|---|---|---|---|
| **W21 — Swarm templates** | H1+ (after W1/W2) | TPL-1..7: `evva swarm init`, five team recipes, templatize | 🆕 [swarm-templates](PRD/swarm-templates.md) | T5, T6 |
| **W31 — Replay Lab** | H4 (after W4/W7) | RPL-1..7: interactive transcript inspector + what-if branching | 🆕 [replay-lab](PRD/replay-lab.md) | T4, T6 |
| **W32 — Human-in-the-swarm** | H4 (after W1) | HUM-1..7: people as roster members, task inbox, SLA/escalation | 🆕 [human-in-the-swarm](PRD/human-in-the-swarm.md) | T2, T6 |
| **W33 — Swarm retrospective** | H4 (after W6/W21) | RET-1..6: post-run learning → memory + template feedback | 🆕 [swarm-retrospective](PRD/swarm-retrospective.md) | T2, T6 |
| **W34 — Audit trail** | H5 / post-v2.0 (after ARC-1) | AUD-1..6: tamper-evident hash-chained action log | 🆕 [audit-trail](PRD/audit-trail.md) | T4, T6 |
| **W35 — Chat bridges** | H5 / post-v2.0 | CHB-1..7: personas over Telegram/Slack/Discord, restricted profiles | 🆕 [chat-bridges](PRD/chat-bridges.md) | T5, T6 |

**Sequencing note.** Batch-2 numbers (W20–W35) are **labels, not order** —
several are deliberately early (W20 doctor, W21 templates), several are
gated late (W34 audit needs ARC-1; W35 bridges needs the full safety
stack). The real constraints are the dependency edges in §7, now extended
for both batches. When the operator finishes a backbone horizon, the
batch-2 waves tagged for that horizon become eligible.

---

## 4. Why this order

- **H1 first because it's already designed.** Six swarm PRDs + sandbox + eval
  were written in the 2026-07 design reviews with audit-grade provenance;
  they decay fastest if left unshipped. W2 also retires the v1.9 bookkeeping
  anomaly (§9.1).
- **Safety (W3) before autonomy (W14–W17).** Sandboxing and secret redaction
  are prerequisites for everything that later runs with less supervision —
  gardener and federation both list them as dependencies.
- **Eval harness (W4) early, on purpose.** Every later wave changes prompts
  and tool schemas; landing the regression harness in 2026 means the other
  15 waves ship with a behavioral safety net instead of "ship and watch".
- **TUI horizon (H2) is one coherent story** — context engine → memory →
  sessions → steering are four faces of T1 and share surfaces (status bar,
  overlays, session store). Batching them keeps the TUI churn in one quarter.
- **Routing (W9) before vision/browser/CI** — failover and budget enforcement
  should exist before waves that add expensive multimodal calls and
  unattended CI spend.
- **Federation (W14) before takeover (W15)** — the takeover protocol should
  be designed against the remote-member reality, not retrofitted to it.
- **Workflow scripts (W16) after DWF has soaked** — it deliberately reuses
  the shipped task-graph semantics (`depends_on`, verify policy, fan-out)
  rather than inventing a second orchestration model.
- **v2.0 last and alone.** The major is a promise (semver freeze), not a
  feature. Its refactors (ARC) are staged as riders on earlier waves where
  possible (§5) so W19 is mostly *finishing*, not *starting*.

---

## 5. Architecture-optimization track (ARC riders)

Standing refactors that don't need their own wave — each rides the earliest
wave that touches its seam, and [arch-v2](PRD/arch-v2.md) collects whatever
is left. Full detail lives in that PRD; the riding plan:

| Refactor | Rides | Rationale |
|---|---|---|
| `llm.Client` decorator/middleware shape (retry, failover, metering as wrappers) | W9 (routing) | routing *is* the first decorator; build the shape once |
| Typed tool-result metadata registry (replace opaque `Metadata` type-asserts) | W10 (vision) | image payloads are the forcing function for typed metadata |
| Event schema versioning (`pkg/event` payloads become externally consumable) | W11 (interop) | MCP-server/ACP clients are the first external event consumers |
| Config layering (global → project `.evva/` → session) | W13 or earlier | every wave adds knobs; layering stops the flag sprawl |
| claude/glm engine dedupe (glm is a documented self-contained copy) | any H3 wave touching `pkg/llm` | cheapest debt in the codebase; pure consolidation |
| ~~Session store backend interface (jsonl today, pluggable later)~~ | ~~W7~~ → W19 | **premise was wrong** — the store is one JSON file per session, not jsonl, and W7's audit cut the catalog, so nothing new opened this seam. Parked for the v2.0 sweep |

---

## 6. Explore spikes (EX-7 … EX-13)

Hypotheses, deliberately *not* PRDs — same contract as EX-1..6: a spike
graduates to a ticket family only after a validation note. Continuing the
Veronica numbering:

- **EX-7 — Voice input.** Local whisper.cpp dictation into the TUI prompt.
  Validate: is push-to-talk in a terminal actually pleasant?
- **EX-8 — Shadow mode.** Opt-in shell observer (history + exit codes only):
  evva notices you've failed the same command three times and offers help.
  Privacy story must be airtight before any code.
- **EX-9 — Swarm-of-swarms.** A leader registers as a *member* of a parent
  swarm — org-chart composition. Validate on a real two-team task first.
- **EX-10 — Self-tuning prompts.** The eval harness (W4) scores prompt
  variants; a dream-style background pass proposes prompt diffs with eval
  evidence attached. Human merges.
- **EX-11 — Hosted sandbox backends.** E2B/Modal-style remote `sandbox_runtime`
  values (already flagged in sandbox-isolation §7). Waits on a concrete
  "no local Docker" scenario.
- **EX-12 — Small-model harness ("evva-lite").** A reduced prompt/tool
  surface tuned for 7–32B local models via Ollama — fewer tools, tighter
  descriptions, aggressive structured output. Validate: can Qwen-32B finish
  a real ticket under it?
- **EX-13 — Collaborative attach.** Two humans, one session: the swarm
  web console pattern brought to the solo TUI (read-only first).

Batch-2 spikes (EX-14+), same contract:

- **EX-14 — Structural edits.** Editing through the tree-sitter AST (W28) —
  rename-in-scope, extract-function as tools, not text patches. Validate:
  do syntax-tree edits beat well-targeted text edits often enough to justify
  the surface?
- **EX-15 — Semantic code search.** Embedding index over code (reusing W6's
  Embedder + W28's syntax chunker) — `code_search "where do we validate
  auth"`. Validate: does it beat grep+repo-map on real "where is X" questions?
- **EX-16 — Ambient voice pairing.** EX-7 grown up: continuous dictation +
  TTS readback for hands-free review of long autonomous runs. Gated on EX-7.
- **EX-17 — On-device model distillation.** Fine-tune/adapt a small local
  model on an operator's own accepted-diff history (privacy-preserving,
  fully local) for the evva-lite harness (EX-12). Far-future; validate the
  data volume is even sufficient first.

---

## 7. Dependency edges (the real constraints)

Backbone (W1–W19):

```
W2 (worktrees) ──► W14 (federation: remote checkouts assume worktree fabric)
W3 (sandbox)   ──► W17 (gardener: unattended chores run sandboxed)
W3 (secrets)   ──► W12 (CI: redaction before logs leave the machine)
W4 (evals)     ──► W19 (v2.0: freeze needs behavioral regression proof)
W4 (evals)     ──► EX-10 (self-tuning prompts scores against the harness)
W5 (context)   ──► W6 (memory recall injects via the context budget)
W7 (sessions)  ──► W17 (gardener digests are session artifacts)
W9 (routing)   ──► W10/W12/W13 (budget rails before expensive modalities)
W10 (vision)   ──► W13 (browser screenshots need image ingestion)
DWF (shipped)  ──► W16 (workflow scripts reuse task-graph semantics)
W14 (fed)      ──► W15 (takeover designed against remote reality)
W18 (packs)    ──► W19 (pack manifest is part of the frozen surface)
```

Batch-2 (W20–W35) edges into the backbone:

```
W9 (routing)   ──► W26/W27 (provider expansion & batch lane extend the decorator/route shape)
W10 (vision)   ──► W30 (DAP inspect digests reuse image handling? no — but W10 unblocks screenshot-in-review)
W4 (evals)     ──► W31 (Replay Lab reuses the replay engine + request retention)
W7 (sessions)  ──► W31 (what-if forks are session-tree children)
W1 (swarm-ops) ──► W32/W33 (human members & retros ride notifications + cost accounting)
W6 (memory)    ──► W33 (retro routes lessons into semantic recall)
W21 (templates)──► W33 (retro proposes template diffs)
W17 (gardener) ──► W33 (both are third consumers of the generalized idle-gate)
ARC-1 (events) ──► W34 (audit log is an integrity projection of the typed event stream)
W3 (safety)    ──► W35 (chat bridges need the full redaction + restricted-profile stack)
W25/GIT-3      ◄─► W22/DRV-1 (git-intelligence & diff-review SHARE the hunk/patch engine — whichever lands first builds it)
W28 (treesitter)──► EX-14/EX-15 (structural edits & semantic search build on the syntax tier)
W16 (workflows)◄─► W23 (plan-mode-v2 compiles plans INTO workflows)
DWF (shipped)  ──► W23 (plan-mode-v2 compiles plans INTO swarm task graphs)
```

Anything not listed above is reorderable at will. The one hard
build-order coupling worth calling out: **the hunk/patch engine
(GIT-3 ≡ DRV-1)** is a genuine shared dependency — schedule W22 and W25
aware of each other so it's built once, not twice.

---

## 8. Governance

- **This file** = direction truth. Revise at horizon boundaries (roughly
  quarterly) — not on every ship.
- **[overview.md](overview.md)** = ship-status truth, refreshed per release
  as already established. The **32** concept PRDs from these two planning
  passes (16 backbone W1–W19 + 16 batch-2 W20–W35) are tracked there as one
  line item, not 32 rows, until they claim waves. Five have —
  secret-redaction (v1.14), context-engine (v1.17),
  memory-semantic-recall (v1.18), session-tree (v1.19) and steering-v2
  (v1.20) — leaving 27.
- **`CLAUDE.md` wave map** = version truth. A wave's row is appended only at
  pickup, never by this document.
- **Concept → build gate:** no concept PRD may be handed to an implementing
  agent without the audit pass (§1 step 2). Concept PRDs deliberately cite
  packages and shipped behaviors, not line numbers. Seven passes have now run
  (SEC, SBX, EVAL, CTX, MEM, SES) and the gate has changed the design every
  time; overview.md §2 tabulates what each one cost.

  **SES adds a second failure mode worth naming: a work item can be
  unjustified rather than wrong.** Its catalog was coherent, buildable, and
  solved a real problem — enumeration cost — that turned out to measure
  110 ms on a real store. Nothing in the code contradicted the design; only a
  measurement did. **Audit passes should measure, not only read.**

  **STE adds a third: a draft can be wrong about the ground it stands on.**
  Its diagnosis was sharp and its three-level design shipped essentially as
  drawn — but §3 asserted "the loop already owns a per-iteration context"
  (there was exactly one context for the whole run, which is *why* abort was
  the only mid-turn gesture) and §1 listed abort as "double-Esc (existing,
  unchanged)" (a single Esc has always aborted, so the natural interject key
  was taken). Every prior audit *removed* scope; this one added the wave's
  largest item. **Cost a wave from the code, never from the PRD's account of
  the code.**

  **MEM is the case to cite if the gate is ever questioned.** Its premise was
  not stale — it was false when written: the draft described evva recalling
  memories by loading an index into the prompt, when recall has been a
  model-driven semantic selection since v1.4, and one of its work items
  proposed adding a global memory scope to a store that was already global.
  These 32 drafts were written in a single pass from the roadmap's model of
  evva rather than from the code, so **a concept PRD can be wrong on arrival,
  not merely out of date.** Audit accordingly: verify the premise before
  costing the work.

---

## 9. Standing decisions & open strategic questions

### 9.1 The v1.9 anomaly — recommendation: re-home, retire the row

`swarm-worktree-isolation` claimed v1.9 but never landed; no v1.9 tag exists.
Recommendation (option-b-plus-build): when W2 is picked up, mark the v1.9 row
in `CLAUDE.md` as *superseded — re-homed to the W2 minor*, and ship SWT-1..8
under W2's claimed minor. The wave table stays truthful and the work still
happens.

### 9.2 The v2.0 breaking-change budget

All known breaking desires spend at once in W19 (typed events, metadata
registry, config key renames incl. the dead `dangerouslyDisableSandbox`
flag, any `pkg/` signature cleanups). Nothing breaks before it; nothing
breaking is deferred past it. If a mid-roadmap wave *needs* a break, it
escalates to the operator rather than deciding locally.

### 9.3 Dependency policy under pressure

Three roadmap items strain the "minimize external dependencies" rule:
browser CDP (websockets), embeddings storage (W6), and observability export
(ARC). Each PRD carries its own dependency question, but the standing
default is: **hand-roll the protocol, shell out to the binary, or gate the
dependency behind a build tag — in that order of preference.** The Bubble
Tea precedent (a dep accepted because it IS the product surface) does not
generalize to conveniences.

### 9.4 Hosted evva

Several PRDs (sandbox backends, federation, CI) brush against "evva runs
somewhere that isn't the operator's machine". No wave commits to a hosted
product; the roadmap keeps the door open by preferring seams (join tokens,
webapi auth, structured output) that a hosted control plane could later
drive. Revisit as a possible 2028 direction after v2.0.
