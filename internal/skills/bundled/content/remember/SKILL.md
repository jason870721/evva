# remember Review auto-memory and propose promotions to EVVA.md, plus cleanup of stale, duplicate, or conflicting entries

Use this skill when the user wants to review, organize, or tidy their memory ("review my memory", "clean up memory", "what should be promoted to EVVA.md", "/remember"). The goal: survey the whole memory landscape and produce a clear, grouped report of *proposed* changes for the user to approve — promotions into the project's durable instructions, plus cleanup of duplicate, outdated, and conflicting entries. This skill **proposes; the user disposes** — do NOT apply any change without explicit approval.

If `args` is provided (e.g. `/remember focus on the testing notes`), treat it as extra scope or context for the review; otherwise review everything.

## Before you start

This skill operates on evva's persistent memory. If your system prompt has no `# Memory` section, auto-memory is disabled for this session — tell the user that and stop; there is nothing to review.

You are reconciling two layers:

| Layer | What it is | Who it's for |
| --- | --- | --- |
| **EVVA.md** (project root) | Durable project conventions and instructions, loaded into context every session | Every contributor — and evva itself |
| **Auto-memory** (the directory named in your `# Memory` section: a `MEMORY.md` index plus one file per memory, each typed `user` / `feedback` / `project` / `reference`) | Personal, evolving, point-in-time notes | You and this user, across future conversations |

The distinction that drives every proposal: **EVVA.md is a project rule everyone follows; auto-memory is your personal, point-in-time understanding.**

## Step 1 — Gather every layer

- `read` `EVVA.md` from the project root (it may not exist — note that).
- Enumerate auto-memory: `glob` `*.md` in the memory directory, then `read` `MEMORY.md` (the index) and each topic file. Only the `MEMORY.md` index rides in your system prompt, and it can be stale or truncated — read the topic files on disk, don't trust the in-context copy alone.

**Done when:** you have EVVA.md (or know it's absent) and the full contents of every auto-memory file.

## Step 2 — Classify each auto-memory entry

For each substantive memory, pick the best destination:

| Destination | What belongs there | Examples |
| --- | --- | --- |
| **Promote to EVVA.md** | A durable convention or instruction every contributor should follow | "integration tests must hit a real database, not mocks", "release tags only on `main` / `pre-release`", "port tool descriptions from `ref/` verbatim" |
| **Stay in auto-memory** | The user's profile, a personal preference, fast-changing project context, or an external pointer | a `user` memory about someone's background; a `feedback` memory that's a personal style preference; a `project` memory about an in-flight initiative; any `reference` |

Guidance by type:
- `user` and `reference` memories almost always **stay** — they describe a person or an external system, not a project rule.
- `feedback` is the ambiguous one: promote it only when the guidance is clearly a **project-wide convention** (a testing policy, a build invariant), not a personal style preference. When in doubt, flag it as ambiguous and ask.
- `project` memories usually **stay** (they decay fast); promote only the stable, structural facts.

**Done when:** every entry has a proposed destination or is flagged ambiguous.

## Step 3 — Find cleanup opportunities

Scan across EVVA.md and auto-memory for:
- **Duplicates** — a memory already captured in EVVA.md → propose removing it from auto-memory (and its `MEMORY.md` pointer line).
- **Outdated** — an EVVA.md line contradicted by a newer memory → propose updating EVVA.md, noting which is more recent.
- **Conflicts** — two memories, or a memory and EVVA.md, that disagree → propose a resolution, preferring the more recent and what's verifiable in the current code.
- **Stale claims** — a memory that names a file, function, or flag: `grep` / `read` to check it still exists before trusting it; flag dead ones for removal. A memory is a claim about what was true when it was written, not now.

**Done when:** every cross-layer and intra-memory issue is identified.

## Step 4 — Present the report

Output a structured report grouped by action type — proposals only, nothing applied:

1. **Promotions** — entries to move into EVVA.md, each with the proposed wording and a one-line rationale.
2. **Cleanup** — duplicates, outdated lines, conflicts, and stale claims to resolve.
3. **Ambiguous** — entries where you need the user to decide (usually personal-vs-project `feedback`).
4. **No action needed** — a brief note on what should stay put and why.

If auto-memory is empty, say so and offer to review EVVA.md alone for cleanup.

**Done when:** the user can approve or reject each proposal individually.

## Rules

- Present ALL proposals before making ANY change. Do NOT modify EVVA.md, `MEMORY.md`, or any memory file without explicit approval.
- When you DO apply an approved promotion: `edit` it into the right EVVA.md section, delete the now-redundant memory file, and remove its `MEMORY.md` pointer line — leave no dangling index entry.
- Keep EVVA.md's existing structure and voice; merge into the section it belongs in rather than appending a loose list.
- Don't create files that don't need to exist, and don't guess on ambiguous entries — ask with `ask_user_question`.
