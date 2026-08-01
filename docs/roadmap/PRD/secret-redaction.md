# PRD — Secret Redaction at the LLM Egress Boundary

> **Audience:** senior engineers implementing this wave.
> **Status:** ✅ **BUILT** — SEC-1..6 implemented 2026-07-30 on
> `feature/secret-redaction`. The audit pass required by the concept → build
> gate ([../long-range.md](../long-range.md) §1 step 2) was run at
> `dev@d756c90`; the five corrections it produced are recorded below and the
> body of this document is left as written so the delta stays legible.
> **Target release:** claims **v1.14** (the wave → minor row is in
> `CLAUDE.md` / `EVVA.md`). The tentative **W3 / v1.13** slot was taken by
> MCP server mode before this wave was picked up. W3's other half —
> `sandbox-isolation.md` (SBX-1..7) — was unbuilt at the time of writing
> because its acceptance criteria (§8 of that PRD) need a working
> `docker`/`podman` and the build machine had none. **Resolved 2026-08-01:**
> Docker 28.1.1 was available, SBX-1..7 was built under **v1.15**, and W3
> "Safety" is complete.
>
> ### Audit-pass corrections
>
> 1. **The choke point is `execTool`** (`internal/agent/state_machine.go`),
>    which §4 left for the audit to find. It is better than the design
>    assumed: it is the single place a `tools.Result` becomes *both* the
>    `llm.ToolResult` that enters the session (→ provider → snapshot) *and*
>    the `KindToolUseResult` event the TUI renders. One insertion covers
>    egress and display, and it sits after the PostToolUse hooks so a hook's
>    additional context is masked too.
> 2. **"Chunk-overlap handling for streamed bash output" (SEC-2) is moot.**
>    `BashTool.Execute` returns one whole `tools.Result`
>    (`pkg/tools/shell/bash.go:224`); background daemons buffer into
>    `capOutput` and are read whole via `Output()`. No chunked egress path
>    exists for a match to be split across.
> 3. **SEC-4 collapses into SEC-2.** Because redaction runs *before* the
>    session append, the session only ever holds placeholders — snapshots
>    are clean for free and `ResumeSession` round-trips exactly what the
>    model saw. The separate persist-time scrub the PRD sketched would have
>    been strictly worse: it desyncs disk from live and makes resume lossy.
>    SEC-4 shipped as an assertion, not a mechanism.
> 4. **Operator input is out of scope.** §4 has the choke point covering
>    "tool results / user pastes". Only tool results are masked. Pasting a
>    credential is a deliberate act with a human watching; masking it breaks
>    credential-rotation flows and offers only the appearance of protection,
>    since the operator can paste it again. Documented as a stated limit.
> 5. **Reveal is `/redactions`, not a per-span keybinding.** §4's dimmed
>    spans with a reveal binding need span-position tracking through the
>    transcript renderer. An overlay (the `/cost` pattern) answers the same
>    question — what was masked, by which rule, and the real value on
>    demand — for a fraction of the surface, and renders UI-only so
>    revealing never re-enters the session. The status-line counter was
>    dropped: the `[REDACTED:…]` token appears inline in the tool result
>    itself, which tells the operator more than a count, and exactly where
>    it matters.
>
> **Open questions (§7), as resolved:** (1) a placeholder written back into
> a file is **not** blocked — no write-side guard shipped, since the
> placeholder is visibly not a credential and blocking would add a failure
> mode to every edit for a case that announces itself. (2) No one-shot
> `scrub` command for historical sessions; correction 3 means new sessions
> are clean, and rewriting old transcripts in place is a separate,
> destructive operation deserving its own design. (3) Swarm namespace:
> **moot, and better than either option** — placeholders are derived from
> the value itself (`fnv32a`), so every member, sub-agent and session
> already co-references the same secret with the same token, with no shared
> state at all.
>
> **Performance note, since the PRD budgeted it (SEC-1 accept: <1ms on
> 100KB):** the rule table as designed costs ~1.6ms *per rule* on a 100KB
> result — 41ms for the set, on every tool call — because `\b`-anchored
> patterns have no literal prefix for RE2 to anchor on. Shipped at
> **221µs** via per-rule literal prefilters gated by one case-folded bigram
> pass, plus a hand-rolled entropy scan replacing its regex.
> **Roadmap source:** 2026-07-06 long-range planning pass. Every major agent
> vendor shipped some egress guard in 2025–26 (Claude Code redacts obvious
> key shapes in transcripts; enterprise agent platforms treat DLP-at-egress
> as table stakes). evva sends tool results verbatim to five different
> provider APIs today.
> **Reference source:** none in `ref/src` — evva-native.

---

## 1. TL;DR

Everything a tool returns — `cat .env`, a `read` of `config/production.yml`,
a `bash` dump of `printenv` — is appended to the session and shipped to
whichever LLM provider the session uses, then persisted in session
snapshots and logs on disk. There is no layer that recognizes
`AKIA…`-shaped strings, PEM blocks, or `.env` values and stops them from
leaving the machine.

This wave adds a **detector + redactor** applied at two boundaries:
**(a) egress** — tool results and file reads are scanned before they enter
the LLM message history; matches are replaced with stable placeholders like
`[REDACTED:aws-access-key:9f2c]`; **(b) persistence** — session snapshots
and transcript exports are scrubbed with the same rules, so a shared
transcript can't leak what the live session saw. Placeholders are *stable*
(same secret → same tag within a session) so the model can still reason
about "the same key appears in both files" without ever seeing the value.

