# Topologies: choosing a team shape

The user's goal implies a *shape* — how members relate and in what order they act. Pick the shape
first; it dictates the roster, the leader's coordination policy, and the tool sets. Most real swarms
are one of these five, or a small composition of them.

For each shape: when it fits, the roster, and the load-bearing policy the **leader's persona** must
carry.

---

## 1. Pipeline (stages feed the next)

```
goal ─▶ stage 1 ─▶ stage 2 ─▶ stage 3 ─▶ result
        (collect)  (process)  (publish)
```

**Fits:** data pipelines, ETL, multi-phase production where each phase consumes the previous one's
output. (Shipped example: `world-football`.)

**Roster:** a leader/director + one specialist per stage (some stages may have two members working in
parallel within the stage).

**Leader policy:** *stage gates.* "A stage is not opened until the previous stage is fully verified."
The leader verifies each stage's output against a defined format before dispatching the next. A shared
doc (e.g. `PIPELINE.md`) defines the stages and the hand-off format every member reads.

**Tools:** stage-appropriate — collectors get `web_*`/`http_request`/`read`; processors get
`repl`/`json_query`; publishers get `write`.

---

## 2. Parallel fan-out + verify

```
                 ┌─▶ reviewer A ─┐
goal ─▶ leader ──┼─▶ reviewer B ─┼─▶ leader dedups ─▶ verifier ─▶ leader writes report
                 └─▶ reviewer C ─┘   (merge findings)  (refute)
```

**Fits:** review, audit, multi-angle analysis — several independent passes over the same input, then a
skeptical pass that tries to refute each finding. (Shipped example: `code-review-swarm`.)

**Roster:** a leader + N parallel workers (each a distinct angle) + a verifier.

**Leader policy:** the *only* parallel phase is the fan-out; everything after is one task at a time.
The leader **dedups** the merged findings itself before sending them to the verifier (same
location + same root cause → one finding). The verifier re-reads the source and tries to **refute**
each finding; the leader does not add findings of its own or overrule the verifier.

**Tools:** reviewers and verifier are read-only (`read`, `grep`, `glob`, read-only `bash`); the leader
writes the final report.

---

## 3. Turn-based (one speaker at a time)

```
leader ─▶ member 1 (acts) ─▶ leader ─▶ member 2 (acts) ─▶ leader ─▶ …
          ▲ never two at once ▲
```

**Fits:** conversation games, negotiations, simulations, anything where parallel action would corrupt
shared state or leak information. (Shipped example: `werewolf-swarm`.)

**Leader policy:** *strict turn control* — "never query two members in parallel; one speaks, you
process the result, then the next." Plus **information hygiene**: use private `send_message` (not
broadcasts) when a member must not see what another knows. Public rules live in a shared doc
(e.g. `RULES.md`) every member reads.

**Tools:** often minimal — werewolf players have only `read`; they act through the injected
`send_message`.

---

## 4. Debate / adversarial pair

```
goal ─▶ leader ─▶ ⟳ member A ⇄ member B ⟳ ─▶ leader synthesizes
                   (argue N rounds)
```

**Fits:** decisions with genuine trade-offs where you want opposing cases argued before the leader
decides. Often a sub-pattern inside a pipeline (a "debate stage").

**Leader policy:** bound the rounds ("two rounds each, then I decide"), define what each side argues
for, and require the leader to **close the loop** — state the decision and the reasoning back to both.

**Tools:** the debaters need whatever supports their case (`read`, `web_*`); the leader needs `read`
and `write` for the synthesis.

---

## 5. Watchdog / standing patrol

```
   ⏰ every 15m
      │
      ▼
   watchdog ─(scan)─▶ send_message leader on anything red ─▶ leader dispatches a fix
```

**Fits:** monitoring, periodic health checks, market patrols, anything on a cadence rather than a
one-shot goal. Usually a *member within* a larger team rather than a whole swarm.

**Leader/member policy:** the watchdog wakes on a `schedule:`, does a bounded scan, and **only escalates
exceptions** (don't narrate a clean scan to the leader every tick). The leader turns a real alert into
a tracked task.

**Tools + guardrails:** minimal tools (`read`, `bash`, maybe `monitor`); `bypass` + a deny fence + a
`budget_tokens` cap, because it runs unattended and frequently. See
[../building/scheduling-and-guardrails.md](../building/scheduling-and-guardrails.md#a-fully-guarded-autonomous-worker).

---

## Composing shapes

Real teams mix these. A software team might be: a **pipeline** (design → build → test → review) whose
*review* stage is a **fan-out + verify**, with a **watchdog** member running CI checks on a schedule.
Build the dominant shape first, then graft the others as members with their own policy.

## Sizing the team

- **Match members to genuinely distinct roles**, not to "more is faster." Each member adds coordination
  overhead and cost.
- The user's described goal sets the count: a "build-and-review pipeline" is 3 (lead + builder +
  reviewer); a "ship a PR from an issue" team is 5–6 (pm, designer, backend, frontend, qa + lead).
- Don't invent members the user didn't ask for. Start minimal; add with `evva swarm add` later.

## See also

- The discipline that makes any topology hold together: [coordination.md](coordination.md).
- The three shipped shapes in full detail: [examples.md](examples.md).
- Translating a shape into the leader's persona: [../building/personas.md](../building/personas.md).
