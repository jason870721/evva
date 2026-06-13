# Tool recipes by role

Copy-paste starting points for common member roles. Each shows the `tools/active.yml` and (where
useful) `tools/deferr.yml`. Remember: **collaboration tools are injected** — none appear below. Adapt
to the actual task; these are defaults, not mandates.

Principle throughout: **least power that does the job.** Leaving `write`/`edit` off a reviewer makes
"you don't edit" true by construction, not just by persona.

---

## Orchestrator / leader

Plans, delegates, verifies — it does not do the work. It mostly reads results, keeps a state file, and
writes the final report. Give it `write`/`edit` only if it maintains a state file or writes the report
itself (common); a leader that delegates *everything* including the writeup can be read-only.

```yaml
# tools/active.yml
- read
- write          # for review-state.md / the final report (drop if the leader writes nothing)
- edit
- bash           # read-only checks: confirm a path exists, git diff --stat
```

> The leader's power is in its **persona** (the coordination policy), not its tools. See
> [../building/personas.md](../building/personas.md#the-leaders-persona-is-the-swarms-skeleton).

## Coder / builder

Implements changes and runs tests.

```yaml
# tools/active.yml
- read
- write
- edit
- glob
- grep
- bash
```
```yaml
# tools/deferr.yml
- lsp_request    # semantic navigation in a large codebase
- http_request   # if it calls an internal API
```

## Reviewer / auditor (read-only)

Inspects and reports; never edits. `bash` is for **read-only** git (`diff`, `log`, `blame`) and
running tests.

```yaml
# tools/active.yml
- read
- glob
- grep
- bash
```

No `write`/`edit` → it physically cannot modify the target. (If it must write its findings to a file,
add `write` but keep `edit` off, or have it report via `send_message` and let the leader record.)

## Researcher / fact-finder

Investigates and summarizes from the web and the repo; doesn't mutate the filesystem.

```yaml
# tools/active.yml
- read
- grep
- glob
- web_search
- web_fetch
- json_query
```
```yaml
# tools/deferr.yml
- calc
```

This mirrors evva's built-in **ResearchKit** (`read`, `grep`, `glob`, `web_search`, `web_fetch`,
`json_query`, `calc`).

## Data / analyst

Crunches data, runs computations, produces spreadsheets/reports.

```yaml
# tools/active.yml
- read
- write
- bash
```
```yaml
# tools/deferr.yml
- repl
- json_query
- calc
- excel
```

## Integration / ops

Talks to external systems and watches processes.

```yaml
# tools/active.yml
- read
- write
- edit
- bash
```
```yaml
# tools/deferr.yml
- http_request
- monitor
- daemon_list
- daemon_output
- daemon_stop
```

Pair with the manifest: usually `bypass` + a deny fence (see
[../building/permissions.md](../building/permissions.md#the-deny-fence-the-supported-autonomous-but-fenced-pattern)).

## Watchdog / standing patrol

Wakes on a schedule, scans, alerts the leader. Minimal tools, unattended.

```yaml
# tools/active.yml
- read
- bash
```
```yaml
# tools/deferr.yml
- monitor
```

Manifest: a `schedule:` block + `permission_mode: bypass` + a `budget_tokens` cap + a deny fence. See
[../building/scheduling-and-guardrails.md](../building/scheduling-and-guardrails.md#a-fully-guarded-autonomous-worker).

## Writer / documentation

Produces prose artifacts.

```yaml
# tools/active.yml
- read
- write
- edit
- glob
- grep
```

## Turn-based participant (minimal)

A member in a conversation game or strict turn-based protocol may need almost nothing — the shipped
werewolf players have **only** `read`. They act entirely through the injected `send_message`.

```yaml
# tools/active.yml
- read
```

---

## Mapping to evva's built-in kits

If you also build agents with the evva SDK (`pkg/agent`), these recipes line up with the named kits in
`pkg/tools/kits`:

| Recipe | Closest kit | Kit contents |
| --- | --- | --- |
| Coder / builder | **GeneralPurposeKit** / **CodingKit** | fs + shell + todo + util (+ notebook + monitor for Coding); web deferred |
| Reviewer, Researcher | **ReadOnlyKit** | `read`, `grep`, `glob`, `tree`, `web_search`, `web_fetch`, `json_query` |
| Researcher | **ResearchKit** | `read`, `grep`, `glob`, `tree`, `web_search`, `web_fetch`, `json_query`, `calc`, `todo_write` |

(The kits include `todo_write`; for a *swarm* member, drop it — the shared task ledger replaces it.)

## See also

- Every tool, with caveats: [catalog.md](catalog.md).
- Why active vs. deferred, and the two tool sources: [README.md](README.md).
- These recipes in real swarms: [../patterns/examples.md](../patterns/examples.md).
