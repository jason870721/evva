package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/common"
	pubtools "github.com/johnny1110/evva/pkg/tools"
)

// formatSchedule renders a member's timer schedule for list_members. The cron
// and interval forms read differently; a custom wake prompt is quoted when set.
func formatSchedule(sch agentdef.Schedule) string {
	cadence := fmt.Sprintf("every %s", sch.Every)
	if sch.Cron != "" {
		cadence = fmt.Sprintf("cron %q", sch.Cron)
	}
	if p := strings.TrimSpace(sch.Prompt); p != "" {
		return fmt.Sprintf("%s: %q", cadence, p)
	}
	return cadence
}

// newScheduleSet builds the Leader's schedule_set tool: put a member on a
// recurring timer schedule (or replace its existing one). The schedule wakes the
// member on the cron cadence with a <system-reminder> carrying the current time
// and this prompt (RP-7). Leader-only; auto-allowed (team coordination).
func newScheduleSet(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolScheduleSet,
		desc: "Put a worker on a recurring schedule (crontab): it will wake on the cron cadence and run the " +
			"given prompt, even with no new messages — use this for standing duties like periodic patrols or reviews. " +
			"`cron` is a standard 5-field expression (minute hour day-of-month month day-of-week), e.g. \"*/30 * * * *\", " +
			"matched against the system's LOCAL wall clock — " + common.ZoneLabel() + ". " +
			"Each member has at most one schedule; calling this again replaces it. You cannot schedule yourself.",
		schema: `{"type":"object","properties":{` +
			`"member":{"type":"string","description":"Member name to put on a schedule (see list_members)."},` +
			`"cron":{"type":"string","description":"5-field cron expression, e.g. \"0 */2 * * *\" for every two hours."},` +
			`"prompt":{"type":"string","description":"What the member should do each time it fires. Keep it self-contained — it runs with no other context."}` +
			`},"required":["member","cron","prompt"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Member string `json:"member"`
				Cron   string `json:"cron"`
				Prompt string `json:"prompt"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("schedule_set: invalid input: %v", err), nil
			}
			member := strings.TrimSpace(in.Member)
			if member == "" {
				return errf("schedule_set: 'member' is required"), nil
			}
			if bad := guardSchedulable(mc, member); bad != nil {
				return *bad, nil
			}
			sch := agentdef.Schedule{Cron: strings.TrimSpace(in.Cron), Prompt: in.Prompt}
			if err := sch.Validate(); err != nil {
				return errf("schedule_set: %v", err), nil
			}
			if err := mc.Space.SetMemberSchedule(member, sch); err != nil {
				return errf("schedule_set: %v", err), nil
			}
			return okf("Scheduled %s on cron %q (local %s). It will wake on that cadence and run: %s", member, sch.Cron, common.ZoneLabel(), in.Prompt), nil
		},
	}
}

// newScheduleClear builds the Leader's schedule_clear tool: remove a member's
// recurring schedule so it only wakes on messages/tasks again.
func newScheduleClear(mc swarm.MemberContext) pubtools.Tool {
	return &swarmTool{
		name: toolScheduleClear,
		desc: "Remove a worker's recurring schedule (set by schedule_set). The member stops waking on the timer " +
			"and only runs on messages or assigned tasks again. You cannot clear your own schedule.",
		schema: `{"type":"object","properties":{` +
			`"member":{"type":"string","description":"Member name whose schedule to clear (see list_members)."}` +
			`},"required":["member"]}`,
		exec: func(_ context.Context, input json.RawMessage) (pubtools.Result, error) {
			var in struct {
				Member string `json:"member"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return errf("schedule_clear: invalid input: %v", err), nil
			}
			member := strings.TrimSpace(in.Member)
			if member == "" {
				return errf("schedule_clear: 'member' is required"), nil
			}
			if bad := guardSchedulable(mc, member); bad != nil {
				return *bad, nil
			}
			if err := mc.Space.ClearMemberSchedule(member); err != nil {
				return errf("schedule_clear: %v", err), nil
			}
			return okf("Cleared %s's schedule. It now wakes only on messages or tasks.", member), nil
		},
	}
}

// guardSchedulable enforces the two shared preconditions of the schedule tools:
// the target must be a current member, and it must not be the caller itself. The
// self-guard implements "the leader can't reschedule itself" (RP-7 §3.3): a
// member's own cadence is the operator's steering wheel (via the web, RP-8), not
// something the leader can quietly change. Returns a model-visible error to
// return, or nil when the target is OK.
func guardSchedulable(mc swarm.MemberContext, member string) *pubtools.Result {
	if member == mc.Name {
		r := errf("schedule: you cannot change your own schedule — a member's own cadence is set by the operator via the web, not by the leader.")
		return &r
	}
	if ok, names := rosterHas(mc.Space, member); !ok {
		r := errf("schedule: no swarm member named %q. Valid members: %s. Run list_members for exact names.",
			member, strings.Join(names, ", "))
		return &r
	}
	return nil
}
