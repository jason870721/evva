# PRD — Cron Scheduling Tools — Implementation Plan

> **Audience:** senior engineers implementing this phase.
> **Status:** proposed; ready to build.
> **Target release:** TBD (proposed `v1.6+` candidate; small, reuses the alarm
> scheduler).
> **Roadmap source:** `CLAUDE.md` → "one runtime, many personas" tool surface;
> alarm PRD §5.6 ("When `cron_*` is implemented, it can reuse this `Scheduler`").
> **Reference source:** `ref/src/tools/ScheduleCronTool/` (CronCreateTool,
> CronListTool, CronDeleteTool, prompt.ts); `ref/src/utils/cronScheduler.ts`,
> `ref/src/utils/cronTasks.ts`, `ref/src/utils/cronTasksLock.ts`.
> **Live-source verification (2026-06-17, on `dev`):** the alarm scheduler
> (`pkg/tools/alarm/scheduler.go`: `Alarm` struct, `Arm`/`Cancel`/`List`/
> `LoadAndRearm`/`Stop`), the cron engine (`internal/swarm/agentdef/schedule.go`:
> `Schedule`, `Next(after)`, `parseCron`, `cronExpr.next`), the stub tools
> (`pkg/tools/cron/cron.go`: four `tools.NewStub` singletons with full schemas),
> the registration (`internal/toolset/builtins.go:156-160`), the profile listing
> (`internal/agent/profiles.go:192` DeferredTools, `:406-409` `soloSchedulingTools`),
> the tag set (`pkg/toolset/tags.go:43-45`), the delivery path (`WakeupQueue` +
> `SignalAlarm` + `drainWakeupPrompts`), and the swarm schedule tools
> (`internal/swarm/tools/set.go`: `schedule_set`/`schedule_clear`, leader-only)
> were all read and verified.

---

## 1. TL;DR — what this phase actually is

evva has three scheduling surfaces, two of which work:

| Tool | State | Mechanism |
|---|---|---|
| `schedule_wakeup` | **implemented** | Relative delay, blocks, ≤1 hour cap |
| `alarm_*` | **implemented** | One-shot absolute instant, durable, survives restart |
| `cron_*` | **stubs** | Return "not implemented yet" — schemas fully defined |

The stubs are not placeholders waiting for a design; they are a **complete API
contract** with schemas, descriptions, and registration — they just have no
engine behind them. The alarm PRD explicitly designed the scheduler for this
extension (§5.6: "add a recurring entry whose fire callback re-arms `Next()`").

This phase replaces the four stubs with **real implementations** by extending
the alarm scheduler with a recurring-fire mode. The cron engine — a hand-written,
dependency-free 5-field parser with bitset matching — already exists in
`internal/swarm/agentdef/schedule.go` and is battle-tested by the swarm's own
`schedule_set` tools. The delivery path (`WakeupQueue.Enqueue` + `SignalAlarm`
+ `drainWakeupPrompts`) is reused verbatim from alarm.

**Why the swarm's `schedule_set`/`schedule_clear` is not enough:**

- Swarm tools are **leader-only** (`internal/swarm/tools/set.go`). A solo evva
  session has no supervisor and no member roster.
