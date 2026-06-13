# The shipped examples, deconstructed

The evva repo ships three complete, ready-to-run swarms under
[`examples/evva-swarm/`](https://github.com/Johnny1110/EVVA/tree/main/examples/evva-swarm). Each is
pure config — a manifest, agent definitions, and one root-level shared-knowledge doc — and their
shapes are deliberately different. **The fastest way to build a swarm is to copy the closest example
and adapt it** (rename members, rewrite personas, adjust the manifest).

| Example | Roster | Shape | Demonstrates |
| --- | --- | --- | --- |
| `werewolf-swarm/` | 1 god + 12 players | turn-based game | strict turn control, private-message hygiene, pure `send_message` (no task board) |
| `world-football/` | 1 director + 7 specialists | six-stage pipeline | task-board dispatch, stage gates, parallel collection, multi-round debate |
| `code-review-swarm/` | 1 lead + 4 members | fan-out + verify | parallel review, leader-side dedup, adversarial verification |

Each ships with `.vero/` and runtime outputs git-ignored — the example itself is just config.

---

## `werewolf-swarm` — turn-based, minimal tools

**Roster:** `god` (leader) + `player-1` … `player-12` (workers). 4 wolves, seer, witch, hunter, guard,
4 villagers — each identity written only in that player's `system_prompt.md`, invisible to the others.

**What to steal from it:**

- **No task board at all.** A conversation game has no "work units," so the god coordinates purely
  through `send_message`. Not every swarm needs the ledger — match the mechanism to the work.
- **Players have only `read`.** They act entirely through the injected `send_message`. The smallest
  tool set that does the job means players can't wander off-script. (See
  [../tools/recipes-by-role.md](../tools/recipes-by-role.md#turn-based-participant-minimal).)
- **Strict turn control + information hygiene** live in the god's persona: one speaker at a time;
  private night-phase info goes point-to-point, never as a broadcast. Public rules sit in a shared
  `RULES.md` every player can `read`.
- **`bypass` + high `max_iterations` (120)** because a full round (night actions + 12 speeches + a
  vote) is a long, high-message run that shouldn't stall for approvals.

**Adapt it for:** negotiations, simulations, tabletop-style games, any one-at-a-time protocol.

---

## `world-football` — pipeline with stage gates and a debate

**Roster:** `director` (leader) + `planner`, `collector-1/2/3`, `qa`, `analyst`, `predictor`.
Pipeline order: planner → three collectors (parallel) → qa → analyst → analyst↔predictor debate →
predictor's final prediction.

**What to steal from it:**

- **Task-board dispatch with verified stage gates.** The director opens each stage only after the
  previous one is verified; a shared `PIPELINE.md` defines the six stages and the hand-off format
  every member reads.
- **Parallel *within* a stage.** The three collectors run at once (the one parallel exception); every
  other stage is one task at a time.
- **A debate stage.** `analyst` and `predictor` exchange several rounds before the prediction is
  locked — a debate sub-pattern embedded in a pipeline.
- **Tool sets matched to stage:** collectors/analyst run `bash` (`curl`/`python`) to gather and model;
  qa writes `xlsx`. `bypass` + `max_iterations: 100` for the long, tool-heavy run.

**Adapt it for:** ETL, research pipelines, report generation, anything multi-phase with hand-offs.

---

## `code-review-swarm` — parallel fan-out + adversarial verification

**Roster:** `lead` (leader) + `reviewer-correctness`, `reviewer-security`, `reviewer-quality`,
`verifier`. (Walked through in detail below — it's the cleanest template for review/audit work.)

**Flow:** P1 three reviewers in parallel → P2 the leader dedups, then the verifier re-reads the code
and tries to *refute* each finding → P3 the leader writes the report.

**What to steal from it:**

- **The leader doesn't find problems or draw conclusions** — it hosts: splits the goal into phases,
  dispatches, collects, arranges verification, dedups, and assembles the report. Its persona is the
  longest file; the reviewers' are short.
- **Leader-side dedup before verification** — same location + same root cause merge into one finding
  (fuller description kept, higher severity wins) *before* the verifier spends tokens on them.
- **Adversarial verification** — the verifier's job is to refute, not to agree; the leader neither adds
  its own findings nor overrules a verdict.
- **Everything is read-only toward the target repo** — reviewers and verifier have `read`, `grep`,
  `glob`, and read-only `bash` (git diff/log/blame); all output is written into the swarm's own
  `review/` folder, never the target.
- **A `review-state.md` with action-bound update triggers** is the leader's memory across the long run.

**Adapt it for:** code review, security audits, document review, any "multiple angles then a skeptical
pass" job.

---

## How to adapt an example (the procedure)

1. **Copy the closest folder** to the user's workdir:
   `cp -r examples/evva-swarm/code-review-swarm my-review` (or download it from the repo).
2. **Rename members** to fit the goal (folder under `agents/{main,sub}/` + the `agent:` entry in the
   manifest must match).
3. **Rewrite each `system_prompt.md`** for the new domain — keep the *structure* (team map,
   coordination policy, state-file format for the leader), change the content.
4. **Adjust the shared doc** (`REVIEW-GUIDE.md` / `PIPELINE.md` / `RULES.md`) to the new rules/format.
5. **Tune the manifest**: permission mode, `max_iterations`, budgets, any schedules.
6. **Register and test**: `evva swarm .`, then send the leader a first goal.

> **Operational note:** a member's `profile.yml` (model, effort) is fixed when the space is created.
> Changing it later requires `evva swarm reset <ref>` (or re-register) to take effect.

## See also

- Which shape fits the user's goal: [topologies.md](topologies.md).
- The coordination habits these examples embody: [coordination.md](coordination.md).
- Running and adapting: [../operations/running.md](../operations/running.md).
