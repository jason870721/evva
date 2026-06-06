// Package service is the process-singleton swarm host: the 127.0.0.1:8888
// HTTP/WS server that fronts one or more isolated SwarmSpaces.
//
// Service is the multi-space container (design §3.1, invariant #2): a registry
// of fully-isolated spaces, each with its own store/bus/roster/agents. It fans
// every space's tagged event stream out to the right WebSocket (via
// webapi.Hub) and routes inbound browser commands to the right
// Controller/Supervisor. Multi-space is native — there is no single-space
// hardcode.
//
// SPRD-1-9 layers daemonization (pidfile/log under ~/.evva/service/) and the
// `evva swarm .` CLI on top of the Register/StopSpace surface exposed here.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	swarmtools "github.com/johnny1110/evva/internal/swarm/tools"
	"github.com/johnny1110/evva/internal/swarm/webapi"
	"github.com/johnny1110/evva/pkg/agent"
	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/event"
	"github.com/johnny1110/evva/pkg/tools"
	"github.com/johnny1110/evva/pkg/toolset"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/web"
)

// DefaultAddr is the loopback bind the service uses unless overridden. Binding
// to 127.0.0.1 (not 0.0.0.0) is the security baseline (invariant #6): agents
// run shell and edit files, so the workstation is RCE-equivalent and must not
// be reachable off-box by default.
const DefaultAddr = "127.0.0.1:8888"

// DefaultToken is the fixed session token while the project is pre-1.0 and the
// service is loopback-only: a memorable "root" the operator types once, instead
// of copy-pasting a freshly minted UUID every restart. This is a deliberate
// test-convenience tradeoff, safe because the host binds 127.0.0.1 (DefaultAddr).
// TODO(security): before any non-loopback exposure, replace with a minted
// per-start secret (or real auth) — see docs/veronica/veronica-design-v1.md §8.3.
const DefaultToken = "root"

// manifestFile is the per-workdir swarm declaration `evva swarm .` reads.
const manifestFile = "evva-swarm.yml"

// Service is the swarm host. One per process.
type Service struct {
	addr  string
	token string
	log   *slog.Logger

	hub *webapi.Hub
	srv *http.Server

	// rootCtx is the lifetime of the whole host; every space's supervisor and
	// event pump run as its children, so Stop cancels all of them at once.
	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu     sync.RWMutex
	ln     net.Listener // bound listener, nil until Listen
	spaces map[string]*spaceEntry

	// stateDir, when set, is where the set of registered workdirs is persisted
	// (spaces.json) so Reconcile can rebuild every space after a restart. Empty
	// disables persistence (tests that register stub spaces in-memory).
	stateDir string

	// loadConfig builds the per-space *config.Config for a workdir. Overridable
	// in tests to inject a stub LLM provider without touching disk/env.
	loadConfig func(workdir string) (*config.Config, error)
}

// spacesFileName holds the registered workdirs across restarts (SPRD-1-11).
const spacesFileName = "spaces.json"

// spaceStatus is a space's lifecycle state. A stopped space keeps its identity
// (id/name/workdir) in the registry — Docker-style — so `evva swarm run <ref>`
// can rebuild it and `evva swarm ls` can still show it; only `rm` forgets it.
type spaceStatus string

const (
	statusRunning spaceStatus = "running"
	statusStopped spaceStatus = "stopped"
)

// spaceEntry holds one space's identity plus, while running, the live handles
// needed to tear it down independently of its siblings. A stopped entry keeps
// id/name/workdir and leaves the live fields nil.
type spaceEntry struct {
	id      string
	name    string // unique human handle: --name > manifest name > generated
	workdir string
	status  spaceStatus

	space    *swarm.SwarmSpace
	super    *swarm.Supervisor
	cancel   context.CancelFunc // stops the supervisor's run loops + timer tick
	stopPump chan struct{}      // closed after Shutdown so the pump drains then exits
	pending  *gateTracker       // outstanding approval/question gates, for reconnect replay
}

// live reports whether the entry currently has a running space behind it.
func (e *spaceEntry) live() bool { return e != nil && e.status == statusRunning && e.space != nil }

// gateTracker remembers a space's outstanding approval/question gates so a
// browser that connects late — after a gate fired, or across a WS reconnect gap
// — can hydrate them and answer, instead of the member hanging unseen (RP-2
// §3.3). It is fed from the event pump (every gate event passes through it) and
// drained when the gate is answered or its run ends. It stores the raw gate
// events, so the reconnect path replays the exact shape the live WS sends.
type gateTracker struct {
	mu    sync.Mutex
	gates map[string]event.Event // requestID -> the approval_needed / question_needed event
}

func newGateTracker() *gateTracker { return &gateTracker{gates: map[string]event.Event{}} }

// observe folds one event into the tracker: a gate event is recorded; a
// run-terminal event clears any gate still pending for that agent (a member
// suspended mid-approval leaves a dead gate nobody will answer).
func (g *gateTracker) observe(e event.Event) {
	switch e.Kind {
	case event.KindApprovalNeeded:
		if e.ApprovalNeeded != nil {
			g.put(e.ApprovalNeeded.RequestID, e)
		}
	case event.KindQuestionNeeded:
		if e.QuestionNeeded != nil {
			g.put(e.QuestionNeeded.RequestID, e)
		}
	case event.KindRunEnd, event.KindRunCancelled, event.KindError:
		g.clearAgent(e.AgentID)
	}
}

func (g *gateTracker) put(reqID string, e event.Event) {
	if reqID == "" {
		return
	}
	g.mu.Lock()
	g.gates[reqID] = e
	g.mu.Unlock()
}

func (g *gateTracker) remove(reqID string) {
	g.mu.Lock()
	delete(g.gates, reqID)
	g.mu.Unlock()
}

func (g *gateTracker) clearAgent(agentID string) {
	g.mu.Lock()
	for id, e := range g.gates {
		if e.AgentID == agentID {
			delete(g.gates, id)
		}
	}
	g.mu.Unlock()
}

func (g *gateTracker) snapshot() []event.Event {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]event.Event, 0, len(g.gates))
	for _, e := range g.gates {
		out = append(out, e)
	}
	return out
}