Opt-out, not opt-in: this is a safety default. `redaction: off` and a
per-pattern allowlist exist for the rare session that legitimately needs
raw credentials.

## 2. Motivation

- The swarm and future unattended modes (gardener, CI) widen the blast
  radius: nobody is watching the transcript when a member cats a prod env
  file at 3am.
- Five providers ×  N jurisdictions: the operator's secrets policy should
  not depend on which `llm.Client` a persona picked.
- Transcript sharing (session-tree wave, W7) and CI logs (W12) both make
  transcripts *more* public over time — scrubbing must exist first
  (dependency edge in long-range §7).

## 3. Goals / non-goals

### Goals

- A `pkg/redact` package: rule table (well-known credential formats: AWS,
  GCP, GitHub/GitLab tokens, Slack, Stripe, private-key PEM blocks, JWTs,
  generic `KEY=high-entropy-value` env shapes) + a Shannon-entropy fallback
  for quoted high-entropy literals. Pure functions, table-tested, zero deps.
- Egress integration: one choke point where tool results / user pastes are
  appended to the LLM history — redact there, not per-tool (audit pass must
  identify the exact seam in `internal/session` / the agent loop).
- Stable placeholders with a short non-reversible fingerprint; a per-session
  in-memory map for operator-side "reveal" in the TUI (the value never
  re-enters the LLM path).
- Snapshot/export scrubbing with the same rule table.
- Config: `redaction: on|off` (default **on**), `redaction_allow` pattern
  list, per-rule disable.

### Non-goals (this wave)

- **Semantic DLP** (names, addresses, PII classifiers) — format-based
  credentials only.
- **Blocking the tool itself** — the file is still read; only what leaves
  the process is masked. The permission gate stays the "should this run"
  layer; redaction is the "what may leave" layer. Different axes.
- **Redacting the model's own outputs** (it can't leak what it never saw).
- Retroactive scrubbing of pre-existing sessions on disk (a `evva doctor`
  style one-shot scrub command is an open question, §7).

## 4. Design sketch

- **Detector:** ordered rule list, each `{id, regex, entropyFloor?,
  contextHint?}`; first match wins per span; overlapping spans merge.
  Entropy fallback fires only inside quoted strings / `=`-assignments of
  length ≥ 20 to keep false positives near zero on ordinary code.
- **Placeholder:** `[REDACTED:<rule-id>:<fnv32(value) mod 0xffff, hex>]`.
  Same value → same placeholder within the session, giving the model a
  stable co-reference token. Placeholders round-trip harmlessly through
  `edit` (if the model tries to write one back into a file, the write-side
  guard warns — the audit pass decides whether to hard-block).
- **Choke point, not per-tool:** the audit must confirm the single seam
  through which tool results become `llm.Message` content; redaction lives
  there so future tools inherit it for free. Bash streaming output is
  scanned per flushed chunk with a small overlap window so split matches
  aren't missed.
- **TUI:** redacted spans render dimmed with the rule id; a keybound
  "reveal" shows the local value (never re-sent). Status line counts
  redactions this session.

## 5. Work items

- **SEC-1 — `pkg/redact` detector.** Rule table + entropy fallback + merge
  logic. *Accept:* table tests cover every rule with true/false-positive
  corpora; a benchmark shows < 1ms on a 100KB tool result.
- **SEC-2 — Egress integration.** Redact at the history-append choke point;
  chunk-overlap handling for streamed bash output. *Accept:* `bash: cat
  .env` reaches the provider payload with placeholders (verified via a
  recording fake client); TUI shows the dimmed spans.
- **SEC-3 — Stable placeholder map + reveal.** Per-session map, TUI reveal
  binding, redaction counter. *Accept:* same secret in two files yields one
  placeholder; reveal never mutates history.
- **SEC-4 — Snapshot & export scrub.** Session snapshots, transcript
  exports, and the structured-output stdout path are scrubbed. *Accept:*
  a snapshot written mid-session contains no raw match for any rule.
- **SEC-5 — Config + allowlist.** Knobs per §3, following the established
  config-knob template (field, Clone, Get/Set, YAML pointer, defaulting,
  round-trip test). *Accept:* YAML round-trip; `redaction: off` restores
  byte-identical passthrough.
- **SEC-6 — Docs + changelog.** User-guide page (en + zh-tw): what is
  caught, what is not (explicitly: this is not DLP), the reveal binding,
  the allowlist. CHANGELOG + version bump ride the wave's release.

## 6. Risks

| Risk | Mitigation |
|---|---|
| False positives corrupt a legitimate flow (e.g. editing a lockfile full of hashes) | entropy fallback scoped to assignment/quoted contexts; per-rule disable; placeholders are visually obvious so the operator sees *why* something broke |
| Model writes a placeholder into a file | write-side guard (warn or block — audit decides); reveal path documented for the operator to fix by hand |
| Performance on huge tool results | single-pass scan, size-capped by the existing tool-result truncation upstream |
| A rule misses a real secret | defense in depth, not perfection — documented as "reduces, does not eliminate"; rule table is easy to extend and ships updates with normal releases |

## 7. Open questions

1. Hard-block vs warn when the model writes a placeholder back into a file?
2. One-shot `scrub` command for historical sessions on disk?
3. Should swarm members share one placeholder namespace per space (so the
   leader's brief and a worker's result co-reference), or stay per-session?
   Leaning per-space — audit against the swarm mail path.
