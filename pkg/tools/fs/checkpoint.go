package fs

// CheckpointSink receives the pre-mutation state of files the edit/write tools
// are about to change, so the runtime's checkpoint/rewind engine can later
// restore them. The interface is declared here, on the consumer side, so
// pkg/tools/fs stays free of an internal/checkpoint import; the runtime wires
// a concrete implementation in via WithCheckpoints.
//
// A nil sink disables capture entirely — the tools nil-check before calling,
// and the runtime only installs a sink when checkpointing is enabled — so the
// feature has zero cost on the edit hot path when it is off.
type CheckpointSink interface {
	// CaptureBefore records absPath's current on-disk bytes before the caller
	// overwrites the file. Idempotent per path within a turn (the first call
	// wins, preserving the earliest before-image). Best-effort by contract: it
	// must never block or error the edit.
	CaptureBefore(absPath string)
}
