# setup-mcp Research and configure an MCP server in evva's settings.json

Use this skill when the user wants to add, connect, or configure an MCP (Model Context Protocol) server ("set up an MCP server", "connect the filesystem MCP", "add the GitHub MCP", "/setup-mcp <name>"). The goal: find the right server, write a correct `mcpServers` entry into the right `settings.json`, and hand off cleanly. Do NOT use it to remove servers or write MCP client code — the client already exists in `pkg/mcp`.

If `args` names a server (e.g. `/setup-mcp filesystem`), start the research there; if `args` is empty, ask the user what the server should do before searching.

## Where MCP config lives

evva reads MCP servers from `settings.json` — the SAME file the hooks system uses, under the `mcpServers` key (project entries override user entries on a name collision):

| Scope | Path | Use for |
| --- | --- | --- |
| Project | `<workdir>/.evva/settings.json` | A server this repo needs (committed = team-shared, gitignored = personal-per-repo) |
| User | `<APP_HOME>/settings.json` (typically `~/.evva/settings.json`) | A server you want in every project |

Read the target file FIRST with `read`. Merge the new server into the existing `mcpServers` map — NEVER overwrite the whole file, and never drop hook config or other servers already there.

## The `mcpServers` schema

Two transports are supported: `stdio` (a local subprocess) and `http` (a remote streamable-HTTP endpoint).

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${HOME}/work"],
      "env": { "SOME_FLAG": "1" }
    },
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": { "Authorization": "Bearer ${GITHUB_MCP_TOKEN}" },
      "timeout": 60
    }
  }
}
```

Field rules (source of truth: `pkg/mcp/config.go` + `pkg/mcp/types.go`):
- `type` — `"stdio"` or `"http"`. If omitted it is inferred: `command` present → stdio, `url` present → http. Set it explicitly anyway.
- stdio requires `command`; `args` is the argument list; `env` is an optional map merged into the subprocess environment.
- http requires `url`; `headers` is an optional map (auth tokens go here).
- `disabled: true` keeps the entry but skips connecting it.
- `timeout` — connect timeout in seconds (default 30, max 600).
- **Env expansion:** any value may use `${VAR}` or `${VAR:-default}`; evva expands it from the environment at load (`pkg/mcp/envexpand.go`). A missing `${VAR}` with no default is a load warning, not a crash.

## Workflow

### Step 1 — Understand the goal

If it's not already clear from `args` or the conversation, use `ask_user_question` to learn what the server should do (filesystem access? a SaaS API? a database?). This decides which server you search for.

### Step 2 — Research the server with the web tools

Don't guess the package name or transport. Use `web_search` to find the official server, then `web_fetch` its README / docs page to read the exact run command and config:
- Search for the canonical implementation first (e.g. `modelcontextprotocol/servers` for reference servers, or the vendor's own MCP docs).
- From the docs, extract: the launch command + args (for stdio, e.g. `npx -y <pkg> <path>`) OR the endpoint URL (for http), plus any required env vars / tokens and what they're called.
- Prefer the documented invocation verbatim. Note the runtime it assumes (`npx`/`bunx`/`uvx`/a binary) — flag to the user if that runtime isn't installed.

### Step 3 — Confirm transport and scope

- Transport falls out of the docs (subprocess → `stdio`, hosted endpoint → `http`).
- Use `ask_user_question` for scope unless the user already said: **project** (`<workdir>/.evva/settings.json`) for a server tied to this repo, **user** (`<APP_HOME>/settings.json`) for one you want everywhere.

### Step 4 — Write the entry

`read` the target `settings.json` (it may not exist). Merge the new server into `mcpServers` with `edit`; use `write` only when creating the file fresh. Keep the rest of the file intact.

If you're creating `<workdir>/.evva/settings.json` for the first time and the repo doesn't already track `.evva/`, offer to add `.evva/` (or `.evva/settings.json`) to `.gitignore` — a personal/local server config usually shouldn't be committed. Ask if unsure.

### Step 5 — Handle secrets safely

NEVER hardcode a token, key, or password in `settings.json`. Put the secret in an environment variable (or the project's `.env`, which evva loads) and reference it with `${VAR}` in the `headers`/`env`/`args`. Tell the user exactly which variable to set and where, and confirm it's set (`bash`: `[ -n "${VAR:-}" ] && echo set || echo MISSING`) before relying on it.

### Step 6 — Validate the JSON

A malformed `settings.json` silently drops every server in that scope. Validate with `bash`:

```
jq -e '.mcpServers["<server-name>"]' <target-file>
```

Exit 0 with the entry printed = valid and present. Any error = fix the JSON before continuing. Re-run until it passes.

### Step 7 — Hand off

MCP servers connect when evva STARTS — config is read once at boot, there is no mid-session hot-reload. So:
- Tell the user to restart evva for the new server to connect.
- After restart, they can run `/mcp` to see the server's live status (connected / failed / needs-auth / disabled), its tool and resource counts, and any connection error.
- Once connected, the server's tools are reachable as `mcp__<server>__<tool>` and surface through `tool_search`; `list_mcp_resources` / `read_mcp_resource` reach its resources. An http server that needs OAuth shows `needs-auth` in `/mcp` and is authenticated on demand via its `mcp__<server>__authenticate` tool.

## Common patterns

### Local filesystem server (stdio)

```json
{
  "mcpServers": {
    "filesystem": {
      "type": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "${HOME}/projects"]
    }
  }
}
```

### Hosted API server with a bearer token (http)

```json
{
  "mcpServers": {
    "github": {
      "type": "http",
      "url": "https://api.githubcopilot.com/mcp/",
      "headers": { "Authorization": "Bearer ${GITHUB_MCP_TOKEN}" }
    }
  }
}
```

(Then: `export GITHUB_MCP_TOKEN=...` in the user's shell/`.env`, never in the JSON.)

### Temporarily disable a server without deleting it

Set `"disabled": true` on its entry — `/mcp` will list it as `disabled` and evva won't try to connect it.

## Troubleshooting

If a server doesn't show as connected in `/mcp` after restart:
1. Re-read the target `settings.json` and re-run the `jq -e` check from Step 6 — a JSON error in this scope drops ALL of its servers.
2. For stdio: run the `command` + `args` directly with `bash` to confirm the binary exists and starts (e.g. the `npx`/`uvx` package resolves).
3. For http: confirm the URL is reachable and any `${TOKEN}` env var is actually set in the environment evva was launched from.
4. Confirm you edited the scope evva is actually reading (project file is `<workdir>/.evva/settings.json`, not `<workdir>/settings.json`).
5. Settings load at startup — if you edited mid-session, the user must restart evva.

## Reference

- Config schema + loader: `pkg/mcp/config.go`, `pkg/mcp/types.go`.
- Env-var expansion: `pkg/mcp/envexpand.go`.
- Live status (what `/mcp` shows): `pkg/mcp/manager.go` `Status()`.
