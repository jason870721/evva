// This file is the team blackboard (BB): one leader-curated markdown document
// at <workdir>/.vero/blackboard.md — the swarm's standing "current picture"
// (goal, decisions made, who-owns-what, current phase). It is deliberately the
// smallest possible shared-state design: one file, one writer role, whole-
// document replace. The file — not a DB row — so the operator can cat it, grep
// it, and fix it in an editor with the service live; root-anchored beside the
// ledger so worktree-isolated members (SWT) still share it. Updates cost zero
// wakes: every member simply sees the new content in its next wake brief
// (scheduler.go), whenever that wake happens for its own reasons. History
// lives in the event log's blackboard_updated lines, like every other swarm
// mutation.

package swarm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/pkg/event"
)

// KindBlackboardUpdated is the engine event announcing one blackboard write —
// through the same pump as the DWF engine lines, so the live console, the
// timeline, and the durable event log all carry who rewrote the team picture
// and when. Operator disk edits produce no event (documented); the mtime-based
// freshness line still updates.
const KindBlackboardUpdated = event.Kind("blackboard_updated")

// blackboardFile is the document's name under .vero/ — beside the ledger and
// the event log, deliberately NOT in the members' checkout (the worktree
// wave's blast-radius rule: space state is root-anchored).
const blackboardFile = "blackboard.md"

func (sp *SwarmSpace) blackboardPath() string {
	return filepath.Join(sp.Workdir, ".vero", blackboardFile)
}

// blackboardCap is the write-time size gate. Hand-built spaces (settings zero
// value) get the default rather than an unwritable board — LoadManifest
// normalizes the knob, so 0 here only ever means "no manifest ran".
func (sp *SwarmSpace) blackboardCap() int {
	if sp.settings.BlackboardMaxBytes > 0 {
		return sp.settings.BlackboardMaxBytes
	}
	return agentdef.DefaultBlackboardMaxBytes
}

// WriteBlackboard replaces the whole board (BB §4: replace, not patch — an LLM
// writer is far more reliable re-emitting a bounded document than addressing
// patches into one, and a retried write converges). Oversize content fails
// loudly naming the cap and the overage — this one check is what bounds the
// wake-brief token cost across N members × every wake. The write is temp-file
// + atomic rename, so a reader (wake brief, web, operator cat) never sees a
// torn file. Empty content clears the board back to dormant.
func (sp *SwarmSpace) WriteBlackboard(writer, content string) (int, error) {
	if cap := sp.blackboardCap(); len(content) > cap {
		return 0, fmt.Errorf("swarm: blackboard content is %d bytes — %d over the %d-byte cap "+
			"(settings.blackboard_max_bytes). Prune stale lines and rewrite the smaller document",
			len(content), len(content)-cap, cap)
	}
	// Serialize same-process writes: production has one writer (the leader's
	// run goroutine), but on Windows two replace-renames racing on the same
	// destination transiently deny each other — a mutex makes in-process
	// writes ordered, and renameWithRetry absorbs the cross-process cases
	// (an operator's editor, an AV scanner) the mutex cannot see.
	sp.blackboardWriteMu.Lock()
	defer sp.blackboardWriteMu.Unlock()
	path := sp.blackboardPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, fmt.Errorf("swarm: blackboard: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), blackboardFile+".*.tmp")
	if err != nil {
		return 0, fmt.Errorf("swarm: blackboard: %w", err)
	}
	_, werr := tmp.WriteString(content)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp.Name(), 0o644) // CreateTemp's 0600 would hide the file from group readers
	}
	if werr == nil {
		werr = renameWithRetry(tmp.Name(), path)
	}
	if werr != nil {
		os.Remove(tmp.Name())
		return 0, fmt.Errorf("swarm: blackboard: %w", werr)
	}

	// Remember who wrote, keyed to the file version we just produced: a later
	// disk edit changes the mtime, and Blackboard() then drops the stale "by".
	// In-memory only — after a restart the writer is unknown until the next
	// tool write, and the brief simply omits it.
	var mtime time.Time
	if fi, err := os.Stat(path); err == nil {
		mtime = fi.ModTime()
	}
	sp.mu.Lock()
	sp.blackboardBy, sp.blackboardByAt = writer, mtime
	sp.mu.Unlock()

	line := fmt.Sprintf("blackboard updated by %s (%d bytes)", writer, len(content))
	if content == "" {
		line = "blackboard cleared by " + writer
	}
	sp.emitEngineEvent(KindBlackboardUpdated, writer, line)
	return len(content), nil
}

// Blackboard reads the current board: content, its mtime, and the last tool
// writer when the file is still that writer's version ("" for a fresh
// restart or after an operator disk edit — the freshness line then carries no
// attribution rather than a wrong one). Absent or empty file = dormant: "",
// zero time. Disk is the truth — read fresh per call, never cached — so an
// operator's hand edit is live at every member's next wake with no service
// hook. A hand-edited file past the hard ceiling is truncated on the read
// side (the write-side cap can't gate an editor).
func (sp *SwarmSpace) Blackboard() (content string, mtime time.Time, by string) {
	path := sp.blackboardPath()
	b, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, ""
	}
	content = strings.TrimSpace(string(b))
	if content == "" {
		return "", time.Time{}, ""
	}
	if len(content) > agentdef.MaxBlackboardMaxBytes {
		content = content[:agentdef.MaxBlackboardMaxBytes] + "\n… (truncated at the blackboard ceiling — trim .vero/blackboard.md)"
	}
	if fi, err := os.Stat(path); err == nil {
		mtime = fi.ModTime()
	}
	sp.mu.Lock()
	if sp.blackboardBy != "" && mtime.Equal(sp.blackboardByAt) {
		by = sp.blackboardBy
	}
	sp.mu.Unlock()
	return content, mtime, by
}

// blackboardWakeSection renders the board as a wake-brief section (BB §5.3) —
// the same seam the memory index rides (scheduler.go), injected on EVERY wake
// kind so a post-compaction member re-acquires the team picture automatically.
// "" when the board is dormant: a board-less space's wake reminder stays
// byte-identical to the pre-BB form. The freshness line makes staleness
// visible to every reader — a "updated 9d ago" board discredits itself.
func (sp *SwarmSpace) blackboardWakeSection(now time.Time) string {
	content, mtime, by := sp.Blackboard()
	if content == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Team blackboard (updated ")
	b.WriteString(AgeString(now.Sub(mtime)))
	if by != "" {
		b.WriteString(" by ")
		b.WriteString(by)
	}
	b.WriteString(")\n")
	b.WriteString(content)
	return b.String()
}

// renameWithRetry is os.Rename with a short bounded retry: on Windows a
// replace-rename transiently fails with "Access is denied" while the
// destination is mid-replacement or briefly held by an external process (an
// editor save, an AV scan). A few attempts over ~100ms absorb the transient;
// a persistent failure still surfaces as the last error. On Unix the first
// attempt succeeds and the loop never spins.
func renameWithRetry(from, to string) error {
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if err = os.Rename(from, to); err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Millisecond)
	}
	return err
}

// AgeString humanizes a freshness age for the wake brief and the read tool:
// coarse on purpose ("3m ago", not "3m12s") — the reader needs "current vs
// stale", not a stopwatch. Negative ages (clock skew) read as just now.
func AgeString(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
