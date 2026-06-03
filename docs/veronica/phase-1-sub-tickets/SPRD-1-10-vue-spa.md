# SPRD-1-10 — vue.js SPA: space picker, Team Board, Roster, Leader Chat, overlays

> Milestone: M1–M3 ｜ Status: TODO ｜ Owner: (unassigned) ｜ Depends on: 1-8
> Parent: [`../prd-phase1-swarm.md`](../prd-phase1-swarm.md) (元件 6) ｜ Design: [`../veronica-design-v1.md`](../veronica-design-v1.md) §8.2, §8.3

## 1. Goal

The **operator UI**: a vue3 + vite SPA (the scaffold from 1-1, now filled in) that
consumes the 1-8 API — a space picker, a 5-state Team Board, an Agent Roster, the Leader
Chat, and the permission/question approval overlays — built to `web/dist` and embedded in
the binary.

## 2. Scope

**In (by milestone, matching the gates):**
- **Space picker** (M0/M1): list `/api/swarms`; enter a space.
- **Leader Chat** (M0): send a prompt to the leader (`Controller.Run` via WS); render the
  streamed `event.Event`s (text / tool-use / thinking) live.
- **Team Board** (M1): 5-column kanban (pending/running/suspended/verifying/completed)
  from `/api/tasks`, live-updated by task events.
- **Agent Roster** (M1): members with membership (active/frozen) + run-status
  (idle/busy/suspended) + current task; **add / freeze** controls (M3).
- **Approval overlays** (M3): permission + question prompts surfaced in Leader Chat,
  answered via `RespondPermission`/`RespondQuestion` (§8.3).
- **Per-agent view**: click a member → its transcript + mailbox.
- A WS client for live events; REST for initial snapshots; the session token from the service.

**Out:** any trading/domain UI (Phase 2); auth beyond the session token.

## 3. Dependencies & what this unblocks

- Depends on: 1-8 (REST + WS + the embedded-asset serving).
- Unblocks: the human-visible M0–M3 gates ("every milestone can be run and seen");
  1-13 (the e2e can assert via the same API the SPA uses).

## 4. Technical design

`web/` (vite + vue3, TypeScript) → `npm run build` → `web/dist` → `embed.FS` (1-8 serves).

- A small typed API client over `/api/*` + a WS wrapper that demuxes events by
  `(spaceID, AgentID)`.
- State: a light store (Pinia or composables) per active space; the board / roster derive
  from the event stream layered on the initial REST snapshot.
- Components: `SpacePicker`, `LeaderChat`, `TeamBoard`, `Roster`, `AgentTranscript`,
  `ApprovalOverlay`.
- Mirror evva's TUI event semantics (event kinds → render) so Web behavior matches the TUI.

## 5. Acceptance criteria

1. The picker lists registered spaces; entering one shows its roster + board.
2. A prompt typed in Leader Chat reaches the leader and the reply streams token-by-token.
3. A task moving through the 5 states is reflected on the kanban with no manual refresh.
4. The roster shows live membership + run-status; the add/freeze controls call the API.
5. A permission prompt appears as an overlay and the user's decision unblocks the tool.
6. `npm run build` produces `web/dist`; the embedded SPA loads from `:8888`.

## 6. Verification

- Component/unit tests (vitest) for the API-client demux + the board / roster reducers.
- A manual end-to-end (documented in the PR): start service, register a space, drive a
  task through the board, approve a permission.
- `npm run build` runs in CI (from 1-1's pipeline).

## 7. Definition of Done

- [ ] Space picker, Leader Chat (streaming), Team Board (5-state), Roster (+add/freeze),
      approval overlays, per-agent transcript.
- [ ] WS live updates + REST snapshots; token-authenticated.
- [ ] `web/dist` builds + embeds and loads from the service.
- [ ] vitest unit tests green; behavior matches evva event semantics.
