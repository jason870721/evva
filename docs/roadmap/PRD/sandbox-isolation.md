# PRD — Sandboxed Execution (OS-level isolation for bash + fan-out) — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** ✅ **BUILT** — SBX-1..7 implemented 2026-08-01 on
> `feature/sandbox-isolation`. The audit pass required by the concept → build
> gate ([../long-range.md](../long-range.md) §1 step 2) was run at
> `dev@7ae8d7a` (v1.14.0); the six corrections it produced are recorded below
> and the body of this document is left as written so the delta stays legible.
> **Target release:** claims **v1.15** — W3's second half, landing four
> minors after its tentative slot because W3/v1.13 went to MCP server mode
> and SEC took v1.14.
> **Unblocked 2026-08-01.** This wave sat blocked, not merely unstarted:
> `secret-redaction.md`'s header and `overview.md` §5 both recorded that its
> acceptance criteria (§8) need a working `docker`/`podman` and the build
> machine had none. Docker **28.1.1** is now installed and verified running
> containers, so §8 is reachable and the wave was picked up.
> **Roadmap source:** 2026-07-06 web research pass — OS-level agent
> sandboxing (microVMs, gVisor, containers) is one of the defining 2026
> agentic-coding trends (Codex "bets on cloud sandboxing"; E2B, Modal,
> Northflank, Cloudflare, Vercel all shipped sandbox execution products in
> the last ~18 months). evva runs `bash` directly on the host with zero OS
> isolation today.
> **Evaluation provenance:** live-source audit at `dev@ef84887`
> (v1.10.0-beta.1), 2026-07-06; **re-audited at `dev@7ae8d7a` (v1.14.0),
> 2026-08-01** — see the corrections below.
> **Reference source:** none — no `ref/src` analogue (Claude Code's own
> sandboxing is a hosted-product concern outside the ported surface).
> Evva-native, motivated by the external sandboxing ecosystem, not a port.
>
> ### Audit-pass corrections
>
> 1. **§3.4 is materially obsolete — SWT already closed the filesystem
>    half.** The PRD's highest-leverage claim was that swarm clones are "the
>    single least-supervised `bash`-capable code path", because
>    `constructMember` "never reassigns `WorkDir`". SWT-1..8 (v1.12, shipped
>    after this PRD was drafted) changed exactly that:
>    `internal/swarm/space.go:454` now sets `acfg.WorkDir =
>    memberWorktree(sess, sp.Workdir)` whenever
>    `agentdef.ResolveWorktree(sp.settings.WorktreeIsolation, ld.Worktree)`
>    is true, plus `acfg.SessionWorkdir = sp.Workdir` for root-state pinning
>    (D7). And `CloneMember` copies the base's whole `Loaded`
>    (`internal/swarm/spawn.go:59`) including `Worktree`, so an ephemeral
>    clone **inherits its base's isolation stance and gets its own worktree +
>    branch**. What survives of §3.4: `member_spawn`'s schema still has no
>    isolation knob (`from`/`count`/`retire` only), and there is still zero
>    **process/network** isolation anywhere. The gap is real; it is just
>    one axis narrower than the PRD describes.
> 2. **Therefore §4.4 / SBX-5's shape is wrong.** Adding an `isolation` enum
>    to `member_spawn`'s tool schema would fork the vocabulary a minor after
>    SWT established the idiom: space-wide `Settings.WorktreeIsolation` +
>    per-member `Member.Worktree` override, resolved through
>    `agentdef.ResolveWorktree` (`manifest.go:162-167`). Sandboxing follows
>    that same shape — `Settings.Sandbox` + `Member.Sandbox` +
>    `ResolveSandbox` — because clone-inherits-base-definition is precisely
>    the right inheritance semantics for "this worker is untrusted".
>    **SBX-5 ships as a manifest/settings field, not a spawn-tool parameter.**
> 3. **§4.1 got cheaper.** "Sandbox = worktree + bind-mounted container" no
>    longer needs to build the worktree half for the swarm — SWT provisions
>    `mode.WorktreeSession` per member already, carrying `Path`/`Branch`/
>    `RepoRoot`. The container is a strict addition on top of an existing
>    session object.
> 4. **§4.1/§4.2 miss a path-mapping requirement.** The design bind-mounts
>    the worktree at `/workspace` and assumes `cmd.Dir` handles the rest, but
>    `cmd.Dir` is a **host** path and means nothing to `docker exec`. Where a
>    workdir is nested inside the mounted root — exactly what SWT's
>    `memberWorktree(sess, sp.Workdir)` produces for a space nested in a
>    larger repo — the host path must be mapped onto its guest equivalent and
>    passed as `docker exec -w`. Implemented as `Container.GuestPath`.
> 5. **§3.1 verified unchanged, so §4.2's minimal diff applies verbatim.**
>    Four minors on, every reference still lands: `exec.CommandContext` at
>    `bash.go:170`, `cmd.Dir` at `:171`, timeouts `:37-39`, `proc.Group`
>    `:182`, `KillTree` `:186-189`, `WaitDelay` `:193`, and the no-op
>    `dangerouslyDisableSandbox` at `:91,109,145-147`.
> 6. **§3.6 undercounts the work.** `daemon.Kind` did gain members since
>    (`KindLocalWorkflow`, `KindDream`), and a new kind must also register a
>    rune in the `idPrefix` map (`pkg/tools/daemon/kind.go:41-49`) — not just
>    the const block. §3.7's greenfield claim otherwise holds: every
>    docker/podman hit in `pkg`+`internal` is a comment or a test, with one
>    reusable precedent — `pkg/mcp/config.go:192-195` already identifies
>    container runtimes by command basename to warn about `--rm`-less MCP
>    servers.
>
> **Line drift** (informational, four minors' worth): `SetWorktreeController(a)`
> `agent.go:568` → `:637`; `resolveWorktreeController` `:65-70` → `:71-76`;
> `WorktreeControllerLookup` `:38` → `:34-38`; the `WorktreeSession` struct now
> lives in `worktree_controller.go:46-69` and gained `RepoRoot` (SWT);
> `spawn.go`'s `req.Isolation == "worktree"` `:62` → `:83` and `cfgClone`
> `:67-69` → `:88-90`; `ToolState.Workdir()` `:371-376` → `:458-463`;
> `SetWorktreeController` `toolset.go:229-239` → `:283-287`; `NewBashWithHost`
> `builtins.go:72` → `:78`; worktree provisioning split out into
> `internal/tools/mode/worktree_core.go`.

---

> ## ⚠️ Terminology collision — read this first
>
> evva already has a config knob spelled `dangerouslyDisableSandbox`
> (`pkg/tools/shell/bash.go:91,109,145-147`) — **it is an explicit no-op**.
> Its own comment says why: *"the permission gate now mediates execution"* —
> in evva's current vocabulary, "sandbox" means **the permission-approval
> layer** (should this dangerous action ask the user first?), not OS
> isolation. `pkg/permission/decision.go:278` says it outright: *"a guardrail
> for the file tools, not a sandbox."*
>
> **This PRD is about a completely different axis**: process/filesystem
> isolation at the OS level, orthogonal to whether an action needed approval.
> A command can be permission-approved *and* still run unsandboxed (today,
> always) or sandboxed (this PRD). Implementers must not conflate the two —
> recommend the new knobs use a name that doesn't collide (`sandbox_runtime`
> is used below **for the container mechanism specifically**; consider
> renaming the existing dead `dangerouslyDisableSandbox` flag in the same
> changeset to reduce future confusion, e.g. to `dangerouslySkipApproval`,
> as a small honest cleanup — flagged as an open question, §7).

