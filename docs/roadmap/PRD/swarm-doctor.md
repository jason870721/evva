# PRD — Swarm Doctor (Preflight Validation) — Implementation Plan

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed.
> **Target release:** TBD — small wave-sized minor (`v1.11+` candidate), or
> a follow-on riding an adjacent swarm wave if the operator prefers. Per the
> checkpoint-rewind precedent, the CLAUDE.md wave → minor row is added only
> when the operator confirms the wave.
> **Roadmap source:** swarm design review 2026-07-04 — "a typo'd model pin
> only explodes deep inside a run" plus a family of setup failures (missing
> API key, wrong persona tier, stale `.vero` from a newer binary) that all
> surface *after* register, mid-flight, as confusing member errors.
> **Evaluation provenance:** live-source audit at `dev@be2f949`
> (v1.8.5-beta.1), 2026-07-04/05. All file:line references verified against
> that commit.
> **Reference source:** none — evva-native. (Spirit sibling: `git fsck` /
> `brew doctor` — read-only, layered, exit-coded.)

---

## 1. TL;DR

Register-time validation is deliberately narrow. `LoadManifest` fail-fasts
structure (leader present, unique names — `validate()`,
internal/swarm/agentdef/manifest.go:477; cron/effort/permission-mode/
duration parses, :294-366), but the expensive mistakes pass through:

- A **model pin** is deliberately *not* checked against the constant table
  (space.go:322-332 — SDK hosts register custom models), so
  `model: claude-sonet-5` registers fine and fails at LLM-client build,
  deep in the member's first run.
- A **missing provider API key** fails the same way, later still.
- A **persona name** that isn't in the registry, or isn't main-tier, fails
  at register (space.go:244-252) — but only after the service has already
  accepted and begun assembling the space.
- **State drift** — a `.vero/vero.db` written by a newer binary
  (migrations are forward-only, store.go:107-111), a corrupt
  `runtime.json` (silently treated as empty, resume.go:100) — surfaces as
  mysteries, not messages.

This wave adds **`evva swarm doctor [dir]`**: a read-only, offline-first
preflight that runs the whole ladder — manifest → member definitions
(a real `Loader.Build` dry-run per member) → models/efforts →
provider keys → `.vero`/runtime state → (optionally) the live service —
and prints a sectioned ✓/⚠/✗ report with a meaningful exit code. The CLI
already links everything needed: `cmd/evva/swarm.go` imports `agentdef`
today (:12), and the binary carries the full config/constant/loader stack.

---

## 2. Goals / non-goals

### Goals

- One command answers "will `evva swarm .` work here, and will the members
  actually run?" before anything registers.
- Strictly read-only: doctor never creates directories, never migrates a
  database, never writes config, never registers a space (§4).
- Offline-first: everything except the service section works with no
  daemon; `--offline` skips the service probes entirely.
- Deterministic exit codes for scripting: `0` clean, `1` any ✗,
  `2` under `--strict` when any ⚠ remains.
