# PRD — Plan Mode v2 (researched plans, plan artifacts, plan-to-execution handoff) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W23**, suggested horizon H2
> per [../long-range.md](../long-range.md) §3b.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> evva has plan mode (enter/exit tools, per-turn attachments) — the
> *gate* half of planning. What 2026 harnesses added on top is the
> *quality* half: plans built from parallel research rather than one
> context's guesswork, plans as reviewable artifacts rather than chat
> scrollback, and plans that hand off into execution structures
> instead of evaporating at approval.
> **Reference source:** `ref/src` plan-mode surfaces (the shipped port)
> + its architect/plan agent patterns; the artifact + handoff design is
> evva-native.

---

## 1. TL;DR

Three upgrades to the shipped plan mode, one theme: **a plan is a
first-class object, not a message.**

1. **Researched planning.** In plan mode, the planner can fan out
   read-only research subagents (the Explore/Plan agent kinds exist)
   over the relevant subsystems in parallel, then synthesize — the
   plan cites what was actually read (files, line-ranges) instead of
   what the model assumed. Cheap discipline, large correctness gain on
   big repos — and it's exactly the "audit pass" this roadmap's own
   concept→build gate demands, mechanized.
2. **Plan artifacts.** `exit_plan_mode` writes the approved plan to
   `.evva/plans/<slug>.md` (structured: goal, constraints, evidence
   citations, steps with target files, verify strategy, risks) and
   registers it in the session. Plans survive compaction by reference
   (the context engine pins a digest, the file holds the truth),
   survive session death (session-tree resume re-anchors), and diff
   like code when revised.
3. **Execution handoff.** An approved plan can hand off three ways:
   **(a)** continue in-session (today's flow, now with the artifact as
   standing context); **(b)** compile to a workflow (`workflow.yml`
   skeleton from the steps — W16); **(c)** compile to a swarm task
   graph (leader consumes the plan's steps as `task_create` calls with
   `depends_on` — DWF). One planning surface, three execution engines.

Plus the review loop the artifact enables: plan revisions are diffs,
approval is per-revision, and drift detection ("step 3 touched files
the plan never mentioned") becomes possible in execution.

## 2. Goals / non-goals

### Goals

- Research fan-out inside plan mode: planner-invocable read-only
  subagents with a structured findings contract (file:line evidence
  lists); plan-mode toolset already excludes mutations — the audit
  confirms subagent spawn is (or becomes) plan-mode-safe with
  read-only enforcement inherited.
- Plan artifact schema: markdown with a structured frontmatter/section
  contract (parseable enough for handoff compilation, human enough to
  review); slug/versioning; `.evva/plans/` placement (project config
  layer).
- Approval UX: exit-plan flow renders the artifact (not a chat blob),
  approval stamps a revision; subsequent re-plans append revisions
  with visible diffs.
- Handoff compilers: plan→workflow skeleton (steps→steps, verify
  strategy→schemas/policies, TODOs where human input is needed) and
  plan→swarm-brief/task-graph (steps→tasks with dependencies, evidence
  →task briefs). Both produce *drafts for review*, never auto-execute.
- Execution anchoring: in-session execution keeps a pinned plan digest
  (W5 pin mechanism) + lightweight step-state tracking (`/plan status`)
  so long sessions don't lose the thread — and the drift note fires
  when edits leave the plan's declared file set.

### Non-goals (this wave)

- Enforcing plans (drift is a *note*, not a block — judgment stays
  with the model/operator; hard enforcement is CI-profile territory).
- Multi-plan concurrency in one session (one active plan; archived
  plans remain referenceable).
- Auto-replanning on drift (the note invites it; the model decides).
- Portfolio/roadmap-level planning (this is task-scale planning; the
  docs/roadmap tree remains the human-scale instrument).

## 3. Design sketch

- **Research contract:** research subagents return
  `{questions_answered: [{q, answer, evidence: [file:line]}], surprises}`
  (structured-output, shipped) — the planner's synthesis step is
  prompted to cite evidence per plan step and to flag steps with *no*
  evidence as assumptions. Assumption-flagging is the quality
  mechanism: reviewers see exactly where the plan is guessing.
- **Artifact contract:** sections with stable headings (Goal /
  Constraints / Evidence / Steps / Verify / Risks / Out-of-scope);
  steps carry optional machine hints (`files:`, `depends:`, `verify:`)
  that compilers consume and humans ignore. Parse tolerance: a plan
  that's just prose still works — compilers do less, nothing breaks.
- **Compilation is template + extraction, not magic:** plan→workflow
  emits steps with `depends_on` from `depends:` hints (default:
  sequential), prompts from step bodies, schemas only where `verify:`
  hints name one. The output is explicitly a draft with `TODO(plan)`
  markers. Same posture for the swarm compiler.
- **Anchoring economics:** the pinned digest is small (goal + current
  step + next step); the full artifact re-enters context only on
  `/plan show` or drift events — long-execution context cost stays
  flat.

## 4. Work items

- **PLN-1 — Research fan-out.** Plan-mode-safe spawn path, findings
  contract, synthesis prompting. *Accept:* a fixture planning session
  fans out 3 researchers and produces a plan whose steps carry
  evidence citations; mutation tools provably unavailable to
  researchers.
- **PLN-2 — Artifact schema + storage.** Contract, slugging,
  revisions, `.evva/plans/`. *Accept:* exit-plan writes a
  schema-conformant artifact; a re-plan produces revision 2 with a
  renderable diff.
- **PLN-3 — Approval UX.** Artifact-based exit-plan render, revision
  stamping, `/plan show|status`. *Accept:* approval flow presents the
  artifact; status reflects step progression in a scripted execution.
- **PLN-4 — Anchoring + drift notes.** Pinned digest, step tracking,
  file-set drift detection. *Accept:* an edit outside the plan's file
  set triggers exactly one drift note; pinned digest survives
  compaction in a long fixture.
- **PLN-5 — Plan→workflow compiler.** Extraction, skeleton emission,
  TODO markers. *Accept:* a hinted fixture plan compiles to a valid
  (dry-run-passing) workflow with TODOs where hints were absent.
- **PLN-6 — Plan→swarm compiler.** Task-graph draft from steps,
  briefs from evidence. *Accept:* fixture plan yields a leader-ready
  task list with correct `depends_on` edges (validated against DWF
  semantics).
- **PLN-7 — Docs + changelog.** User-guide (en + zh-tw): the three
  handoffs, artifact conventions, assumption flags as review signal.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Research fan-out makes planning slow/expensive for small tasks | fan-out is planner-optional; prompt guidance scales research to task size; zero-research planning stays exactly as fast as today |
| Artifact bureaucracy for trivial plans | prose-tolerant contract — a three-line plan is a valid artifact; compilers and tracking degrade gracefully |
| Compilers produce plausible-but-wrong execution structures | drafts-for-review posture + TODO markers + dry-run validation; never auto-execute |
| Drift notes nag | file-set matching is coarse (dir-level option) and the note fires once per file-set violation, not per edit |

## 6. Open questions

1. Should plan artifacts be git-tracked by default (`.evva/plans/` in
   or out of .gitignore templates)? Team visibility vs repo noise —
   operator default, leaning tracked.
2. Researcher count/depth heuristics — planner-decided with a cap, or
   config? Leaning planner-decided, cap in config.
3. Does `/plan status` belong in the status bar (active step) for
   long executions? Cheap, probably yes.
