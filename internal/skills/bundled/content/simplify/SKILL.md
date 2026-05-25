# simplify Review changed code for reuse, quality, and efficiency, then fix the issues found

Use this skill when the user asks for a clean-up pass on recent changes ("simplify this", "clean up the diff", "/simplify"). The skill spawns three parallel reviewers, then applies their findings.

## Phase 1 — Identify the change set

Run `git diff` (or `git diff HEAD` if there are staged changes) via `bash`. If there are no git changes, fall back to the files the user most recently mentioned or that you edited in this conversation. Cap the working set: if the diff exceeds ~2000 lines, ask the user (via `ask_user_question`) to scope the review to a subdirectory or file list.

## Phase 2 — Launch three reviewers in parallel

Emit three `agent` tool_use blocks in a SINGLE assistant turn with `subagent_type: "explore"`. Pass each agent the full diff so it has complete context. Give each agent the relevant section below verbatim as its prompt.

### Agent 1 — Code reuse review

For each change:

1. **Search for existing utilities and helpers** that could replace newly written code. Look for similar patterns elsewhere — common locations: utility directories, shared packages, files adjacent to the changed ones.
2. **Flag new functions that duplicate existing functionality.** Suggest the existing function.
3. **Flag inline logic that could use an existing utility** — hand-rolled string manipulation, manual path handling, custom environment checks, ad-hoc type guards.

### Agent 2 — Code quality review

Review the same diff for hacky patterns:

1. **Redundant state** — state that duplicates existing state; cached values that could be derived; observers/effects that could be direct calls.
2. **Parameter sprawl** — new parameters added to a function instead of generalizing or restructuring.
3. **Copy-paste with slight variation** — near-duplicate blocks that should be unified.
4. **Leaky abstractions** — exposing internal details that should be encapsulated; breaking existing boundaries.
5. **Stringly-typed code** — raw strings where constants, enums, or branded types already exist.
6. **Unnecessary comments** — comments explaining WHAT (well-named identifiers already do that), narrating the change, or referencing the task/caller. Keep only non-obvious WHY (hidden constraints, subtle invariants, workarounds).
7. **Speculative abstractions** — helpers, generics, or interfaces introduced for hypothetical future requirements. Three similar lines is better than a premature abstraction.

### Agent 3 — Efficiency review

Review the same diff for efficiency:

1. **Unnecessary work** — redundant computations, repeated file reads, duplicate API calls, N+1 patterns.
2. **Missed concurrency** — independent operations run sequentially when they could run in parallel.
3. **Hot-path bloat** — new blocking work added to startup or per-request/per-render hot paths.
4. **Recurring no-op updates** — state/store updates inside polling loops, intervals, or event handlers that fire unconditionally; add a change-detection guard.
5. **Unnecessary existence checks** — pre-checking file/resource existence before operating (TOCTOU). Operate directly and handle the error.
6. **Memory** — unbounded data structures, missing cleanup, listener leaks.
7. **Overly broad operations** — reading entire files when a portion is needed; loading all items when filtering for one.

## Phase 3 — Apply fixes

Wait for all three agents to complete. Aggregate findings. Fix each issue directly using `edit` / `write`. Rules:

- **Respect the diff's intent.** evva's `# Doing tasks` policy explicitly says "three similar lines is better than a premature abstraction" and "don't add features beyond what was asked". If a reviewer's finding asks for an abstraction the change doesn't need, **skip it** — note the skip in the summary.
- If a finding is a false positive or speculative, note it and move on. Do not argue with the finding; do not file an issue about it.
- If two reviewers contradict each other, pick the path that aligns with evva's `# Doing tasks` policy (minimum complexity wins).

## Phase 4 — Summarize

After fixes, run `git diff` again to confirm only the intended changes landed. Briefly summarize: what was fixed, what was deliberately skipped (with reason), and a confidence note on the code's current state.

Do NOT commit the changes. The user runs the `commit` skill (or asks for a commit) when ready.
