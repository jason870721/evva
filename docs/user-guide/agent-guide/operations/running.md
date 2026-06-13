# Running a swarm

A swarm runs under the **service** — a local daemon that hosts every space and serves the web console.
These are commands the **user** runs in their terminal. If you're an agent helping them, you usually
*tell them* what to run rather than running it yourself (the service may need to live in their own
session).

## The service

```bash
evva service start          # start the daemon on 127.0.0.1:8888
evva service status         # is it running? where's the session token?
evva service stop           # stop it
evva service install-unit   # install an OS service unit so it autostarts (see the user guide)
```

- Each `start` mints a fresh **session token** and prints where it's stored.
- A browser **on the same machine** signs in automatically (loopback bootstrap) — only remote
  browsers and scripts need the token.
- The service binds `127.0.0.1:8888` by default. **Never expose it to a network** without
  `--allow-remote`; with that flag, every endpoint requires the token.

## Register and control a swarm

`<ref>` below is a space **id** or its **name** (the NAME column of `evva swarm ls`).

```bash
cd my-swarm
evva swarm .                       # register ./evva-swarm.yml as a new space — builds + starts it
evva swarm . --name my-team        # …with an explicit name

evva swarm ls                      # list all spaces (running + stopped)
evva swarm run <ref>               # (re)start a stopped space
evva swarm stop <ref>              # stop a space (freeze all members); keep it — run restarts it
evva swarm rm <ref>                # forget a space entirely
evva swarm reset <ref>             # wipe the ledger + clear context, SAME id (fresh start)
evva swarm add <ref> <member>      # hot-load a new member from agents/sub/<member>/
evva swarm send <ref> <member> <text|->   # message a member as the operator (- reads stdin)
evva swarm vacuum <ref> [--days N] [--dry-run]   # run a retention pass now
```

`evva swarm help` prints the full reference.

### What each lifecycle command is for

- **`evva swarm .`** — the everyday command. Run it once to create the space, and again after **any
  manifest or agent-definition edit** to apply the change. Re-registering resets runtime schedules to
  the manifest baseline.
- **`reset`** vs. **`rm`** — `reset` keeps the space id but wipes its history and context (use it after
  changing a member's `profile.yml` model/effort, which is fixed at creation); `rm` forgets it
  entirely.
- **`add`** — hot-load a member you've added to the manifest + created the directory for, without
  resetting the running space.
- **`send`** — the scriptable nudge. Fire-and-forget; an idle member wakes, a busy one folds the
  message into its current run; either way it shows in the web transcript as a `user` message. This is
  the fast persona-iteration loop: send → watch the console → tune the persona → `evva swarm .` →
  send again.

## The web console

The register output prints a URL like `http://127.0.0.1:8888/?space=<id>`. Open it in a browser on the
same machine. The console gives you:

- **Roster (left)** — every member, its role, status, context meter, and daily token spend vs. budget;
  pending ⏰ alarms. Click a member to open its console.
- **Member console (center)** — the member's transcript. Send it a message (to the leader, usually) to
  drive the team.
- **Task board** — tasks moving `pending → running → verifying → completed`. The leader's plan, live.
- **Timeline** — the message flow *between* members (the `send_message` traffic you don't receive as
  the operator).
- **Approval overlay** — in `default`/`accept_edits` modes, pops when a member wants to write files or
  run shell. Click *Allow* or *Always allow* (for the session).
- **Proposals tab** — the worker→leader proposal queue (read-only window).
- **Metrics / Memory** — see [observability.md](observability.md).
- **Skills / schedule editors** — add/remove a member's skills, edit schedules (changes are durable
  but reset by `evva swarm .`).

## A first run, end to end

```bash
evva service start
cd my-swarm && evva swarm .
# open the printed URL, pick the leader, and send it the first goal:
#   "Build a hello-world CLI in this folder, then have it reviewed."
```

Then watch the board move and approve tool calls if you're in `default` mode. A terminal smoke test
without the browser:

```bash
evva swarm send my-swarm lead "status report, please"
```

## See also

- Reading the cost and health data: [observability.md](observability.md).
- Waking the team from another system: [external-events.md](external-events.md).
- When something doesn't work: [troubleshooting.md](troubleshooting.md).
