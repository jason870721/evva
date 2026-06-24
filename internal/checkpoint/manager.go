package checkpoint

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/pkg/permission"
)

// Manager is the per-agent, session-scoped checkpoint runtime. It owns the
// "current turn" checkpoint the fs tools capture into and the restore engine
// the controller drives.
//
// Construction is main-agent-only: subagents and swarm members don't
// checkpoint (their isolation already bounds blast radius — rewind PRD §5.6).
// A nil *Manager is never wired as a sink, so the capture path stays inert
// when the feature is off.
//
// Concurrency: Begin runs on the agent-loop goroutine (the one that won the
// run flag); CaptureBefore runs on the parallel tool-dispatch goroutines;
// List/Restore run on the UI goroutine while no Run is in flight. A single
// mutex serializes all of them.
type Manager struct {
	mu        sync.Mutex
	workdir   string
	root      string // <workdir>/.evva/checkpoints
	sessionID string
	retention Retention
	logger    *slog.Logger

	cur *Record // the in-progress checkpoint for the active turn; nil before the first Begin
}

// NewManager builds a checkpoint manager rooted at <workdir>/.evva/checkpoints
// for the given session. Returns nil when workdir or sessionID is empty (the
// caller treats nil as "checkpointing disabled" and never wires it as a sink).
func NewManager(workdir, sessionID string, ret Retention, logger *slog.Logger) *Manager {
	if workdir == "" || sessionID == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		workdir:   workdir,
		root:      filepath.Join(workdir, filepath.FromSlash(permission.CheckpointDirSegment)),
		sessionID: sessionID,
		retention: ret,
		logger:    logger,
	}
}

// SetSession re-scopes the manager to a new session id (after /clear or
// /resume) and drops the in-progress checkpoint — the old turn belonged to the
// old session's namespace. A no-op when the id is unchanged.
func (m *Manager) SetSession(sessionID string) {
	if m == nil || sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if sessionID == m.sessionID {
		return
	}
	m.sessionID = sessionID
	m.cur = nil
}

// SetRetention updates the prune policy live (the /config form may change it
// mid-session).
func (m *Manager) SetRetention(ret Retention) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.retention = ret
	m.mu.Unlock()
}

func (m *Manager) dir() string { return sessionDir(m.root, m.sessionID) }

// Begin opens a fresh checkpoint for a user turn: it records the conversation
// cut-point and compaction epoch, persists an (initially file-less) Record,
// and makes it the capture target. A turn that never touches a file still
// leaves this conversation-only checkpoint on disk.
//
// Old checkpoints beyond the retention budget are pruned here (one sweep per
// turn). Begin must be called only at a real user-turn start (Run, not
// Continue) so a continued/iter-limit turn keeps capturing into the same
// checkpoint.
func (m *Manager) Begin(cutLen, fullCompactCount int, prompt string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	dir := m.dir()
	seq := maxSeq(dir) + 1
	rec := &Record{
		Version:          Version,
		SessionID:        m.sessionID,
		Seq:              seq,
		CreatedAt:        time.Now(),
		PromptPreview:    previewOf(prompt),
		CutLen:           cutLen,
		FullCompactCount: fullCompactCount,
	}
	if err := writeRecord(dir, rec); err != nil {
		m.logger.Warn("checkpoint.begin.persist", "err", err, "seq", seq)
		// Keep cur so in-turn captures still attempt to persist later; a
		// transient write error shouldn't disable capture for the whole turn.
	}
	m.cur = rec

	// Prune AFTER adding so the budget holds post-Begin and the just-created
	// checkpoint (always the newest) is never the one dropped.
	prune(dir, m.retention)
	// Cross-session footprint cap: bound how many session namespaces persist.
	pruneSessionDirs(m.root, m.sessionID, maxSessionDirs)
	m.logger.Debug("checkpoint.begin", "seq", seq, "cut_len", cutLen, "full_compact", fullCompactCount)
}