---

## 1. TL;DR

Every evva `bash` call — solo session, subagent, or swarm member — runs as a
direct host subprocess: `exec.CommandContext(cctx, shell, "-c",
in.Command)` (`pkg/tools/shell/bash.go:170`, mirrored in
`bash_daemon.go:158`). There is a timeout and a process-group kill-tree
(§3.1), but **no resource limits, no filesystem jail, no network boundary**.
The existing `isolation: "worktree"` option (subagent spawn, `parallel-
fanout-reconcile`) only isolates the **git tree** — it is explicitly
documented as filesystem-only (`internal/tools/meta/spawner.go:33-41`:
*"Isolation selects a **filesystem**-isolation strategy"*) with zero
process/network isolation. And the swarm's brand-new ephemeral fan-out
clones (`member_spawn`, DWF-1..8, shipped in `v1.10.0-beta.1`) don't even
get *that* — `constructMember` clones the config but never reassigns
`WorkDir` (`internal/swarm/space.go:345-348`), so a clone shares the parent
space's exact workdir and host process, and `newMemberSpawn`'s schema has no
isolation option at all (`internal/swarm/tools/members.go:22-60`).

This PRD adds an OS-level isolation tier: **`isolation: "sandbox"`** runs the
worktree-isolated session's `bash` calls inside a container instead of
directly on the host, via `docker exec` / `podman exec` against a
bind-mounted, long-lived container — shelled out exactly the way evva
already shells out to `git` for worktrees (no new Go dependency, per
CLAUDE.md's "minimize external dependencies"). It closes the isolation gap
for the two contexts that most need it: **subagents processing
untrusted/external content**, and **ephemeral swarm clones that retire
themselves with no operator review in the loop**.

---

## 2. Goals / non-goals

### Goals

- A new `isolation: "sandbox"` value (superset of `"worktree"`: git-tree
  isolation **plus** an OS-process/filesystem/network boundary) for subagent
  spawn (`internal/agent/spawn.go`) and — the higher-leverage target — for
  swarm `member_spawn`, which has no isolation option today at all.
- `bash` calls in a sandboxed session run inside a container via `docker
  exec`/`podman exec`; the existing timeout, kill-tree, and workdir logic in
  `bash.go`/`bash_daemon.go` wrap unchanged — only the command line
  routing changes (§4.2).
- Image selection follows an existing standard rather than inventing one:
  if the repo has a devcontainer config (`.devcontainer/devcontainer.json`),
  use its image; otherwise an explicit `sandbox_image` config is required;
  otherwise sandboxing **refuses to start, loudly** (§4.3) — never silently
  degrades to unsandboxed.
- Zero new Go dependencies — shells out to whatever container runtime
  (`docker` or `podman`) is already on the operator's `PATH`, the same
  external-binary pattern already used for `git`.

### Non-goals (this wave)

- **microVMs (Firecracker) or gVisor.** Real 2026 prior art (E2B uses
  Firecracker, Modal uses gVisor) but both are cloud-platform technologies
  built for multi-tenant server fleets, not a developer's local Windows/
  macOS/Linux laptop running a CLI tool. Container isolation (weaker, but
  universally available via Docker Desktop) is the right v1 tier for where
  evva actually runs.
- **Remote/hosted sandbox providers** (pointing evva at an E2B- or
  Modal-style API instead of local Docker). Plausible future backend once
  there's a concrete "evva runs somewhere without a local daemon" need —
  not this wave (§7).
- **Network-level policy beyond an on/off knob.** `sandbox_network:
  "allow"|"none"` is the whole surface; no per-domain allowlists, no proxy
  interception.
- **Building custom images.** v1 only *runs* an existing image
  (devcontainer or explicit config); it never builds one.
- **Sandboxing the `fs` edit/write tools' host-side execution.** Those stay
  exactly as today (they write to the worktree checkout on the host
  filesystem so the container can see the results via bind mount — see
  §4.1). Only `bash` execution moves into the container. This mirrors why
  `edit-diagnostics-sync.md` and `resilient-edit.md` never touched `bash`
  either — different tool, different tier.
- **Windows-native isolation primitives** (Job Objects) or **macOS
  `sandbox-exec`** (Apple-deprecated). If a container runtime responds on
  `PATH`, use it, regardless of host OS — no OS-specific branching.

---

## 3. Verified current state

### 3.1 Bash execution today — direct host exec, no resource limits

- `exec.CommandContext(cctx, shell, "-c", in.Command)` — `bash.go:170`
  (synchronous) and `bash_daemon.go:158` (background daemon); shell resolved
  by `proc.Shell()` (`pkg/common/proc/shell.go:26` — `/bin/sh` or Git Bash).
- Timeout: `defaultBashTimeout` = 2m, `maxBashTimeout` = 10m
  (`bash.go:36-39`) via `context.WithTimeout` (`:167`). On timeout/cancel,
  `proc.Group` (`:182`) + `cmd.Cancel` → `proc.KillTree` (`:186-189`) +
  `cmd.WaitDelay = bashKillGrace` (`:193`) kill the whole process-group tree
  — mirrored in `bash_daemon.go:162-169`. This machinery is solid and
  reused unchanged (§4.2).
- **Resource limits: none.** Confirmed by reading
  `pkg/common/proc/{proc,proc_unix,proc_windows}.go` end to end — no
  rlimit/cgroup/ulimit/`Rusage` anywhere, only the kill-tree logic above.
- **Filesystem scoping: `cmd.Dir` only.** `cmd.Dir = t.workdir`
  (`bash.go:171`) / `cmd.Dir = d.workdir` (`bash_daemon.go:159`) is a plain
  `exec.Cmd.Dir` — not a chroot/jail. Nothing stops `cd /` or an absolute
  path escaping the workdir.
- `dangerouslyDisableSandbox` is accepted as a parameter but is an
  **explicit no-op** (`bash.go:91,109,145-147`) — see the terminology
  callout above. Today, "sandbox" in evva's own vocabulary already means
  something else (the permission gate).

### 3.2 The `WorktreeController` seam (the pattern to extend)

- Interface: `internal/tools/mode/worktree_controller.go:24-32`; late-bound
  lookup closure `WorktreeControllerLookup` (`:38`), resolved via
  `resolveWorktreeController` (`:65-70`). `EnterWorktree`/`ExitWorktree`
  call it inside `Execute` (`worktree.go:89,239`). Wired into `ToolState`
  (`internal/toolset/toolset.go:58,229-239`), registered in
  `internal/toolset/builtins.go:130-137`; the agent installs itself as the
  controller only when it's root (`internal/agent/agent.go:564-568`,
  `SetWorktreeController(a)` at `:568`).
