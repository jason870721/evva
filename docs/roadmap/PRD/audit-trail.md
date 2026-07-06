# PRD — Audit Trail (tamper-evident action log for compliance contexts) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W34**, suggested horizon
> H5 / post-v2.0 per [../long-range.md](../long-range.md) §3b. Depends
> on typed events (ARC-1), secret redaction (W3), and the permission
> broker (shipped).
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> Every prior wave makes evva *more* autonomous and *more* connected
> (unattended gardener, CI runs, chat bridges, human-in-swarm). The
> flip side of that trust is accountability: in any regulated,
> team-owned, or client-facing context, "what did the agent do, when,
> under whose authority, and can we prove the record wasn't altered"
> is a hard requirement — and evva has no answer today beyond
> scattered logs and session snapshots that anyone can edit.
> **Reference source:** none — evva-native; prior art: audit-log
> patterns (append-only, hash-chained), not agent-specific.

---

## 1. TL;DR

An opt-in **tamper-evident audit log**: every consequential action —
tool executions (esp. bash, edit/write, network), permission decisions
(who/what/when, rule-derived vs prompted), model calls (model, cost,
provider), config changes, swarm ledger transitions, checkpoint/rewind
operations — is recorded as a structured, redacted (W3) entry in an
append-only, **hash-chained** log (each entry carries the hash of the
prior, so any deletion or edit breaks the chain and is detectable).

```
$ evva audit verify ~/work/api
✓ 4,182 entries, chain intact, 2026-06-01 → 2026-07-06
$ evva audit query --actor evva --action bash --since 24h --format json
  [ … structured entries … ]
```

The design leans entirely on ARC-1's typed events (the audit log is a
*durable, integrity-protected projection* of the event stream — not a
second instrumentation pass) and W3's redaction (secrets never enter
the permanent record). What it adds is **integrity** (the hash chain),
**completeness guarantees** (a declared set of must-log actions that
fail loud if unlogged), and **query/verify/export** tooling for the
humans who have to answer for the agent.

This is the wave that lets evva into rooms it currently can't enter:
regulated industries, client work under contract, team environments
with change-control requirements.

## 2. Goals / non-goals

### Goals

- Append-only hash-chained store (`<EVVA_HOME>/audit/` or per-project
  `.evva/audit/`, operator choice): each entry `{seq, ts, actor,
  action, subject, authority, outcome, cost?, prev_hash, hash}`;
  chain verification detects any mutation/deletion/reordering.
- Must-log action set (declared, enforced): tool executions, permission
  decisions, model calls, config writes, swarm ledger transitions,
  session lifecycle, checkpoint/rewind. An action in the set that
  fails to log is a hard error (fail-closed when `audit: strict`),
  a loud warning otherwise — silence is never acceptable for a
  must-log action.
- Redaction-integrated: entries pass through the W3 redactor before
  hashing; the log proves *what happened* without leaking *secrets
  seen*.
- Tooling: `audit verify` (chain integrity + coverage gaps), `audit
  query` (filter by actor/action/time/subject, JSON/text out), `audit
  export` (signed bundle for handing to an auditor — self-contained,
  verifiable offline).
- Authority provenance: each entry records *why* an action was allowed
  — a persisted permission rule (which), an interactive approval (who,
  when), a profile default (which profile), or a CI/bridge automated
  policy. "Under whose authority" is answerable per action.
- Modes: `off` (default), `on` (log + warn on gaps), `strict`
  (fail-closed — a must-log action that can't be recorded refuses to
  execute).

### Non-goals (this wave)

- Cryptographic *signing* by an external authority / blockchain
  anchoring (hash-chaining gives tamper-*evidence*; notarization is a
  documented v2 for contexts that require third-party attestation).
- Centralized/remote log aggregation (SIEM shipping) — the export
  bundle is the integration point; a syslog/OTLP sink can ride ARC-6's
  observability export separately.
- Replacing session snapshots or the event log (audit is a distinct,
  integrity-focused projection — different retention, different
  guarantees).
- Access control *on the audit log itself* beyond filesystem perms
  (the operator owns their machine; multi-user audit RBAC is a hosted
  concern).

