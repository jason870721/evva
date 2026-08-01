# Eval fixtures

Behavioral regression fixtures for `evva eval`. Each file records a scenario —
the user turns to replay and the tool-call sequence the agent produced — so a
prompt, tool-description or model change can be checked against evva's own
prior behavior instead of shipped and watched.

```
evva eval list                  # what is here
evva eval run                   # replay everything, score it
evva eval run -judge            # also score the prose expectations (advisory)
evva eval run -model <id>       # check a candidate model against the same set
```

`evva eval run` exits non-zero on any structural divergence, so it drops
straight into CI or the release preflight.

## Adding one

Capture a real session you already ran:

```
evva eval list -sessions
evva eval capture <session-id> -name my-fixture -desc "what this guards"
```

Or hand-author a JSON file in this directory — the format is small on purpose,
and the seed fixtures here were written by hand.

Keep each fixture **trimmed to the minimum turns that exercise its behavior**.
Every replay is real, billable LLM traffic multiplied by the fixture count, so
the set is curated rather than exhaustive.

## The two tiers

**Structural** (default, hard gate). Compares the *sequence* of tool calls
against `baseline` — same tools, same order, same identity args. Arguments are
normalized so a fixture recorded in one checkout still matches a replay from
another: paths reduce to base names, commands to their leading verb. Arguments
the baseline never recorded are ignored, so a tool gaining an optional
parameter is not a false alarm.

Use this for behavior with a right shape: read before edit, verify after
change, don't touch files outside scope.

**Judge** (`-judge`, advisory). For fixtures carrying `expected_outcome`, one
extra LLM call scores whether the run still satisfies that prose. Never affects
the exit code — a probabilistic scorer wired into a release gate produces
exactly the flaky failures that teach people to bypass gates.

Use this where the exact shape is not the point: refusals, explanations,
summarization. A fixture can carry an empty `baseline` and rely entirely on the
judge.

## When a fixture fails

A divergence is **not automatically a bug**. It means behavior changed. Decide
which it is:

- **Regression** — fix the prompt/tool/model change that caused it.
- **Intended change** — re-baseline it:
  ```
  evva eval capture --update <name>
  ```

"This fixture has failed for weeks" is a workflow bug, not an acceptable steady
state. A gate nobody trusts is a gate nobody reads.

## Note on the seed set

These three were hand-authored to demonstrate the format and to cover the
regression classes the PRD calls out. Their baselines describe the *shape*
expected behavior takes, and they reference files as illustrative targets — the
first real capture from your own history is worth more than all three, because
it encodes what your agent actually does rather than what it ought to.

See `docs/roadmap/PRD/agent-eval-harness.md`.
