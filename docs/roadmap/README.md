# Roadmap & planning

Internal planning artifacts: phase plans, product requirements, subsystem designs, and the
Veronica multi-agent project. These track *how features were/are being built* — they are not
user documentation (see [../user-guide/](../user-guide/README.md) for that).

> The authoritative **wave → minor-version map** and the release process live in
> [../../CLAUDE.md → Release workflow](../../CLAUDE.md#release-workflow). This folder holds the
> per-wave detail behind that map.

## Contents

| Area | What |
|---|---|
| [overview.md](overview.md) | **Progress tracker** — status of every PRD (shipped / beta / proposed) across the whole roadmap tree. Start here if you're unsure what's done. Refresh at each release. |
| [long-range.md](long-range.md) | **Direction — 2026 H2 → 2028.** 19 waves across 5 horizons (TUI excellence, interop, Swarm 2.0, platform/v2.0), the ARC refactor track, new explore spikes, dependency graph. Concept PRDs it spawned live in [PRD/](PRD/) marked "long-range concept draft" — each needs a live-source audit before build. |
| [roadmap_healthcheck.md](roadmap_healthcheck.md) | Codebase + design-gap audit (must-have vs nice-to-have). |
| [v1/](v1/) | Phase plans for the v1.x line — hooks, OpenAI provider, MCP, bundled skills, config tool. |
| [design/](design/) | Subsystem designs — agent runtime, daemon model, task ledger, LSP, fs-edit gate parity. |
| [PRD/](PRD/) | Feature PRDs — Windows support, typed memory, build-agent skill, output styles, structured output, alarm tool, sandbox isolation, MCP server mode, agent eval harness, and the swarm `v1.11+` candidates. |
| [evva-sdk/](evva-sdk/) | SDK hardening notes (the v1.0 "harden to stable" effort). |
| [veronica/](veronica/README.md) | The Veronica multi-agent swarm project — design, PRDs, 5 refinement waves, explore spikes. |

## Conventions

- One roadmap wave = one minor version (see CLAUDE.md).
- Ticket families: `RP-*` (Veronica refine plan), `EX-*` (explore spikes), `SPRD-*`
  (phase-1 sub-tickets), `FE-*` (web UI v2), `WIN-*` (Windows).