- Contrast with the checkpoint sink's pattern (a chained functional option
  at build time, `fs.NewEdit(...).WithCheckpoints(ts.CheckpointSink())`,
  `builtins.go:50,54`, fed by `SetCheckpointSink`/`agent.go:372`, gated by
  `!a.IsSubagent() && a.cfg.GetEnableCheckpoints()` at `agent.go:368`) — the
  worktree/plan pattern **re-resolves the controller at `Execute` time**
  instead of capturing it at construction, specifically to dodge an
  init-order hazard. A sandbox controller needs a live session handle
  (the running container ID) in the same way a worktree session needs a
  live checkout path, so it should follow the **worktree** pattern, not the
  checkpoint pattern.

### 3.3 Subagent spawn — filesystem-only, explicitly documented as such

- `req.Isolation == "worktree"` (`internal/agent/spawn.go:62`) →
  `mode.CreateForSubagent` (`worktree.go:646`) → `cfgClone := a.cfg.Clone();
  cfgClone.WorkDir = sess.Path` (`spawn.go:67-69`) → `WithConfig(childCfg)`
  (`:77`). The child's `ToolState.Workdir()` (`toolset.go:371-376`) then
  returns the worktree path, and `shell.NewBashWithHost(ts.Workdir(),ts)`
  (`builtins.go:72`) captures it at construction.