// New builds the host bound (logically) to addr. An empty addr uses
// DefaultAddr. A session token is minted now and required on every /api + /ws
// request. Call Listen then Serve (Serve calls Listen if you skip it).
func New(addr string) *Service {
	if addr == "" {
		addr = DefaultAddr
	}
	rootCtx, rootCancel := context.WithCancel(context.Background())
	s := &Service{
		addr:       addr,
		token:      DefaultToken,
		log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		hub:        webapi.NewHub(),
		rootCtx:    rootCtx,
		rootCancel: rootCancel,
		spaces:     make(map[string]*spaceEntry),
		loadConfig: defaultLoadConfig,
	}

	var spa fs.FS
	if sub, err := fs.Sub(web.Dist, "dist"); err == nil {
		spa = sub
	}
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           webapi.NewRouter(s, s.hub, spa),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

func defaultLoadConfig(workdir string) (*config.Config, error) {
	return config.Load(config.LoadOptions{WorkDir: workdir})
}

// Token is the session token clients must present. Printed to the terminal on
// start so a local user can authenticate the browser.
func (s *Service) Token() string { return s.token }

// SetLogger swaps the host's structured logger (SPRD-1-9 wires the daemon log).
func (s *Service) SetLogger(l *slog.Logger) {
	if l != nil {
		s.log = l
	}
}

// SetStateDir enables restart persistence: the set of registered workdirs is
// written under dir/spaces.json so Reconcile can rebuild every space after a
// process death (SPRD-1-11). Call before Reconcile / the first Register.
func (s *Service) SetStateDir(dir string) { s.stateDir = dir }

// Listen binds the configured address without serving. Exposed so callers
// (and tests using a :0 ephemeral port) can read Addr() before Serve blocks.
// Idempotent: a second call is a no-op once bound.
func (s *Service) Listen() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln != nil {
		return nil
	}
	ln, err := net.Listen("tcp", s.srv.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.addr = ln.Addr().String()
	return nil
}

// Serve serves until ctx is cancelled, then gracefully drains and tears down
// every registered space. It binds first if Listen was not already called. A
// context-triggered shutdown returns nil; any other server error is returned.
func (s *Service) Serve(ctx context.Context) error {
	if err := s.Listen(); err != nil {
		return err
	}

	errc := make(chan error, 1)
	go func() { errc <- s.srv.Serve(s.ln) }()

	select {
	case <-ctx.Done():
		return s.Stop()
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// Stop tears down every space and the HTTP server — a graceful process
// shutdown. Crucially it does NOT rewrite spaces.json: the registered set is
// preserved so the next start reconciles the same spaces back (SPRD-1-11). Use
// StopSpace to deliberately drop one from the reconcile set.
func (s *Service) Stop() error {
	s.mu.Lock()
	ents := make([]*spaceEntry, 0, len(s.spaces))
	for id, ent := range s.spaces {
		ents = append(ents, ent)
		delete(s.spaces, id)
	}
	s.mu.Unlock()
	for _, ent := range ents {
		teardownSpace(ent)
	}
	s.rootCancel()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.srv.Shutdown(shutCtx)
}

// teardownSpace stops a space's supervisor, shuts its agents + store down, and
// drains then stops its event pump. Shared by Stop (whole host), StopSpace, and
// RemoveSpace. Nil-safe on every live handle so it is a no-op for an entry that
// is already stopped (its live fields were cleared when it was stopped).
func teardownSpace(ent *spaceEntry) {
	if ent == nil {
		return
	}
	if ent.cancel != nil {
		ent.cancel() // stop run loops + timer (no new runs)
	}
	if ent.super != nil {
		// Drain the run engine before the store closes: cancel only signals, so a
		// serve goroutine mid-ClaimUnread would otherwise race Store.Close and hit a
		// closed DB (and keep .vero files alive past teardown). Wait makes it ordered.
		ent.super.Wait()
	}
	if ent.space != nil {
		ent.space.Shutdown() // cancel agents + close store; trailing events still buffered
	}
	if ent.stopPump != nil {
		close(ent.stopPump) // pump does a final drain, then exits
	}
}

// Addr returns the address the service is bound to. Before Listen it is the
// configured address; after Listen it is the resolved one (the concrete port
// when :0 was requested).
func (s *Service) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Register reads <workdir>/evva-swarm.yml, builds its agents, and brings the
// space up as a new isolated member of the registry. The space's handle name is
// resolved Docker-style: an explicit name (CLI --name) wins, else the manifest's
// `name:`, else a generated handle; it must be unique across all known spaces
// (running or stopped). Returns the generated (UUID) space id. This is the
// production path the `evva swarm .` CLI (SPRD-1-9) calls.
func (s *Service) Register(workdir, name string) (string, error) {
	m, loaded, cfg, err := s.loadSpace(workdir)
	if err != nil {
		return "", err
	}
	eff := strings.TrimSpace(name)
	if eff == "" {
		eff = strings.TrimSpace(m.Name)
	}
	s.mu.Lock()
	if eff == "" {
		eff = s.genNameLocked()
	} else if s.nameTakenLocked(eff) {
		s.mu.Unlock()
		return "", fmt.Errorf("swarm: name %q is already in use — pick another with --name", eff)
	}
	s.mu.Unlock()
	return s.register(common.GenUUID(), eff, m, loaded, cfg)
}

// loadSpace resolves a workdir to its parsed manifest, built agent definitions,
// and per-space config — the read-only half of bring-up, shared by Register and
// ResetSpace (which rebuilds the same workdir under its existing id).
func (s *Service) loadSpace(workdir string) (agentdef.Manifest, []agentdef.Loaded, *config.Config, error) {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return agentdef.Manifest{}, nil, nil, fmt.Errorf("swarm: resolve workdir %q: %w", workdir, err)
	}
	cfg, err := s.loadConfig(abs)
	if err != nil {
		return agentdef.Manifest{}, nil, nil, fmt.Errorf("swarm: load config for %q: %w", abs, err)
	}
	m, err := agentdef.LoadManifest(filepath.Join(abs, manifestFile))
	if err != nil {
		return agentdef.Manifest{}, nil, nil, err
	}
	loaded, warnings, err := agentdef.NewLoader().BuildAll(abs, m)
	if err != nil {
		return agentdef.Manifest{}, nil, nil, err
	}
	for _, w := range warnings {
		s.log.Warn("swarm: agent load warning", "agent", w.Agent, "msg", w.Msg)
	}
	return m, loaded, cfg, nil
}

// register is the shared bring-up core: assemble the space, start its
// supervisor and event pump under a fresh child context, and add it to the
// registry as a RUNNING entry under id+name. Split out so tests, Reconcile, and
// RunSpace can bring a space up with a chosen id+name without re-resolving it.
func (s *Service) register(id, name string, m agentdef.Manifest, loaded []agentdef.Loaded, cfg *config.Config) (string, error) {
	sp, err := swarm.NewSpace(id, m, loaded, swarmtools.Set{}, cfg)
	if err != nil {
		return "", err
	}

	// Restore any prior on-disk state (transcripts, unread mail, frozen
	// membership) before the supervisor starts the run loops — a no-op for a
	// fresh workdir, the restart-resume path for one that died (SPRD-1-11 §6.2).
	sp.Reload()

	super := swarm.NewSupervisor(sp)
	super.SetLogger(s.log) // member wake/run lifecycle into the service log
	spaceCtx, cancel := context.WithCancel(s.rootCtx)
	super.Start(spaceCtx)

	stopPump := make(chan struct{})
	s.mu.Lock()
	s.spaces[id] = &spaceEntry{
		id: id, name: name, workdir: sp.Workdir, status: statusRunning,
		space: sp, super: super, cancel: cancel, stopPump: stopPump, pending: newGateTracker(),
	}
	s.mu.Unlock()

	go s.pump(sp, stopPump)
	s.persistSpaces()
	s.log.Info("swarm: space registered", "id", id, "name", name, "workdir", sp.Workdir, "members", len(loaded))
	return id, nil
}

// StopSpace stops a running space but KEEPS its record as stopped (Docker-style):
// the live supervisor/agents/store/pump are torn down, but id/name/workdir stay
// in the registry so RunSpace can rebuild it and ls still shows it — only
// RemoveSpace forgets it. ref is the space id or its name. AC#2 isolation:
// siblings are untouched. Idempotent on an already-stopped space; unknown errors.
func (s *Service) StopSpace(ref string) error {
	s.mu.Lock()
	ent := s.resolveLocked(ref)
	if ent == nil {
		s.mu.Unlock()
		return fmt.Errorf("swarm: unknown space %q", ref)
	}
	if ent.status != statusRunning {
		s.mu.Unlock()
		return nil // already stopped — nothing to tear down
	}
	// Flip to stopped and detach the live handles UNDER the lock, so a concurrent
	// reader never observes a half-torn-down running space; tear them down after.
	live := &spaceEntry{cancel: ent.cancel, super: ent.super, space: ent.space, stopPump: ent.stopPump}
	ent.status = statusStopped
	ent.space, ent.super, ent.cancel, ent.stopPump, ent.pending = nil, nil, nil, nil, nil
	id, name := ent.id, ent.name
	s.mu.Unlock()

	teardownSpace(live)
	s.persistSpaces() // record the stopped status so a restart restores it stopped
	s.log.Info("swarm: space stopped", "id", id, "name", name)
	return nil
}

// RunSpace (re)starts a stopped space, rebuilding it from its remembered workdir
// under the SAME id and name so existing URLs keep working — the Docker `start`
// to StopSpace's `stop`. ref is the id or name. Idempotent for an already-running
// space; an unknown ref errors. Returns the (unchanged) space id.
func (s *Service) RunSpace(ref string) (string, error) {
	s.mu.RLock()
	ent := s.resolveLocked(ref)
	var id, name, workdir string
	var running bool
	if ent != nil {
		id, name, workdir, running = ent.id, ent.name, ent.workdir, ent.status == statusRunning
	}
	s.mu.RUnlock()
	if ent == nil {
		return "", fmt.Errorf("swarm: unknown space %q", ref)
	}
	if running {
		return id, nil // already up
	}

	m, loaded, cfg, err := s.loadSpace(workdir)
	if err != nil {
		return "", fmt.Errorf("swarm: run %q: %w", ref, err)
	}
	// Drop the stopped placeholder and rebuild under the same id+name. On failure
	// re-insert the placeholder so the space is never silently lost.
	s.mu.Lock()
	delete(s.spaces, id)
	s.mu.Unlock()
	if _, err := s.register(id, name, m, loaded, cfg); err != nil {
		s.mu.Lock()
		s.spaces[id] = &spaceEntry{id: id, name: name, workdir: workdir, status: statusStopped}
		s.mu.Unlock()
		s.persistSpaces()
		return "", fmt.Errorf("swarm: run %q: rebuild failed: %w", ref, err)
	}
	s.log.Info("swarm: space started", "id", id, "name", name, "workdir", workdir)
	return id, nil
}

// RemoveSpace forgets a space entirely (the Docker `rm`): a running space is torn
// down first, then its record is dropped from the registry and the reconcile set
// so a restart won't revive it. ref is the id or name; unknown errors. The
// durable .vero ledger and agent transcripts on disk are left intact — rm forgets
// the registration, not the workdir's data (use reset to wipe data).
func (s *Service) RemoveSpace(ref string) error {
	s.mu.Lock()
	ent := s.resolveLocked(ref)
	if ent == nil {
		s.mu.Unlock()
		return fmt.Errorf("swarm: unknown space %q", ref)
	}
	delete(s.spaces, ent.id)
	live := &spaceEntry{cancel: ent.cancel, super: ent.super, space: ent.space, stopPump: ent.stopPump}
	id, name := ent.id, ent.name
	s.mu.Unlock()

	teardownSpace(live) // no-op if it was already stopped
	s.persistSpaces()
	s.log.Info("swarm: space removed", "id", id, "name", name)
	return nil
}

// ResetSpace wipes a space back to a blank slate and brings it back up under the
// SAME id: it tears the live space down, deletes its .vero ledger (tasks +
// messages + membership) and every member's persisted transcript, then rebuilds
// fresh from the (re-read) manifest. The operator's space id / URL stays valid.
//
// A beta-stage operator tool, and deliberately destructive: all task history,
// messages, and agent context for the space are gone. The manifest is re-read and
// validated up front so a broken workdir fails the reset BEFORE the live space is
// torn down — reset never leaves the operator with no space.
func (s *Service) ResetSpace(ref string) (string, error) {
	s.mu.RLock()
	ent := s.resolveLocked(ref)
	var id, name, workdir string
	if ent != nil {
		id, name, workdir = ent.id, ent.name, ent.workdir
	}
	s.mu.RUnlock()
	if ent == nil {
		return "", fmt.Errorf("swarm: unknown space %q", ref)
	}

	m, loaded, cfg, err := s.loadSpace(workdir)
	if err != nil {
		return "", fmt.Errorf("swarm: reset %q: %w", ref, err)
	}

	// Tear any live space down (cancels in-flight runs, shuts agents, closes the
	// DB so its files are free to delete) and drop it from the registry. Detach
	// the live handles under the lock; teardown is a no-op when already stopped.
	s.mu.Lock()
	live := &spaceEntry{cancel: ent.cancel, super: ent.super, space: ent.space, stopPump: ent.stopPump}
	delete(s.spaces, id)
	s.mu.Unlock()
	teardownSpace(live)

	// Wipe durable state so the rebuild is truly blank: the .vero ledger, then
	// every member's transcript under <AppHome>/sessions/<workdir-slug>/ (via the
	// public pkg/agent seam — the swarm never reaches the session store directly).
	if err := store.RemoveData(workdir); err != nil {
		s.log.Warn("swarm: reset: remove .vero", "id", id, "err", err)
	}
	if err := agent.ResetWorkdirSessions(cfg.AppHome, workdir); err != nil {
		s.log.Warn("swarm: reset: clear sessions", "id", id, "err", err)
	}

	// Rebuild fresh under the same id+name (NewSpace re-opens a migrated db;
	// Reload finds nothing to resume, so every member starts with empty context).
	if _, err := s.register(id, name, m, loaded, cfg); err != nil {
		return "", fmt.Errorf("swarm: reset %q: rebuild failed: %w", ref, err)
	}
	s.log.Info("swarm: space reset", "id", id, "name", name, "workdir", workdir)
	return id, nil
}

// spacesFile is the persisted reconcile manifest path, or "" when persistence
// is disabled (no state dir).
func (s *Service) spacesFile() string {
	if s.stateDir == "" {
		return ""
	}
	return filepath.Join(s.stateDir, spacesFileName)
}

// persistedSpace is one space's durable record (SPRD-1-11): enough to rebuild it
// (workdir) under its stable identity (id/name) and current lifecycle (status).
type persistedSpace struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Workdir string `json:"workdir"`
	Status  string `json:"status"`
}

// persistedSpaces is the on-disk shape of spaces.json. Workdirs is the legacy
// pre-naming field — still READ on reconcile so an older state file upgrades
// cleanly, but no longer written.
type persistedSpaces struct {
	Spaces   []persistedSpace `json:"spaces"`
	Workdirs []string         `json:"workdirs,omitempty"`
}

// persistSpaces snapshots every known space (running AND stopped) to spaces.json
// so a later Reconcile restores the exact set — running ones rebuilt, stopped
// ones remembered as stopped. Best-effort: a write failure costs the post-restart
// auto-restore, never live correctness.
func (s *Service) persistSpaces() {
	path := s.spacesFile()
	if path == "" {
		return
	}
	s.mu.RLock()
	recs := make([]persistedSpace, 0, len(s.spaces))
	for _, ent := range s.spaces {
		recs = append(recs, persistedSpace{ID: ent.id, Name: ent.name, Workdir: ent.workdir, Status: string(ent.status)})
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(persistedSpaces{Spaces: recs}, "", "  ")
	if err != nil {
		return
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		s.log.Warn("swarm: persist spaces.json", "err", err)
	}
}

// Reconcile rebuilds every space recorded in spaces.json — the boot path after
// a process death (SPRD-1-11). A per-space failure is logged and skipped so one
// bad workdir never blocks the rest; the first error is returned for the caller
// to surface. A no-op when persistence is disabled or the file is absent.
func (s *Service) Reconcile() error {
	path := s.spacesFile()
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("swarm: read spaces.json: %w", err)
	}
	var ps persistedSpaces
	if err := json.Unmarshal(b, &ps); err != nil {
		return fmt.Errorf("swarm: parse spaces.json: %w", err)
	}

	recs := ps.Spaces
	// Legacy upgrade: an older state file only listed workdirs, all running.
	if len(recs) == 0 {
		for _, wd := range ps.Workdirs {
			recs = append(recs, persistedSpace{Workdir: wd, Status: string(statusRunning)})
		}
	}

	var firstErr error
	for _, rec := range recs {
		if spaceStatus(rec.Status) == statusStopped {
			// A stopped space carries no live parts — restore the record only so
			// it stays addressable (run/ls/rm) without spending tokens.
			id := rec.ID
			if id == "" {
				id = common.GenUUID()
			}
			s.mu.Lock()
			name := rec.Name
			if name == "" || s.nameTakenLocked(name) {
				name = s.genNameLocked()
			}
			s.spaces[id] = &spaceEntry{id: id, name: name, workdir: rec.Workdir, status: statusStopped}
			s.mu.Unlock()
			s.log.Info("swarm: reconciled space (stopped)", "id", id, "name", name, "workdir", rec.Workdir)
			continue
		}
		id, name, err := s.rebuild(rec)
		if err != nil {
			s.log.Warn("swarm: reconcile space failed", "workdir", rec.Workdir, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		s.log.Info("swarm: reconciled space", "workdir", rec.Workdir, "id", id, "name", name)
	}
	s.persistSpaces() // normalise the file to the current shape + any assigned ids/names
	return firstErr
}

// rebuild brings a persisted RUNNING space back up under its stable id+name,
// assigning either when the record predates them (legacy upgrade). Used only by
// Reconcile, which runs single-threaded, so the name check needs no extra guard.
func (s *Service) rebuild(rec persistedSpace) (string, string, error) {
	m, loaded, cfg, err := s.loadSpace(rec.Workdir)
	if err != nil {
		return "", "", err
	}
	id := rec.ID
	if id == "" {
		id = common.GenUUID()
	}
	s.mu.Lock()
	name := rec.Name
	if name == "" {
		name = strings.TrimSpace(m.Name)
	}
	if name == "" || s.nameTakenLocked(name) {
		name = s.genNameLocked()
	}
	s.mu.Unlock()
	if _, err := s.register(id, name, m, loaded, cfg); err != nil {
		return "", "", err
	}
	return id, name, nil
}

// pump drains one space's event stream into the hub for the life of the space.
// On stop it makes a final non-blocking pass so events emitted during Shutdown
// (e.g. a run-cancelled) still reach any connected browser before it exits.
func (s *Service) pump(sp *swarm.SwarmSpace, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			for {
				select {
				case e := <-sp.Events():
					s.publish(e)
				default:
					return
				}
			}
		case e := <-sp.Events():
			s.publish(e)
		}
	}
}

// publish records gate lifecycle for reconnect replay, then marshals one spaced
// event and fans it out by (spaceID, AgentID).
func (s *Service) publish(e swarm.SpacedEvent) {
	if pending, ok := s.pendingFor(e.SpaceID); ok {
		pending.observe(e.Event)
	}
	payload, err := json.Marshal(wireEvent{SpaceID: e.SpaceID, Event: e.Event})
	if err != nil {
		return
	}
	s.hub.Publish(e.SpaceID, e.Event.AgentID, payload)
}

// wireEvent is the JSON envelope pushed over the WebSocket: the raw agent
// event plus the space it belongs to (the AgentID is already on the event).
type wireEvent struct {
	SpaceID string `json:"spaceId"`
	Event   any    `json:"event"`
}

// entry resolves a ref (space id or name) to its RUNNING entry — the common
// runtime path, since every read snapshot and command needs a live space. A
// stopped or unknown ref reports ok=false, which callers map to "unknown space"
// / 404 (you interact with a stopped space only via run/rm/reset).
func (s *Service) entry(ref string) (*spaceEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := s.resolveLocked(ref)
	if !e.live() {
		return nil, false
	}
	return e, true
}

// pendingFor returns a live space's gate tracker, read UNDER the lock so a
// concurrent StopSpace (which nils ent.pending under the write lock) can't race
// the field access — the bug entry()'s callers hit by dereferencing ent.pending
// after the lock was already dropped. The returned tracker is safe to use once
// the lock releases: it has its own mutex, and StopSpace only detaches the
// reference, never mutates the object behind it.
func (s *Service) pendingFor(ref string) (*gateTracker, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e := s.resolveLocked(ref)
	if !e.live() || e.pending == nil {
		return nil, false
	}
	return e.pending, true
}

// resolveLocked finds an entry by id first, then by name (any status); nil when
// neither matches. Caller holds s.mu (R or W). ids are UUIDs and names are human
// handles, so the two namespaces don't overlap — id-first is unambiguous.
func (s *Service) resolveLocked(ref string) *spaceEntry {
	if e, ok := s.spaces[ref]; ok {
		return e
	}
	for _, e := range s.spaces {
		if e.name == ref {
			return e
		}
	}
	return nil
}

// --- webapi.Backend implementation ---------------------------------------

// HasSpace reports whether a space id is registered.
func (s *Service) HasSpace(id string) bool {
	_, ok := s.entry(id)
	return ok
}

// ListSpaces returns a snapshot of every known space — running AND stopped
// (GET /api/swarms), like `docker ps -a`. Member count is the live roster for a
// running space, 0 for a stopped one (no live roster to count).
func (s *Service) ListSpaces() []webapi.SpaceInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]webapi.SpaceInfo, 0, len(s.spaces))
	for _, ent := range s.spaces {
		members := 0
		if ent.live() {
			members = len(ent.space.Roster.Snapshot())
		}
		out = append(out, webapi.SpaceInfo{
			ID:      ent.id,
			Name:    ent.name,
			Workdir: ent.workdir,
			Status:  string(ent.status),
			Members: members,
		})
	}
	return out
}

