# PRD — Vision Completion (image input across TUI, tools, and providers) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (NOT audited; pin
> file:line references in the audit pass before implementation).
> **Target release:** TBD — tentative slot **W10 / v1.20** per
> [../long-range.md](../long-range.md).
> **Roadmap source:** 2026-07-06 long-range planning pass. Image input is
> table stakes in 2026 harnesses (paste a screenshot of the bug, agent
> fixes it). evva's `pkg/llm` message model **already carries image
> content blocks** (`tools.ContentBlockImage`, with a `[Image: mime,
> bytes]` text-stub fallback) — the plumbing exists; what's missing is
> every way to actually get an image *into* it and provider parity for
> what happens after.
> **Reference source:** `ref/src` image-attachment surfaces — port the UX;
> the block model is already evva-native.

---

## 1. TL;DR

The gap analysis (verify in audit): image blocks exist in the message
model and at least the Anthropic-shaped clients touch them, but (a) the
TUI has no attach path — no paste, no `@image.png` reference, no drag; (b)
the `read` tool doesn't ingest images as image blocks; (c) providers
without vision (or with divergent image encodings) fall back to text stubs
with unknown consistency; (d) nothing produces screenshots for the model
to look at.

This wave completes the story end to end:

- **In:** paste-from-clipboard in the composer, `@path/to/image.png`
  references, and `read` on image extensions returning image blocks
  (size-capped, downscaled when needed).
- **Capture:** a `screenshot` tool (macOS `screencapture`, Windows
  PowerShell, Linux compositor tools — shell-out, zero deps) for
  see-what-I-see flows and self-verification of TUI/GUI work.
- **Across:** per-model vision capability flags in `pkg/constant`; vision
  requests to non-vision models degrade to the existing stub *loudly*
  (status-line notice + history note), and routing (W9) skips non-vision
  hops for image-bearing turns via the capability guard.
- **Through:** subagent and swarm mail can carry image references so a
  member can hand the leader a screenshot of a rendering bug.

## 2. Goals / non-goals

### Goals

- Composer attach: terminal-image paste where the terminal supports it,
  file-path attach everywhere (`@`-completion already exists for files —
  extend, don't invent); attached images render as thumbnails/placeholders
  per terminal capability.
- `read` tool: image extensions produce image blocks with automatic
  downscale to provider limits (longest edge / byte caps per provider from
  the capability table); PDFs stay as shipped.
- `screenshot` tool family: full-screen / window / region where the
  platform allows; output lands as a session-scoped file + image block.
  Permission-gated like other observation tools.
- Capability table: `pkg/constant` models gain `vision: bool` + image
  limits; `/model` overlay displays it; W9 chain guard consumes it.
- Provider parity: image encoding verified per provider (Anthropic base64
  blocks; OpenAI-surface data-URLs; Ollama multimodal models; DeepSeek/GLM
  per their current API state — the audit fixes the truth table).

### Non-goals (this wave)

- Image *generation* — input only.
- Video, audio.
- OCR preprocessing (send the pixels; models read text fine).
- Computer-use / GUI driving (browser wave W13 handles the one concrete
  case: page screenshots).
- Inline terminal graphics protocols beyond best-effort (sixel/kitty
  support is a rendering nicety, not a blocker — placeholder rendering is
  acceptable everywhere).

## 3. Design sketch

- **One ingestion func:** `imageblock.FromFile(path, providerLimits)` —
  decode, downscale (stdlib `image` + `x/image` if needed for formats),
  re-encode, return block + metadata. Every entry path (paste, `@ref`,
  read, screenshot, browser later) uses it; caps enforced in exactly one
  place.
- **History weight:** image blocks are heavy — they register in the
  context-engine ledger (W5) under an `image` category with aggressive
  prune defaults (screenshot from 50 turns ago tombstones early;
  tombstone carries the file path for re-attach).
- **Swarm transport:** mail carries image *references* (paths in the
  space), not bytes; the member's loop ingests via the same func. Web
  console renders from the file.
- **Failure honesty:** attaching an image on a non-vision route yields a
  visible composer warning *before* send, not a silent stub downgrade.

## 4. Work items

- **VIS-1 — Ingestion func + caps.** Decode/downscale/encode + capability
  table in `pkg/constant`. *Accept:* oversized PNG downscales under
  provider caps; corrupt file yields a clean tool error.
- **VIS-2 — Composer attach.** Paste + `@image` references + pre-send
  non-vision warning. *Accept:* pasted image reaches an Anthropic-shaped
  request as a proper block (recording fake).
- **VIS-3 — `read` image support.** Extension routing into image blocks.
  *Accept:* `read screenshot.png` on a vision model returns an image
  block; on a non-vision model returns the stub + loud note.
- **VIS-4 — `screenshot` tool.** Platform shell-outs, permission gating,
  session-scoped output files. *Accept:* on macOS CI (or manual
  validation), full-screen capture lands as an ingestible file; tool
  refuses gracefully on headless Linux.
- **VIS-5 — Provider parity pass.** Truth table + per-provider encoding +
  tests with recording fakes. *Accept:* every provider either sends real
  image payloads or stubs loudly — no third state.
- **VIS-6 — Subagent/swarm image handoff.** Mail references, spawn-prompt
  attachments, web console render. *Accept:* member → leader screenshot
  handoff renders in the web console and enters the leader's context as an
  image block.
- **VIS-7 — Docs + changelog.** User-guide (en + zh-tw): attach paths,
  provider support matrix, context-weight note.

## 5. Risks

| Risk | Mitigation |
|---|---|
| Context bloat from casual screenshots | ledger category + aggressive prune defaults + composer shows estimated token cost of an attach |
| Provider image APIs drift | truth table lives in one file; parity tests are fixture-based and re-runnable per provider |
| Terminal paste support is wildly uneven | file-path attach is the universal fallback and the documented primary path; paste is progressive enhancement |
| Screenshot tool on Wayland/X11 fragmentation | best-effort tool detection with a clear refusal message; not a blocker for the wave |

## 6. Open questions

1. Which `x/image` formats are worth the (golang.org/x, precedent exists)
   dependency vs stdlib-only (PNG/JPEG/GIF)? Leaning stdlib-only v1.
2. Thumbnail rendering in Bubble Tea: invest in kitty/sixel now or ship
   placeholders first? Leaning placeholders (the model sees it; the human
   has the file path).
3. Should `screenshot` of the *evva TUI itself* be special-cased for
   self-verification of TUI development? (Fun, useful for evva-on-evva
   work; possibly free if window capture works.)
