# PRD — Swarm Team Blackboard — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **recommended to schedule after the v1.9 worktree
> wave ships** (§4 explains why its value spikes there); no hard code
> dependency either way. Per the checkpoint-rewind precedent, the CLAUDE.md
> wave → minor row is added only when the operator confirms the wave.
> **Target release:** TBD — small wave-sized minor (`v1.11+` candidate).
> **Roadmap source:** swarm design review 2026-07-04 — members share
> standing context today only via broadcast mail (which scrolls away and
> costs a wake per member) or via files in the shared workdir (a side
> channel the v1.9 worktree wave removes for opted-in members).
> **Evaluation provenance:** live-source audit at `dev@be2f949`
> (v1.8.5-beta.1), 2026-07-04/05. All file:line references verified against
> that commit.
> **Reference source:** none — evva-native. (The name is the classic
> blackboard-architecture pattern: one shared surface, one writer role,
> many readers.)

---

## 1. TL;DR

A swarm has no shared "current picture". The leader's plan, decisions made,
who-owns-what — each exists only as scrollback in per-member mailboxes.
Broadcast mail (`to:"all"`) fans one durable row per member and wakes
everyone (bus.go fan-out), so leaders ration it; the alternative — writing
a STATUS.md into the shared workdir — is unsanctioned, invisible to the
web, and disappears for worktree-isolated members the moment the v1.9 wave
lands (each member then sees its own checkout, not the root).

This wave adds the **team blackboard**: one leader-curated markdown
document at `.vero/blackboard.md` (root-anchored beside the ledger,
store.go:28-30), size-capped, injected as a section of every member's
**wake brief** (`composeMailPrompt`, internal/swarm/scheduler.go:434 — the
exact seam the memory reminder already rides, :172 →
`memoryWakeReminder`, space.go:679). The leader maintains it with one tool
(`blackboard_write`, whole-document replace); every member can
`blackboard_read` mid-run; the operator reads it on the web and may edit
the file directly on disk. Updating the blackboard costs **zero wakes** —
members see the new content whenever they next wake for their own reasons.

This is deliberately the smallest possible shared-state design: one file,
one writer role, whole-replace semantics, no history machinery (the durable
event log records each update line).

---

## 2. Goals / non-goals

### Goals

- The leader keeps one bounded, always-current team picture that every
  member sees at every wake — without broadcast storms and without the
  shared-workdir side channel.
- Updates are cheap and passive: no member wake, no mail rows; readers get
  freshness metadata ("updated 3m ago") in the brief.
- Operator-legible: a plain markdown file on disk, readable in the web
  console, hand-editable in an editor (the file is the truth, not a DB
  row).
- Bounded by construction: a write over the cap fails loudly at the tool,
  so wake-brief token cost has a hard ceiling.

### Non-goals (this wave)

- No append/patch/merge semantics, no sections API — whole-document
  replace only (§4).
- No history/versioning beyond the event log's update lines (`.vero`
  archive philosophy: the log is the memory).
- No worker write access, not even proposals — workers influence the board
  through reports and `task_propose`, the leader curates (the ledger's
  single-writer spirit).
