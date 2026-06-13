# Tool catalog

Every domain tool evva offers, what it does, and whether it belongs on a swarm member. These are the
tools you put in a member's `tools/active.yml` / `tools/deferr.yml`. The **collaboration** tools
(`send_message`, `task_*`, …) are injected automatically and are documented separately in
[collaboration-tools.md](collaboration-tools.md) — do not list them here.

**Swarm relevance legend:**

- ✅ **Common** — a normal domain tool you'll assign to swarm members often.
- ⚙️ **Situational** — useful for specific roles/tasks; assign when the job needs it.
- ⛔ **Single-session** — an interactive-evva or remote-agent tool that usually does **not** belong on
  a swarm member (the swarm provides the equivalent another way). Reason given.

The "Tool name" column is the exact string you write in a tool file.

---

## Filesystem

| Tool name | Swarm | Capability | When to assign |
| --- | --- | --- | --- |
| `read` | ✅ | Read a file from disk; also handles **PDF pages, Jupyter notebooks, and images**. | Almost every member. The first tool you reach for. |
| `write` | ✅ | Overwrite a file's contents or create a new one. | Members that produce artifacts (code, reports). *Also enables long-term memory* — the runtime injects the memory protocol for any member with `write` or `edit`. |
| `edit` | ✅ | Apply a precise `old_string → new_string` replacement in an existing file (requires a prior `read`). | Coders. Preferred over `write` for modifying existing files. Also enables memory. |
| `glob` | ✅ | Match file paths against glob patterns, sorted by modification time. | Members that navigate a codebase. |
| `notebook_edit` | ⚙️ | Edit cells in a Jupyter notebook by index. | Data/notebook members working in `.ipynb` files. |

> Reading PDFs and images needs no extra tool — `read` covers them.

## Shell

| Tool name | Swarm | Capability | When to assign |
| --- | --- | --- | --- |
| `bash` | ✅ | Execute a shell command; returns combined stdout/stderr. Supports background execution. | The workhorse: git, build/test runs, `find`/`rg`, any CLI. In non-`bypass` modes each call asks for operator approval. |
| `grep` | ✅ | Regex-search file contents recursively across a directory. | Search/inspection. Faster and cleaner than `bash grep`. |
| `tree` | ✅ | Print a directory tree to a given depth. | Orientation in an unfamiliar repo. |

## Web

| Tool name | Swarm | Capability | When to assign |
| --- | --- | --- | --- |
| `web_search` | ⚙️ | Search the public web via Tavily for up-to-date information. | Research members. Requires a Tavily API key configured on the host. |
| `web_fetch` | ⚙️ | Fetch and extract readable text from a URL. | Research members — and **any member that should consult live docs** (including fetching *this* agent-guide). |
| `http_request` | ⚙️ | Call an HTTP/JSON API (method, url, headers, body) and read the parsed response — the structured alternative to `curl`. | Integration members talking to REST APIs/webhooks. |

## Code intelligence

| Tool name | Swarm | Capability | When to assign |
| --- | --- | --- | --- |
| `lsp_request` | ⚙️ | Query a Language Server — go-to-definition, find references, hover, document symbols. | Coders in a large codebase where semantic navigation beats text search. Needs a configured language server. |

## Compute and data

| Tool name | Swarm | Capability | When to assign |
| --- | --- | --- | --- |
| `repl` | ⚙️ | Run a Python or JavaScript snippet in a fresh subprocess; returns combined stdout+stderr. | Data/analysis members; scratch computation; quick transforms. |
| `calc` | ⚙️ | Evaluate a mathematical expression with full operator support. | Lightweight arithmetic without spinning up `repl`. |
| `json_query` | ⚙️ | Extract a value from JSON using a dot/bracket path expression. | Parsing API responses or other tools' JSON output. |
| `excel` | ⚙️ | Read, write, create, and manipulate Excel `.xlsx` files — cells, formulas, sheets, charts, pivot tables, data validation. | Finance/data members that produce or consume spreadsheets. |