// Spaces satisfies webapi.Backend.
func (s *Service) Spaces() []webapi.SpaceInfo { return s.ListSpaces() }

func (s *Service) Roster(id string) ([]webapi.MemberInfo, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return nil, false
	}
	views := ent.space.Roster.Snapshot()
	out := make([]webapi.MemberInfo, 0, len(views))
	for _, v := range views {
		var agentID string
		if ctl, ok := ent.space.Roster.Controller(v.Name); ok {
			agentID = ctl.AgentID()
		}
		mi := webapi.MemberInfo{
			Name:        v.Name,
			AgentID:     agentID,
			Role:        string(v.Role),
			Membership:  string(v.Membership),
			Run:         string(v.Run),
			Phase:       string(v.Phase),
			Tool:        v.Tool,
			PhaseSince:  v.PhaseSince,
			CurrentTask: v.CurrentTask,
			WhenToUse:   v.WhenToUse,
		}
		// Schedule lives in the space's map (RP-7 didn't put it on MemberView);
		// surface it on the wire so the roster card can show/edit the crontab (RP-8).
		if sch, ok := ent.space.ScheduleFor(v.Name); ok {
			mi.Cron, mi.SchedulePrompt = sch.Cron, sch.Prompt
		}
		out = append(out, mi)
	}
	return out, true
}

