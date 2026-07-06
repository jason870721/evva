# PRD — Workflow Scripts (deterministic orchestration for solo evva) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W16 / v1.26** per
> [../long-range.md](../long-range.md). Deliberately sequenced after the
> DWF task graph (v1.10) has soaked and structured output has shipped —
> it reuses both.
> **Roadmap source:** 2026-07-06 long-range planning pass. Deterministic
> multi-agent orchestration (fan-out → verify → synthesize pipelines
> with machine-controlled control flow) is the highest-leverage pattern
> in 2026 harnesses — model judgment inside the steps, deterministic
> code between them. evva has all the primitives (subagent spawn,
> worktree/sandbox isolation, structured output, DWF graph semantics)
> and no way to compose them without burning leader-model tokens on
> bookkeeping.
> **Reference source:** conceptual prior art only (orchestration DSLs);
> the design is evva-native and intentionally reuses DWF vocabulary.

---

## 1. TL;DR

A **workflow** is a declarative YAML document describing a graph of agent
steps that the *engine* — not a model — executes:

```yaml
# .evva/workflows/review.yml
name: parallel-review
inputs: { diff_ref: {type: string} }
steps:
  dimensions:
    map_over: ["correctness", "security", "performance"]
    agent: general-purpose
    prompt: "Review ${inputs.diff_ref} for ${item} issues only. ..."
    output_schema: findings.schema.json      # structured output, shipped
    isolation: worktree
  verify:
    map_over: ${steps.dimensions.results | flatten_findings}
    depends_on: [dimensions]                 # DWF vocabulary
    agent: general-purpose
    prompt: "Adversarially verify this finding: ${item}. ..."
    output_schema: verdict.schema.json
  report:
    depends_on: [verify]
    agent: main
    prompt: "Synthesize the confirmed findings: ${steps.verify.results}"
outputs: { report: ${steps.report.text} }
```

`evva run review.yml --input diff_ref=HEAD~3` (headless) or `/workflow
review` (TUI) executes it: `map_over` fans out concurrent subagents,
`depends_on` joins, schemas make step outputs typed and template-able,
isolation reuses worktree/sandbox spawning, and progress renders on the
existing task surfaces. No LLM tokens are spent deciding "what's next" —
that is the point.

The engine is **the DWF dispatch semantics extracted for solo use**: same
AND-join dependency model, same "machine dispatches, judgment stays in
the steps" philosophy — one orchestration mental model across evva tui
and evva swarm, not two.

## 2. Goals / non-goals

### Goals

- YAML spec (versioned `workflow/v1`): steps with `agent`, `prompt`
  templating (`${inputs.*}`, `${steps.*.results}`, `${item}`/`${index}`),
  `depends_on`, `map_over` (static list or expression over prior
  results), `output_schema`, `isolation`, per-step `model`/`route`,
  `retries`, `on_error: fail|skip|continue`.
- Engine: topological execution, bounded concurrency (config cap),
  fan-out spawning through the existing subagent spawner, structured
  outputs captured per step, whole-run result as JSON (stdout in
  headless — composes with CI, W12).
- Expression language kept minimal and total: path access, `flatten`,
  `filter` on schema fields, length — no loops, no user code, no Turing
  completeness. Anything smarter belongs inside a step's agent.
- Failure policy: a failed step (after retries) marks dependents skipped
  (DWF-style cascade rules), run exits with the CI exit-code contract.
- Progress: steps register on the task/todo surfaces (TUI) and emit
  events; `--dry-run` prints the resolved plan without spawning.
- Run journal: per-step prompts/results persisted under the session dir
  — resumable re-run of only-failed steps (`--resume <run-id>`).

### Non-goals (this wave)

- An embedded scripting language (goja/starlark) — the dependency-policy
  and determinism answer is "no user code in v1"; §6 keeps the door open.
- Long-lived workflows spanning restarts (runs are session-scoped;
  resume covers the crash case).
- Replacing the swarm for persistent teams — workflows are ephemeral,
  roster-less, store-less; the swarm remains the durable-team product.
  The boundary is documented: *hours vs weeks, steps vs roles*.
