# PRD — Session Replay Lab (what-if branching over recorded runs) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W33**, suggested horizon H4
> per [../long-range.md](../long-range.md) §3b. Hard dependencies:
> the eval harness (W4 — replay machinery + fixture format) and
> session tree (W7 — fork semantics + catalog). This wave is their
> deliberate composition.
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> When a session goes sideways, today's post-mortem is scrollback
> archaeology, and today's "would the cheaper model have managed?" is
> a shrug. The eval harness answers these questions for *fixtures*;
> the lab points the same machinery at *your actual sessions* — the
> flight recorder becomes a wind tunnel.
> **Reference source:** none — evva-native.

---

## 1. TL;DR

`evva lab` — an operator instrument (CLI + TUI overlay) for running
counterfactuals on recorded sessions:

```