// boardCompletedPreview is how many of the newest completed tasks the board
// snapshot carries: the completed column shows these plus the total count, while
// the full history is paged on-demand via TasksByStatus (the Completed tab) (RP-6).
const boardCompletedPreview = 5

// activeStatuses are the four non-terminal states — the board's live columns.
func activeStatuses() []store.Status {
	return []store.Status{store.StatusPending, store.StatusRunning, store.StatusSuspended, store.StatusVerifying}
}

func toTaskInfo(t store.Task) webapi.TaskInfo {
	return webapi.TaskInfo{
		ID: t.ID, Title: t.Title, Spec: t.Spec, Status: string(t.Status),
		Assignee: t.Assignee, CreatedBy: t.CreatedBy, Result: t.Result,
		VerifyNote: t.VerifyNote, ParentID: t.ParentID,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

func toTaskInfos(tasks []store.Task) []webapi.TaskInfo {
	out := make([]webapi.TaskInfo, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, toTaskInfo(t))
	}
	return out
}

// Tasks is the board snapshot: all active tasks (oldest-first) + the newest few
// completed, with Total = the full completed count. Bounded so the 2.5s board
// poll never re-ships the whole (monotonic) completed history (RP-6).
func (s *Service) Tasks(id string) (webapi.TaskPage, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return webapi.TaskPage{}, false
	}
	st := ent.space.Store
	active, err := st.ListTasks(store.TaskFilter{Statuses: activeStatuses()})
	if err != nil {
		s.log.Warn("swarm: list active tasks", "space", id, "err", err)
		return webapi.TaskPage{Tasks: []webapi.TaskInfo{}}, true
	}
	recent, err := st.ListTasks(store.TaskFilter{Status: store.StatusCompleted, Limit: boardCompletedPreview, Newest: true})
	if err != nil {
		s.log.Warn("swarm: list recent completed", "space", id, "err", err)
	}
	total, err := st.CountTasks(store.TaskFilter{Status: store.StatusCompleted})
	if err != nil {
		s.log.Warn("swarm: count completed", "space", id, "err", err)
	}
	return webapi.TaskPage{Tasks: toTaskInfos(append(active, recent...)), Total: total}, true
}

