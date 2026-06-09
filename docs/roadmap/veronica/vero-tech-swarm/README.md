# vero-tech-swarm — evva's engineering team

A ready-to-run **7-member software engineering team** that takes a real
development request and ships a working, verified result. Unlike the minimal
[`example-swarm/`](../example-swarm/) (a 3-member demo), this is a full delivery
team with professional, role-specific system prompts and toolsets — the reference
for "what a serious swarm looks like".

> Guides: [English](../user-guide-en.md) ｜ [中文](../user-guide-zh.md)

```
vero-tech-swarm/
├── evva-swarm.yml                 # the team manifest
└── agents/
    ├── main/lead/                 # Team Lead — plans, delegates, verifies, reports
    └── sub/
        ├── pm/                    # Product Manager — PRD + acceptance criteria
        ├── designer/              # Product Designer — UX flows + UI/visual spec
        ├── backend-a/             # Backend Engineer
        ├── backend-b/             # Backend Engineer (second parallel track)
        ├── frontend/              # Frontend Engineer
        └── qa/                    # QA Engineer — verifies, tests, files bugs
```

## The team & how it flows

The lead orchestrates a standard delivery pipeline; members talk directly to each
other over the swarm's message bus, so tracks run in parallel where they can.

```
        ┌──────────────────────────── lead ────────────────────────────┐
        │  frames the goal · decomposes · assigns · verifies · reports  │
        └───────────────────────────────────────────────────────────────┘
            │            │                  │                      │
            ▼            ▼                  ▼                      ▼
           pm  ──────▶ designer ──────▶ backend-a │ backend-b ──▶ qa
        (PRD + AC)   (UX + UI spec)      frontend  (build)     (verify vs AC)
```

- **`pm`** turns the request into a lean PRD with **testable acceptance criteria**.
- **`designer`** turns the PRD into a concrete, implementable UI/UX spec.
- **`backend-a` + `backend-b`** build the server side in parallel (split by slice,
  coordinate on the shared contract); **`frontend`** builds the UI from the spec.
- **`qa`** verifies the result against the PRD's acceptance criteria, writes/runs
  tests, and files reproducible bug reports; the lead routes rework until it's
  shippable.

## Run it

```sh
# 1. Start the host (prints a session token).
evva service start

# 2. Copy this folder somewhere it can safely create a project, then enter it.
#    (Replace <evva-repo> with wherever you have evva checked out.)
cp -r <evva-repo>/docs/veronica/vero-tech-swarm ~/vero-tech
cd ~/vero-tech

# 3. Register the swarm into the running service.
evva swarm .
#    → registered space <id>
#        open: http://127.0.0.1:8888/?space=<id>
```

Open `http://127.0.0.1:8888`, paste the token from step 1, and enter the
**vero-tech-swarm** space.

> No model is pinned in the profiles, so every member runs against **whatever LLM
> provider you've configured** for evva. All members run at `effort: high` — this
> is a senior team; expect deeper (and pricier) reasoning than the minimal demo.

## Try this

In the **Member Console** (focused on the `lead` by default), send a real
request — the lead will run the pipeline end-to-end:

> Build a small **task-tracker web app** under `./workspace`: a REST backend (in
> whatever stack you think fits) with `tasks` having title/status/due-date, and a
> clean single-page frontend to list, add, complete, and filter tasks. Have the PM
> spec it, the designer define the UI, the backend and frontend build it, and QA
> verify it before you sign off.

Now watch the team work:

- the **Team Board** moves cards `pending → running → verifying → completed`,
- click **`pm`** to watch it write the PRD, then **`designer`**, **`backend-a`**,
  **`frontend`**, … each in its own console, live;
- **`qa`** reads the result, runs/writes tests, and reports issues back to the
  **`lead`**, who routes rework and finally summarises for you.

Per-agent **colors** (the roster dot, and the mailbox `● sender → ● recipient`
lines) make it easy to follow who is handing what to whom.

## Permissions

This example ships `permission_mode: bypass` so the team runs **hands-off** —
engineers write files and run shell/tests with no per-action approval — which is
what lets a full build→QA cycle complete on its own. Run it in a **project folder
you trust** (the agents have a real shell).

Prefer to stay in the loop? Edit `evva-swarm.yml`:

- `default` — file writes / shell pop an **approval overlay** in the web UI; click
  **Allow**, or **Always allow** to allow that tool for the rest of the session.
- `accept_edits` — file writes auto-allow; shell still asks.

The lead's `task_create` / `task_assign` / `task_verify` never gate, either way.

## Talk to anyone (flat comms)

Click **any** member in the roster to focus its console and message it directly —
ask `backend-a` _"what endpoints have you built so far?"_, or tell `designer`
_"make the empty state friendlier."_ Your message rides the same bus the team
uses, so it reaches that member **without disrupting** the ongoing task flow.

## Reset / clean up

```sh
evva swarm ls                              # find the space id
evva swarm stop <id>                       # stop this swarm
rm -rf ~/vero-tech/.vero ~/vero-tech/workspace   # wipe its db + generated project
```

> Note: stopping a swarm does **not** delete its transcripts (`~/.evva/sessions/`)
> or its `.vero` ledger — re-registering the same folder resumes where it left
> off. Delete those (above) for a truly fresh start, or run from a new folder.

## How it's wired (peek inside)

- `evva-swarm.yml` — names the team, points at the agent folders, and sets the
  space-wide `permission_mode` / `max_iterations`.
- `agents/main/lead/` — the leader (under `main/`); its `tools/active.yml` grants
  only **read + research** tools, because it verifies and delegates rather than
  writes.
- `agents/sub/<member>/` — the workers (under `sub/`); engineers get the full
  `read`/`write`/`edit`/`bash`/… toolset, while `pm` and `designer` get
  write-without-shell (they produce specs, not running code).

> **Notice what the prompts *don't* say.** Each `system_prompt.md` describes only
> the member's **persona, expertise, standards, and collaboration intent** — none
> of them explain the task ledger, when to message, or how to report. That **swarm
> collaboration protocol is injected automatically** based on role (leader vs
> worker), and so are the collaboration **tools** (`task_create`, `task_assign`,
> `task_verify`, `send_message`, `list_members`, `my_tasks`, `task_get`). You write
> *who each agent is and how it should collaborate*; the swarm teaches it *how the
> machinery works*. `active.yml` is only for the regular evva tools.
