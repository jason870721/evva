package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/memdir/dream"
	"github.com/johnny1110/evva/internal/session"
	"github.com/johnny1110/evva/pkg/constant"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/llm"
	"github.com/johnny1110/evva/pkg/permission"
	"github.com/johnny1110/evva/pkg/tools"
)

// dreamMaxIters caps the consolidation loop: orient → read a handful of files →
// merge/prune → rewrite the index is a short job, not an open-ended task.
const dreamMaxIters = 40

// dreamTimeout hard-stops a dream that wanders. Comfortably longer than a real
// run, shorter than the lock's staleAfter so a timed-out dream's lock is
// rolled back (below) AND reclaimable if the rollback itself fails.
const dreamTimeout = 10 * time.Minute

// maybeFireDream runs the dream gate when the MAIN agent goes idle and, if it
// opens, launches a background consolidation pass. Non-blocking: the gate is a
// stat (+ a ≤10-min-throttled dir scan) and the dream runs in a goroutine, so
// the user is never stalled. No-op for subagents, when auto-dream/auto-memory
// is off, with no permission store (the fence needs it — §5.3), or when a dream
// is already running. Called from the main-agent idle branch of done().
func (a *Agent) maybeFireDream() {
	if a.IsSubagent() || a.cfg == nil {
		return
	}
	if !a.cfg.GetEnableAutoDream() || !a.cfg.GetEnableAutoMemory() {
		return
	}
	memDir := a.memSnap.MemoryDir
	if memDir == "" {
		return
	}
	// The memory-dir write fence is enforced through the permission gate; with
	// no store the gate short-circuits to allow-all and the dream would be
	// unfenced. Refuse rather than run an autonomous writer unconfined.
	if a.permissionStore == nil {
		a.logger.Debug("dream.skip", "reason", "no permission store (fence unavailable)")
		return
	}
	// One dream per process at a time (the file lock guards across processes;
	// this guards the goroutine). Reset on the not-fired paths + on completion.
	if !a.dreaming.CompareAndSwap(false, true) {
		return
	}

	d, scanAt, err := dream.GateOpen(dream.GateInput{
		Cfg: dream.GateConfig{
			Enabled:     true,
			MinHours:    a.cfg.GetAutoDreamMinHours(),
			MinSessions: a.cfg.GetAutoDreamMinSessions(),
		},
		AppHome:          a.cfg.AppHome,
		MemDir:           memDir,
		CurrentSessionID: a.ID,
		Now:              time.Now(),
		LastScanAt:       a.lastDreamScanAt,
	})
	a.lastDreamScanAt = scanAt
	if err != nil {
		a.logger.Debug("dream.gate.error", "err", err)
	}
	if !d.Fire {
		a.dreaming.Store(false)
		a.logger.Debug("dream.skip", "reason", d.Reason)
		return
	}
	a.logger.Info("dream.fire", "reason", d.Reason, "sessions", d.Sessions)
	go a.runDream(memDir, d.Prior, d.Sessions)
}

// runDream builds and runs the fenced consolidation subagent, then surfaces a
// completion line. Owns the lock from here: any failure/timeout rolls it back
// so the time-gate re-opens next idle. Always clears the in-process guard.
func (a *Agent) runDream(memDir string, prior time.Time, sessions int) {
	defer a.dreaming.Store(false)

	ctx, cancel := context.WithTimeout(a.rootCtx, dreamTimeout)
	defer cancel()

	provider, model, effort := a.dreamModelTarget()

	// Clone the config so New's WorkDir backfill + the recall-off flag don't
	// touch the parent's live config. Recall is also off via IsSubagent, but
	// the explicit flag keeps the intent local.
	cfg2 := a.cfg.Clone()
	cfg2.EnableMemoryRecall = false

	sink := newDreamSink()
	// A minimal, shell-free tool set: read/glob/grep/tree to explore, edit/write
	// to consolidate. No bash (no shell escape), no web, no agent (§5.3).
	profile := General(cfg2, provider, model, []llm.Option{llm.WithEffort(effort)},
		tools.READ_FILE, tools.GLOB, tools.GREP, tools.TREE, tools.WRITE_FILE, tools.EDIT_FILE)

	child, err := New(a, profile,
		WithName("dream"),
		WithConfig(cfg2),
		WithMemoryDir(memDir), // recall target + the in-dir write auto-allow carve-out
		WithPermissionMode(permission.ModeDefault),
		WithPermissionStore(a.permissionStore), // carries the memDir carve-out + any deny rules
		WithPermissionBroker(autoDenyBroker{}), // out-of-memDir writes → ask → auto-denied
		WithSink(sink),
		WithMaxIterations(dreamMaxIters),
		WithRootContext(ctx),
	)
	if err != nil {
		a.logger.Warn("dream.build_failed", "err", err)
		dream.Rollback(memDir, prior)
		return
	}
	defer child.Shutdown()

	transcriptRoot := filepath.Join(a.cfg.AppHome, session.SessionsSubdir)
	prompt := dream.BuildPrompt(memDir, transcriptRoot, sessions)

	a.logger.Info("dream.start", "model", model, "mem_dir", memDir, "sessions", sessions)
	if _, runErr := child.Run(ctx, prompt); runErr != nil {
		a.logger.Warn("dream.run_failed", "err", runErr)
		dream.Rollback(memDir, prior)
		return
	}

	summary, files := sink.result()
	a.logger.Info("dream.done", "files", files, "summary", truncateSummary(summary, 200))

	// Consolidation is the one thing that rewrites memory bodies wholesale —
	// merging two files into one, pruning a third — so the vector index is
	// guaranteed stale afterwards. Re-syncing here means the change is
	// reflected before the next search rather than at the next session open.
	//
	// Runs inline: this is already a background goroutine, and the index must
	// settle before anything reads it. Failure is logged, not surfaced —
	// searches keep working against the pre-dream rows, and the next session
	// open re-syncs anyway.
	if s := a.memorySearcher; s != nil && s.HasEmbedder() {
		if res, err := s.Sync(context.Background()); err != nil {
			a.logger.Warn("dream.reindex_failed", "err", err)
		} else if res.Embedded > 0 || res.Dropped > 0 {
			a.logger.Info("dream.reindex", "embedded", res.Embedded, "dropped", res.Dropped, "total", res.Total)
		}
	}

	a.surfaceDreamDone(files)
}

