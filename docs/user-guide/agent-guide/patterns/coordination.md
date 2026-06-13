# Coordination: the discipline that makes a swarm work

A topology is a diagram; coordination is what keeps it from falling apart over a long run. These are
the load-bearing habits — most belong in the **leader's persona** (the runtime injects the tool
mechanics, but not your team's *policy*).

## 1. The ledger is the source of truth, not the chat

Tasks live on the board; messages are for the conversation *around* tasks. Concretely, in the leader's
persona:

- **Dispatch through tasks.** Every unit of work is a `task_create` + `task_assign`, with the
  assignment details in the **task spec** — not buried in a `send_message`. The spec says: the goal,
  the exact inputs (paths, scope), the expected output (path + acceptance criteria).
- **Messages supplement, never contradict.** A `send_message` nudges, clarifies, or answers — but the
  task spec is authoritative. If the two disagree, the worker is stuck.
- **Check the board, don't rely on memory.** `task_list` is the truth about what's in flight; the
  leader should consult it rather than trust its recollection across a long run.

## 2. Verify by reading, not by hearing

A worker reporting "done" is a claim, not a completion. The leader's verify step must **open the
output** and check it against the acceptance criteria before `task_verify { approve: true }`:

> "Verification = you personally `read` the output file and confirm it meets the spec. A verbal
> 'done' is not verification."

If it falls short, `task_verify { approve: false }` with a specific note sends it back to `running` for
rework — name *what* is wrong and *where*.

## 3. Stage gates (for pipelines and phased work)

When phases depend on each other, the leader enforces order explicitly:

> "P1's three tasks must all be verified before P2 opens. P2 must be verified before the report is
> written. Within P1, the three reviews may run in parallel; everywhere else, one task at a time."

State the parallel exception precisely — "*only* this phase is parallel" — so the leader doesn't
fan out where it shouldn't.

## 4. The state file is the leader's reliable memory

A long-running leader's context gets compacted; its in-head plan does not survive. Give it an on-disk
state file (e.g. `review-state.md`, `pipeline-state.md`) and bind updates to **concrete actions**:

```markdown
Update review-state.md:
1. BEFORE every task_create (write the file first).
2. Immediately after each task_verify.
3. When entering a new phase, change the "phase" field first.
If you're about to dispatch but haven't updated the file — stop and write it first.
```

A trigger tied to an action survives where "remember to keep notes" does not. Keep the format simple
(phase, per-task status, counts, notes) and have the leader treat it as authoritative on wake.

## 5. The reply protocol (fight forgetting)

Members forget the mechanics between turns. Two habits counter this:

- **Every dispatch ends with a one-line reminder**: "When done, reply to me with `send_message` and
  include the output file path." Repeat it on *every* task — the repetition is the feature.
- **Workers report exactly once, then stop.** "Do the work, send one report to the leader, then wait."
  Prevents chatter and double-starts.

## 6. Downgrade on silence (never deadlock)

One unresponsive member must not stall the whole team. Define the fallback in the leader's persona:

> "If a member doesn't reply, re-ask once. Still silent → note it in the state file and proceed,
> marking that member's piece as uncovered/abstained. Never let one member block the run."

This turns a hang into a documented gap instead of a deadlock.

## 7. Close the loop downward

When a teammate's report informs a decision, the leader replies with *what it decided and why* —
whether or not it acted on the input:

> "adopted — switching to the simpler approach"; "holding off — the failure looks flaky, re-running."

A teammate that can't tell whether its input landed can't calibrate, and the operator loses the trail
from advice to action. (The runtime nudges the leader on this, but reinforcing it in the persona
helps.)

## 8. Dedup before expensive downstream work

In fan-out shapes, the leader merges overlapping results *before* the costly verify/report step: same
location + same root cause → one item (keep the fuller description, the higher severity). Doing this
first avoids paying for the verifier to judge the same finding three times.

## 9. Address members by name

`send_message` and `task_create` target a member's **name**, which may not literally be "leader" or
"worker". Members resolve names with `list_members` (the leader is "the member whose role is leader").
Addressing a non-member dead-letters into a mailbox nobody drains — the runtime guards obvious slips,
but write personas to use `list_members` rather than guessing.

## 10. Information hygiene (for turn-based / private work)

When members must not see each other's information, use **private** `send_message` (one recipient),
not broadcasts. Public, shared knowledge goes in a root-level doc everyone may `read`; private state
flows point-to-point. (The werewolf swarm lives or dies on this.)

## Where this lives

| Habit | Lives in | Injected by runtime? |
| --- | --- | --- |
| Tool mechanics (how `task_create` works, channels) | — | ✅ yes — don't rewrite |
| Dispatch-through-tasks, verify-by-reading | leader persona | no — your policy |
| Stage gates, parallel exceptions | leader persona | no |
| State-file format + triggers | leader persona | no |
| Reply reminder, downgrade rule | leader persona (+ worker persona) | partially — reinforce it |
| Dedup, close-the-loop | leader persona | partially |

## See also

- Putting these into a persona: [../building/personas.md](../building/personas.md).
- The shapes these support: [topologies.md](topologies.md).
- All three habits visible in real swarms: [examples.md](examples.md).
