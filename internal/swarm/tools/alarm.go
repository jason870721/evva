package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/common"
	pubtools "github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/tools/alarm"
)

// newAlarmSet builds the per-agent alarm_set tool: arm a ONE-SHOT alarm that
// fires at an absolute wall-clock instant and wakes a member with a prompt,
// delivered as a durable message. A worker may only alarm itself; the leader
// may also target another member (the cross-member authority mirrors the
// schedule tools). Distinct from schedule_set, which is recurring cron.
func newAlarmSet(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolAlarmSet,
		desc: "Set a ONE-SHOT alarm that fires at an absolute date/time (second precision) and wakes a member with a prompt, " +
			"delivered as a durable message. Omit `member` to wake yourself; only the leader may target another member. " +
			"Use this for a specific instant or a one-off follow-up (\"re-check the testnet run at 2026-09-11 12:31:50\") — " +
			"for a RECURRING cadence use schedule_set instead. The alarm fires once, then is gone. " +
			"A time without an explicit offset is LOCAL system time — " + common.ZoneLabel() + "; " +
			"the confirmation echoes the parsed instant with its UTC twin, so check it when your intent was a UTC time.",
		schema: `{"type":"object","properties":{` +
			`"at":{"type":"string","description":"Absolute fire time, second precision: \"2006-01-02 15:04:05\" (LOCAL system time) or RFC3339 \"2006-01-02T15:04:05Z07:00\" with an explicit offset. Must be in the future."},` +
			`"prompt":{"type":"string","description":"What the woken member should do. Keep it self-contained — it arrives with no other context."},` +
			`"member":{"type":"string","description":"Member to wake (see list_members). Omit to wake yourself. Only the leader may target someone else."},` +
			`"label":{"type":"string","description":"Optional short label shown in the fire banner and list_members."}` +
			`},"required":["at","prompt"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				At     string `json:"at"`
				Prompt string `json:"prompt"`
				Member string `json:"member"`
				Label  string `json:"label"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("alarm_set: invalid input: %v", err), nil
			}
			if strings.TrimSpace(in.Prompt) == "" {
				return errf("alarm_set: 'prompt' is required"), nil
			}
			fireAt, err := alarm.ParseFireTime(in.At, time.Local)
			if err != nil {
				return errf("alarm_set: %v", err), nil
			}

			// Resolve the target: empty => self; otherwise validate membership and
			// gate cross-member targeting to the leader.
			target := mc.Name
			if m := strings.TrimSpace(in.Member); m != "" {
				target = mc.Space.Roster.ResolveRecipient(m)
				if target != mc.Name {
					if mc.Role != agentdef.RoleLeader {
						return errf("alarm_set: only the leader may set an alarm for another member; omit 'member' to set one for yourself."), nil
					}
					if ok, names := rosterHas(mc.Space, target); !ok {
						return errf("alarm_set: no swarm member named %q. Valid members: %s. Run list_members for exact names.",
							in.Member, strings.Join(names, ", ")), nil
					}
				}
			}

			a, err := mc.Space.AlarmScheduler().Arm(alarm.Alarm{
				FireAt:  fireAt,
				Prompt:  in.Prompt,
				Label:   strings.TrimSpace(in.Label),
				Target:  target,
				Origin:  mc.Name,
				Durable: true,
			})
			if err != nil {
				return errf("alarm_set: %v", err), nil
			}
			who := "you"
			if target != mc.Name {
				who = target
			}
			return okf("Alarm %s set for %s. It will fire once and wake %s with: %s",
				a.ID, common.StampWithUTC(fireAt), who, in.Prompt), nil
		},
	}
}

// newAlarmClear builds the per-agent alarm_clear tool: cancel a pending alarm by
// id before it fires. A member may clear an alarm it set or one targeting it;
// the leader may clear any.
func newAlarmClear(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolAlarmClear,
		desc: "Cancel a pending one-shot alarm by id (from alarm_set, or the ⏰ entries in list_members) so it never fires. " +
			"You can clear an alarm you set or one aimed at you; the leader can clear any.",
		schema: `{"type":"object","properties":{` +
			`"id":{"type":"string","description":"Alarm id (e.g. alm_xxxxxxxx) from alarm_set or list_members."}` +
			`},"required":["id"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("alarm_clear: invalid input: %v", err), nil
			}
			id := strings.TrimSpace(in.ID)
			if id == "" {
				return errf("alarm_clear: 'id' is required"), nil
			}

			// Authorize against the live pending set: a member may only clear its
			// own alarms (set by it or aimed at it) unless it is the leader.
			var found *alarm.Alarm
			for _, a := range mc.Space.AlarmScheduler().List() {
				if a.ID == id {
					a := a
					found = &a
					break
				}
			}
			if found == nil {
				return errf("alarm_clear: no pending alarm with id %q", id), nil
			}
			if mc.Role != agentdef.RoleLeader && found.Origin != mc.Name && found.Target != mc.Name {
				return errf("alarm_clear: alarm %q is not yours to clear.", id), nil
			}
			if mc.Space.AlarmScheduler().Cancel(id) {
				return okf("Alarm %s cancelled.", id), nil
			}
			return errf("alarm_clear: no pending alarm with id %q", id), nil
		},
	}
}
