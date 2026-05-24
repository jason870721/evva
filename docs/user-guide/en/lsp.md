# LSP — Language Server Protocol Support

evva integrates with Language Servers to provide semantic code intelligence directly in your terminal coding agent session.

## What It Does

The `lsp_request` tool lets the agent query a language server for:

| Operation | Description |
|---|---|
| `go_to_definition` | Jump to where a symbol is defined |
| `find_references` | Find all usages of a symbol |
| `hover` | Get type information and docs at a position |
| `document_symbols` | List all symbols in a file |
| `workspace_symbol` | Search the entire project for a symbol by name |
| `go_to_implementation` | Find implementations of an interface or type |
| `call_hierarchy` | Trace the call graph (incoming/outgoing calls) |

Additionally, LSP servers push **diagnostics** (errors, warnings) automatically — they appear as system reminders in the conversation without the agent needing to request them.

---

## Step-by-Step Setup (Go Example)

This walkthrough uses Go and gopls as an example. The same pattern applies to TypeScript, Rust, or any language with an LSP server.

### 1. Install the LSP Server

```bash
go install golang.org/x/tools/gopls@latest
```

Verify it's on your PATH:

```bash
which gopls
# /Users/you/go/bin/gopls

gopls version
# golang.org/x/tools/gopls v0.21.1
```

### 2. Start evva in Your Project

Navigate to a Go project (any directory with a `go.mod` file) and start evva:

```bash
cd /path/to/your-go-project
evva
```

evva auto-detects `go.mod` and `gopls` on PATH — no config file needed.

If auto-detection doesn't work (rare), create a minimal config:

```yaml
# .evva/lsp_servers.yml
servers:
  gopls:
    command: gopls
    extensions:
      ".go": "go"
    startupTimeout: "120s"
    maxRestarts: 3
```

### 3. Verify LSP Is Working

In the evva session, ask the agent to use LSP:

```
find the definition of the Server type in server.go
```

The agent will call `lsp_request` with `operation: "go_to_definition"`. The first request starts gopls (may take 30–60 seconds for initial indexing). Subsequent requests are instant.

To verify manually, check the daemon list:

```
daemon_list
```

You should see an LSP daemon entry:

```
daemon l1 [lsp/running] server=gopls state=running restarts=0/3
```

### 4. Test Common Operations

Try these prompts in evva to exercise different LSP features:

- **Definition:** "where is `Manager` defined in `pkg/tools/lsp/tool.go`?"
- **References:** "find all references to `Daemon` in the project"
- **Hover:** "what type is `ctx` at line 22 of `tool.go`?"
- **Symbols:** "list all symbols in `agent.go`"
- **Workspace search:** "search the workspace for symbols matching 'Agent'"
- **Call hierarchy:** "show me the call hierarchy for `NewTool`"

---

## Setup for Other Languages

### TypeScript / JavaScript

```bash
npm install -g typescript-language-server typescript
```

Auto-detected when `package.json` exists and `.ts`/`.tsx` files are present.

### Rust

```bash
rustup component add rust-analyzer
```

Auto-detected when `Cargo.toml` exists.

### Other Languages

Create `.evva/lsp_servers.yml` with your language's server. Common servers:

| Language | Server | Install |
|---|---|---|
| Python | pyright | `pip install pyright` |
| Zig | zls | [zigtools.org/zls](https://zigtools.org/zls/) |
| C/C++ | clangd | `apt install clangd` / `brew install llvm` |

Config example for Python:

```yaml
servers:
  pyright:
    command: pyright-langserver
    args: ["--stdio"]
    extensions:
      ".py": "python"
    startupTimeout: "60s"
```

---

## Manual Configuration Reference

Create `.evva/lsp_servers.yml` in your project root (project-level) or `~/.evva/lsp_servers.yml` (user-level, applies to all projects). Project-level entries override user-level for the same server name.

Full config format:

```yaml
servers:
  gopls:
    command: gopls                    # required: binary name or path
    args: []                          # optional: CLI arguments
    extensions:                       # required: file extension → language ID
      ".go": "go"
    env:                              # optional: environment variables
      GOPATH: "${HOME}/go"
    startupTimeout: "120s"            # optional: max time to wait for init (default 30s)
    maxRestarts: 3                    # optional: crash recovery limit (default 3)
```

Environment variable expansion (`${VAR}`, `${HOME}`) works in `command`, `args`, and `env` values.

---

## Usage

The `lsp_request` tool is **deferred** — the agent discovers it via `tool_search` when it needs LSP capabilities. You can ask the agent things like:

- "Where is `UserService` defined?"
- "Find all references to `authenticate`"
- "What's the type of this variable?"
- "List all symbols in `handler.go`"
- "Who calls `processRequest`?"

The agent will use `lsp_request` automatically when appropriate.

---

## Checking Server Status

LSP servers register as daemons in evva's daemon system. Use `daemon_list` to see running LSP servers:

```
daemon l1 [lsp/running] server=gopls state=running restarts=0/3
```

Use `daemon_output l1` to see the server's recent log output.

---

## Troubleshooting

**"gopls not found in PATH"**
Install the missing server (see install commands above), restart evva, and try again.

**"No LSP server configured for extension .py"**
Add a config entry for your language's server in `.evva/lsp_servers.yml`. Use the `SuggestServerForExt` hint in the error message to see which server to install.

**Server starts but requests return nothing**
gopls needs time to index your project on first start. Large projects may take 60–120 seconds. Increase `startupTimeout` in config and wait after the first `lsp_request` — subsequent requests will be fast.

**Diagnostics not appearing**
Diagnostics arrive after a file is opened via `lsp_request`. If you edit a file with `write`/`edit`/`bash`, call `lsp_request` on that file to refresh diagnostics.

**Zombie gopls processes**
Run `pkill gopls` to clean up. evva kills servers on shutdown, but if evva crashes, the server process may remain.