- `internal/tools/meta/spawner.go:33-41` documents the boundary explicitly:
  *"Isolation selects a **filesystem**-isolation strategy... its filesystem
  mutations stay off the host workdir."* No process or network isolation is
  implied or present — this PRD is exactly the gap that sentence describes.

### 3.4 Swarm `member_spawn` — no isolation option at all

- `CloneMember` (`internal/swarm/spawn.go:44-80`) → `constructMember`
  (`space.go:345`) does `acfg := sp.cfg.Clone()` (`:348`) but **never
  reassigns `WorkDir`** — an ephemeral clone shares the parent
  `SwarmSpace`'s exact workdir and host process. `newMemberSpawn`'s schema
  (`internal/swarm/tools/members.go:22-60`) has only `from`/`retire`/
  `count` — unlike the `AgentTool`, there is no isolation knob whatsoever.
  Given these clones **self-retire with no operator review gate**
  (`on_complete`, per the DWF-1..8 changelog entry), this is the single
  least-supervised `bash`-capable code path in the system today, and the
  highest-leverage place to land sandboxing first.

### 3.5 Config knob template

`EnableCheckpoints` is the pattern to copy: field + doc (`config.go:
169-177`), `Clone()` copy (`:271-272`), mutex-guarded `Get/Set…` +
`SaveFile()` (`:382-398`), YAML pointer field (`file_config.go:75-81`, `*bool`
so nil≠false), defaulting (`load.go:230-239`), assembly (`:281-282`),
round-trip (`config.go:854,882`).

