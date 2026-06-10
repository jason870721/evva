package alarm

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/tools"
)

// Names lists every tool name this package contributes.
func Names() []tools.ToolName {
	return []tools.ToolName{tools.ALARM_CREATE, tools.ALARM_LIST, tools.ALARM_CANCEL}
}

// durableSuffix renders the durability marker shared by create + list output.
func durableSuffix(d bool) string {
	if d {
		return " [durable]"
	}
	return " [session-only]"
}

// --- alarm_create -----------------------------------------------------------

// CreateTool implements ALARM_CREATE. Execute is non-blocking: it arms a
// one-shot timer on the shared Scheduler and returns immediately.
type CreateTool struct{ sched *Scheduler }

// NewCreate constructs an ALARM_CREATE tool bound to the given scheduler — the
// same instance the agent's fire callback (WakeupQueue + wake signal) is wired
// into.
func NewCreate(s *Scheduler) *CreateTool { return &CreateTool{sched: s} }

func (t *CreateTool) Name() string { return string(tools.ALARM_CREATE) }

func (t *CreateTool) Description() string {
	return "Set a one-shot alarm that wakes you at an ABSOLUTE wall-clock instant (second precision), then re-enters the conversation with your prompt as a fresh user message.\n\n" +
		"Use this — not schedule_wakeup — whenever the wake time is a specific date/time, or more than an hour away. schedule_wakeup is a blocking RELATIVE sleep capped at 1 hour; an alarm is non-blocking, fires at an exact timestamp arbitrarily far in the future, and (when durable) survives process restarts.\n\n" +
		"On fire: if you are idle a fresh turn starts automatically; if you are mid-task the prompt lands at the next step. The alarm fires once, then is gone.\n\n" +
		"`at` accepts \"2006-01-02 15:04:05\" (your local timezone — " + common.ZoneLabel() + ") or RFC3339 with an explicit offset; it must be in the future. " +
		"The confirmation echoes the parsed instant with its UTC twin — check it when your intent was a UTC time. " +
		"Write `prompt` as a note-to-self that will still make sense with no surrounding context. `durable` defaults to true."
}

