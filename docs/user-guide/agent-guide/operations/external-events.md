# External events: waking the team from another system

A swarm doesn't only respond to the operator. Other systems — CI, a cron job, a monitoring alert, a
webhook from a SaaS — can wake the team by POSTing an event. The team then assesses it: if it warrants
work, the leader breaks it into tasks; if not, it's noted briefly.

## The endpoint

```
POST http://127.0.0.1:8888/api/swarm/<ref>/event
Content-Type: application/json

{ "body": "Build #4821 failed on main: 3 tests red in payments/." }
```

`<ref>` is the space id or name. The event arrives to a member as a message **from `webhook`** — the
injected protocol tells members to assess a `webhook` message rather than treat it as chatter.

### Request fields

| Field | Required | Meaning |
| --- | --- | --- |
| `body` | ✅ | The event text the member receives. |
| `to` | — | Target member name. Defaults to the **leader**. |
| `source` | — | A label for where the event came from (for the transcript). |
| `title` | — | A short title for the event. |
| `idempotency_key` | — | Collapses retries — repeated POSTs with the same key are treated as one event. Use it whenever the sender might retry. |

## Authentication

Two modes, depending on whether you set `settings.webhook_secret`:

- **No secret set** — only **same-machine (loopback)** callers are accepted; non-loopback POSTs are
  rejected outright. Fine for a local cron or CI runner on the same host.
- **Secret set** — every POST must carry it as the `X-Evva-Webhook-Secret` header. This is what lets a
  remote system wake the team (in combination with `--allow-remote` on the service and the session
  token where required).

```yaml
# evva-swarm.yml
settings:
  webhook_secret: "a-long-random-string"
```

```bash
curl -X POST http://127.0.0.1:8888/api/swarm/my-team/event \
  -H "Content-Type: application/json" \
  -H "X-Evva-Webhook-Secret: a-long-random-string" \
  -d '{"body":"Nightly backup failed on db-2","title":"backup-alert","idempotency_key":"backup-2026-06-13"}'
```

## Designing the member's response

The member that receives a webhook event should know, from its persona, how to triage external
signals — e.g. the leader: "A `webhook` message is an external trigger. If it describes a real problem,
open tasks and dispatch; if it's informational, log it and move on. Don't start a fire drill over a
routine notice." (The runtime injects the *general* "assess webhook messages" rule; the *policy* for
this team's signals is yours to write.)

## Common integrations

- **CI** — POST on build failure so the team triages and proposes a fix.
- **Monitoring/alerting** — POST a fired alert; the leader dispatches an investigation.
- **A cron job on the host** — a loopback POST (no secret needed) to kick off a periodic job that's
  more complex than a single scheduled member.
- **Inbound webhooks from SaaS** — set a secret and `--allow-remote`; collapse retries with
  `idempotency_key`.

## Webhook vs. schedule vs. alarm

| Mechanism | Trigger | Use for |
| --- | --- | --- |
| **Webhook** (this page) | an external system POSTs | event-driven work from outside |
| **Schedule** (`schedule:` / `schedule_set`) | a recurring timer | standing duties on a cadence |
| **Alarm** (`alarm_set`) | a one-shot future instant | "do X once at time T" |

## See also

- Securing the service for remote access: [running.md](running.md#the-service).
- The injected communication protocol that handles `webhook` messages:
  [../concepts/architecture.md](../concepts/architecture.md#authored-vs-auto-injected).
