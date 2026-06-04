# Example swarm — a demo web team

A ready-to-run **3-member swarm** you can spin up in a couple of minutes to feel
how a swarm collaborates: a **lead** that plans/assigns/verifies, a **builder**
that writes code, and a **reviewer** that QAs the result.

> Guides: [English](../user-guide-en.md) ｜ [中文](../user-guide-zh.md)

```
example-swarm/
├── evva-swarm.yml                 # the team manifest
└── agents/
    ├── main/lead/                 # leader: plans, assigns, verifies
    └── sub/
        ├── builder/               # worker: writes the files
        └── reviewer/              # worker: reviews the output
```

## Run it

```sh
# 1. Start the host (prints a session token).
evva service start

# 2. Copy this folder somewhere it can safely write files, then enter it.
#    (Replace <evva-repo> with wherever you have evva checked out.)
cp -r <evva-repo>/docs/veronica/example-swarm ~/demo-swarm
cd ~/demo-swarm

# 3. Register the swarm into the running service.
evva swarm .
#    → registered space <id>
#        open: http://127.0.0.1:8888/?space=<id>
```

Open `http://127.0.0.1:8888`, paste the token from step 1, and enter the
**demo-web-team** space.

> No model is pinned in the profiles, so the team runs against **whatever LLM
> provider you've configured** for evva.

## Try this

In the **Member Console** (focused on the lead by default), send:

> Build a small one-page landing site for a fictional coffee shop called
> **"Bean There"** under `./site` (an `index.html` + a `style.css`). Assign the
> build to the builder, then have the reviewer check it before you approve.

Now watch the swarm work:

- the **Team Board** moves cards `pending → running → verifying → completed`,
- click **builder** in the roster to watch its tool calls live as it writes
  files under `./site/`,
- the **reviewer** reads the output and reports back,
- the **lead** verifies and completes, then summarises for you.

When it finishes, open `~/demo-swarm/site/index.html` in a browser.

### Approvals

In `permission_mode: default`, file writes and shell commands pop up an
**approval overlay** in the web UI — click **Allow** to let the builder proceed.
Want it fully hands-off? Set `permission_mode: bypass` in `evva-swarm.yml`
(fine for a throwaway demo folder; see the security notes in the guide).

## Talk to anyone (flat comms)

Click **any** member in the roster to focus its console and message it directly
— e.g. ask the **builder** _"what are you working on right now?"_ or tell the
**reviewer** _"also check that it looks OK at mobile width."_ Your message rides
the swarm's message bus, so it reaches that member **without disrupting** the
ongoing task flow between the lead and the team.

## Reset / clean up

```sh
evva swarm ls                 # find the space id
evva swarm stop <id>          # stop this swarm
rm -rf ~/demo-swarm/.vero ~/demo-swarm/site   # wipe its db + generated output
```

## How it's wired (peek inside)

- `evva-swarm.yml` — names the team and points at the agent folders.
- `agents/main/lead/` — the leader (under `main/`); its `tools/active.yml` only
  grants read tools, because it verifies rather than writes.
- `agents/sub/builder/`, `agents/sub/reviewer/` — workers (under `sub/`); the
  builder gets `write`/`edit`/`bash`, the reviewer is read-only.

> **Notice what the prompts *don't* say.** The `system_prompt.md` files only
> describe each member's persona — none of them explain the task ledger, when to
> message, or how to report. That **swarm collaboration protocol is injected
> automatically** based on role (leader vs worker), and so are the collaboration
> **tools** (`task_create`, `task_assign`, `task_verify`, `send_message`,
> `list_members`, `my_tasks`, `task_get`). You write *who the agent is and when to
> collaborate*; the swarm teaches it *how*. `active.yml` is only for the regular
> evva tools (`read`, `write`, `bash`, …).
