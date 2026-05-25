# setup-hooks Configure lifecycle hooks in evva's settings.json

Use this skill when the user wants something to happen automatically in response to an EVENT ("after writes, run prettier", "before bash, log the command", "when I send a prompt, prepend X"). Automated behaviors require hooks — memory and prompt preferences cannot trigger automated actions; the harness executes hooks, not the agent.

## Where settings live

evva's hook engine reads two files (project hooks fire before user hooks):

| Scope | Path | Git | Use for |
| --- | --- | --- | --- |
| Project | `<workdir>/.evva/settings.json` | commit (team-shared) or gitignore (personal-per-repo) | Team-wide hooks, repo-specific automation |
| User | `<APP_HOME>/settings.json` (typically `~/.evva/settings.json`) | N/A | Cross-project personal hooks |

Read the existing file FIRST before writing. Merge new hook entries with existing ones — never replace the whole file.

## The six events (and only six)

evva supports exactly these events. Do not advertise others.

| Event | When it fires | Receives | Can block? | Can mutate? | Can inject context? |
| --- | --- | --- | --- | --- | --- |
| `SessionStart` | Once, when a session opens | `source`, `model` | No | No (no tool yet) | Yes — `initialUserMessage` or `additionalContext` prepended to the first turn |
| `UserPromptSubmit` | Each time the user submits a prompt | `prompt` | Yes — dropping the prompt with a `reason` | No | Yes — `additionalContext` appended to the prompt |
| `PreToolUse` | Before the permission gate, for every tool call | `tool_name`, `tool_input`, `tool_use_id` | Yes — short-circuits the tool with `block` | Yes — `updatedInput` replaces the args the tool executes with | Yes — folded into the tool result |
| `PostToolUse` | After the tool returns | `tool_name`, `tool_input`, `tool_response`, `is_error` | No (post-hoc) | No | Yes — `additionalContext` appended to the tool result content |
| `Stop` | When the agent reaches a terminal turn (no more tool calls) | `last_assistant_message`, `stop_hook_active` | Yes — re-enters the loop exactly ONCE (the `stop_hook_active` flag guards a second pass) | No | No |
| `Notification` | Out-of-band side channel — iteration limit, approval needed | `message`, `title`, `notification_type` | No (async fire-and-forget) | No | No (stdout ignored) |

## settings.json schema

```json
{
  "hooks": {
    "<EventName>": [
      {
        "matcher": "<tool-name-glob>",
        "hooks": [
          {
            "type": "command",
            "command": "/path/to/hook.sh",
            "timeout": 60
          }
        ]
      }
    ]
  }
}
```

Field rules:
- `matcher` is a doublestar glob on the tool name. Empty = match-all. Required for `PreToolUse`/`PostToolUse`; meaningless for the other four events.
- `type` is `"command"` (shell subprocess) or `"http"` (webhook POST).
- For `type: "command"`: `command` is required (passed to `/bin/sh -c`).
- For `type: "http"`: `url` is required. Optional: `method` (default `POST`), `headers`, `async` (default `true` for http, fire-and-forget).
- `timeout` is seconds in `[1, 600]`. `0` or omit = use the event's default.

## The hook payload (stdin for command, body for http)

Every hook receives a JSON envelope:

```json
{
  "session_id": "...",
  "transcript_path": "...",
  "cwd": "/abs/path/to/workdir",
  "permission_mode": "default" | "accept_edits" | "plan" | "bypass",
  "agent_id": "...",
  "agent_type": "main" | "explore" | "plan" | "general-purpose",
  "hook_event_name": "PreToolUse"
}
```

Per-event fields attach on top — PreToolUse adds `tool_name`, `tool_input` (the JSON the LLM emitted), `tool_use_id`. PostToolUse adds `tool_response`, `is_error` (plus `tool_use_id`). UserPromptSubmit adds `prompt`. Stop adds `last_assistant_message`, `stop_hook_active`.

## The decision JSON (parse stdout, exit 0)

The hook's stdout (when exit 0) is parsed as a JSON decision object:

```json
{
  "continue": true,
  "decision": "approve" | "block" | "",
  "reason": "why",
  "systemMessage": "shown to user",
  "hookSpecificOutput": {
    "permissionDecision": "allow" | "deny" | "ask",
    "permissionDecisionReason": "why",
    "updatedInput": {"command": "echo replaced"},
    "additionalContext": "appended to result / prompt / session start",
    "initialUserMessage": "prepended to the first turn (SessionStart only)"
  }
}
```

Field semantics (cross-reference: `pkg/hooks/decision.go`):
- `continue: false` OR `decision: "block"` → block the operation (PreToolUse / UserPromptSubmit / Stop). On PreToolUse the tool returns an `is_error` result with `reason` as the content.
- `decision: "approve"` → on PreToolUse, allow the tool unconditionally (overrides any pending permission prompt).
- `hookSpecificOutput.permissionDecision` → on PreToolUse, overrides the permission gate's behavior. `"allow"` skips the gate; `"deny"` blocks without asking; `"ask"` forces a prompt even when a rule would auto-allow.
- `hookSpecificOutput.updatedInput` → on PreToolUse, the tool executes with this JSON instead of the LLM's original `tool_input`. Last-write-wins across multiple hooks in the chain.
- `hookSpecificOutput.additionalContext` → text appended to the tool result (PreToolUse / PostToolUse) or the prompt (UserPromptSubmit) or the first turn (SessionStart). Concatenated across hooks.
- `hookSpecificOutput.initialUserMessage` → SessionStart only. Prepended as a synthetic user message at the start of the first turn.

## Exit codes (matter when stdout is not JSON)

- `0` → parse stdout as the decision JSON above. Empty stdout = no opinion.
- `1` → log the error and continue (non-blocking, treated as pass-through).
- `2` → block. Stderr is used as `reason` if no decision JSON is on stdout.
- Timeout → block. The configured timeout (or 60s default) wins.

## Constructing a hook — the verification flow

Don't just write JSON and hope. Follow this flow — each step catches a different failure class. A hook that silently does nothing is worse than no hook.

### Step 1 — Dedup check

Read the target settings file with `read`. If an entry already exists for the same `event + matcher`, show the user the existing command and ask (via `ask_user_question`) whether to keep it, replace it, or add alongside.

### Step 2 — Construct the command for THIS project

The hook receives a JSON payload on stdin. Build a command that:
- Extracts payload fields safely — use `jq -r` into a quoted variable, or `{ read -r f; ... "$f"; }`, NOT unquoted `| xargs` (splits on spaces).
- Invokes the project's actual tool — check `package.json` scripts, `Makefile`, `go.mod`, etc., before assuming `npx` / `bunx` / `npm` / global install.
- Skips inputs the tool doesn't handle — formatters often have `--ignore-unknown`; if not, guard by extension.
- Stays RAW for now — no `|| true`, no stderr suppression. You'll wrap it after pipe-testing.

### Step 3 — Pipe-test the raw command

Synthesize the stdin payload the hook will receive and pipe it through with `bash`:

For `PreToolUse` / `PostToolUse` on `write` / `edit`-like tools:
```
echo '{"tool_name":"edit","tool_input":{"file_path":"<a real file from this repo>"}}' | <cmd>
```

For `PreToolUse` / `PostToolUse` on `bash`:
```
echo '{"tool_name":"bash","tool_input":{"command":"ls"}}' | <cmd>
```

For `SessionStart` / `UserPromptSubmit` / `Stop`: most commands don't read stdin, so `echo '{}' | <cmd>` suffices.

Check the exit code AND the side effect (file actually formatted, log line actually written, etc.). If it fails you get a real error — fix it (wrong package manager? tool not installed? jq path wrong?) and retest. Once it works, wrap with `2>/dev/null || true` UNLESS the user wants the hook to block on failure.

### Step 4 — Write the JSON

Merge the new entry into the target file with `edit`. If you're creating `<workdir>/.evva/settings.json` for the first time, ALSO add it to `.gitignore` if the project doesn't already commit `.evva/` — the `write` tool doesn't auto-gitignore. (Project-shared hooks belong in a committed file; personal-per-repo hooks belong in a gitignored one. Ask if unclear.)