// TasksByStatus is the on-demand paged view of one status (the Completed tab):
// limit/offset page the rows (completed newest-first), Total is the full count
// for that status — so the UI can show "showing N of TOTAL" and page (RP-6).
func (s *Service) TasksByStatus(id, status string, limit, offset int) (webapi.TaskPage, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return webapi.TaskPage{}, false
	}
	st := ent.space.Store
	match := store.TaskFilter{Status: store.Status(status)}
	page := match
	page.Limit, page.Offset, page.Newest = limit, offset, status == string(store.StatusCompleted)
	tasks, err := st.ListTasks(page)
	if err != nil {
		s.log.Warn("swarm: list tasks by status", "space", id, "status", status, "err", err)
		return webapi.TaskPage{Tasks: []webapi.TaskInfo{}}, true
	}
	total, err := st.CountTasks(match)
	if err != nil {
		s.log.Warn("swarm: count tasks by status", "space", id, "status", status, "err", err)
	}
	return webapi.TaskPage{Tasks: toTaskInfos(tasks), Total: total}, true
}

func (s *Service) Messages(id string) ([]webapi.MessageInfo, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return nil, false
	}
	msgs, err := ent.space.Store.ListMessages(0)
	if err != nil {
		s.log.Warn("swarm: list messages", "space", id, "err", err)
		return []webapi.MessageInfo{}, true
	}
	out := make([]webapi.MessageInfo, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, webapi.MessageInfo{
			ID: m.ID, Sender: m.Sender, Recipient: m.Recipient, Subject: m.Subject,
			Body: m.Body, RefTask: m.RefTask, ReadAt: m.ReadAt, ClaimedAt: m.ClaimedAt, CreatedAt: m.CreatedAt,
		})
	}
	return out, true
}

