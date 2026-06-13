# The tool model

Tools are how a member *acts* on the world. In a swarm there are two distinct sources of tools, and
keeping them straight is essential:

```
                 ┌────────────────────────────────────────────────┐
                 │                 a swarm member                  │
                 │                                                 │
  YOU choose ───▶│  DOMAIN tools   (tools/active.yml, deferr.yml)  │
                 │     read, write, bash, web_fetch, repl, …       │
                 │                                                 │
 RUNTIME adds ──▶│  COLLABORATION tools   (by role, auto)          │
                 │     send_message, task_*, alarm_*, skill, …     │
                 └────────────────────────────────────────────────┘
```

## Two sources

1. **Domain tools — you choose these.** They go in `tools/active.yml` and `tools/deferr.yml`. They are
   the member's *capabilities*: reading files, running shell, fetching the web, evaluating Python.
   This is the decision the rest of this section helps you make.
   - The complete list: [catalog.md](catalog.md).
   - Recommended sets per role: [recipes-by-role.md](recipes-by-role.md).

2. **Collaboration tools — the runtime injects these, by role.** `send_message`, `list_members`, the
   `task_*` ledger tools, `proposal_*`, `schedule_*`, `alarm_*`, `skill_publish`, and the `skill`
   tool. **You never list these** in a tool file, and you never explain them in a persona.
   - The full list and who gets what: [collaboration-tools.md](collaboration-tools.md).

> The single most common authoring mistake is putting a collaboration tool in `active.yml`, or writing
> "use `task_create` to…" into a persona. Both duplicate what the runtime already provides.

## Active vs. deferred (a turn-cost decision)

Within your domain tools, each tool is either active or deferred:

| | Where | Cost | Use for |
| --- | --- | --- | --- |
| **Active** | `tools/active.yml` | Schema is in the prompt **every turn** | The member's everyday tools (read, edit, bash). |
| **Deferred** | `tools/deferr.yml` | Name only until the model calls `tool_search` to load the schema | Occasional tools (web, repl, excel, lsp) you want available but not crowding the prompt. |

A member with **any** deferred tools automatically gets `tool_search` wired in so it can discover and
load them. If a member has no deferred tools, it doesn't need `tool_search`.

Rule of thumb: 4–8 active tools the member uses constantly; everything situational goes deferred.

## How to choose a member's tools — the procedure

1. **Start from the role.** "Coder" → fs + shell. "Reviewer" → read-only (read, grep, glob, bash for
   read-only git). "Researcher" → read + web + json_query. Match to a recipe in
   [recipes-by-role.md](recipes-by-role.md).
2. **Add what the *task* needs.** Working on Jupyter notebooks? Add `notebook_edit`. Calling a JSON
   API? Add `http_request`. Reading PDFs? `read` already handles them.
3. **Give the least power that does the job.** A reviewer that only reports doesn't need `write`/`edit`
   — leaving them off makes "you review, you don't edit" true by construction, not just by persona.
   The shipped werewolf players have only `read`.
4. **Split active vs. deferred** by frequency, as above.
5. **Never add collaboration tools** — they're injected.

## A worked example

A backend worker that codes, runs tests, and occasionally hits an internal API and reads a PDF spec:

```yaml
# tools/active.yml — used every turn
- read
- write
- edit
- glob
- grep
- bash
```
```yaml
# tools/deferr.yml — occasional; loaded on demand
- http_request
```

`read` already covers the PDF spec (no extra tool needed). `http_request` is deferred because it's
rare. No collaboration tools appear — the runtime injects `send_message`, `my_tasks`, etc.

## See also

- [catalog.md](catalog.md) — every domain tool, what it does, and whether it suits a swarm member.
- [recipes-by-role.md](recipes-by-role.md) — copy-paste tool sets for common roles.
- [collaboration-tools.md](collaboration-tools.md) — the injected tools (so you know what to leave out).
