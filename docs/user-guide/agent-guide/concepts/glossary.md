# Glossary

Terms used across this guide, in one place.

| Term | Meaning |
| --- | --- |
| **Swarm** | A team of long-lived agents collaborating on a shared goal in one directory. evva's multi-agent subsystem (codename *Veronica*). |
| **Space** | One isolated, running swarm: a leader + workers + a private message bus + a per-space ledger. You can run several spaces at once. Identified by an id or a name. |
| **Member** | Any agent in a space — the leader or a worker. Every member name must be unique within the space. |
| **Leader** | The member that owns the task ledger: it plans, delegates, and verifies. Declared under `leader:` in the manifest; its directory is `agents/main/<name>/`. The *only* member that may write task status. |
| **Worker** | A member that carries out assigned tasks and reports back. Declared under `workers:`; its directory is `agents/sub/<name>/`. Cannot write the ledger; can *propose* work. |
| **Operator** | The human (you). Talks to members via the web console; receives members' *output text*, not their internal `send_message` traffic. |
| **Manifest** | `evva-swarm.yml` — the file declaring the team (leader, workers) and space-wide settings. See [../building/manifest.md](../building/manifest.md). |
| **Workdir** | The directory holding the manifest, the `agents/` tree, and (at runtime) `.vero/`. Should be git-tracked/persistent. |
| **Agent definition** | One member's directory: `system_prompt.md` (+ optional `profile.yml`, `tools/*.yml`, `skills/`). |
| **Persona** | The content of `system_prompt.md` — the member's identity and domain judgment. The runtime appends operational scaffolding to it. |
| **Profile** | `profile.yml` — optional per-member overrides: `model`, `effort`, `when_to_use`, `inject_memory`, `advertise_skills`, `schedule`. |
| **Role** | `leader` or `worker`. Determines which collaboration tools and protocol the runtime injects. |
| **Task ledger** | The shared board of tasks and their states (`pending → running → verifying → completed`, plus `suspended`). Single source of truth for work; single-writer (leader). |
| **Task** | One unit of trackable work: a title, a spec, an assignee, and a status. |
| **Proposal** | A worker-raised candidate task (`task_propose`). The leader accepts it (creating a real task) or declines it. The worker's only way to add trackable work without piercing the single-writer rule. |
| **Message** | A direct or broadcast note between members, sent with `send_message`. Stored in the ledger; visible to the operator in the timeline but not delivered to them. |
| **Mailbox** | A member's queue of unread messages. The wake chain drains it; a backlog signals a frozen or stuck member. |
| **Collaboration tools** | The team-coordination tools the runtime injects by role (`task_*`, `send_message`, `list_members`, `proposal_*`, `schedule_*`, `alarm_*`, `skill_publish`). You never list these in a tool file. See [../tools/collaboration-tools.md](../tools/collaboration-tools.md). |
| **Domain tools** | The capability tools you *do* choose for each member (`read`, `write`, `bash`, `web_fetch`, …). Listed in `tools/active.yml` / `tools/deferr.yml`. See [../tools/catalog.md](../tools/catalog.md). |
| **Active tool** | A tool exposed to the model every turn (`tools/active.yml`). |
| **Deferred tool** | A tool advertised by name only; the model must `tool_search` to load its schema before use (`tools/deferr.yml`). Keeps the active set small. |
| **Persona member** | A manifest member that references a registry main-tier persona (`persona:` instead of `agent:`) rather than a workdir directory. See [../building/manifest.md](../building/manifest.md#persona-members). |
| **Permission mode** | The trust stance for file/shell side effects: `default`, `accept_edits`, `plan`, or `bypass`. Space-wide via `settings.permission_mode`, overridable per member. See [../building/permissions.md](../building/permissions.md). |
| **`permissions.json`** | Optional Claude-Code-compatible fine-grained rules next to a member's definition. Deny rules bind in **every** mode, including `bypass`. |
| **Deny fence** | The "bypass + deny rules" pattern: a member runs unattended (`bypass`) but specific dangerous actions are hard-blocked by deny rules. |
| **Token budget** | A per-member daily cap on input+output tokens (`settings.daily_budget_tokens`, overridable per member). A member that crosses it freezes until the day rolls over. |
| **Watchdog** | The set of timers that alert when work is stuck: `stall_threshold` (one hung run), `task_stale_threshold` (a parked task), `mailbox_stale_threshold` (undrained mail). |
| **Schedule** | A recurring wake (`cron` or `every`) attached to a member, with a synthetic prompt injected on each tick. Leader-managed at runtime via `schedule_set`. |
| **Alarm** | A one-shot wake at an absolute time (`alarm_set { at, prompt }`). A worker can wake itself; the leader can wake a teammate. |
| **Skill** | A Markdown instruction file (`SKILL.md`) a member can invoke by name. Per-member (`<member>/skills/`) or space-shared (`agents/skills/`). The leader can publish one at runtime with `skill_publish`. |
| **`.vero/`** | The runtime-owned per-space directory: the SQLite ledger, `events/` forensics (jsonl), and `archive/` retention exports. **Never edit or delete it** — deleting resets the space. |
| **Service** | The local daemon (`127.0.0.1:8888`) that hosts every space and serves the web console. Started with `evva service start`. |
| **Register** | `evva swarm .` — read the manifest, build the team, start the space. Re-run after manifest edits. |
| **Webhook** | An external HTTP entry point (`POST /api/swarm/<ref>/event`) that wakes the team from another system. See [../operations/external-events.md](../operations/external-events.md). |
| **Event log** | The optional jsonl mirror of a space's events under `.vero/events/` (`settings.event_log`), used for cost analysis and forensics. |
| **Retention / vacuum** | The daily pass (`settings.retention_days`) that archives and deletes old read mail and completed tasks; runnable on demand with `evva swarm vacuum`. |
