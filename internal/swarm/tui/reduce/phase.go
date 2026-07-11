package reduce

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/johnny1110/evva/internal/swarm/tui/wire"
	"github.com/johnny1110/evva/pkg/event"
)

// thinkingPhases collapse to one prominent "thinking" label so the operator
// sees "the model is working" instead of a flicker — events.ts
// THINKING_PHASES.
var thinkingPhases = []string{"running", "thinking", "texting"}

// PhaseFor maps one wire event to the fine run sub-phase it implies — the Go
// twin of events.ts phaseFor (itself the JS twin of the server-side
// phaseDeriver; three implementations, one vocabulary, keep in lockstep).
// ok=false when the event implies no phase change.
func PhaseFor(ev *wire.Event) (phase, tool string, ok bool) {
	if ev == nil {
		return "", "", false
	}
	switch ev.Kind {
	case event.KindRunStart, event.KindRunResume, event.KindTurnStart,
		event.KindTurnEnd, event.KindToolUseResult, event.KindCompactingEnd:
		return "running", "", true
	case event.KindRunEnd, event.KindIdle, event.KindRunCancelled:
		return "ready", "", true
	case event.KindThinking, event.KindThinkingChunk:
		return "thinking", "", true
	case event.KindText, event.KindTextChunk:
		return "texting", "", true
	case event.KindToolUseStart:
		if p := ev.ToolUseStart; p != nil {
			tool = p.Name
		}
		return "executing", tool, true
	case event.KindApprovalNeeded:
		if p := ev.ApprovalNeeded; p != nil {
			tool = p.ToolName
		}
		return "waiting-approval", tool, true
	case event.KindQuestionNeeded:
		return "waiting-input", "", true
	case event.KindDrainInbox, event.KindDrainBackgroundTask, event.KindDrainMonitorEvents:
		return "draining", "", true
	case event.KindCompacting:
		return "compacting", "", true
	case event.KindIterLimit:
		return "paused", "", true
	case event.KindError:
		return "error", "", true
	default:
		return "", "", false
	}
}

// LivePhase is one agent's event-derived fine phase; Since restamps only when
// the PHASE changes (a tool-only change keeps the clock — roster.go setPhase).
type LivePhase struct {
	Phase string
	Tool  string
	Since time.Time
}

// PhaseMap is the per-agent live-phase overlay keyed by agent id.
type PhaseMap map[string]LivePhase

// ReducePhase folds one event into the map — events.ts reducePhase, mutating
// in place (the TUI has no reactivity contract to honor).
func ReducePhase(m PhaseMap, ev *wire.Event, now time.Time) {
	if ev == nil || ev.AgentID == "" {
		return
	}
	phase, tool, ok := PhaseFor(ev)
	if !ok {
		return
	}
	cur, exists := m[ev.AgentID]
	if exists && cur.Phase == phase && cur.Tool == tool {
		return
	}
	since := now
	if exists && cur.Phase == phase {
		since = cur.Since
	}
	m[ev.AgentID] = LivePhase{Phase: phase, Tool: tool, Since: since}
}

// Member is the roster shape the phase/attention helpers read — the subset of
// webapi.MemberInfo they need, with the live-phase overlay already applied
// (events.ts PhaseLike + RosterSortable).
type Member struct {
	Name       string
	AgentID    string
	Role       string
	Run        string
	Membership string
	Phase      string
	Tool       string
	PhaseSince time.Time
}

// DisplayPhase composes coarse run + fine event-derived phase into one label
// — events.ts displayPhase / swarm.MemberView.DisplayPhase (RP-3).
func DisplayPhase(m Member) string {
	if m.Run == "suspended" {
		return "suspended"
	}
	if m.Phase == "" {
		return m.Run
	}
	if slices.Contains(thinkingPhases, m.Phase) {
		return "thinking"
	}
	if m.Tool != "" {
		return m.Phase + ":" + m.Tool
	}
	return m.Phase
}

// AttentionKind classifies whether a member needs the operator now (RP-4):
// "act" = blocked on a human; "warn" = errored/paused; "" = fine.
func AttentionKind(m Member) string {
	switch m.Phase {
	case "waiting-approval", "waiting-input":
		return "act"
	case "error", "paused":
		return "warn"
	}
	return ""
}

// Attention stall thresholds — events.ts AttentionOpts defaults.
const (
	StallExec  = 5 * time.Minute
	StallThink = 3 * time.Minute
)

// AttentionItem is one "needs you" row, events.ts AttentionItem.
type AttentionItem struct {
	Name    string
	Kind    string // act | warn
	Phase   string
	Tool    string
	Since   time.Time
	Stalled bool
}

// AttentionItems distills the roster into what to act on, most-urgent first:
// act (blocked on a human) before warn (errored / paused / stalled), then
// longest-waiting first. A stall is an executing/thinking phase older than
// its threshold — surfaced as warn with Stalled=true.
func AttentionItems(roster []Member, now time.Time) []AttentionItem {
	var items []AttentionItem
	for _, m := range roster {
		kind := AttentionKind(m)
		stalled := false
		if kind == "" && !m.PhaseSince.IsZero() {
			age := now.Sub(m.PhaseSince)
			if m.Phase == "executing" && age > StallExec {
				kind, stalled = "warn", true
			} else if slices.Contains(thinkingPhases, m.Phase) && age > StallThink {
				kind, stalled = "warn", true
			}
		}
		if kind == "" {
			continue
		}
		items = append(items, AttentionItem{
			Name: m.Name, Kind: kind, Phase: m.Phase, Tool: m.Tool,
			Since: m.PhaseSince, Stalled: stalled,
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Kind != items[j].Kind {
			return items[i].Kind == "act"
		}
		return items[i].Since.Before(items[j].Since)
	})
	return items
}

// OrderRoster sorts members for the roster pane so a long roster never buries
// an active worker: leader first, then needs-attention (in attentionOrder's
// urgency order), then busy → idle → suspended → frozen, alphabetical within
// a tier — events.ts orderRoster. Pure: returns a new slice.
func OrderRoster(members []Member, attentionOrder []string) []Member {
	attIdx := make(map[string]int, len(attentionOrder))
	for i, n := range attentionOrder {
		if _, dup := attIdx[n]; !dup {
			attIdx[n] = i
		}
	}
	rank := func(m Member) int {
		if m.Role == "leader" {
			return 0
		}
		if _, ok := attIdx[m.Name]; ok {
			return 1
		}
		if m.Membership == "frozen" {
			return 5
		}
		switch m.Run {
		case "busy":
			return 2
		case "suspended":
			return 4
		}
		return 3
	}
	out := make([]Member, len(members))
	copy(out, members)
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := rank(out[i]), rank(out[j])
		if ri != rj {
			return ri < rj
		}
		if ri == 1 {
			return attIdx[out[i].Name] < attIdx[out[j].Name]
		}
		return strings.Compare(out[i].Name, out[j].Name) < 0
	})
	return out
}

// Elapsed formats now−since as a compact clock — events.ts elapsed.
func Elapsed(since, now time.Time) string {
	if since.IsZero() {
		return ""
	}
	s := max(int(now.Sub(since).Seconds()), 0)
	if s < 60 {
		return fmt.Sprintf("%ds", s)
	}
	m := s / 60
	if m < 60 {
		return fmt.Sprintf("%d:%02d", m, s%60)
	}
	return fmt.Sprintf("%d:%02d:%02d", m/60, m%60, s%60)
}