- The swarm scheduler fires by **poke** (a buffered channel send into the
  member's run loop), not by `WakeupQueue`. Different delivery.
- Solo cron needs **durable persistence** and **restart recovery** — the swarm
  store is in-memory via `store.PutSchedule`.
- The swarm scheduler advances `nextDue` on every tick regardless of whether
  the fire actually delivered (busy/frozen members are skipped). Solo cron has
  no such concern — the agent is always either running or idle.

`cron_*` and `alarm_*` remain **siblings**: alarm = fire once at an absolute
instant; cron = fire on a recurring wall-clock pattern. They share the scheduler
but diverge on fire semantics.

Concretely:

1. **`pkg/tools/alarm/scheduler.go`** gains a `CronExpr` field on `Alarm` and
   a recurring-fire path in the timer callback: if `CronExpr != ""`, compute
   `Next(fireAt)` and re-arm instead of removing.
2. **`pkg/tools/cron/cron.go`** is rewritten from four stubs to three real
   `Tool` implementations (`cron_create`, `cron_list`, `cron_delete`) bound
   to the shared scheduler. `REMOTE_TRIGGER` stays a stub (separate feature,
   out of scope — §6).
3. **The agent wiring** is a delta on the alarm path: the scheduler is already
   constructed, the `OnFire` callback already enqueues + signals. The only new
   plumbing is making `cron_create`/`cron_list`/`cron_delete` resolve the
   shared scheduler from `ToolState`.

---

## 2. Inventory — what already exists (do not re-build)

### 2.1 The alarm scheduler — `pkg/tools/alarm/scheduler.go`

The production scheduler, verified line-by-line:

| Symbol | Lines | Role | Cron reuse |
|---|---|---|---|
| `Alarm` struct | 37-49 | `{ID, FireAt, Prompt, Label, Durable, Created, Target, Origin}` | **Add `CronExpr string`** — when non-empty, the entry is recurring |
| `Scheduler` struct | 51-66 | `mu sync.Mutex`, `alarms map[string]*armed`, `onFire func(Fired)`, `path string`, `maxAlarms int` | **Shared** — cron entries live in the same map |
| `Arm(a Alarm)` | 141-171 | Validates future time, assigns ID, `time.AfterFunc(fireAt.Sub(now), fireCb)`, persists | **Reuse with extension** — when `CronExpr != ""`, `fireCb` re-arms |
| `Cancel(id)` | 175-183 | Stops timer, removes, persists | **Reuse verbatim** |
| `List()` | 186-195 | Returns sorted by `FireAt` | **Reuse** — cron entries sort alongside alarms |
| `LoadAndRearm()` | 239-253 | Reads JSON, re-arms future, fires past-due once | **Reuse** — cron entries re-arm at their `Next(now)` |
| `Stop()` | 280-286 | Stops all timers | **Reuse verbatim** |
| `Fired` struct | 76-80 | `{ID, Prompt, Label, Late bool}` | **Add `Recurring bool`** — fire message distinguishes |
| Persistence | 197-236 | JSON file, write-temp-rename, atomic | **Shared** — cron entries serialize alongside alarms |

The scheduler's internal `fireCb` closure (inside `Arm`, `:155-167`) is the
**key extension point**:

```go
// Current (one-shot):
fireCb := func() {
    s.mu.Lock()
    delete(s.alarms, id)
    s.persistLocked()
    s.mu.Unlock()
    s.onFire(Fired{ID: id, Prompt: a.Prompt, Label: a.Label})
}

// Needed (recurring):
fireCb := func() {
    s.mu.Lock()
    if a.CronExpr != "" {
        next := cronNext(a.CronExpr, now)  // from schedule.go
        if next.IsZero() || next.Sub(now) > maxRecurringHorizon {
            // expired or auto-expire (7-day default)
            delete(s.alarms, id)
        } else {
            armed.entry.FireAt = next
            armed.timer = time.AfterFunc(next.Sub(now), fireCb) // re-arm
        }
        s.persistLocked()
    } else {
        delete(s.alarms, id)
        s.persistLocked()
    }
    s.mu.Unlock()
    s.onFire(Fired{ID: id, Prompt: a.Prompt, Label: a.Label, Recurring: a.CronExpr != ""})
}
```

The self-referencing `fireCb` closure (it captures itself for the re-arm) is
the standard Go pattern for recursive `time.AfterFunc` callbacks. It works
because Go closures capture the variable, not the value.

### 2.2 The cron engine — `internal/swarm/agentdef/schedule.go`

A hand-written, zero-dependency 5-field cron parser:

| Symbol | Lines | Role |
|---|---|---|
| `Schedule` struct | 19-23 | `{Cron string, Every time.Duration, Prompt string}` |
| `Next(after time.Time)` | 72-84 | First activation strictly after `after` |
| `parseCron(spec string)` | 109-211 | Parses `M H DoM Mon DoW` into `cronExpr` |
| `cronExpr` struct | 94-97 | `uint64` bitsets for each field + `domStar`/`dowStar` |
| `cronExpr.next(after)` | 213-224 | Steps minute-by-minute, bounded to ~5 years |

**Cron does not currently export `parseCron` or `cronExpr`** — they are
unexported. This phase either (a) exports them (`ParseCron`, `CronExpr`,
`CronExpr.Next`) to `pkg/tools/cron` or (b) moves them to a shared location.
**Recommended:** export from `internal/swarm/agentdef` and import from
`pkg/tools/cron`. The cron engine is a pure function with no swarm coupling;
exporting it does not violate any boundary. If the team prefers not to export
from `internal/`, a copy into `pkg/tools/cron/engine.go` is the fallback
(~150 LoC, self-contained).

### 2.3 The stub tools — `pkg/tools/cron/cron.go`

Four `tools.NewStub` singletons with complete schemas and descriptions:

| Tool | Schema fields | Port to real? |
|---|---|---|
| `CRON_CREATE` | `cron` (string, 5-field), `prompt` (string), `recurring` (bool, default true), `durable` (bool, default false) | **Yes** — becomes a real `Tool` |
| `CRON_LIST` | `{}` | **Yes** — real `Tool` |
| `CRON_DELETE` | `id` (string) | **Yes** — real `Tool` |
| `REMOTE_TRIGGER` | `action` (enum), `trigger_id`, `body` | **No** — stays a stub (§6) |

The descriptions are well-written and steer the model correctly (e.g. "Avoid
:00 and :30 minute marks" to spread load). Port them verbatim.

### 2.4 Registration and profile plumbing

| File | Lines | Current state | Change |
|---|---|---|---|
| `internal/toolset/builtins.go` | 156-160 | `MustRegister` with `return cron.Create, nil` (stub) | **Rewrite** factories to resolve the shared scheduler and return real tools |
| `internal/agent/profiles.go` | 192 | `cron.Names()` in Main profile's `DeferredTools` | **No change** — already deferred, loaded via `tool_search` |
| `internal/agent/profiles.go` | 406-409 | `soloSchedulingTools()` = alarm + cron + wakeup | **No change** — stripped from swarm personas (correct) |
| `pkg/toolset/tags.go` | 43-45 | Cron tools tagged `{"schedule", "cron", "recurring", "timer", "future"}` | **No change** |
| `pkg/tools/name.go` | 105-110 | `CRON_CREATE`, `CRON_LIST`, `CRON_DELETE`, `REMOTE_TRIGGER` constants | **No change** |

### 2.5 Delivery path — reused from alarm

The fire delivery is identical and already proven in production:

1. Scheduler's `onFire(Fired)` callback (injected by `internal/toolset/toolset.go:277-293`)
2. → `WakeupQueue.Enqueue(fired.Message())` — the prompt lands in the queue
3. → `NotifyAlarm()` → `SendSignal(SignalAlarm)` — wakes idle agent
4. → `drainWakeupPrompts()` (`internal/agent/state_machine.go:72-84`) — folds prompt as fresh user message

The `Fired.Message()` method formats the banner. For cron, it should include
the recurring flag: `"⏰ Cron job fired (recurring, next: <next-time>)"`.

### 2.6 Reference (`ref/src/`)

| File | What it does | Port? |
|---|---|---|
| `tools/ScheduleCronTool/CronCreateTool.ts` | Builds a cron tool; validates the cron expression; persists to a scheduler | **Yes** — adapt to Go, reuse the existing schema |
| `tools/ScheduleCronTool/CronListTool.ts` | Lists pending jobs | **Yes** — trivial |
| `tools/ScheduleCronTool/CronDeleteTool.ts` | Deletes by ID | **Yes** — trivial |
| `tools/ScheduleCronTool/prompt.ts` | Steers the model on when/how to schedule | **Yes** — port key guidance |
| `utils/cronScheduler.ts` | The scheduler engine: parse, compute next, persist, fire | **Mostly** — the Go scheduler already exists; port the auto-expiry (7-day) and jitter logic |
| `utils/cronTasks.ts` | Per-job execution wrapper | **No** — evva's fire path is `WakeupQueue`, not a separate task runner |
| `utils/cronTasksLock.ts` | Concurrent-fire mutex | **No** — the Go scheduler is already mutex-guarded |
| `utils/cron.ts` | Cron expression parser | **No** — `internal/swarm/agentdef/schedule.go` already has one |
| `utils/cronJitterConfig.ts` | Jitter to avoid thundering herd | **Partial** — add a small random jitter to the fire time (§5.4) |

---

## 3. Goal & acceptance criteria

**Goal:** replace the four cron stubs with real, working scheduling tools —
the agent can create recurring cron jobs, list them, delete them, and they
fire on schedule using the same idle-wake and delivery path as alarms.

Ship is complete when **all** of these pass:

1. **A1 — Cron create with valid expression.** `cron_create` with
   `{"cron": "*/5 * * * *", "prompt": "check CI status"}` creates a recurring
   job that fires every 5 minutes. The tool returns the job ID and the next
   fire time.
2. **A2 — Recurring fire re-arms.** After a recurring job fires, the scheduler
   automatically computes the next fire time and re-arms. The agent sees
   `⏰ Cron job fired (recurring, next: <time>)` as a fresh user message.
3. **A3 — One-shot cron.** `cron_create` with `{"cron": "30 14 * * *",
   "recurring": false, "prompt": "..."}` fires once at the next matching time,
   then auto-deletes.
4. **A4 — Invalid cron rejected.** `cron_create` with an unparseable cron
   expression returns an error listing what was wrong.
5. **A5 — Cron list.** `cron_list` returns all pending cron jobs with ID,
   expression, next fire time, recurring flag, prompt (truncated), and
   durable flag.
6. **A6 — Cron delete.** `cron_delete` removes a pending job by ID. Deleting
   a recurring job stops future fires.
7. **A7 — Durable persistence.** `cron_create` with `"durable": true` persists
   the job to disk; it survives a restart and re-arms on startup (using the
   same `LoadAndRearm` path as alarms).
8. **A8 — Auto-expiry.** Recurring jobs auto-expire after 7 days from creation
   (configurable later). After expiry, the job is removed and no longer fires.
   This prevents indefinite token drain from forgotten jobs.
9. **A9 — Shared scheduler.** Cron entries and alarm entries coexist in the
   same `Scheduler` instance, same persistence file, same `List()` output
   (sorted by `FireAt`). `cron_list` filters to cron entries only;
   `alarm_list` filters to alarm entries only.
10. **A10 — Subagent exclusion.** Cron tools are in the root/Main profile only.
    Subagents and swarm members do not get them (already enforced by
    `soloSchedulingTools` stripping, `profiles.go:406-409`).
11. **A11 — Jitter.** Recurring jobs add a small random jitter (0–30s) to each
    fire time to avoid thundering-herd when multiple jobs share the same cron
    expression.
12. **A12 — Tests green.** Unit tests for: cron parse validation, next-fire
    computation, recurring re-arm, one-shot auto-delete, auto-expiry, list
    filtering, durable round-trip, jitter bounds. `go test ./...` green;
    `go vet ./...` clean.
13. **A13 — `REMOTE_TRIGGER` stays a stub.** The fourth tool in `cron.go` is
    unrelated to scheduling and remains a stub (§6).

---

## 4. Work breakdown (ordered)

### Task 0 — Export the cron engine

Make the cron parser in `internal/swarm/agentdef/schedule.go` importable by
`pkg/tools/cron`. Two options (pick one):

**Option A (recommended): export from `agentdef`.** Rename:
- `parseCron` → `ParseCron`
- `cronExpr` → `CronExpr`
- `cronExpr.next` → `CronExpr.Next`

The `Schedule` struct stays as-is (it's the swarm's higher-level wrapper).
The exported symbols are pure functions with no swarm state. Import from
`pkg/tools/cron` does not create a cycle (`pkg/tools/cron` → `internal/swarm/agentdef`
is allowed because `pkg/tools` can import `internal/` via the toolset layer;
verify and use `pkg/tools/cron/engine.go` as a forwarding wrapper if not).

**Option B (fallback): copy to `pkg/tools/cron/engine.go`.** The parser is
~150 LoC and self-contained. Duplicate it, add a comment pointing at the
original. Accept the drift risk (mitigate with a test that runs both parsers
on the same inputs).

### Task 1 — Extend the alarm scheduler for recurring entries

`pkg/tools/alarm/scheduler.go`:

1. Add `CronExpr string` to the `Alarm` struct. When empty, the entry is a
   one-shot alarm (existing behavior). When non-empty, it is a recurring cron
   job.

2. Add `Expiry time.Time` to the `Alarm` struct. Zero means no expiry. For
   recurring cron jobs, default to `Created.Add(7 * 24 * time.Hour)`.

3. Add `Recurring bool` to the `Fired` struct. The fire banner in `Message()`
   includes `(recurring, next: <time>)` when true.

4. Modify `Arm` to accept a `CronExpr`. When `CronExpr != ""`:
   - Parse with `ParseCron(expr)` — reject invalid expressions.
   - Compute `FireAt = cronExpr.Next(now)`.
   - Set `Expiry = now.Add(7 * 24 * time.Hour)` if not already set.
   - The `fireCb` closure re-arms: compute `next = cronExpr.Next(fireAt)`,
     check `next.Before(Expiry)`, re-arm with a new `time.AfterFunc`, or
     delete if expired.

5. Add a `NewScheduler` option or method to configure `maxRecurringHorizon`
   (default 7 days).

6. Modify `List()` to return entries sorted by `FireAt` as today. Callers
   (alarm_list vs cron_list) filter by `CronExpr` emptiness.

7. Add jitter: when arming a recurring entry, add
   `time.Duration(rand.Intn(30)) * time.Second` to `FireAt`. Re-apply jitter
   on each re-arm.

Unit tests (`scheduler_test.go` additions):
- Arm a recurring entry with `*/1 * * * *` (every minute); verify `FireAt`
  is the next minute boundary + jitter.
- Simulate fire → verify re-arm with next fire time.
- Simulate fire past expiry → verify auto-delete.
- Invalid cron expression → error from `Arm`.
- One-shot alarm (empty `CronExpr`) → unchanged behavior (regression test).
- Mixed list: alarm + cron entries sorted correctly.

### Task 2 — Rewrite the cron tools

`pkg/tools/cron/cron.go` — replace the three stubs (`Create`, `List`, `Delete`)
with real `Tool` implementations:

```go
// Package cron provides scheduling tools: CronCreate, CronList, CronDelete.
// RemoteTrigger remains a stub.
package cron

import (...)

// Create builds a cron_create tool bound to the given scheduler.
func NewCreate(sched *alarm.Scheduler) tools.Tool { ... }

// NewList builds a cron_list tool bound to the given scheduler.
func NewList(sched *alarm.Scheduler) tools.Tool { ... }

// NewDelete builds a cron_delete tool bound to the given scheduler.
func NewDelete(sched *alarm.Scheduler) tools.Tool { ... }
```

**`cron_create`:**
- Schema: same as the existing stub (preserved verbatim).
- `Execute`: parse the `cron` field with `ParseCron`; on error, return a
  descriptive error. Build an `alarm.Alarm` with `CronExpr`, `Prompt`,
  `Durable`, and `Expiry` (7 days for recurring, zero for one-shot). Call
  `sched.Arm(a)`. Return `"Cron job <id> scheduled: <cron> — next fire: <time>"`.
- For `recurring: false`: set `CronExpr` but also mark the entry as one-shot.
  The scheduler fires it once and deletes it (same as a one-shot alarm with
  a cron-shaped `FireAt`).

**`cron_list`:**
- Schema: empty object (same as stub).
- `Execute`: `sched.List()`, filter to entries with `CronExpr != ""`, format
  as a table: ID, cron expression, next fire time (local), time-until,
  recurring flag, prompt (truncated to 80 chars), durable flag.

**`cron_delete`:**
- Schema: `{ "id": string }` (same as stub).
- `Execute`: `sched.Cancel(id)`. If the entry was not a cron entry, still
  succeed (it was deleted). Return `"Cron job <id> cancelled"`.

Tool descriptions: port from the existing stub descriptions, which are already
well-written. Add a line contrasting with `alarm_create` ("for a one-shot
trigger at an exact time, use `alarm_create` instead").

### Task 3 — Wire the factories in `builtins.go`

`internal/toolset/builtins.go` — rewrite the three cron factories (lines
156-159) to resolve the shared alarm scheduler:

```go
// --- cron (recurring scheduling, shares the alarm scheduler) ---
r.MustRegister(tools.CRON_CREATE, func(s tools.State) (tools.Tool, error) {
    return cron.NewCreate(s.(*toolset.ToolState).AlarmScheduler()), nil
})
r.MustRegister(tools.CRON_LIST, func(s tools.State) (tools.Tool, error) {
    return cron.NewList(s.(*toolset.ToolState).AlarmScheduler()), nil
})
r.MustRegister(tools.CRON_DELETE, func(s tools.State) (tools.Tool, error) {
    return cron.NewDelete(s.(*toolset.ToolState).AlarmScheduler()), nil
})
```

`REMOTE_TRIGGER` stays unchanged (stub).

### Task 4 — Tests

- `pkg/tools/alarm/scheduler_test.go`: recurring arm/fire/re-arm/expiry tests.
- `pkg/tools/cron/cron_test.go`: tool-level tests (schema validation, parse
  errors, list filtering, create→list→delete round-trip).
- Integration: a short-delay recurring cron fire (e.g. `* * * * *` = next
  minute) in a test harness confirms the `WakeupQueue` delivery path.

### Task 5 — Docs + version + changelog

- `docs/user-guide/{en,zh-tw}/user-guide.md`: add a "Cron scheduling" section
  explaining the three tools, the cron expression format, recurring vs one-shot,
  durability, and auto-expiry.
- `CHANGELOG.md` `[Unreleased]` → `### Added`: cron scheduling tools.
- `pkg/version/version.go`: bump at release time.

---

## 5. Design decisions & risks

### 5.1 — Shared scheduler, not a separate cron scheduler

The alarm PRD §5.6 explicitly designed the scheduler for this: "add a recurring
entry whose fire callback re-arms `Next()`." A separate scheduler would
duplicate persistence, the `OnFire` wiring, `LoadAndRearm`, and the
`WakeupQueue` integration. Sharing keeps the surface small and the persistence
file unified. The cost is one field (`CronExpr`) and one branch in `fireCb`.

### 5.2 — Cron entries and alarm entries coexist in `List()`

`alarm_list` and `cron_list` both call `sched.List()` but filter by
`CronExpr` emptiness. This is simpler than maintaining two separate maps, and
it means the persistence file is one JSON array, not two files with their own
atomic-write machinery. A user who sets both an alarm and a cron job sees them
in one `alarms.json`, which is the correct mental model ("all my scheduled
things").

### 5.3 — Auto-expiry at 7 days prevents indefinite token drain

A recurring cron job fires a real agent run every time. A forgotten
`*/5 * * * *` job would fire 288 times/day, each costing tokens. The 7-day
auto-expiry bounds the worst case to ~2016 fires. The expiry is stored per-job
(`Expiry` field) so it survives restarts. The 7-day default matches ref's
behavior (`ref/src/utils/cronScheduler.ts`: `AUTO_EXPIRE_DAYS = 7`). A future
config knob (`cron.max_expiry_days`) can override.

### 5.4 — Jitter prevents thundering herd

Multiple cron jobs sharing the same expression (e.g. two `*/5 * * * *` jobs)
would fire at the same instant, causing a burst of `WakeupQueue.Enqueue` calls
and multiple prompts landing in one drain cycle. A 0–30 second random jitter
per fire spreads the load. The jitter is applied at arm time and re-applied on
each re-arm, so it doesn't accumulate drift. Jitter is bounded (< 30s) so a
`*/1 * * * *` job still fires within the minute.

### 5.5 — `internal/swarm/agentdef` export vs. copy

The cron parser is pure and self-contained. Exporting it from `agentdef` is
the cleanest path. If the team objects to `pkg/` importing `internal/` (even
though the existing `pkg/tools/cron` already imports `pkg/tools` and
`pkg/common`, and the toolset layer in `internal/toolset` already bridges
the two), the fallback is a copy into `pkg/tools/cron/engine.go`. The copy
is ~150 LoC and can be pinned by a test that runs both parsers on the same
input set to detect drift.

### 5.6 — `REMOTE_TRIGGER` stays a stub

`REMOTE_TRIGGER` is a different feature entirely — it's an HTTP API client
for remote workflow triggers, not a scheduling tool. It lives in `pkg/tools/cron`
for historical reasons (same package in ref). This PRD does not implement it.
A future phase can either move it to its own package or implement it when the
remote trigger API is available.

### 5.7 — Cron in swarm context

Swarm members already have `schedule_set`/`schedule_clear` (leader-only)
driven by the supervisor's own scheduler. Solo cron tools are explicitly
stripped from swarm personas (`profiles.go:406-409`, `soloSchedulingTools`).
This phase does not change that boundary. If a future need arises for
swarm members to use cron expressions, the supervisor's `SetSchedule`
already accepts `Schedule.Cron` — no new tool needed.

### 5.8 — Risks

- **Token spend on recurring fires.** Same as alarm (§5.9) but amplified: a
  recurring job fires repeatedly. Mitigations: 7-day auto-expiry (A8), the
  `cron_list` tool gives visibility, the agent sees the recurring banner and
  can self-delete if the task is done, and a future config can cap concurrent
  cron jobs. The default `recurring: true` matches the stub's schema default
  and ref's behavior.
- **Cron expression errors.** The parser in `schedule.go` is battle-tested by
  the swarm, but edge cases exist (e.g. `30 14 31 2 *` = Feb 31, never fires).
  The 5-year bound in `cronExpr.next` prevents infinite loops. `cron_create`
  should warn when `Next(now)` returns zero (impossible expression).
- **Persistence file growth.** Each cron entry is ~200 bytes JSON. With a
  reasonable cap (same `maxAlarms` as alarms, ~100), the file stays under
  20 KB. No concern.
- **Clock changes / sleep / suspend.** Same as alarm (PRD §5.9): `time.AfterFunc`
  is monotonic; a laptop sleeping past the fire time fires on wake. For
  recurring jobs, this means a missed fire is skipped (the re-arm computes
  `Next(now)` from the current time, not from the missed fire time). This is
  correct behavior — catching up on missed recurring fires would be wasteful.

---

## 6. Out of scope

- **`REMOTE_TRIGGER` implementation** — separate feature, stays a stub (§5.6).
- **6-field cron (with seconds)** — the existing parser is 5-field (minute
  precision). Adding seconds would require extending the parser and is not
  demanded by any use case. Standard cron is 5-field.
- **Natural-language scheduling** ("every weekday at 3pm") — the model
  translates NL to cron. No NLP layer needed.
- **Cron jobs that run shell commands** — cron fires a prompt, not a command.
  The agent decides what to do with the prompt. This is consistent with
  alarm's behavior.
- **Per-cron-job permission gating** — future config. Cron jobs fire
  autonomously, same as alarms.
- **Swarm cron** — the swarm has its own `schedule_set` mechanism (§5.7).
- **Cron expression validation UI** — `cron_create` returns parse errors as
  text; no interactive validation needed.

---

## 7. Verification checklist (PR gate)

- [ ] **A1:** `cron_create` with `*/5 * * * *` creates a recurring job;
      `cron_list` shows it with correct next fire time.
- [ ] **A2:** A `* * * * *` (every minute) recurring job fires; the agent
      receives `⏰ Cron job fired (recurring, next: <time>)`; `cron_list`
      shows the updated next fire time.
- [ ] **A3:** `cron_create` with `"recurring": false` fires once and
      disappears from `cron_list`.
- [ ] **A4:** `cron_create` with `"invalid expression"` returns a clear error.
- [ ] **A5:** `cron_list` shows only cron entries (not alarm entries).
- [ ] **A6:** `cron_delete` removes a cron job; it does not fire afterward.
- [ ] **A7:** `cron_create` with `"durable": true` + restart → job re-arms.
- [ ] **A8:** A recurring job past its 7-day expiry is auto-deleted.
- [ ] **A9:** `alarm_list` does not show cron entries; `cron_list` does not
      show alarm entries. Both share the same scheduler and persistence file.
- [ ] **A10:** Subagent/swarm profiles do not expose cron tools.
- [ ] **A11:** Two jobs with the same expression fire at slightly different
      times (jitter).
- [ ] **A12:** `go test ./...` green; `go vet ./...` clean.
- [ ] **A13:** `REMOTE_TRIGGER` still returns "not implemented."
- [ ] **Manual:** set a `*/1 * * * *` cron job; wait 60s; confirm the agent
      wakes and the prompt is injected. Delete it; confirm no further fires.

---

## 8. File-by-file change list (cheat sheet)

| File | Change |
|---|---|
| `internal/swarm/agentdef/schedule.go` | Export `ParseCron`, `CronExpr`, `CronExpr.Next` (Task 0, Option A) |
| `pkg/tools/alarm/scheduler.go` | +`CronExpr`, `Expiry` on `Alarm`; +`Recurring` on `Fired`; recurring re-arm in `fireCb`; jitter (Task 1) |
| `pkg/tools/alarm/scheduler_test.go` | +recurring arm/fire/re-arm/expiry/jitter tests (Task 1) |
| `pkg/tools/cron/cron.go` | **Rewrite** — three real tools + `REMOTE_TRIGGER` stub (Task 2) |
| `pkg/tools/cron/cron_test.go` | **New** — tool-level tests (Task 4) |
| `internal/toolset/builtins.go` | Rewrite three cron factories to resolve shared scheduler (Task 3) |
| `docs/user-guide/{en,zh-tw}/user-guide.md` | +Cron scheduling section (Task 5) |
| `CHANGELOG.md` | +`### Added` entry (Task 5) |

---

## 9. Effort estimate (informational)

| Task | Approx LOC | Approx wall time (focused) |
|---|---|---|
| Task 0 — export cron engine | ~15 (rename) or ~150 (copy) | 30 min |
| Task 1 — scheduler extension | ~80 | 2 h |
| Task 2 — tool rewrite | ~150 | 2 h |
| Task 3 — factory wiring | ~15 | 15 min |
| Task 4 — tests | ~250 | 2.5 h |
| Task 5 — docs + changelog | ~60 | 45 min |

Total: ~550–700 LOC, ~8–9 hours focused. The smallest scheduling PRD: the
engine, the delivery path, the registration, and the profile plumbing all
exist. The work is one struct extension, three tool rewrites, and tests.
Highest-risk area is the recurring re-arm closure in `fireCb` (Task 1) —
a self-referencing closure with a mutex is a classic concurrency trap; the
unit tests must cover the re-arm-under-lock path explicitly.
