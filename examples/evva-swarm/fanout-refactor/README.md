# fanout-refactor — dynamic workflow demo

The DWF showcase: the leader plans a **task graph** once, spawns **ephemeral
clones** for width, and the **engine** runs the structure — blocked tasks flip
to running on their own as dependencies complete, clones appear and fold
themselves away, and the leader wakes only to verify.

```
survey ──▶ refactor a.md ──┐
      ├──▶ refactor b.md ──┼──▶ join: consistency check ──▶ leader verifies
      └──▶ refactor c.md ──┘        (verify: leader)
           (verify: auto, one per clone)
```

## Run it

```bash
evva service start
cd examples/evva-swarm/fanout-refactor
# drop a few files to transform into ./work/ first, e.g.:
mkdir -p work && for f in a b c; do printf '# %s\nTODO: old style.\n' "$f" > "work/$f.md"; done
evva swarm . --name fanout-demo
# open the printed URL, then tell the lead (web Composer):
#   "Rewrite every file under work/ into cheerful haiku form, one task per file."
```

Watch the board and roster while it runs:

- the graph lands as one `pending` survey + N `blocked` unit tasks (⛓ badges)
  + one `blocked` join task;
- `refactorer-2`, `refactorer-3`, … appear with the ⧉ clone pill
  (`member_spawn`);
- completing the survey auto-dispatches every unit task at once — no leader
  relay (the `task_dispatched` lines in the stream are the engine talking);
- `verify: auto` unit tasks complete the instant their clone reports
  `task_done`; the join task waits for all of them, then the leader signs off;
- idle clones with finished work retire themselves (`retire: on_complete`).

Reset between runs: `evva swarm reset fanout-demo` (wipes ledger + context,
keeps the registration).