func (t *CreateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["at","prompt"],
		"properties":{
			"at":{"type":"string","description":"Absolute fire time, second precision. \"2006-01-02 15:04:05\" (local tz) or RFC3339 \"2006-01-02T15:04:05Z07:00\". Must be in the future."},
			"prompt":{"type":"string","description":"The prompt injected as a fresh user message when the alarm fires. Write it self-contained — it will make sense with no other context."},
			"label":{"type":"string","description":"Optional short label shown in alarm_list and the fire banner."},
			"durable":{"type":"boolean","description":"true (default) = survive process restarts via on-disk store. false = session-only, lost when this process exits."}
		}
	}`)
}

type createInput struct {
	At      string `json:"at"`
	Prompt  string `json:"prompt"`
	Label   string `json:"label"`
	Durable *bool  `json:"durable"` // pointer so omitted defaults to true
}

func (t *CreateTool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	if t.sched == nil {
		return tools.Result{IsError: true, Content: "alarm_create: no scheduler configured (alarms are root-agent only)"}, nil
	}
	var in createInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("alarm_create: decode: %v", err)}, nil
	}
	if strings.TrimSpace(in.Prompt) == "" {
		return tools.Result{IsError: true, Content: "alarm_create: prompt is required"}, nil
	}
	fireAt, err := ParseFireTime(in.At, time.Local)
	if err != nil {
		return tools.Result{IsError: true, Content: "alarm_create: " + err.Error()}, nil
	}
	durable := true
	if in.Durable != nil {
		durable = *in.Durable
	}
	a, err := t.sched.Arm(Alarm{
		FireAt:  fireAt,
		Prompt:  in.Prompt,
		Label:   strings.TrimSpace(in.Label),
		Durable: durable,
	})
	if err != nil {
		return tools.Result{IsError: true, Content: "alarm_create: " + err.Error()}, nil
	}
	logger.Debug("alarm.armed", "id", a.ID, "fire_at", a.FireAt.Format(time.RFC3339), "durable", a.Durable)
	until := time.Until(a.FireAt).Round(time.Second)
	return tools.Result{
		Content: fmt.Sprintf("alarm %s set for %s (in %s)%s. It fires once and injects your prompt as a fresh user message.",
			a.ID, common.StampWithUTC(a.FireAt), until, durableSuffix(durable)),
		Metadata: a,
	}, nil
}

// --- alarm_list -------------------------------------------------------------

// ListTool implements ALARM_LIST.
type ListTool struct{ sched *Scheduler }

// NewList constructs an ALARM_LIST tool bound to the given scheduler.
func NewList(s *Scheduler) *ListTool { return &ListTool{sched: s} }

func (t *ListTool) Name() string { return string(tools.ALARM_LIST) }

func (t *ListTool) Description() string {
	return "List all pending alarms set via alarm_create — id, fire time, time remaining, label, durability, and prompt. Fired and cancelled alarms are not shown."
}

func (t *ListTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
}

func (t *ListTool) Execute(_ context.Context, _ *slog.Logger, _ json.RawMessage) (tools.Result, error) {
	if t.sched == nil {
		return tools.Result{IsError: true, Content: "alarm_list: no scheduler configured"}, nil
	}
	alarms := t.sched.List()
	if len(alarms) == 0 {
		return tools.Result{Content: "No pending alarms."}, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d pending alarm(s):\n", len(alarms))
	now := time.Now()
	for _, a := range alarms {
		label := ""
		if a.Label != "" {
			label = " [" + a.Label + "]"
		}
		fmt.Fprintf(&b, "- %s%s — %s (in %s)%s\n    %s\n",
			a.ID, label, common.Stamp(a.FireAt),
			a.FireAt.Sub(now).Round(time.Second), durableSuffix(a.Durable), truncate(a.Prompt, 120))
	}
	return tools.Result{Content: strings.TrimRight(b.String(), "\n"), Metadata: alarms}, nil
}

// --- alarm_cancel -----------------------------------------------------------

// CancelTool implements ALARM_CANCEL.
type CancelTool struct{ sched *Scheduler }

// NewCancel constructs an ALARM_CANCEL tool bound to the given scheduler.
func NewCancel(s *Scheduler) *CancelTool { return &CancelTool{sched: s} }

func (t *CancelTool) Name() string { return string(tools.ALARM_CANCEL) }

func (t *CancelTool) Description() string {
	return "Cancel a pending alarm by id (from alarm_create or alarm_list) so it never fires."
}

func (t *CancelTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"additionalProperties":false,
		"required":["id"],
		"properties":{"id":{"type":"string","description":"Alarm id returned by alarm_create / shown by alarm_list."}}
	}`)
}

func (t *CancelTool) Execute(_ context.Context, logger *slog.Logger, input json.RawMessage) (tools.Result, error) {
	if t.sched == nil {
		return tools.Result{IsError: true, Content: "alarm_cancel: no scheduler configured"}, nil
	}
	var in struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return tools.Result{IsError: true, Content: fmt.Sprintf("alarm_cancel: decode: %v", err)}, nil
	}
	if strings.TrimSpace(in.ID) == "" {
		return tools.Result{IsError: true, Content: "alarm_cancel: id is required"}, nil
	}
	if t.sched.Cancel(in.ID) {
		logger.Debug("alarm.cancelled", "id", in.ID)
		return tools.Result{Content: fmt.Sprintf("alarm %s cancelled.", in.ID)}, nil
	}
	return tools.Result{IsError: true, Content: fmt.Sprintf("alarm_cancel: no pending alarm with id %q", in.ID)}, nil
}

// truncate clips s to n runes, appending an ellipsis when shortened.
func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
