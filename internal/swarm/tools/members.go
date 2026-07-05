package tools

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/johnny1110/evva/internal/swarm"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// spawnMaxCount caps one member_spawn call. Wider fan-outs are still possible
// (call it again) — the cap keeps a single tool call from claiming half the
// roster ceiling in one stroke, and max_members rules the total regardless.
const spawnMaxCount = 8

// newMemberSpawn is the Leader's fan-out lever (DWF): clone an existing
// worker N times under derived names ("<base>-2", "-3", …). Clones inherit
// the base's definition — prompt, tools, model/effort pins, permission
// stance, budget cap — but not its schedule; each is a full member with its
// own mailbox and memory dir. settings.max_members bounds the live roster.
func newMemberSpawn(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolMemberSpawn,
		desc: "Spawn ephemeral worker clones of an existing worker for fan-out (e.g. 5 files to refactor in " +
			"parallel → 5 clones). Clones inherit the base's definition and budget; with retire:'on_complete' " +
			"(default) each retires itself once its assigned tasks complete and it is idle. Create tasks for " +
			"the returned names. Only the Leader spawns; settings.max_members caps the live roster.",
		schema: `{"type":"object","properties":{` +
			`"from":{"type":"string","description":"Existing WORKER to clone (see list_members). Clones cannot be cloned."},` +
			`"count":{"type":"integer","description":"How many clones (default 1, max 8 per call)."},` +
			`"retire":{"type":"string","enum":["on_complete","manual"],"description":"'on_complete' (default): auto-retire when its tasks are done and it is idle. 'manual': lives until member_retire."}` +
			`},"required":["from"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				From   string `json:"from"`
				Count  int    `json:"count"`
				Retire string `json:"retire"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("member_spawn: invalid input: %v", err), nil
			}
			if strings.TrimSpace(in.From) == "" {
				return errf("member_spawn: 'from' is required — the worker to clone (see list_members)"), nil
			}
			count := in.Count
			if count <= 0 {
				count = 1
			}
			if count > spawnMaxCount {
				return errf("member_spawn: count %d exceeds the per-call max %d — spawn in batches if you truly need more", count, spawnMaxCount), nil
			}
			names := make([]string, 0, count)
			for range count {
				name, err := mc.Space.SpawnWorker(in.From, in.Retire)
				if err != nil {
					if len(names) > 0 {
						return errf("member_spawn: spawned %s, then failed: %v", strings.Join(names, ", "), err), nil
					}
					return errf("member_spawn: %v", err), nil
				}
				names = append(names, name)
			}
			return okf("Spawned %s (from %s, retire: %s). Create tasks for them — they wake on assignment like any member.",
				strings.Join(names, ", "), in.From, retireOrDefault(in.Retire)), nil
		},
	}
}

func retireOrDefault(r string) string {
	if r == "" {
		return swarm.RetireOnComplete
	}
	return r
}

// newMemberRetire retires one SPAWNED member by hand — the escape hatch for
// retire:'manual' clones and for cutting a fan-out short. Manifest members
// are refused (their membership is the operator's contract), as is a clone
// mid-run (never chop work).
func newMemberRetire(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolMemberRetire,
		desc: "Retire a SPAWNED worker clone (see member_spawn). Refused for manifest members and for a clone " +
			"that is mid-run — retry when it is idle. on_complete clones normally retire themselves; use this " +
			"for retire:'manual' clones or to cut a fan-out short. Only the Leader retires.",
		schema: `{"type":"object","properties":{` +
			`"name":{"type":"string","description":"The spawned member to retire."}` +
			`},"required":["name"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("member_retire: invalid input: %v", err), nil
			}
			if err := mc.Space.RetireWorker(in.Name); err != nil {
				return errf("member_retire: %v", err), nil
			}
			return okf("Member %s retired. Its transcripts and ledger history remain.", in.Name), nil
		},
	}
}