### 3.6 A naming seam already exists for this

`pkg/tools/daemon/kind.go:26,33` already distinguishes `KindLocalBash` from
`KindRemoteAgent` for daemon/process observability — a natural home for a
new `KindSandboxedBash` so `worktree_list` and daemon listings can show
which processes are running inside a container (§5, SBX-6).

### 3.7 Grep confirms this is genuinely greenfield

`grep -rliE "docker|container|firecracker|gvisor|\bsandbox\b" --include=
"*.go" pkg internal` returns zero implementation hits — only comments
(the `dangerouslyDisableSandbox`/permission-gate prose above, and one
unrelated leaked-container mention in a test comment). No container/VM
library is vendored anywhere.

---

## 4. Design

### 4.1 Shape: sandbox = worktree + a bind-mounted container

A sandboxed session is a worktree session (unchanged — the `fs` edit/write
tools keep writing to the worktree checkout on the host, §2 non-goals) with
one addition: after the worktree is created, start a long-lived container
with that worktree bind-mounted at `/workspace`:

```
docker run -d --rm -v <worktree-path>:/workspace -w /workspace \
  [--network none if sandbox_network=="none"] <image> sleep infinity
```

`bash` calls for that session then run as `docker exec <container-id>
<shell> -c <command>` instead of a direct host `exec.Cmd` — one `docker
run` per session (paying container-startup cost once), many cheap `docker
exec`s thereafter, mirroring how the daemon bash already amortizes a
long-lived process (`bash_daemon.go`).

**What this isolates:** the rest of the host filesystem (no `~/.ssh`, no
sibling repos, no `rm -rf /` outside the mount), arbitrary installs (`curl |
sh` lands in the disposable container, not the host), and — with
`sandbox_network: "none"` — network access entirely. **What this does not
isolate:** the worktree checkout itself, which the container can freely
read/write (by design — the agent needs to see its own build output). Git-
tree isolation (already shipped) and OS isolation (this PRD) are separate,
complementary boundaries: one protects the rest of the repo from this
session, the other protects the rest of the host from this session.

### 4.2 Bash routing — minimal-diff wrapper, not a rewrite

`bash.go:170`'s call site gains one branch:

```go
if sandboxID := t.sandboxContainerID(); sandboxID != "" {
    cmd = exec.CommandContext(cctx, containerRuntime, "exec", sandboxID, shell, "-c", in.Command)
} else {
    cmd = exec.CommandContext(cctx, shell, "-c", in.Command) // unchanged
}
```

