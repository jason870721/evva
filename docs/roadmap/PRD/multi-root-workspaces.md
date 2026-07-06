# PRD — Multi-Root Workspaces (one session, several repos) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W29**, suggested horizon H3
> per [../long-range.md](../long-range.md) §3b.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> Real work spans repos: a service + its client SDK, a backend + the
> infra repo, evva + its `ref/` twin. evva's session model assumes one
> `WorkDir`; today the operator either starts the session at a common
> parent (losing project config, EVVA.md, and permission scoping for
> both repos) or shuttles between sessions by hand. Editors solved
> this a decade ago with workspace folders; LSP has native support.
> **Reference source:** none — evva-native; prior art: VS Code
> multi-root workspaces, LSP `workspaceFolders`.

---

## 1. TL;DR

Let a session declare **roots** — an ordered set of directories, each a
first-class project:

```
evva --root ~/work/api --root ~/work/api-client
# or in-session:
/root add ~/work/infra
```

Each root brings its own project identity: its EVVA.md/agent
instructions, its `.evva/` config layer, its git state, its LSP
servers, its permission scope. The session presents them to the model
as named roots (`api`, `api-client`) with a combined map; relative
paths in tool calls resolve against a **primary root** (the first),
and cross-root paths are always explicit — no ambient ambiguity.

What this is *really* buying, in order: (1) correct project config for
every file the model touches — today the second repo's EVVA.md simply
never loads; (2) permission scoping that matches operator intent —
"allow edits under api-client" is currently inexpressible; (3) sane
LSP/git behavior per repo instead of a confused common-parent view.

## 2. Goals / non-goals

### Goals

- Root model: ordered list, first = primary; add/remove in-session
  (`/root`); persisted in the session (resume restores all roots —
  session-tree W7 interplay).
- Config layering per root: instruction files and `.evva/` config load
  per root; the audit maps how the current single-project loading works
  and generalizes it. Conflicts resolve by explicit precedence
  (primary root wins for session-global knobs; file-scoped behavior
  follows the file's root).
- Tool-path semantics: every fs/shell tool call resolves its paths to
  exactly one root; results are rendered with root-prefixed display
  paths (`api-client:src/index.ts`). `bash` runs with cwd = primary by
  default, per-call `workdir` override validated against the root set.
- Permission scoping: rules gain an optional root qualifier; the
  default expansion of "allow X in this project" binds to the root it
  was granted in.
- Per-root subsystem instances: git status/operations, LSP managers
  (or LSP workspace-folders where servers support it — audit decides
  per-server), repo-map sections labeled by root.
- Subagent/worktree interplay: worktree isolation binds to the root the
  task targets; a spawn can scope a subagent to a single root (a
  cheaper, honest "focus" mechanism).

### Non-goals (this wave)

- Cross-root atomic operations (a "commit in both repos" macro) — the
  model coordinates; the wave provides correct per-root primitives.
- Monorepo *sub*-root intelligence (workspaces inside one git repo —
  package-level scoping is a different, later problem).
- Swarm multi-root spaces (members keep single workdirs; a swarm
  wanting two repos uses two members or one member with this feature
  once solo semantics soak).
- Virtual/remote roots (all roots are local directories).

## 3. Design sketch

- **The audit's core question:** how deeply does single-`WorkDir`
  assumption run (config, toolset construction, permission matcher,
  LSP wiring, repo-map, checkpoint scope)? The wave's real cost is
  this inventory; the design below assumes the seams are localized —
  if they aren't, ticket zero is a `RootSet` abstraction those
  subsystems consume instead of a bare workdir string.
- **Explicitness over cleverness:** no fuzzy "which repo did you
  mean" inference. Ambiguous relative paths resolve to primary,
  period; the display-path convention keeps the model's world labeled;
  cross-root references require the `root:` prefix or an absolute
  path. Predictability is the feature.
- **Checkpoint/rewind scope:** checkpoints capture per-root git state;
  rewind restores all roots to the checkpoint's snapshot set — the
  invariant "one checkpoint = one consistent world" holds across
  roots or the feature refuses (mid-session root adds create a
  checkpoint epoch boundary).
- **Context surfaces:** session-open loads each root's instructions
  (labeled), the map interleaves labeled sections, and the status bar
  shows root count + primary.

## 4. Work items

- **WSP-1 — RootSet inventory + abstraction (ticket zero).** Audit
  every workdir consumer; introduce the RootSet seam with
  single-root behavior byte-identical. *Accept:* full suite green with
  RootSet in place and one root — the refactor lands invisible.
- **WSP-2 — Root lifecycle + CLI/TUI.** `--root` flags, `/root`
  add/remove/list, session persistence, status bar. *Accept:* two-root
  session round-trips through resume with both roots live.
- **WSP-3 — Path resolution + display convention.** Resolver,
  root-prefixed rendering, bash workdir validation. *Accept:* table
  tests over relative/absolute/prefixed paths; escape attempts
  (`../../other`) resolve-or-refuse correctly per scoping rules.
- **WSP-4 — Per-root config + instructions.** Layered load, labeled
  session-open injection, precedence rules. *Accept:* both fixture
  roots' EVVA.md contents present and labeled; conflicting knobs
  follow documented precedence.
- **WSP-5 — Permission scoping.** Root qualifier in rules, grant-time
  binding, matcher integration. *Accept:* an edit-allow granted in
  root A does not authorize root B (regression-style table tests).
- **WSP-6 — Subsystem instances.** Per-root git/LSP/repo-map wiring +
  checkpoint multi-root scope. *Accept:* mixed-language two-root
  fixture shows per-root LSP diagnostics and a labeled combined map;
  rewind restores both roots.
- **WSP-7 — Docs + changelog.** User-guide (en + zh-tw): when to use
  roots vs separate sessions, path conventions, permission semantics.

## 5. Risks

| Risk | Mitigation |
|---|---|
| WorkDir assumptions are load-bearing everywhere (the refactor balloons) | WSP-1 is a formal inventory with a go/re-scope decision before further tickets; single-root fast path stays the untouched default |
| Model confuses roots despite labeling | explicit resolution rules + prefixed display paths + prompt guidance; ambiguity always resolves the same way |
| Permission-scope regressions (the security-relevant surface) | rule qualifier ships with exhaustive matcher table tests; default-deny posture across roots unless granted |
| Checkpoint consistency across roots | epoch-boundary rule on root add/remove; refuse cross-epoch rewind with a clear message |

## 6. Open questions

1. Root aliases: auto-derive from dir names (collision-suffixed) or
   require explicit names on add? Leaning auto with `--as` override.
2. Should `/root add` under an active worktree session be allowed, or
   is root topology frozen while isolated? Leaning frozen — worktrees
   and root churn compose badly.
3. Primary-root switching mid-session (`/root primary <name>`) — v1 or
   fast-follow? Leaning fast-follow; changing path semantics mid-flight
   is subtle.