// PendingGates returns the space's outstanding approval/question gate events in
// their raw wire shape, so a reconnecting browser re-renders overlays for members
// still blocked (RP-2 §3.3). false when the space id is unknown.
func (s *Service) PendingGates(id string) ([]any, bool) {
	pending, ok := s.pendingFor(id)
	if !ok {
		return nil, false
	}
	evs := pending.snapshot()
	out := make([]any, 0, len(evs))
	for _, e := range evs {
		out = append(out, e)
	}
	return out, true
}

func (s *Service) Transcript(id, agent string) ([]webapi.TranscriptEntry, bool) {
	ctl, ok := s.controller(id, agent)
	if !ok {
		return nil, false
	}
	msgs := ctl.Messages()
	out := make([]webapi.TranscriptEntry, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, webapi.TranscriptEntry{Role: string(m.Role), Text: m.Content})
	}
	return out, true
}

// Run drives one member for a turn. It is asynchronous: the browser sees the
// turn via the event stream, so the HTTP/WS call returns immediately. A second
// concurrent run on the same agent is the agent layer's concern (it guards).
func (s *Service) Run(id, agent, prompt string) error {
	ctl, ok := s.controller(id, agent)
	if !ok {
		return fmt.Errorf("swarm: unknown space/agent %q/%q", id, agent)
	}
	go func() {
		if _, err := ctl.Run(s.rootCtx, prompt); err != nil {
			s.log.Warn("swarm: web-driven run", "space", id, "agent", agent, "err", err)
		}
	}()
	return nil
}

// SendUserMessage drops an operator message onto a member's mailbox as sender
// "user" (or broadcasts when to == "all"). It deliberately reuses Bus.Send — the
// exact path inter-agent mail takes — so the supervisor's wake/drain delivers it
// without any new orchestration: an idle member is woken (drain A), a busy one
// folds it mid-run (drain B), and the task ledger is untouched. This is the
// non-disruptive core of flat operator↔member comms.
func (s *Service) SendUserMessage(id, to, subject, body string) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("swarm: message body is required")
	}
	// Role-addressing (§3.5): let an operator address "leader" without knowing
	// its member name; "all" and exact names pass through unchanged.
	to = ent.space.Roster.ResolveRecipient(to)
	if to != store.RecipientAll {
		if _, known := ent.space.Roster.Controller(to); !known {
			return fmt.Errorf("swarm: unknown member %q", to)
		}
	}
	_, err := ent.space.Bus.Send(store.Message{
		Sender:    "user",
		Recipient: to,
		Subject:   subject,
		Body:      body,
	})
	return err
}

func (s *Service) RespondPermission(id, agent, reqID, behavior, reason, ruleTool string) error {
	ctl, ok := s.controller(id, agent)
	if !ok {
		return fmt.Errorf("swarm: unknown space/agent %q/%q", id, agent)
	}
	dec := ui.PermissionDecision{Behavior: behavior, Reason: reason}
	// "Always allow": seed a tool-wide session allow rule (empty Content matches
	// every call to that tool) so the agent's gate stops re-prompting for it this
	// session — what makes a coding swarm practical in a non-bypass mode.
	if ruleTool != "" {
		dec.AddRule = &ui.PermissionRuleSeed{ToolName: ruleTool}
	}
	if pending, ok := s.pendingFor(id); ok {
		pending.remove(reqID) // answered — drop it from the reconnect-replay set
	}
	return ctl.RespondPermission(reqID, dec)
}