// dreamModelTarget resolves the (provider, model, effort) for the dream agent.
// An explicit, credentialed AutoDreamModel wins; otherwise it reuses the recall
// side-query's cheap per-provider default — a consolidation pass is judgment-
// light gardening, not flagship reasoning, and must run on a credentialed key.
func (a *Agent) dreamModelTarget() (constant.LLMProvider, constant.Model, int) {
	provider, model, effort := a.recallTarget()
	if raw := a.cfg.GetAutoDreamModel(); raw != "" {
		if m, ok := constant.GetModel(raw); ok {
			if p, ok := providerForModel(m); ok && a.providerConfigured(p.Name) {
				return p, m, effort
			}
		}
	}
	return provider, model, effort
}

// surfaceDreamDone emits a dim background-completion line to the user's
// transcript naming the files the dream improved. Stays silent when nothing
// changed — a no-op dream is not worth a notification. The summary text is
// logged (above), not shown, to keep the line tight; a richer surface (a
// dedicated dream block) is a v1.8 follow-up.
func (a *Agent) surfaceDreamDone(files []string) {
	if len(files) == 0 {
		return
	}
	// The BgResult block renders "task-<id> <status>", so the readable note
	// rides in Status (Output is not rendered).
	status := fmt.Sprintf("🌙 consolidated memory — improved %d file(s): %s", len(files), strings.Join(files, ", "))
	a.emit(event.KindBgResult, func(e *event.Event) {
		e.BgResult = &event.BgResultPayload{TaskID: "dream", Status: status, AgentID: a.ID}
	})
}

// autoDenyBroker denies every approval request without prompting. Installed
// ONLY on the dream agent: a write/edit that escapes the memory-dir carve-out
// reaches the gate as "ask", and there is no user watching a background pass,
// so it must be cleanly DENIED rather than block forever. Reads never reach
// here (ReadOnlyOrSelfTools auto-allow) and in-memDir writes never reach here
// (the auto-memory carve-out), so this only ever fires on an attempted escape.
type autoDenyBroker struct{}

func (autoDenyBroker) Request(context.Context, permission.ApprovalRequest) (permission.Decision, error) {
	return permission.Decision{
		Behavior: permission.BehaviorDeny,
		Reason:   "dream agent: writes are confined to the memory directory",
	}, nil
}
func (autoDenyBroker) Respond(string, permission.Decision) error { return nil }
func (autoDenyBroker) Cancel(string) error                       { return nil }

// dreamSink captures the dream subagent's output: the last assistant text (its
// summary) and the basenames of any files it wrote/edited (for the completion
// line). Concurrency-safe — the agent serializes Emit, but result() may be
// read from the orchestrating goroutine after the run.
type dreamSink struct {
	mu      sync.Mutex
	summary string
	touched map[string]struct{}
}

func newDreamSink() *dreamSink { return &dreamSink{touched: map[string]struct{}{}} }

func (s *dreamSink) Emit(e event.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch e.Kind {
	case event.KindText:
		if e.Text != nil {
			s.summary = e.Text.Text // the last full text block is the summary
		}
	case event.KindToolUseStart:
		if e.ToolUseStart == nil {
			return
		}
		if e.ToolUseStart.Name == string(tools.WRITE_FILE) || e.ToolUseStart.Name == string(tools.EDIT_FILE) {
			if p := readFilePath(e.ToolUseStart.Input); p != "" {
				s.touched[filepath.Base(p)] = struct{}{}
			}
		}
	}
}

func (s *dreamSink) result() (summary string, files []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	files = make([]string, 0, len(s.touched))
	for f := range s.touched {
		files = append(files, f)
	}
	sort.Strings(files)
	return strings.TrimSpace(s.summary), files
}