### Step 5 — Validate syntax + schema

Run with `bash`:

```
jq -e '.hooks.<EventName>[] | select(.matcher == "<matcher>") | .hooks[] | select(.type == "command") | .command' <target-file>
```

Exit 0 with your command printed = correct. Exit 4 = matcher doesn't match the path. Exit 5 = malformed JSON or wrong nesting. **A broken settings.json silently disables ALL hooks from that file** — fix any pre-existing malformation too, with the user's permission.

### Step 6 — Prove the hook fires (PreToolUse / PostToolUse only)

For `Pre/PostToolUse` on a matcher you can trigger in-turn (`edit` for write-like tools, `bash` for bash):
- For a formatter on `PostToolUse` for `write|edit`: introduce a detectable violation via `edit` (two consecutive blank lines, bad indentation, missing semicolon — something this formatter corrects; NOT trailing whitespace, `edit` strips that before writing), re-read, confirm the hook **fixed** it.
- For anything else: temporarily prefix the command in settings.json with `echo "$(date) hook fired" >> /tmp/evva-hook-check.txt; `, trigger the matching tool (an `edit` for `write|edit`, a harmless `bash` `true` for `bash`), read the sentinel file.

**Always clean up** — revert the violation, strip the sentinel prefix — whether the proof passed or failed.

For `SessionStart`, `UserPromptSubmit`, `Stop`, `Notification`: those fire outside this turn. Skip the proof; trust the pipe-test from step 3 plus the `jq -e` validation in step 5.

### Step 7 — Handoff

Tell the user the hook is live, point them at the target settings file path so they can edit or disable it later, and remind them that:
- Project hooks (`<workdir>/.evva/settings.json`) fire BEFORE user hooks (`<APP_HOME>/settings.json`) in the dispatcher's sequential walk.
- A `continue: false` from an earlier hook short-circuits later hooks in the chain.

## Common patterns

### Auto-format on write

```json
{
  "hooks": {
    "PostToolUse": [{
      "matcher": "write|edit",
      "hooks": [{
        "type": "command",
        "command": "jq -r '.tool_input.file_path' | { read -r f; gofmt -w \"$f\" 2>/dev/null || prettier --write \"$f\" 2>/dev/null; } || true"
      }]
    }]
  }
}
```

### Block destructive bash commands

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "bash",
      "hooks": [{
        "type": "command",
        "command": "jq -e '.tool_input.command | startswith(\"rm -rf\")' >/dev/null && echo '{\"continue\":false,\"reason\":\"rm -rf blocked by project policy\"}' || echo '{}'"
      }]
    }]
  }
}
```

### Log every bash command

```json
{
  "hooks": {
    "PreToolUse": [{
      "matcher": "bash",
      "hooks": [{
        "type": "command",
        "command": "jq -r '\"\\(now) \\(.tool_input.command)\"' >> ~/.evva/bash-log.txt"
      }]
    }]
  }
}
```

### Inject context at session start

```json
{
  "hooks": {
    "SessionStart": [{
      "hooks": [{
        "type": "command",
        "command": "echo '{\"hookSpecificOutput\":{\"additionalContext\":\"This repo deploys via GitHub Actions on tag push. Do not push to main directly.\"}}'"
      }]
    }]
  }
}
```

## Troubleshooting

If a hook isn't firing:
1. Re-read the settings file with `read`. Confirm the JSON is valid.
2. Run the `jq -e` validation from Step 5.
3. Check the matcher matches the tool's wire name. Tool names are lowercase: `bash`, `edit`, `write`, `read`, `grep`, etc. (see `pkg/tools/name.go`).
4. Pipe-test the command in isolation (Step 3) — if it doesn't work in the shell, it won't work as a hook.
5. Settings files are loaded at session start. If you edited settings mid-session, the user needs to restart evva for the new hooks to take effect.

## Reference

- Engine: `pkg/hooks` (loader, dispatcher, runner, decision).
- Wiring & guarantees: `docs/extending.md` → `## Lifecycle hooks`.
