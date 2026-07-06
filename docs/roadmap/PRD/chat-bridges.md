# PRD — Chat Bridges (personas reachable from Telegram / Slack / Discord) — Concept Draft

> **Audience:** senior engineers implementing this wave.
> **Status:** proposed — **long-range concept draft** (batch 2; NOT
> audited; pin file:line references in the audit pass before
> implementation).
> **Target release:** TBD — batch-2 wave **W35**, suggested horizon
> H5 / post-v2.0 per [../long-range.md](../long-range.md) §3b. Hard
> dependencies: secret redaction (W3), the CI-style unattended
> permission profile (W12's rails generalized), service mode
> (shipped: `evva service` + autostart units).
> **Roadmap source:** 2026-07-06 long-range planning pass, batch 2.
> The persona vision ("nono the financial manager, noen the math
> teacher") has always implied a form factor the terminal can't serve:
> personas you talk to from your *phone*, mid-day, about a question
> that has nothing to do with a git repo. The swarm's webhook +
> service + notification plumbing built most of the transport story
> already — this wave gives personas a doorway the rest of the world
> actually uses.
> **Reference source:** none — evva-native.

---

## 1. TL;DR

A **bridge** connects a chat platform account to a persona running
under the evva service:

```yaml
# <EVVA_HOME>/bridges.yml
bridges:
  - platform: telegram
    token_env: TG_BOT_TOKEN
    persona: nono
    allow_users: ["@johnny"]        # allowlist is mandatory, never open
    workdir: ~/finance              # the persona's world
    profile: remote-restricted      # unattended permission profile
```

DMs to the bot become session turns; replies stream back
(edited-message streaming where the platform allows); each chat thread
maps to a durable session (session-tree W7 catalog — the same
conversation continues for weeks, survives service restarts). Approval
requests that the restricted profile can't auto-decide render as
platform-native buttons (Telegram inline keyboards / Slack Block Kit)
— turning the phone into the remote-approval device the
outbound-notifications wave hinted at.

Platform order is chosen by transport honesty: **Telegram first**
(pure HTTPS long-poll — zero dependencies, no public endpoint, works
from a laptop behind NAT), **Slack second** (Socket Mode — reuses the
WS client that browser-tools/federation already justified), **Discord
third** (WS gateway, same client). One `internal/bridge` core, thin
platform adapters.

This is the wave that makes "one runtime, many personas" legible to
people who will never open a terminal — and it's post-v2.0 on purpose:
it rides a frozen SDK, a mature permission model, and every safety
wave before it.

## 2. Goals / non-goals

### Goals

- `internal/bridge` core: platform-neutral inbound
  (`{user, thread, text, attachments}`) / outbound (streaming text,
  buttons, files) contracts; thread↔session mapping with per-bridge
  session policy (`thread | user | daily`); rate limiting per user.
- Telegram adapter: long-poll, message editing for streaming, inline
  keyboards for approvals, image attachments (vision W10 ingestion).
- Slack adapter (Socket Mode) + Discord adapter (gateway) on the
  shared WS client.
- **Security model (the heart of it):**
  - user allowlists mandatory; unknown senders get silence (not even
    an error — no oracle);
  - every bridge binds ONE persona + ONE workdir + ONE restricted
    profile (no bash by default; tool surface explicitly enumerated
    per bridge — nono gets read/web_search/calc, not edit);
  - approval buttons carry single-use signed tokens; approve/deny
    round-trips the standard permission broker;
  - all inbound text is treated as untrusted (the CI-profile
    prompt-injection posture applies verbatim);
  - SEC redaction on every outbound message.
- Service integration: bridges are service-managed units (start/stop/
  status via the existing service CLI; autostart units already
  documented for the swarm apply here).
- Operator visibility: bridge sessions land in the session catalog
  flagged by origin; `/cost` and budgets (W9) apply per bridge.

### Non-goals (this wave)

- Group-chat participation (DM-only in v1 — group semantics, mention
  parsing, and social ambiguity are a separate, riskier wave).
- Voice notes, calls (attachment images only).
- A coding persona over chat (nothing *prevents* binding `evva`, but
  the restricted-profile defaults and docs steer hard toward
  read-only/advisory personas; mutation-capable bridges demand
  explicit, loud configuration).
- Matrix/WhatsApp/iMessage adapters (contract is adapter-shaped;
  follow demand).
- Multi-tenant hosting (one operator's machine, one operator's
  personas — the hosted question stays parked per long-range §9.4).

## 3. Design sketch

- **Session mapping:** default `thread` policy — a Telegram chat is
  one continuous session, compaction and memory working exactly as a
  long-lived TUI session would; nono actually *remembers* last month's
  budget discussion (typed memory + W6 recall are what make this more
  than a stateless bot).
- **Streaming ergonomics:** platform-appropriate — Telegram edits one
  message in place with throttled updates; Slack updates a thread
  message; final state replaces the stream artifacts. Tool-call
  activity renders as a compact status line ("looking at
  finance/2026-06.csv…"), not raw tool spam.
- **Approval flow:** restricted-profile denials that are
  *escalatable* (per rule config) render buttons; timeout = deny;
  every decision journals to the audit trail (W34 synergy). The
  operator can also pre-approve rule sets per bridge exactly as in
  the TUI's permission store.
- **Attachment path:** inbound images run through vision ingestion
  (a photographed receipt → nono reads it — the demo that sells the
  wave); outbound files size-capped and redaction-scanned.

## 4. Work items

- **CHB-1 — Bridge core.** Contracts, thread↔session mapping,
  allowlist enforcement, rate limits, service-unit lifecycle.
  *Accept:* a fake-platform adapter drives a full turn against a
  fixture persona; unknown user yields silence + a log line.
- **CHB-2 — Restricted profile + approval tokens.** Per-bridge tool
  enumeration, broker integration, signed single-use buttons,
  timeout-deny. *Accept:* a denied-by-default action round-trips an
  approval button; replayed tokens rejected; timeout denies.
- **CHB-3 — Telegram adapter.** Long-poll, streaming edits,
  keyboards, image inbound. *Accept:* live smoke against a real bot
  (manual); fixture tests for update parsing incl. edited/forwarded
  messages.
- **CHB-4 — Slack + Discord adapters.** Socket-mode/gateway on the
  shared WS client, Block Kit/components for approvals. *Accept:*
  fixture conformance per adapter; live smoke documented.
- **CHB-5 — Session catalog + cost integration.** Origin flags,
  per-bridge budgets, `/cost` visibility, bridge status in service
  CLI. *Accept:* bridge sessions listed and resumable read-only from
  the TUI; budget breach pauses the bridge with an operator
  notification.
- **CHB-6 — Docs + changelog.** User-guide (en + zh-tw): setup per
  platform, the security model (allowlist/profile/buttons), the
  advisory-persona guidance, threat notes.
- **CHB-7 — Example personas.** `examples/bridges/`: nono-finance
  (read/search/calc) and a repo-Q&A persona (read-only over one
  repo) — the two shapes the docs recommend.

## 5. Risks

| Risk | Mitigation |
|---|---|
| A leaked bot token = a doorway into the machine | token grants conversation only; allowlist + restricted profile + no-bash defaults bound the blast radius; approval tokens are separately signed; docs mandate env-only token storage |
| Prompt injection from chat content | untrusted-input posture, enumerated tool surfaces, deny-by-default escalation — same rails as CI; advisory personas have nothing dangerous to inject into |
| Long-lived bridge sessions bloat (context/cost) | standard compaction + W5 engine + per-bridge budgets; `daily` session policy for high-traffic bridges |
| Platform API churn | adapters are thin over a stable core; fixtures pin behavior; Telegram's API stability is why it ships first |

## 6. Open questions

1. Should a bridge session be *joinable* from the TUI (operator drops
   into nono's Telegram conversation live) — collaborative-attach
   (EX-13) prerequisite, or read-only view enough for v1?
2. Group chats: is there a safe v1.5 shape (respond only when
   @-mentioned, per-group allowlist), or hold the DM-only line until
   demand forces the design?
3. Per-bridge memory scope: does nono-via-Telegram share the global
   memory store or get a bridge-scoped one? Leaning persona-scoped
   (same as any nono session) — but verify against the W6 scope model.