## 3. Design sketch

- **Projection, not re-instrumentation:** ARC-1 events are the source;
  the audit writer subscribes to the event sink (the architecture's
  `event.Sink` fan-out is exactly this seam) and projects must-log
  events into chained entries. This means the audit log's completeness
  is provable *against the event vocabulary* — a new consequential
  event kind must declare its audit disposition (log/skip) or the
  build/test catches it (a registry completeness test).
- **The chain:** `hash = H(seq || ts || canonical(entry) ||
  prev_hash)`; the head hash is the log's fingerprint. `verify`
  recomputes the chain; a break localizes to the first bad seq.
  Rotation (size/time) chains across files (each file's first entry
  references the prior file's head) so rotation doesn't break
  verification.
- **Fail-closed discipline (strict mode):** the audit writer sits
  *before* the irreversible action commits where possible (a bash
  exec in strict mode logs-intent → executes → logs-outcome; if
  intent can't be written, the exec refuses). This is the same
  "never silently downgrade safety" principle as sandbox-isolation's
  refuse-loud rule.
- **Redaction ordering:** redact → canonicalize → hash, so the hash
  covers the *stored* (redacted) form; the log is internally
  consistent and an auditor verifying it never needs the secrets.

## 4. Work items

- **AUD-1 — Chained store + verify.** Entry schema, hash chain,
  rotation-across-files, `audit verify`. *Accept:* property test —
  any single-entry mutation/deletion/reorder is detected and
  localized; rotation preserves verifiability.
- **AUD-2 — Event projection + must-log registry.** Sink subscriber,
  must-log action set, per-event-kind audit disposition +
  completeness test. *Accept:* every must-log action produces exactly
  one entry in a fixture run; a new event kind without a disposition
  fails the registry test.
- **AUD-3 — Redaction integration + authority provenance.** W3
  redact-before-hash; authority field populated per decision source.
  *Accept:* a bash logging a secret-bearing command stores redacted +
  hashes the redacted form; each entry's authority resolves to a real
  rule/approval/profile.
- **AUD-4 — Modes + fail-closed.** off/on/strict, gap warnings,
  strict-mode refuse-on-unloggable. *Accept:* strict mode blocks an
  action whose intent can't be logged; `on` warns and proceeds;
  `off` is zero-overhead (no subscriber).
- **AUD-5 — Query + export.** Filtered query (actor/action/time/
  subject), JSON/text, self-contained verifiable export bundle.
  *Accept:* queries return correct subsets; an exported bundle
  verifies offline with a shipped `audit verify --bundle`.
- **AUD-6 — Docs + changelog.** User-guide (en + zh-tw): threat model
  (tamper-evidence ≠ tamper-proof), modes, what's logged, the
  export/verify workflow, the notarization non-goal.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Operators over-trust "tamper-evident" as "tamper-proof" | docs are blunt: a local attacker with write access can rewrite the *whole* chain from a point (they just can't edit *within* it undetected); external notarization is the v2 answer, named explicitly |
| Performance overhead on hot paths | `off` = no subscriber (zero cost); on/strict append is a hash + fsync-batched write; must-log set is deliberately consequential-actions-only, not every event |
| Completeness gaps (an action slips the must-log net) | the registry completeness test fails the build when an event kind lacks a disposition — coverage is enforced, not hoped |
| Redaction leak into the permanent record | redact-before-hash ordering + W3's own tests + an audit-specific test asserting no rule-matching content in stored entries |

## 6. Open questions

1. Store location default: global (`<EVVA_HOME>/audit/`, cross-project
   accountability) vs per-project (`.evva/audit/`, travels with the
   repo)? Leaning per-project default with a global option — most
   compliance contexts are repo/client-scoped.
2. External notarization (RFC 3161 timestamping / transparency-log
   anchoring) as the v2 — what regulatory ask triggers building it?
3. Should swarm spaces get their own space-scoped audit log
   (multi-actor accountability is sharpest there), or fold into the
   project log with actor fields? Leaning space-scoped for swarms,
   given the multi-member authority story.
