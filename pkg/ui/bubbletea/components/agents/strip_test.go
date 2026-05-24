package agents

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/johnny1110/evva/pkg/ui/bubbletea/theme"
	"github.com/johnny1110/evva/pkg/tools/daemon"
)

func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// stripANSI — local helper for assertion-only use.
func stripANSI(s string) string {
	var b strings.Builder
	skip := false
	for _, r := range s {
		if r == 0x1b {
			skip = true
			continue
		}
		if skip {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '\x07' {
				skip = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// agentFixture stands in for a real agentDaemon in strip render tests.
// All accessors return immutable values so the strip can render without
// touching the daemon's run loop.
type agentFixture struct {
	snap daemon.DaemonSnapshot
}

func (f *agentFixture) Snapshot() daemon.DaemonSnapshot { return f.snap }
func (f *agentFixture) Kill(_ context.Context) error    { return nil }
func (f *agentFixture) Output() string                  { return "" }

// newStateWithAgents materialises a DaemonState and seeds it with
// local_agent snapshots so Render can pull them via SnapshotByKind.
func newStateWithAgents(t *testing.T, snaps []daemon.DaemonSnapshot) *daemon.DaemonState {
	t.Helper()
	state := daemon.NewState(func() {})
	for _, s := range snaps {
		state.Register(&agentFixture{snap: s})
	}
	return state
}

// agentSnap builds a local_agent DaemonSnapshot fixture with the given
// description, async marker, daemon status, and Phase.
func agentSnap(id, name, phase string, status daemon.DaemonStatus, async bool) daemon.DaemonSnapshot {
	return daemon.DaemonSnapshot{
		ID:          id,
		Kind:        daemon.KindLocalAgent,
		Status:      status,
		Description: name,
		StartedAt:   time.Now(),
		Metadata: daemon.LocalAgentMeta{
			AgentType: "general-purpose",
			Async:     async,
			Phase:     phase,
		},
	}
}

func TestRenderEmpty(t *testing.T) {
	// The App passes a nil DaemonState when no daemon has registered yet.
	if got := Render(nil, 80, theme.Default(), 0); got != "" {
		t.Errorf("nil state should render empty, got %q", got)
	}
}

func TestRenderSingleChip(t *testing.T) {
	state := newStateWithAgents(t, []daemon.DaemonSnapshot{
		agentSnap("ag-1", "explorer", "thinking", daemon.StatusRunning, false),
	})
	out := Render(state, 80, theme.Default(), 0)
	plain := stripANSI(out)
	if !strings.Contains(plain, "explorer") {
		t.Errorf("chip should include agent name: %q", plain)
	}
	if !strings.Contains(plain, "‹") || !strings.Contains(plain, "›") {
		t.Errorf("chip should be bracketed with chevrons: %q", plain)
	}
}

func TestRenderAsyncMarker(t *testing.T) {
	state := newStateWithAgents(t, []daemon.DaemonSnapshot{
		agentSnap("ag-1", "bg-job", "executing", daemon.StatusRunning, true),
	})
	out := stripANSI(Render(state, 80, theme.Default(), 0))
	if !strings.Contains(out, "ᵃ") {
		t.Errorf("async chip should include 'ᵃ' marker: %q", out)
	}
}

func TestRenderTruncatesLongName(t *testing.T) {
	long := strings.Repeat("a", 50)
	state := newStateWithAgents(t, []daemon.DaemonSnapshot{
		agentSnap("ag-1", long, "thinking", daemon.StatusRunning, false),
	})
	out := stripANSI(Render(state, 80, theme.Default(), 0))
	if strings.Contains(out, long) {
		t.Errorf("long name should be truncated, got: %q", out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("truncated name should end with '…': %q", out)
	}
}

func TestRenderWrapsToMultipleLines(t *testing.T) {
	// Six chips at ~16 cols easily overflow 30.
	now := time.Now()
	snaps := []daemon.DaemonSnapshot{
		agentSnap("a", "one", "thinking", daemon.StatusRunning, false),
		agentSnap("b", "two", "executing", daemon.StatusRunning, false),
		agentSnap("c", "three", "draining", daemon.StatusRunning, false),
		agentSnap("d", "four", "ready_report", daemon.StatusCompleted, false),
		agentSnap("e", "five", "crushed", daemon.StatusFailed, false),
		agentSnap("f", "six", "init", daemon.StatusRunning, false),
	}
	// Strip groups by StartedAt; spread them so order is stable.
	for i := range snaps {
		snaps[i].StartedAt = now.Add(time.Duration(i) * time.Millisecond)
	}
	state := newStateWithAgents(t, snaps)
	out := Render(state, 30, theme.Default(), 0)
	if strings.Count(out, "\n") < 1 {
		t.Errorf("expected at least one line wrap at width 30, got:\n%q", out)
	}
}

func TestRenderSpinnerFrameAdvances(t *testing.T) {
	state := newStateWithAgents(t, []daemon.DaemonSnapshot{
		agentSnap("ag-1", "spin", "thinking", daemon.StatusRunning, false), // active → animates
	})
	frame0 := Render(state, 80, theme.Default(), 0)
	frame3 := Render(state, 80, theme.Default(), 3)
	if frame0 == frame3 {
		t.Errorf("frame change should alter animated chip: frame0=%q frame3=%q", frame0, frame3)
	}
}