- No web *editor* this wave — web is read-only (the member-memory tab
  precedent, api.go:777); operator edits happen on disk (open question #1).
- Not a replacement for member memory (RP-25 — private, per-member,
  long-term) or shared skills (RP-26 — capabilities). The blackboard is
  *shared, ephemeral, situational*.

---

## 3. Verified current state

### 3.1 The wake brief is the injection seam

`composeMailPrompt` (scheduler.go:426-434 doc + func) renders each wake's
claimed mail batch and already carries one standing section — the memory
index reminder, threaded at the call site (:172,
`s.sp.memoryWakeReminder(name)`; implementation space.go:679-684, which
renders a *path*, not content). A blackboard section slots beside it with
identical mechanics; the difference — injecting bounded *content* — is why
the write-side cap exists.

### 3.2 The "leader curates a shared artifact" precedent

`skill_publish` (internal/swarm/tools/skills.go:14-23, RP-26 Part B) is the
shape: leader-only tool, validated input, writes under a root-anchored
shared dir (`SharedSkillsDir`, agentdef/member.go:116), duplicate-guarded
(:62). The blackboard tools copy the pattern with a file target and a size
gate.

### 3.3 Root-anchored state layout

`.vero/` already holds the ledger (`vero.db`, store.go:28-33), the event
log (`.vero/events`, eventlog.go:32,51), `runtime.json` (resume.go:57) —
all reading `sp.Workdir`, all deliberately unaffected by member worktrees
(the worktree PRD's §3.4 blast-radius table). `blackboard.md` joins them.

### 3.4 Already built — reuse, do not redo

| Piece | Where | What it gives this wave |
|---|---|---|
| Wake-brief section threading | composeMailPrompt (scheduler.go:434) + call site (:172) | The injection point — one new parameter/section |
| Leader-tool shape | newSkillPublish (skills.go:14) + role gating (set.go:89) + auto-allow safelist (:55-64) | `blackboard_write`/`blackboard_read` scaffolding |
| Root-anchored dir discipline | store.go:28-30, eventlog.go:51, worktree PRD §3.4 | Placement and the worktree-safety argument |
| Synthetic event lines | scheduleChangeEvent et al. (service/service.go:1408-1609) | `blackboard_updated` timeline/chatlog line |
| Read-only web panel precedent | member memory endpoint (api.go:777) | The `GET …/blackboard` surface + FE panel shape |
| Protocol chokepoints | teamProtocolSuffix (teamprompt.go:46), leaderProtocol (:155), common (:133) | Where the curation discipline is taught |

---

## 4. The shape decision: one file, one writer, whole-replace

- **File, not DB row.** The operator must be able to `cat` it, grep it, and
  fix it in an editor with the service live; a markdown file beside the
  ledger does that, a `vero.db` row hides it. Reads are on wake-cadence
  (human-paced), so file I/O cost is irrelevant. Concurrency is trivial:
  one writer role × whole-file atomic rename.
- **Whole-replace, not append/patch.** An LLM writer is far more reliable
  re-emitting a bounded document than addressing patches into one;
  replace is idempotent (a retried write converges), makes the cap
  enforceable at one point, and needs no merge semantics. History lives in
  the event log, where every other swarm mutation already records itself.
- **Leader-only writes.** The board is the leader's synthesized picture —
  the same judgment monopoly the task ledger enforces
  (store/tasks.go:122). Workers already have two sanctioned inlets
  (reports, `task_propose`); giving N members write access to one document
  reintroduces exactly the racy shared-file editing the worktree wave
  exists to end.
- **Why after v1.9:** today a leader *can* fake this with a root-checkout
  file (all members share the workdir). Once worktree isolation ships,
  opted-in members stop seeing root-checkout changes until a merge — the
  blackboard becomes the only sanctioned live shared surface. Shipping it
  alongside/after v1.9 turns a regression into an upgrade.

---

## 5. Design

### 5.1 D1 — Storage + cap

`<workdir>/.vero/blackboard.md`, written via temp-file + atomic rename.
`settings.blackboard_max_bytes` (default 4096, max 16384 — ≈1k–4k tokens)
validated at `LoadManifest`; `blackboard_write` rejects oversize content
naming the cap and the overage. Empty/absent file = feature dormant (no
brief section, no panel content).

### 5.2 D2 — Tools

```
blackboard_write {content: "<full document>"}     # leader-only
blackboard_read  {}                               # every member
```

Both register in `toolNamesForRole` (set.go:89) and the coordination
auto-allow safelist (set.go:55-64 — governance-shaped: no shell, writes one
capped file inside `.vero`). `blackboard_write` returns the new size +
"visible to members from their next wake"; `blackboard_read` returns
content + age (for mid-run refresh after a teammate mentions an update).

### 5.3 D3 — Wake-brief injection

`composeMailPrompt` gains the section (rendered only when non-empty):

```
## Team blackboard (updated 3m ago by lead)
<content>
```

Injected on **every** wake — message, timer, and schedule wakes alike — so
post-compaction members re-acquire the picture automatically (the same
reasoning that keeps the memory reminder on every wake). Freshness comes
from file mtime; "by lead" from the last `blackboard_updated` event when
cheaply available, else omitted.

### 5.4 D4 — Observability + protocol

- One synthetic `blackboard_updated` event per write (size, writer) — the
  scheduleChangeEvent pattern — landing in the live feed, timeline, and
  durable chatlog. Operator *disk* edits produce no event (documented; the
  mtime-based freshness line still updates).
- `GET /api/swarm/{id}/blackboard` → `{content, updatedAt}`; FE: a
  read-only panel with the markdown rendered (the memory-tab shape).
- `leaderProtocol` (teamprompt.go:155): maintain the board — goal,
  standing decisions, who-owns-what, current phase; update at milestones,
  not per message; stay under the cap by pruning stale lines; the board
  replaces broadcast for standing context (broadcast remains for "wake up
  and act now").
- `teamProtocolCommon` (:133): the blackboard section in your brief is the
  leader's current picture — trust it over older mail; `blackboard_read`
  refreshes it mid-run.

---

## 6. Work items

**BB-1 — Store file + cap + config.**
Atomic write/read helpers under `.vero`, `blackboard_max_bytes` knob
(parse/validate/round-trip), dormant-when-empty semantics.
*Accept:* concurrent-ish write test (rename atomicity), oversize rejected
with cap named, absent file renders nothing anywhere.

**BB-2 — Tools + protocol.**
`blackboard_write` (leader-only) / `blackboard_read` (all), safelist +
role-gating entries, `SelectableTools` denylist entries (service.go:1704 —
role-managed), teamprompt additions (5.4).
*Accept:* worker `blackboard_write` is refused; write→read round-trips;
protocol text present for both roles; a persona member gets the tools via
the same chokepoints (space.go:201,244).

**BB-3 — Wake-brief injection.**
The composeMailPrompt section + freshness line; every wake kind covered.
*Accept:* integration — a write is visible in the next message-wake AND the
next timer-wake brief; empty board adds zero bytes to the brief; the
section renders under the cap worst-case.

**BB-4 — Web read surface + event + docs.**
`GET …/blackboard`, FE panel, `blackboard_updated` synthetic event through
eventlog/chatlog; user guide (en, zh-tw) "the team blackboard" — curation
discipline, operator disk-edit note, when to broadcast instead; CHANGELOG.
*Accept:* panel renders and refreshes on the event; chatlog replay shows
update lines; docs in both languages.

Sequencing: `BB-1 → BB-2 → {BB-3, BB-4}`.

---

## 7. CI plan summary

| Stage | Change | Cost |
|---|---|---|
| BB-1/2 | tool + fs unit suites on temp workdirs | seconds |
| BB-3 | scheduler integration (existing space fixtures) | seconds |
| BB-4 | webapi DTO test + web2 type-check | unchanged |
| all | no new dependencies | — |

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Wake-brief token bloat across N members × every wake | Hard cap at write time (default 4 KiB); protocol teaches pruning; the knob lets a frugal operator halve it |
| Leader forgets to update → confidently stale board | Freshness line ("updated 2d ago") makes staleness visible to every reader; protocol ties updates to milestones; open question #2 (staleness nudge) |
| Board contradicts fresher mail | Protocol ranks explicitly: direct task mail > board for *your* task; board > old broadcast for team context |
| Operator disk edit races a leader write | Atomic rename means readers never see a torn file; last write wins — acceptable for a curated doc, documented |
| Prompt-injection amplification (one poisoned doc reaches all members) | Writer is the leader only — the same trust level that already dispatches tasks and mail to everyone; the web panel + event line make every change auditable |
| Scope creep toward a CRDT/wiki | §2 non-goals + §4 rationale are the fence: one file, one writer, replace-only |

---

## 9. Open questions

1. **Web write access (operator edits in the console)?** Recommend
   fast-follow, not this wave — it needs an edit-vs-leader-write conflict
   story; disk edit covers the need meanwhile.
2. **Staleness nudge (ops line when the board is untouched for N days on
   an active space)?** Recommend defer; the freshness line may be enough.
   Revisit with usage.
3. **Inject-on-change-only (skip the section when unchanged since the
   member's last wake)?** Recommend no for v1 — post-compaction recovery
   and prompt-simplicity beat the marginal token saving at a 4 KiB cap.
4. **Per-member boards (leader → one member standing brief)?** Recommend
   no — that's what task specs and mail are; one board keeps the concept
   crisp.

---

## 10. Rollout

1. BB-1..BB-4 via `feature/swarm-blackboard` → `dev`.
2. `pre-release feature` cuts the first beta under the minor assigned at
   wave confirmation (recommended: after the v1.9 worktree wave, §4).
3. Beta validation: a tech-team run where the leader maintains the board
   through two milestones; verify wake-brief rendering for dir + persona
   members, timer wakes, and a post-compaction member; operator disk edit
   visible on next wake.
4. `release` promotes.