## Background processes (daemons)

A unified abstraction over long-running units: background `bash` tasks, monitors, async subagents.

| Tool name | Swarm | Capability | When to assign |
| --- | --- | --- | --- |
| `monitor` | ⚙️ | Stream events/lines from a background task or process as they arrive. | A member watching a long-running process (a dev server, a tail of logs). |
| `daemon_list` | ⚙️ | Enumerate every registered background unit with status + metadata. | Pair with `monitor`/background `bash`. |
| `daemon_output` | ⚙️ | Fetch the captured output of one daemon (tail-able). | Inspect a background task's output. |
| `daemon_stop` | ⚙️ | Terminate a running daemon by id (idempotent). | Clean up background units. |

> Assign the `daemon_*` trio together with `monitor` or background `bash`; alone they have nothing to
> manage.

## MCP (Model Context Protocol)

If the host has MCP servers configured, members can reach them.

| Tool name | Swarm | Capability | When to assign |
| --- | --- | --- | --- |
| `list_mcp_resources` | ⚙️ | List resources exposed by configured MCP servers. | Members integrating with an MCP server. |
| `read_mcp_resource` | ⚙️ | Read a specific MCP resource. | As above. |
| `mcp__<server>__<tool>` | ⚙️ | Per-server tools, discovered at runtime (naming: `mcp__<server>__<tool>`). | Assign the specific server tools a member needs. Names depend on the configured servers. |

## Single-session tools — usually NOT for swarm members

These exist in evva but serve the interactive single-agent session or the remote-agent feature. A
swarm provides the equivalent capability another way, so assigning them to a member is usually a
mistake.

| Tool name | Swarm | Why it's not for a swarm member |
| --- | --- | --- |
| `todo_write` | ⛔ | The swarm has a shared **task ledger** (`task_*`, injected). Use that, not a private todo list. |
| `agent` | ⛔ | Spawns a subagent. In a swarm you add a **teammate** in the manifest instead. (Possible, but rarely the right tool.) |
| `tool_search` | ⛔ (auto) | Auto-wired when a member has deferred tools — never list it yourself. |
| `skill` | ⛔ (auto) | Auto-injected into every member — never list it. |
| `schedule_wakeup` | ⛔ | For interactive `/loop` pacing. Swarm cadence is the leader's `schedule_set` / a manifest `schedule:`; one-shot waits use the injected `alarm_set`. |
| `alarm_create`, `alarm_list`, `alarm_cancel` | ⛔ | The single-agent alarm tools. Swarm members get the injected `alarm_set` / `alarm_clear` instead — don't list these. |
| `cron_create`, `cron_list`, `cron_delete`, `remote_trigger` | ⛔ | Schedule/trigger **remote** agent runs — a different feature from the swarm scheduler. Use a manifest `schedule:` for swarm cadence. |
| `enter_plan_mode`, `exit_plan_mode` | ⛔ | Interactive read-only design mode. Not meaningful for a swarm member. |
| `enter_worktree`, `exit_worktree` | ⛔ | Interactive git-worktree isolation. A member that needs a worktree can run `git worktree` via `bash`. |
| `ask_user_question` | ⛔ | A member addresses the operator through its **output text**, not this tool. For blocking decisions, message the leader. |
| `push_notification` | ⛔ | Operator notifications come from the web console and watchdog alerts. |
| `config` | ⛔ | Reads/writes evva session config; available only on the interactive Main profile, not on members. |
| `feedback` | ⛔ | Dev-mode tool for reporting issues to evva's developers. |

> "Usually not" is not "never." If you have a genuine reason — say a member that legitimately needs to
> spawn a throwaway subagent — you can list the tool. But reach for the swarm-native equivalent first.

## See also

- The model behind active/deferred and the two tool sources: [README.md](README.md).
- Ready-made tool sets per role: [recipes-by-role.md](recipes-by-role.md).
- The injected collaboration tools you must **not** list here: [collaboration-tools.md](collaboration-tools.md).
