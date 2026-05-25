# commit Create a git commit for the staged + relevant unstaged changes

Use this skill when the user asks for a commit ("commit this", "make a commit", "/commit"). Do NOT use it for amending, rebasing, or pushing.

## Context to gather (run these in parallel)

Before drafting the message, run each of the following with `bash`:

1. `git status` — see all changes (untracked + modified). Never pass `-uall`.
2. `git diff HEAD` — staged and unstaged content together so you can judge what to include.
3. `git log --oneline -10` — match this repo's existing commit style.
4. `git branch --show-current` — for context.

## Git safety protocol

- NEVER update `git config`.
- NEVER skip hooks (`--no-verify`, `--no-gpg-sign`) unless the user explicitly asks.
- CRITICAL: always create a NEW commit. Never use `git commit --amend` unless the user explicitly asks.
- Do NOT commit files that may contain secrets (`.env`, `credentials.json`, `*.pem`, `*.key`). Warn the user if they specifically ask for one of these.
- If there are no changes (no untracked files and no modifications), do not create an empty commit — say so and stop.
- Never run interactive git modes (`git rebase -i`, `git add -i`) — they hang on stdin.

## Draft the message

- Match the style of the recent commits you read above.
- Summarize the change in 1–2 sentences focused on the WHY, not the WHAT (the diff already says what).
- Use "add" for net-new features, "update"/"refactor" for changes to existing features, "fix" for bug fixes, "docs" for documentation-only changes.
- If the change spans multiple unrelated concerns, ask the user (via `ask_user_question`) whether to split into separate commits before drafting.

## Stage and commit

Stage the files you intend to include explicitly by name. Do not run `git add -A` or `git add .` because they may sweep in unrelated artifacts or secrets.

Author the commit as evva. Pass the message via a heredoc so multi-line bodies render correctly:

```
git commit --author="evva <frizoevva@gmail.com>" -m "$(cat <<'EOF'
<your commit message>
EOF
)"
```

## After the commit

Run `git status` once more to confirm the commit landed and the working tree is in the state you expect. Do NOT push unless the user explicitly asks — pushing is a shared-state action that needs separate authorization.

If you also touched `pkg/version/version.go` or `CHANGELOG.md` as part of the change, include those in the commit (they belong with the surface they describe).