func (s *Service) RespondQuestion(id, agent, reqID string, answers map[string]string) error {
	ctl, ok := s.controller(id, agent)
	if !ok {
		return fmt.Errorf("swarm: unknown space/agent %q/%q", id, agent)
	}
	if pending, ok := s.pendingFor(id); ok {
		pending.remove(reqID)
	}
	return ctl.RespondQuestion(reqID, ui.QuestionResponse{Answers: answers})
}

func (s *Service) Suspend(id, agent string) error {
	return s.superCmd(id, agent, (*swarm.Supervisor).Suspend)
}
func (s *Service) Resume(id, agent string) error {
	return s.superCmd(id, agent, (*swarm.Supervisor).Resume)
}
func (s *Service) Freeze(id, agent string) error {
	return s.superCmd(id, agent, (*swarm.Supervisor).Freeze)
}
func (s *Service) Unfreeze(id, agent string) error {
	return s.superCmd(id, agent, (*swarm.Supervisor).Unfreeze)
}

// SetSchedule / ClearSchedule are the operator's schedule controls (RP-8). Unlike
// the leader tool (RP-7), there is NO self-guard: the operator may set or clear
// ANY member's schedule, including the leader's — the web is the one place a
// leader's cadence can be changed. Reuses RP-7's live-apply+persist seam.
func (s *Service) SetSchedule(id, agent, cron, prompt string) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	if _, known := ent.space.Roster.Controller(agent); !known {
		return fmt.Errorf("swarm: unknown member %q", agent)
	}
	return ent.super.SetSchedule(agent, agentdef.Schedule{Cron: cron, Prompt: prompt})
}

func (s *Service) ClearSchedule(id, agent string) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	if _, known := ent.space.Roster.Controller(agent); !known {
		return fmt.Errorf("swarm: unknown member %q", agent)
	}
	return ent.super.ClearSchedule(agent)
}

// MemberSkills / AddSkill / DeleteSkill are the operator's agent-skills controls
// (RP-10): list a member's authored skills, author a new one (writes SKILL.md +
// reloads that member's prompt), or remove one. The author path is User/web only —
// there is no agent-facing skill-authoring tool. The space owns the disk layout +
// reload; the service is a thin adapter.
func (s *Service) MemberSkills(id, agent string) ([]webapi.SkillInfo, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return nil, false
	}
	skills, err := ent.space.MemberSkills(agent)
	if err != nil {
		return nil, false // unknown member within a known space → 404
	}
	out := make([]webapi.SkillInfo, 0, len(skills))
	for _, sk := range skills {
		out = append(out, webapi.SkillInfo{Name: sk.Name, Description: sk.Description})
	}
	return out, true
}

func (s *Service) AddSkill(id, agent string, spec webapi.SkillSpec) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	return ent.space.AddMemberSkill(agent, spec.Name, spec.Description, spec.Body)
}

func (s *Service) DeleteSkill(id, agent, skill string) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	return ent.space.RemoveMemberSkill(agent, skill)
}

// CreateMember authors a new worker from the web form (RP-8): hot-load it live,
// record it in the manifest so it survives a restart, then tell the leader (only
// the when_to_use) so its team model updates immediately.
func (s *Service) CreateMember(id string, spec webapi.MemberSpec) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	domain := agentdef.MemberSpec{
		Name:         spec.Name,
		SystemPrompt: spec.SystemPrompt,
		WhenToUse:    spec.WhenToUse,
		Active:       toToolNames(spec.Active),
		Deferred:     toToolNames(spec.Deferred),
	}
	if strings.TrimSpace(spec.Cron) != "" {
		domain.Schedule = &agentdef.Schedule{Cron: spec.Cron, Prompt: spec.Prompt}
	}
	if err := ent.super.CreateMember(domain); err != nil {
		return err
	}
	// Keep the manifest authoritative so the member survives a restart (the user
	// chose manifest-rewrite). Best-effort: the member is already live either way.
	if err := s.addManifestWorker(ent, spec.Name); err != nil {
		s.log.Warn("swarm: member created but manifest update failed", "member", spec.Name, "err", err)
	}
	s.notifyLeader(ent, "New teammate joined",
		fmt.Sprintf("A new teammate %q has joined the team. When to use: %s. Assign it tasks when it fits.", spec.Name, spec.WhenToUse))
	return nil
}

// RemoveMember retires a worker (RP-8). The manifest is updated BEFORE an optional
// dir delete so a restart can never rebuild a member whose dir is gone. The leader
// is told to reassign the departed member's unfinished work.
func (s *Service) RemoveMember(id, agent string, deleteDir bool) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	if err := ent.super.RemoveMember(agent); err != nil {
		return err
	}
	if err := s.removeManifestWorker(ent, agent); err != nil {
		s.log.Warn("swarm: member removed but manifest update failed", "member", agent, "err", err)
	}
	if deleteDir {
		if err := agentdef.RemoveMemberDir(ent.workdir, agent); err != nil {
			s.log.Warn("swarm: delete member dir", "member", agent, "err", err)
		}
	}
	s.notifyLeader(ent, "Teammate left the team",
		fmt.Sprintf("Teammate %q has left the team. Reassign any of its unfinished tasks.", agent))
	return nil
}

// SelectableTools is the catalog the add-agent form offers: every globally
// registered tool minus operator/runtime-only ones. The swarm collaboration
// tools are already absent (registered per-agent via WithCustomTool, never in the
// global registry); they are listed in the deny set anyway as belt-and-braces.
func (s *Service) SelectableTools() []string {
	deny := map[string]bool{
		"tool_search": true, "skill": true, "schedule_wakeup": true,
		"enter_plan_mode": true, "exit_plan_mode": true,
		"enter_worktree": true, "exit_worktree": true,
		"ask_user_question": true, "push_notification": true,
		"feedback": true, "config": true,
		"cron_create": true, "cron_list": true, "cron_delete": true, "remote_trigger": true,
		// collaboration tools — role-injected, not in the global registry:
		"send_message": true, "list_members": true,
		"task_create": true, "task_assign": true, "task_update_status": true,
		"task_verify": true, "task_list": true, "my_tasks": true, "task_get": true,
		"schedule_set": true, "schedule_clear": true,
	}
	var out []string
	for _, n := range toolset.DefaultRegistry().Names() {
		if !deny[string(n)] {
			out = append(out, string(n))
		}
	}
	sort.Strings(out)
	return out
}

