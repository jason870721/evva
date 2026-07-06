# PRD — Browser Tool Family (CDP-driven navigate / read / screenshot / interact) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references — and settle the dependency question in §6 —
> before implementation).
> **Target release:** TBD — tentative slot **W13 / v1.23** per
> [../long-range.md](../long-range.md). Depends on vision completion
> (W10) for screenshot ingestion.
> **Roadmap source:** 2026-07-06 long-range planning pass. Every serious
> 2026 harness can look at a rendered page (Claude Code's Chrome
> integration, browser-use, Playwright MCP servers). evva's `web_fetch`
> gets HTML-as-text; anything client-rendered, and any "does my change
> actually look right" question, is invisible today.
> **Reference source:** none in `ref/src` — evva-native; prior art is the
> Chrome DevTools Protocol ecosystem.

---

## 1. TL;DR

A `pkg/tools/browser` family that drives a locally installed Chrome/
Chromium via the **Chrome DevTools Protocol**, giving evva eyes and hands
on rendered pages:

| Tool | Does |
|---|---|
| `browser_open` | launch/attach (dedicated profile dir, never the user's), navigate, wait-for-load |
| `browser_read` | readability-style text digest + interactable-element inventory (indexed refs) |
| `browser_screenshot` | viewport/element capture → image block via the W10 ingestion func |
| `browser_act` | click/type/select/scroll against element refs from `browser_read` |
| `browser_console` / `browser_network` | console log tail, request summaries — the frontend-debugging loop |

Primary use cases, in order: **self-verification** (evva changes a web
app, then *looks at it* — pairs with the swarm's verify policies and the
`verify`-style flows), **frontend debugging** (console + network + DOM in
one loop), and **research on JS-rendered pages** (where `web_fetch`
returns an empty shell).

The design constraint that shapes everything: CDP speaks WebSocket, Go's
stdlib has no WebSocket client, and CLAUDE.md's dependency policy is
deliberate. §6's dependency question (hand-rolled minimal WS vs
`golang.org/x/net/websocket`-class dep vs `chromedp`) must be settled by
the operator at wave pickup — the PRD recommends **hand-rolling the
client-side WS framing** (RFC 6455 client half is ~300 lines; the repo
already hand-writes LSP/JSON-RPC on the same principle).

## 2. Goals / non-goals

### Goals

- Zero-config launch: find Chrome/Chromium/Edge on PATH/well-known
  locations, launch headless(-new) with a dedicated profile under the
  session dir; attach mode (`--remote-debugging-port`) for watching a
  real session, permission-gated separately.
- Element-reference model: `browser_read` returns a numbered inventory
  (role, name, ref) — `browser_act` targets refs, never raw selectors,
  keeping the model's action space small and the tool results compact.
- Deterministic waits: navigation and actions wait on load/network-idle
  heuristics with bounded timeouts — no model-driven sleep loops.
- Safety: same permission gate as every tool; a domain allowlist knob for
  unattended contexts (CI profile denies `browser_act` by default);
  dialogs auto-dismissed with a note (a JS alert must never wedge the
  loop).
- Session hygiene: browser lifetime tied to the session (daemon-kind
  registration so it shows up in listings and dies on exit).

### Non-goals (this wave)

- Full computer-use (OS-level mouse/keyboard).
- Multi-tab orchestration beyond open/switch/close.
- Firefox/WebKit drivers (CDP-speaking browsers only).
- Auth automation (password managers, OAuth flows) — the attach mode is
  the documented answer for authenticated testing.
- Video/HAR capture.

## 3. Design sketch

- **Transport:** CDP over one WS connection; JSON-RPC-ish envelope
  (id/method/params + events) — mirrors the LSP client's dispatcher
  shape; sessions multiplexed via CDP's flat-session `sessionId`.
  Domains used: Page, Runtime, DOM, Accessibility (for the element
  inventory), Network, Console/Log, Input.
- **Element inventory:** built from the accessibility tree (role+name,
  filtered to interactables + landmarks), not raw DOM — smaller, more
  stable, and matches how models reason about pages. Refs map to backend
  node ids internally; stale refs (post-navigation) error with a
  "re-read the page" hint — the same honest-tombstone philosophy as the
  context engine.
- **Text digest:** Readability-style extraction in-process (port the
  heuristics, no JS-in-page dependency), size-capped with the standard
  truncation idioms.
- **Screenshots:** `Page.captureScreenshot` → W10 ingestion func →
  image block; element screenshots via box-model clip.

## 4. Work items

- **BRW-1 — Transport + dependency decision.** WS client (per §6
  decision) + CDP envelope/dispatcher + launch/attach lifecycle.
  *Accept:* headless launch, navigate, evaluate `1+1` round-trip on CI
  with a pinned Chromium; browser dies with the session.
- **BRW-2 — `browser_open` + waits.** Navigation, load heuristics,
  timeout discipline, daemon-kind registration. *Accept:* SPA fixture
  reaches network-idle deterministically; timeout yields a clean error.
- **BRW-3 — `browser_read`.** Accessibility-tree inventory + text
  digest + caps. *Accept:* fixture page yields stable refs across two
  reads; digest strips nav/boilerplate.
- **BRW-4 — `browser_screenshot`.** Viewport/element capture into image
  blocks (W10). *Accept:* element screenshot of a fixture div matches
  its box; lands in history as a proper image block.
- **BRW-5 — `browser_act`.** Click/type/select/scroll on refs, stale-ref
  errors, dialog auto-dismiss. *Accept:* scripted form-fill fixture
  completes; a triggered `alert()` is dismissed with a system note, loop
  never blocks.
- **BRW-6 — Console + network tails.** Pattern-filterable console read,
  request summaries. *Accept:* fixture page's console error is
  retrievable by pattern; network summary caps at N entries.
- **BRW-7 — Safety + docs.** Domain allowlist, CI-profile defaults,
  permission wiring, user-guide (en + zh-tw) incl. the attach-mode
  security note.

## 5. Risks

| Risk | Mitigation |
|---|---|
| CDP protocol drift across Chrome versions | use only stable core domains; version-probe on connect with a documented minimum |
| WS hand-roll correctness (fragmentation, masking, close handshake) | client-half only, golden-frame tests, fuzz the frame parser; escalate to a dep if the audit finds real trouble (§6) |
| Model wedges the loop via dialogs/infinite pages | auto-dismiss + bounded waits + hard tool timeouts (existing tool timeout machinery) |
| Unattended browsing as an exfiltration channel | domain allowlist + CI-profile deny of `browser_act`; SEC redaction applies to page text entering history |
| Local Chrome absence | clean "install Chrome or point `browser_binary`" refusal; never auto-download a browser in v1 |

## 6. Open questions

1. **The dependency question (operator call):** hand-rolled RFC-6455
   client (recommended; ~300 lines, in-repo precedent) vs
   `golang.org/x/net`-class WS dep (x/ precedent exists via
   singleflight) vs `chromedp` (largest, fastest to ship, heaviest).
2. Attach-to-user's-real-browser: ship in v1 behind a scary permission,
   or defer entirely? (Authenticated-app testing wants it; risk profile
   is real.)
3. Should `browser_read` refs persist across the session (a ref cache
   invalidated on navigation), or be read-scoped only? Leaning
   navigation-scoped.