// CaptureBefore records absPath's current on-disk state into the active
// checkpoint, once per path per turn (first call wins — later edits in the
// same turn keep the earliest before-image). It satisfies fs.CheckpointSink.
//
// Best-effort by contract: every failure path is a logged no-op so a capture
// problem never blocks or errors the user's edit. A missing file is recorded
// as "did not exist" so a rewind deletes it; a path inside the checkpoint
// store itself is ignored.
func (m *Manager) CaptureBefore(absPath string) {
	if m == nil || absPath == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur == nil {
		return // no active turn (e.g. an edit outside a Run) — nothing to attach to
	}
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return
	}
	// Never capture our own storage.
	if strings.HasPrefix(abs, m.root) {
		return
	}
	for i := range m.cur.Files {
		if m.cur.Files[i].Path == abs {
			return // already captured this turn
		}
	}

	info, statErr := os.Stat(abs)
	switch {
	case statErr != nil:
		// Treat any stat failure as "did not exist before" — the common case
		// is a brand-new file the turn is creating, which a rewind deletes.
		m.cur.Files = append(m.cur.Files, FileRef{Path: abs, Existed: false})
	case info.IsDir():
		return // edit/write never target a dir; ignore defensively
	default:
		data, readErr := os.ReadFile(abs)
		if readErr != nil {
			m.logger.Warn("checkpoint.capture.read", "err", readErr, "path", abs)
			return
		}
		hash := hashBytes(data)
		if err := writeBlob(m.dir(), hash, data); err != nil {
			m.logger.Warn("checkpoint.capture.blob", "err", err, "path", abs)
			return
		}
		m.cur.Files = append(m.cur.Files, FileRef{Path: abs, Existed: true, Hash: hash, Size: len(data)})
	}

	if err := writeRecord(m.dir(), m.cur); err != nil {
		m.logger.Warn("checkpoint.capture.persist", "err", err, "seq", m.cur.Seq)
	}
}

// List returns this session's checkpoints, newest first.
func (m *Manager) List() []*Record {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	recs, err := listRecords(m.dir())
	if err != nil {
		m.logger.Warn("checkpoint.list", "err", err)
		return nil
	}
	return recs
}

// Load returns the checkpoint with the given seq, or an error if absent.
func (m *Manager) Load(seq int) (*Record, error) {
	if m == nil {
		return nil, fmt.Errorf("checkpoint: manager unavailable")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return readRecord(filepath.Join(m.dir(), recordName(seq)))
}

// RestoreResult summarizes a code-restore for the caller's status line.
type RestoreResult struct {
	Restored int      // files rewritten from their before-image
	Deleted  int      // files removed (created during the rewound turn)
	Errors   []string // per-file failures; restore is best-effort and never half-silent
}

// RestoreCode rewrites every captured file back to its before-image and
// deletes the files the turn created. Each target must resolve inside the
// workdir or it is refused (a checkpoint can never write outside the project).
// Errors are collected per file rather than aborting, so one unwritable path
// doesn't strand the rest.
func (m *Manager) RestoreCode(r *Record) RestoreResult {
	var res RestoreResult
	if m == nil || r == nil {
		return res
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	dir := m.dir()
	for _, f := range r.Files {
		if !withinDir(m.workdir, f.Path) {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: refused (outside workdir)", f.Path))
			continue
		}
		if !f.Existed {
			if err := os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
				res.Errors = append(res.Errors, fmt.Sprintf("%s: delete: %v", f.Path, err))
				continue
			}
			res.Deleted++
			continue
		}
		data, err := readBlob(dir, f.Hash)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: read before-image: %v", f.Path, err))
			continue
		}
		if err := os.MkdirAll(filepath.Dir(f.Path), 0o755); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: mkdir: %v", f.Path, err))
			continue
		}
		if err := os.WriteFile(f.Path, data, 0o644); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: write: %v", f.Path, err))
			continue
		}
		res.Restored++
	}
	m.logger.Info("checkpoint.restore.code", "seq", r.Seq, "restored", res.Restored, "deleted", res.Deleted, "errors", len(res.Errors))
	return res
}

// previewOf flattens a prompt to a single-line, length-capped preview for the
// /rewind picker.
func previewOf(prompt string) string {
	s := strings.TrimSpace(prompt)
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > PreviewMaxBytes {
		s = s[:PreviewMaxBytes]
	}
	return s
}