// toToolNames converts wire tool-name strings to the typed list, dropping blanks.
func toToolNames(ss []string) []tools.ToolName {
	out := make([]tools.ToolName, 0, len(ss))
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			out = append(out, tools.ToolName(s))
		}
	}
	return out
}

// addManifestWorker / removeManifestWorker keep evva-swarm.yml in step with the
// live roster so dynamic membership survives a restart (the rebuild reads it).
func (s *Service) addManifestWorker(ent *spaceEntry, name string) error {
	path := filepath.Join(ent.workdir, manifestFile)
	m, err := agentdef.LoadManifest(path)
	if err != nil {
		return err
	}
	if err := m.AddWorker(name); err != nil {
		return err
	}
	return agentdef.WriteManifest(path, m)
}

func (s *Service) removeManifestWorker(ent *spaceEntry, name string) error {
	path := filepath.Join(ent.workdir, manifestFile)
	m, err := agentdef.LoadManifest(path)
	if err != nil {
		return err
	}
	m.RemoveWorker(name)
	return agentdef.WriteManifest(path, m)
}

// notifyLeader drops a system-authored message onto the leader's mailbox (RP-8).
// Sender "system" distinguishes it from operator ("user") and teammate mail; it
// rides the same bus+drain, so a busy leader folds it mid-run (drain B).
func (s *Service) notifyLeader(ent *spaceEntry, subject, body string) {
	leader := ent.space.Roster.ResolveRecipient("leader")
	if _, ok := ent.space.Roster.Controller(leader); !ok {
		return
	}
	if _, err := ent.space.Bus.Send(store.Message{Sender: "system", Recipient: leader, Subject: subject, Body: body}); err != nil {
		s.log.Warn("swarm: notify leader", "err", err)
	}
}

// maxEventDataChars caps the verbatim `data` payload folded into the leader's
// prompt, so a huge structured blob can't blow the context.
const maxEventDataChars = 4000

// IngestEvent turns an external webhook event into a single message on the target
// member's mailbox (RP-9). It adds no orchestration: the supervisor's existing
// wake/fold drives the leader (idle → drain A, busy → drain B). `to` defaults to
// the leader; the message is sender "webhook" and time-stamped so the leader sees
// it as an external trigger. An idempotency key collapses retries (duplicate ==
// true, same id, no second wake). Errors carry "unknown space" / "stopped" so the
// (unauthenticated) handler can map 404 / 409; anything else is 400.
func (s *Service) IngestEvent(ref string, evt webapi.EventIn) (messageID string, duplicate bool, err error) {
	ent, ok := s.entry(ref)
	if !ok {
		s.mu.RLock()
		exists := s.resolveLocked(ref) != nil
		s.mu.RUnlock()
		if exists {
			return "", false, fmt.Errorf("swarm: space %q is stopped", ref)
		}
		return "", false, fmt.Errorf("swarm: unknown space %q", ref)
	}
	if strings.TrimSpace(evt.Body) == "" {
		return "", false, fmt.Errorf("swarm: event body is required")
	}
	to := strings.TrimSpace(evt.To)
	if to == "" {
		to = "leader"
	}
	to = ent.space.Roster.ResolveRecipient(to)
	if _, known := ent.space.Roster.Controller(to); !known {
		return "", false, fmt.Errorf("swarm: no member %q in space %q", to, ref)
	}

	subject := "[event]"
	if t := strings.TrimSpace(evt.Title); t != "" {
		subject = "[event] " + t
	}
	source := strings.TrimSpace(evt.Source)
	if source == "" {
		source = "external"
	}
	id, dup, err := ent.space.Bus.SendExternal(store.Message{
		Sender:    "webhook",
		Recipient: to,
		Subject:   subject,
		Body:      shapeEvent(source, evt.Body, evt.Data),
	}, strings.TrimSpace(evt.IdempotencyKey))
	if err != nil {
		return "", false, fmt.Errorf("swarm: ingest event: %w", err)
	}
	return id, dup, nil
}

// shapeEvent frames an external event as the leader's run-start prompt: a
// <system-reminder> carrying the source and the trigger time (the one place a
// wall-clock time enters the conversation, as with RP-7's timer wake) plus the
// verbatim (truncated) data payload, so the leader recognises it as an external
// signal to assess and act on rather than chatter.
func shapeEvent(source, body string, data json.RawMessage) string {
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	fmt.Fprintf(&b, "external-event  source=%s  time=%s\n", source, time.Now().Format("2006-01-02 15:04:05"))
	b.WriteString(body)
	if d := strings.TrimSpace(string(data)); d != "" && d != "null" {
		if len(d) > maxEventDataChars {
			d = d[:maxEventDataChars] + "…(truncated)"
		}
		b.WriteString("\ndata: ")
		b.WriteString(d)
	}
	b.WriteString("\n</system-reminder>")
	return b.String()
}

func (s *Service) HaltAll(id string) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	return ent.super.HaltAll()
}

// superCmd routes a one-member supervisor command, surfacing an "unknown space"
// error the HTTP layer maps to 404.
func (s *Service) superCmd(id, agent string, fn func(*swarm.Supervisor, string) error) error {
	ent, ok := s.entry(id)
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}
	return fn(ent.super, agent)
}

// controller resolves a member's Controller within a space. agent may be either
// the member name (internal callers) or the controller AgentID (the web
// approval/question reply path echoes back the event's AgentID), so it resolves
// by ref — see Roster.ControllerRef.
func (s *Service) controller(id, agent string) (ui.Controller, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return nil, false
	}
	return ent.space.Roster.ControllerRef(agent)
}
