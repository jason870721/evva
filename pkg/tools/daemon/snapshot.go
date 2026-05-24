package daemon

import "time"

// DaemonSnapshot is the immutable view of one daemon's state at a point in
// time. Stores hand out snapshots by value so observers don't race the
// goroutine holding the live struct.
//
// Metadata carries kind-specific payload; renderers type-assert it on Kind.
// New kinds add a struct implementing DaemonMetadata and place it on the
// snapshot — tools / drain / store stay untouched.
type DaemonSnapshot struct {
	ID          string
	Kind        DaemonKind
	Status      DaemonStatus
	Description string
	AgentID     string // spawning agent's id; used by TUI to label rows by owner
	StartedAt   time.Time
	EndedAt     time.Time // zero until terminal

	Metadata DaemonMetadata
}

// DaemonMetadata is the marker interface every kind-specific payload
// implements. Renderers (TUI strips, daemon_output, daemon_list) switch on
// DaemonSnapshot.Kind and type-assert to the concrete struct.
type DaemonMetadata interface{ daemonMetadata() }

// LocalBashMeta is the payload for KindLocalBash snapshots.
//
// Output is the captured stdout+stderr tail, capped at the size the bash
// goroutine enforces (64 KiB). ExitCode is nil while running; set on the
// terminal Lifecycle.
type LocalBashMeta struct {
	Command  string
	ExitCode *int
	Output   string
}

func (LocalBashMeta) daemonMetadata() {}

// LocalAgentMeta is the payload for KindLocalAgent snapshots.
//
// Phase is the fine-grained sub-state ("thinking", "executing",
// "draining", ...) the child agent broadcasts as it works — orthogonal to
// the coarse DaemonStatus (running / completed / failed / killed). The
// TUI subagent strip renders both: DaemonStatus picks the chip color,
// Phase drives the inline glyph.
//
// Summary is populated on terminal Lifecycle with the child agent's final
// response. Err is populated when the child crashed or was killed.
type LocalAgentMeta struct {
	AgentType string // "general-purpose" / "explore" / "plan" / ...
	Prompt    string
	Async     bool
	Phase     string
	Summary   string
	Err       string
}

func (LocalAgentMeta) daemonMetadata() {}

// MonitorMeta is the payload for KindMonitor snapshots.
//
// RecentLines is a ring buffer tail used by daemon_output; events have
// already been drained into the conversation by the time daemon_output
// reads, so we keep a small in-memory tail rather than asking the agent
// to scroll back.
type MonitorMeta struct {
	Command     string
	EventCount  int
	Persistent  bool
	RecentLines []string
}

func (MonitorMeta) daemonMetadata() {}

// LSPMeta is the payload for KindLSP snapshots.
type LSPMeta struct {
	ServerName   string
	Command      string
	State        string // "running", "starting", "error", etc.
	ExitCode     *int
	RestartCount int
	MaxRestarts  int
}

func (LSPMeta) daemonMetadata() {}
