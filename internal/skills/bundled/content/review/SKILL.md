# review Review a GitHub pull request

Use this skill when the user asks you to review a PR ("review #123", "look at this PR", "/review 42"). The skill expects a PR number in `args`; if `args` is empty, list open PRs first and ask which one to review.

## Workflow

1. Resolve the PR.
   - If `args` is empty or non-numeric, run `gh pr list` (via `bash`) and pause: tell the user which PR you want to review and ask them to confirm.
   - If `args` is a number, run `gh pr view <number>` then `gh pr diff <number>` (via `bash`, in parallel).

2. Read the diff in full. For diffs over ~500 lines, delegate exploration of any unfamiliar files referenced in the diff to a subagent with `subagent_type: "explore"` — its read-only nature is the safest preset and keeps your context clean.

3. Produce the review. Use these sections in order:
   - **Summary** — 1–3 sentences explaining what the PR does and why.
   - **Correctness** — concrete bugs, race conditions, off-by-one errors, missing nil-checks at boundaries, broken invariants. Cite `file:line` for every finding.
   - **Conventions** — places the diff deviates from the repo's existing patterns. Skim 2–3 sibling files before flagging a convention violation.
   - **Performance** — only call out hot-path regressions (request handlers, render loops, startup paths). Skip micro-optimizations.
   - **Tests** — does the PR's test coverage exercise the change? Note untested branches, but do not demand 100% — match the repo's existing test bar.
   - **Security** — input validation, authorization, secrets. For a focused security pass, suggest the user invoke the `security-review` skill afterwards.
   - **Nits** (optional) — small style preferences, gathered under one heading so the substantive findings stay legible.

## Tone

- Concrete and actionable. "Move this validation above the early return" beats "consider validation here".
- Cite `file:line` for every finding so the author can navigate.
- Acknowledge what the PR gets right — a review that is only criticism is incomplete signal.
- If the diff is well-scoped and bug-free, say so plainly. Do not invent issues to pad the review.

## Out of scope for this skill

- Don't run code or tests yourself unless the user asks.
- Don't push commits, leave review comments via `gh pr review`, or merge — those are shared-state actions that need separate authorization.
- Don't run `security-review` automatically; surface it as a suggestion if the diff touches an obvious surface (auth, parsers, deserialization, HTML rendering, SQL).