- Custom-model reality respected: an unknown model id is a ⚠ ("custom —
  resolves at client build"), never an ✗, unless `--strict`
  (space.go:322-327's contract is honored, not overruled).

### Non-goals (this wave)

- No auto-fix (`--fix`) — doctor diagnoses; the operator edits.
- No live-space deep diagnostics (stuck runs, mailbox backlogs) — that is
  the watchdogs' and metrics' territory; doctor is *pre*-flight.
- No tool-name dry-run beyond what `Loader.Build` already exercises (open
  question #2).
- No network probes of provider endpoints (an API key's *presence* is
  checked, its *validity* is not — no billable calls from a doctor).

---

## 3. Verified current state

### 3.1 What register already catches vs. lets through

| Failure | Caught today? | Where |
|---|---|---|
| No leader / duplicate / empty member name | ✓ at load | manifest.go:477 |
| Bad cron/duration/permission-mode/effort *string in manifest* | ✓ at load | manifest.go:294-366 |
| Invalid effort in a member profile | ✓ at construct | space.go:334-338 (`ParseEffort`) |
| Persona missing / not main-tier | ✓ at register (late) | space.go:244-252 |
| Model pin typo | ✗ — fails at first LLM client build | space.go:322-332 (deliberate) |
| Provider API key missing | ✗ — fails at first client build/call | config.go:725-729 key store; agent.go:95 unknown-provider arm |
| `.vero` from a newer binary | ✗ — migration mismatch behaviors | store.go:107-111 (forward-only) |
| Corrupt runtime.json | ✗ — silently empty | resume.go:100 |
| Service down / version skew / name collision | ✗ until the HTTP call | servicectl.go:64,100,118 |

### 3.2 The read-only trap in the obvious probes

`store.Open` **creates** `<workdir>/.vero/` (MkdirAll, store.go:46-48) and
**runs migrations** (:74-76) — calling it from doctor would mutate a
pristine project and upgrade a database the operator only asked to inspect.
Doctor therefore opens its own read-only connection (`?mode=ro` on the
existing pure-Go driver) and reads `schema_migrations` directly, comparing
against the embedded set (store.go:24-25) — newer-than-binary is the ⚠
that today has no voice.

### 3.3 Already built — reuse, do not redo

| Piece | Where | What it gives this wave |
|---|---|---|
| Full manifest fail-fast | `LoadManifest` (manifest.go:294) | Section A is one call |
| Real member assembly, sans agents | `Loader.Build` (used per member at supervisor.go:164; `BuildAll` in the register path) | Section B: the highest-fidelity offline probe — prompts, profiles, tools/skills lists load exactly as register would |
| Persona registry | `agent.BuildAgentRegistry(cfg.AppHome)` (the space builds its own copy, space.go:155) | Section B persona resolution + the :244-252 tier mirror |
| Model/provider tables | `ProviderOfModel` (pkg/constant/llm.go:92), providers (:14-31) | Section C |
| Key store | `GetProviderAPIKey`/`SetProviderAPIKey` (pkg/config/config.go:725-798 area) | Section D presence checks |
| Service client | targetAddr/readToken/serviceClient (servicectl.go:64,100,118) | Section F probes |
| Version + health | `/healthz` (api.go:450), `HealthInfo` (:359), pkg/version | Version-skew check |
| Registry file | `spaces.json` (service.go:110, :694) | Name-collision + reconcile-set awareness |

---

## 4. The contract decision: doctor observes, never touches

Every probe is chosen against a single rule — *running doctor twice, or on
a machine you don't own, changes nothing*:

- No `store.Open` (§3.2) — read-only DB connection, and only if `.vero/`
  already exists.
- No `MkdirAll`, no `WriteManifest`, no token writes, no space register,
  no agent construction (`Loader.Build` loads definitions; it does not
  call `agent.New`).
- Service section is GET-only (`/healthz`, `/api/swarms`).
- Output is the only side effect.

This is also why doctor lives CLI-side (`internal/swarm/doctor`,
imported by `cmd/evva`) rather than as a service endpoint: the moments it
exists for — before the service knows the space, or when the service is
the broken part — are exactly the moments a service endpoint can't help.

---

## 5. Design

### 5.1 D1 — Command + report

```
evva swarm doctor [dir] [--strict] [--offline]
```

```
evva swarm doctor
  A manifest        ✓ evva-swarm.yml — leader "lead", 4 workers
  B members         ✓ lead, pm, backend-a (dir)   ✓ nono (persona, main-tier)
                    ✗ backend-b: agents/sub/backend-b/system_prompt.md missing
  C models          ✓ deepseek-v4-pro ×3   ⚠ "claude-sonet-5" (backend-a):
                       not a built-in — custom model? resolves at client build
  D provider keys   ✓ deepseek   ✗ anthropic: no API key configured
  E state           ✓ .vero absent (fresh dir — will be created at register)
  F service         ✓ 127.0.0.1:8888 healthy (v1.8.5-beta.1, matches CLI)
                    ⚠ name "web-team" already registered (stopped)
2 errors, 2 warnings — register would fail.   exit 1
```

Section order is the dependency order; a hard ✗ in A short-circuits B–C
(can't build members off a bad manifest) but E–F still run (independent).

### 5.2 D2 — Probes per section

- **A**: `LoadManifest` verbatim; its error is the finding.
- **B**: per dir member, `Loader.Build(dir, role, sharedSkillsDir)` —
  surfacing exactly the error register would; per persona member, registry
  resolve + `IsMain()` (the space.go:244-252 mirror, same wording).
- **C**: pin → `ProviderOfModel`: hit = note provider switch; miss = ⚠
  custom (✗ under `--strict`). Effort strings → the `ParseEffort` mirror.
- **D**: for the set of providers implied by (default provider + every
  member pin resolving to a built-in), key present in config/env. Ollama
  (keyless local) checks reachability config only — presence of a base URL,
  no network call.
- **E**: if `.vero/` exists: ro-open `vero.db`, `schema_migrations` max vs
  embedded max (older = ✓ "will migrate at register"; newer = ⚠ "written by
  a newer evva"); `runtime.json` parse (corrupt = ⚠ with the silent-empty
  consequence spelled out, resume.go:100); `events/` present-and-dir.
  Absent `.vero/` = ✓ fresh.
- **F** (skipped by `--offline`): `/healthz` reachable + version vs
  `pkg/version` (skew = ⚠); token file readable (servicectl.go:100);
  `GET /api/swarms` name collision against the manifest/`--name`.

### 5.3 D3 — Exit + output modes

`0` all ✓; `1` any ✗; `--strict` promotes ⚠→✗ (exit 2 reserved for
"warnings only under strict" so scripts can distinguish). `--json` emits
the findings as a machine-readable array (one struct per finding: section,
level, member, message) for CI use.

---

## 6. Work items

**DR-1 — Doctor core + sections A/B.**
`internal/swarm/doctor` package: finding model, report renderer, exit
logic; manifest + member-build probes (dir + persona), short-circuit
rules.
*Accept:* fixture workdirs (good, missing-prompt, bad-manifest,
persona-not-main) produce the expected findings and exit codes; zero
filesystem mutation asserted (before/after tree hash in tests).

**DR-2 — Sections C/D: models, efforts, keys.**
Constant-table checks, custom-model ⚠ semantics, `--strict` promotion,
provider-key presence over the config store + env.
*Accept:* a typo'd built-in-adjacent pin warns (errors under strict); a
member set spanning two providers demands both keys; keyless Ollama passes
without network.

**DR-3 — Section E: state probes.**
Read-only sqlite open (`mode=ro`), schema version compare (incl.
newer-than-binary), runtime.json tolerance surfacing, events-dir check;
the §4 no-mutation contract enforced (no Open, no MkdirAll).
*Accept:* a db at migration N+1 warns; a corrupt runtime.json warns with
the "treated as empty at register" text; a pristine dir stays pristine
byte-for-byte.

**DR-4 — Section F + CLI verb + docs.**
Service probes (health/version/token/name-collision), `doctor` in
`runSwarm` (swarm.go:33) + help (:103), `--offline`/`--strict`/`--json`,
user guide (en, zh-tw) + CHANGELOG.
*Accept:* doctor runs green against a live starter-example register;
`--offline` never dials; docs in both languages.

Sequencing: `DR-1 → {DR-2, DR-3} → DR-4`.

---

## 7. CI plan summary

| Stage | Change | Cost |
|---|---|---|
| DR-1..3 | fixture-workdir unit suites, pure offline | seconds |
| DR-4 | one httptest-backed service-probe suite | seconds |
| all | no new dependencies (ro-mode on the existing sqlite driver) | — |

---

## 8. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Doctor's mirrors drift from register's real checks (tier check, effort parse) | Where possible call the real function (`LoadManifest`, `Loader.Build`, `ParseEffort`, `ProviderOfModel`); the two hand-mirrors (tier wording, key resolution) get tests that pin them to the originals' behavior |
| ro-open semantics differ across sqlite driver versions | One integration test opens a WAL db ro and reads `schema_migrations`; WAL + ro is a documented modernc capability |
| False confidence ("doctor passed, run still failed") | The report footer states the boundary: keys are checked for presence, not validity; custom models resolve at build; doctor is preflight, not warranty |
| Key *presence* check leaks key material | Findings never echo values — only provider names and yes/no |
| Section F probes a remote service over plaintext | Same trust surface as every existing CLI verb (servicectl plumbing, RP-15 rules unchanged) |

---

## 9. Open questions

1. **Auto-run doctor inside `evva swarm .` (register) as a pre-gate?**
   Recommend not yet — register's own fail-fast already covers A/B; bolting
   C–E on would make custom-model workflows (⚠-heavy) noisier. Revisit
   with a `--doctor` opt-in flag on register.
2. **Tool-name existence probe (active.yml names vs the tool registry)?**
   Recommend include only if `Loader.Build` doesn't already surface it —
   verify during DR-1; if names resolve lazily at agent construction, add
   a registry cross-check in DR-2 and note it as a mirror-risk.
3. **`--json` schema stability promise?** Recommend mark experimental for
   one minor, then freeze.

---

## 10. Rollout

1. DR-1..DR-4 via `feature/swarm-doctor` → `dev`.
2. `pre-release feature` cuts the first beta under the minor assigned at
   wave confirmation (or the wave rides an adjacent swarm minor as a
   follow-on — operator's call at confirmation).
3. Beta validation: doctor against every shipped example (starter,
   tech-team, code-review, werewolf, world-football) plus three sabotaged
   fixtures; confirm zero-mutation on a pristine checkout.
4. `release` promotes.
