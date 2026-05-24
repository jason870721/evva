// Package daemon is the unified abstraction over every long-running
// background unit the agent can spawn — bash run_in_background tasks, async
// subagents, monitor streams, and future kinds (remote_agent,
// in_process_teammate, local_workflow, dream). It replaces the per-kind
// stores (BgTaskStore, MonitorTaskStore, SpawnGroup) and the per-kind tools
// (task_list / task_stop / task_output) with one DaemonState + one Daemon
// interface + three daemon_* tools.
//
// See docs/design/daemon-design.md for the full RFC.
package daemon

import (
	"crypto/rand"
)

// DaemonKind identifies the variant of one daemon. Used for ID prefix
// allocation, filtering in daemon_list, and rendering hints in the TUI.
//
// New kinds plug in by adding one constant here, one metadata struct in
// snapshot.go, and one file implementing Daemon. Tools / drain / store
// do not change.
type DaemonKind string

const (
	// Implemented today.
	KindLocalBash  DaemonKind = "local_bash"
	KindLocalAgent DaemonKind = "local_agent"
	KindMonitor    DaemonKind = "monitor"
	KindLSP        DaemonKind = "lsp"

	// Reserved — enum entries only, no Daemon impl yet. Listed here so the
	// ID prefix table stays exhaustive and new kinds land as one-file diffs.
	KindRemoteAgent       DaemonKind = "remote_agent"
	KindInProcessTeammate DaemonKind = "in_process_teammate"
	KindLocalWorkflow     DaemonKind = "local_workflow"
	KindDream             DaemonKind = "dream"
)

// idPrefix is the single-letter wire-stable prefix per kind. Matches ref's
// generateTaskId so transcripts and test fixtures read consistently.
var idPrefix = map[DaemonKind]rune{
	KindLocalBash:         'b',
	KindLocalAgent:        'a',
	KindMonitor:           'm',
	KindLSP:               'l',
	KindRemoteAgent:       'r',
	KindInProcessTeammate: 't',
	KindLocalWorkflow:     'w',
	KindDream:             'd',
}

const idAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// GenerateID returns a wire-stable id: kind prefix + 8 base-36 chars.
// 36^8 ≈ 2.8 trillion combinations — sufficient for the lifetime of any
// session. Unknown kinds fall back to 'x' so callers don't need a nil check.
func GenerateID(kind DaemonKind) string {
	prefix, ok := idPrefix[kind]
	if !ok {
		prefix = 'x'
	}
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	out := make([]byte, 0, 9)
	out = append(out, byte(prefix))
	for _, b := range buf {
		out = append(out, idAlphabet[int(b)%len(idAlphabet)])
	}
	return string(out)
}