- Triggering (cron/webhook) — existing cron can invoke `evva run`;
  nothing new needed.
- Human-in-the-loop steps in v1 (pause-for-approval is an obvious v2;
  the permission gate still applies *inside* steps as usual).

## 3. Design sketch

- **Reuse map (the audit's checklist):** step fan-out → the subagent
  spawner (`internal/tools/meta` / `internal/agent` spawn path);
  isolation values → the existing worktree/sandbox enums; typed step
  outputs → `WithStructuredOutput`; dependency/cascade semantics →
  DWF's store-side rules, re-implemented engine-side over an in-memory
  graph (no swarm store dependency — workflows must run without a
  service); progress → the todo/task events the TUI already renders.
- **Templating:** resolved *before* spawn; a step's prompt is immutable
  once dispatched (auditability). Unresolvable references fail the
  dry-run, not the live run.
- **Concurrency:** worker-pool over ready steps; `map_over` items are
  individual schedulable units (item-level retries, item-level results
  array with `null` for failed-skipped items — callers filter).
- **Placement:** `.evva/workflows/*.yml` project-scoped +
  `<EVVA_HOME>/workflows/` global; name resolution mirrors the skills
  loader's precedence.

## 4. Work items

- **WFS-1 — Spec + parser + validator.** Schema, template resolver,
  cycle/reference validation, `--dry-run`. *Accept:* fixture corpus of
  valid/invalid workflows; dry-run prints a correct resolved plan;
  cycles and dangling refs rejected with line-anchored errors.
- **WFS-2 — Engine core.** Topo execution, worker pool, retries,
  cascade-skip, run journal. *Accept:* diamond-graph fixture (A → B,C →
  D) executes with B∥C concurrent; B failure skips D per policy; journal
  reproduces the run.
- **WFS-3 — Step spawning integration.** Subagent spawner wiring,
  isolation pass-through, structured-output capture, per-step
  model/route. *Accept:* map_over of 5 items runs ≤ cap concurrently in
  worktrees; results array is typed per the schema.
- **WFS-4 — `evva run` + `/workflow`.** Headless command (JSON out,
  exit codes) + TUI invocation with progress on task surfaces.
  *Accept:* headless run pipes into `jq`; TUI shows live step states.
- **WFS-5 — Resume.** `--resume` re-runs failed/pending steps from the
  journal, reusing completed results. *Accept:* kill mid-run, resume
  completes without re-spawning finished steps.
- **WFS-6 — Expression functions.** `flatten`/`filter`/`length` +
  tests. *Accept:* the review example's fan-out-over-findings works.
- **WFS-7 — Example library.** `examples/workflows/`: parallel review,
  test-fix loop (bounded retry step), docs sweep, migration fan-out.
  *Accept:* each example runs against a fixture repo in CI (fake LLM).
- **WFS-8 — Docs + changelog.** User-guide (en + zh-tw): spec reference,
  the swarm-vs-workflow boundary, cost expectations.

## 5. Risks

| Risk | Mitigation |
|---|---|
| YAML DSL creep toward a bad programming language | the totality rule (§2) is a hard line; rejected features get documented in the PRD as deliberate |
| Fan-out cost surprises | dry-run shows step count; concurrency cap; budget rails (W9) apply to the whole run; refuse map_over above a size cap without an explicit flag |
| Divergence from DWF semantics confuses swarm users | shared vocabulary (`depends_on`, cascade rules) is a stated acceptance criterion; docs cross-reference |
| Template injection via prior step outputs | interpolation is data-only into prompt strings (no expression evaluation inside results); schemas bound what steps can emit |

## 6. Open questions

1. Expression needs beyond `flatten/filter/length` — resist or batch
   into a v2 with real evidence?
2. Should a workflow step be able to invoke *another workflow* (one
   level, DWF-style)? Defer unless the example library hits the wall.
3. Scripted (non-YAML) workflows via an embedded interpreter — revisit
   only if the totality rule proves too confining in practice.