Everything downstream — timeout, `proc.Group`/`KillTree`, `WaitDelay`,
output capture — is unchanged; `docker exec` is itself just a subprocess,
so the existing kill-tree logic still owns it correctly. `bash_daemon.go`
gets the identical branch.

### 4.3 Image selection — reuse devcontainer.json, don't invent a config

On sandbox session start:

1. If `<repo>/.devcontainer/devcontainer.json` exists, parse its `image` (or
   resolve `dockerFile`/`build.dockerfile` — a fast-follow if the first cut
   only handles the plain `image` key) and use it. This piggybacks on a
   convention already adopted by VS Code Dev Containers and GitHub
   Codespaces — evva doesn't need to invent image-selection UX or ask the
   operator to duplicate config they likely already have.
2. Else, require an explicit `sandbox_image` setting.
3. Else, **refuse to start the sandboxed session** with a clear error
   ("no devcontainer.json and no `sandbox_image` configured — sandboxing
   needs an image"). Never silently fall back to unsandboxed execution —
   that would be a silent safety downgrade exactly when the operator
   thought they'd opted into isolation.

### 4.4 Isolation enum + swarm parity

- `internal/agent/spawn.go`: `Isolation` gains a third value, `"sandbox"`
  (worktree + container, per §4.1) alongside the existing `""`/`"worktree"`.
- `internal/swarm/tools/members.go`'s `newMemberSpawn` schema gains an
  `isolation` field with the same three values — closing the gap in §3.4.
  This is the PRD's highest-value single change: ephemeral, self-retiring
  clones get an actual isolation option for the first time.

### 4.5 Config knobs (follow §3.5's template exactly)

- `sandbox_runtime`: `""` (off, default) | `"docker"` | `"podman"`.
- `sandbox_image`: optional explicit override (§4.3).
- `sandbox_network`: `"allow"` (default — most build/test flows need a
  package registry) | `"none"`.

---

## 5. Work items

**SBX-1 — Sandbox session + container lifecycle.**
Extend `WorktreeSession` (or a sibling struct) with a container ID; on
sandbox-mode `EnterWorktree`, resolve an image per §4.3, `docker/podman run
-d --rm -v ... sleep infinity`; on `ExitWorktree`, `docker rm -f` after the
existing merge/teardown. *Accept:* a sandboxed session's container exists
for the session's lifetime and is removed on exit (success or abort); no
devcontainer + no `sandbox_image` → clear refusal, not silent unsandboxed
fallback.

**SBX-2 — Bash routing.**
`bash.go`/`bash_daemon.go` gain the branch in §4.2. *Accept:* a command run
in a sandboxed session executes inside the container (verifiable via a
container-only marker file/hostname) while timeout/kill-tree behavior is
provably unchanged (existing bash tests still pass unmodified; new tests
cover the container-exec branch with a fake `docker`/`podman` stub binary).

**SBX-3 — Config knobs.**
`sandbox_runtime`/`sandbox_image`/`sandbox_network`, following §3.5's
template. *Accept:* round-trips through YAML load/save; defaults are all
off/allow (opt-in, matching every other recent knob's precedent).

**SBX-4 — Subagent `isolation: "sandbox"`.**
`internal/agent/spawn.go` + `internal/tools/meta/spawner.go` doc update.
*Accept:* `AgentTool` spawn with `isolation:"sandbox"` produces a worktree
session backed by a running container; `"worktree"` behavior is
byte-for-byte unchanged (regression guard).

**SBX-5 — Swarm `member_spawn` isolation parity.**
`internal/swarm/tools/members.go` schema + `CloneMember`/`constructMember`
wiring (`internal/swarm/spawn.go`, `space.go:345-348`). *Accept:* a
`member_spawn` call with `isolation:"sandbox"` produces a clone whose `bash`
calls run containerized; default (no `isolation` field, back-compat) is
today's unisolated behavior, unchanged.

**SBX-6 — Observability.**
`daemon.Kind` gains `KindSandboxedBash` (§3.6); `worktree_list` surfaces
which live worktrees are sandbox-backed. *Accept:* a sandboxed session shows
up distinctly in `worktree_list` output.

**SBX-7 — Docs + version + changelog.**
User-guide "Sandboxed execution" (en + zh-tw): prerequisites (Docker/Podman
installed), the devcontainer.json convention, the network knob, the "this
isolates the host, not the worktree" distinction (§4.1); `CHANGELOG.md`;
`pkg/version/version.go`. Resolve the `dangerouslyDisableSandbox` naming
question (§7 open question 1) in this same changeset if the operator agrees.

Sequencing: `SBX-1 → SBX-2 → SBX-3 → SBX-4 → SBX-5 → SBX-6 → SBX-7`. SBX-5
(swarm parity) is the highest-value item but depends on SBX-1..3 landing
first for the subagent path to prove the mechanism.

---

## 6. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Docker/Podman not installed, or daemon not running | Fail loud at session-start with a clear error; **never** silently degrade to unsandboxed execution (§4.3) — that's a safety-critical invariant, not a nice-to-have |
| Container startup latency | One `docker run` per session lifetime, not per `bash` call; `docker exec` into an already-running container is cheap (mirrors the existing bash-daemon amortization pattern) |
| Bind mount gives the container full read/write to the worktree | By design (§4.1) — this isolates the *host*, not the worktree from itself; document the boundary explicitly so it isn't mistaken for a stronger guarantee than it provides |
| First-run image pull latency (devcontainer or configured image) | Document as a real, expected cost; no attempt to pre-warm or cache beyond what Docker/Podman already do |
| Rootless Podman vs. rootful Docker permission differences across platforms | Out of scope — require "a working `docker`/`podman` CLI on `PATH`" and defer to the operator's existing container setup |
| Confusing this with the existing (dead) `dangerouslyDisableSandbox` permission flag | The terminology callout at the top of this document, plus the renaming open question (§7) |

---

## 7. Open questions

1. **Rename the dead `dangerouslyDisableSandbox` flag** (e.g. to
   `dangerouslySkipApproval`) in the same changeset, to stop the vocabulary
   collision at the source? Recommend yes, small and honest, but it's a
   user-facing config-key rename — operator sign-off needed (breaking
   change for anyone with it set explicitly, however unlikely).
2. **`devcontainer.json` scope for v1** — plain `image` key only, or also
   resolve `build.dockerfile`? Recommend `image`-only first cut (§4.3);
   Dockerfile builds add real complexity (build context, cache) for
   uncertain initial demand.
3. **Remote/hosted sandbox backends** (E2B/Modal-style, §2 non-goals) as a
   future pluggable `sandbox_runtime` value? Recommend defer until there's
   a concrete "evva runs without a local Docker daemon" scenario (e.g. a
   future hosted-evva product) — local containers cover the safety story
   for the CLI tool evva is today.
4. **Should `"sandbox"` always imply `"worktree"`, or should sandboxing-
   without-worktree (containerize the root workdir directly) be
   supported?** Recommend always-bundled for v1 — simpler mental model,
   and root-workdir sandboxing raises its own questions (what happens to
   host-side changes made outside the agent during the session). Revisit
   if a real use case needs it.

---

## 8. Rollout

1. `SBX-1..7` via `feature/sandbox-isolation` → `dev`.
2. `pre-release feature` cuts the first beta under the minor assigned at
   wave confirmation.
3. Beta validation: a subagent spawned with `isolation:"sandbox"` against a
   repo with a real `devcontainer.json`; a `member_spawn` clone with
   `isolation:"sandbox"` in a live swarm space; confirm `docker rm`/`podman
   rm` actually fires on both normal completion and abort; confirm the
   "no Docker installed" path refuses loudly rather than silently running
   unsandboxed.
4. `release` promotes.
