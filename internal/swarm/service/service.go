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
	"path/filepath"
	"sync"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	swarmtools "github.com/johnny1110/evva/internal/swarm/tools"
	"github.com/johnny1110/evva/internal/swarm/webapi"
	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/config"
	"github.com/johnny1110/evva/pkg/ui"
	"github.com/johnny1110/evva/web"
)

// DefaultAddr is the loopback bind the service uses unless overridden. Binding
// to 127.0.0.1 (not 0.0.0.0) is the security baseline (invariant #6): agents
// run shell and edit files, so the workstation is RCE-equivalent and must not
// be reachable off-box by default.
const DefaultAddr = "127.0.0.1:8888"

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

	// loadConfig builds the per-space *config.Config for a workdir. Overridable
	// in tests to inject a stub LLM provider without touching disk/env.
	loadConfig func(workdir string) (*config.Config, error)
}

// spaceEntry holds one live space plus the handles needed to tear it down
// independently of its siblings.
type spaceEntry struct {
	space    *swarm.SwarmSpace
	super    *swarm.Supervisor
	cancel   context.CancelFunc // stops the supervisor's run loops + timer tick
	stopPump chan struct{}      // closed after Shutdown so the pump drains then exits
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
		token:      common.GenUUID(),
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

// Stop tears down every space and the HTTP server. Safe to call once; the
// rootCtx cancel cascades to all supervisors and pumps.
func (s *Service) Stop() error {
	for _, id := range s.spaceIDs() {
		_ = s.StopSpace(id)
	}
	s.rootCancel()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.srv.Shutdown(shutCtx)
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
// space up as a new isolated member of the registry. Returns the generated
// space id. This is the production path the `evva swarm .` CLI (SPRD-1-9) calls.
func (s *Service) Register(workdir string) (string, error) {
	abs, err := filepath.Abs(workdir)
	if err != nil {
		return "", fmt.Errorf("swarm: resolve workdir %q: %w", workdir, err)
	}
	cfg, err := s.loadConfig(abs)
	if err != nil {
		return "", fmt.Errorf("swarm: load config for %q: %w", abs, err)
	}
	m, err := agentdef.LoadManifest(filepath.Join(abs, manifestFile))
	if err != nil {
		return "", err
	}
	loaded, warnings, err := agentdef.NewLoader().BuildAll(abs, m)
	if err != nil {
		return "", err
	}
	for _, w := range warnings {
		s.log.Warn("swarm: agent load warning", "agent", w.Agent, "msg", w.Msg)
	}
	return s.register(m, loaded, cfg)
}

// register is the shared bring-up core: assemble the space, start its
// supervisor and event pump under a fresh child context, and add it to the
// registry. Split out so tests can register a stub-LLM space without disk/env.
func (s *Service) register(m agentdef.Manifest, loaded []agentdef.Loaded, cfg *config.Config) (string, error) {
	id := common.GenUUID()
	sp, err := swarm.NewSpace(id, m, loaded, swarmtools.Set{}, cfg)
	if err != nil {
		return "", err
	}

	super := swarm.NewSupervisor(sp)
	spaceCtx, cancel := context.WithCancel(s.rootCtx)
	super.Start(spaceCtx)

	stopPump := make(chan struct{})
	s.mu.Lock()
	s.spaces[id] = &spaceEntry{space: sp, super: super, cancel: cancel, stopPump: stopPump}
	s.mu.Unlock()

	go s.pump(sp, stopPump)
	s.log.Info("swarm: space registered", "id", id, "name", m.Name, "members", len(loaded))
	return id, nil
}

// StopSpace tears one space down without touching the others (AC#2 isolation):
// stop its supervisor, shut its agents + store down, then drain and stop the
// pump. An unknown id is an error.
func (s *Service) StopSpace(id string) error {
	s.mu.Lock()
	ent, ok := s.spaces[id]
	if ok {
		delete(s.spaces, id)
	}
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("swarm: unknown space %q", id)
	}

	ent.cancel()           // stop run loops + timer (no new runs)
	ent.space.Shutdown()   // cancel agents + close store; trailing events still buffered
	close(ent.stopPump)    // pump does a final drain, then exits
	s.log.Info("swarm: space stopped", "id", id)
	return nil
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

// publish marshals one spaced event and fans it out by (spaceID, AgentID).
func (s *Service) publish(e swarm.SpacedEvent) {
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

func (s *Service) spaceIDs() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ids := make([]string, 0, len(s.spaces))
	for id := range s.spaces {
		ids = append(ids, id)
	}
	return ids
}

func (s *Service) entry(id string) (*spaceEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ent, ok := s.spaces[id]
	return ent, ok
}

// --- webapi.Backend implementation ---------------------------------------

// HasSpace reports whether a space id is registered.
func (s *Service) HasSpace(id string) bool {
	_, ok := s.entry(id)
	return ok
}

// ListSpaces returns a snapshot of every registered space (GET /api/swarms).
func (s *Service) ListSpaces() []webapi.SpaceInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]webapi.SpaceInfo, 0, len(s.spaces))
	for id, ent := range s.spaces {
		out = append(out, webapi.SpaceInfo{
			ID:      id,
			Name:    ent.space.Name,
			Workdir: ent.space.Workdir,
			Members: len(ent.space.Roster.Snapshot()),
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
		out = append(out, webapi.MemberInfo{
			Name:        v.Name,
			Role:        string(v.Role),
			Membership:  string(v.Membership),
			Run:         string(v.Run),
			CurrentTask: v.CurrentTask,
			WhenToUse:   v.WhenToUse,
		})
	}
	return out, true
}

func (s *Service) Tasks(id string) ([]webapi.TaskInfo, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return nil, false
	}
	tasks, err := ent.space.Store.ListTasks(store.TaskFilter{})
	if err != nil {
		s.log.Warn("swarm: list tasks", "space", id, "err", err)
		return []webapi.TaskInfo{}, true
	}
	out := make([]webapi.TaskInfo, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, webapi.TaskInfo{
			ID: t.ID, Title: t.Title, Spec: t.Spec, Status: string(t.Status),
			Assignee: t.Assignee, CreatedBy: t.CreatedBy, Result: t.Result,
			VerifyNote: t.VerifyNote, ParentID: t.ParentID,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		})
	}
	return out, true
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
			Body: m.Body, RefTask: m.RefTask, ReadAt: m.ReadAt, CreatedAt: m.CreatedAt,
		})
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

func (s *Service) RespondPermission(id, agent, reqID, behavior, reason string) error {
	ctl, ok := s.controller(id, agent)
	if !ok {
		return fmt.Errorf("swarm: unknown space/agent %q/%q", id, agent)
	}
	return ctl.RespondPermission(reqID, ui.PermissionDecision{Behavior: behavior, Reason: reason})
}

func (s *Service) RespondQuestion(id, agent, reqID string, answers map[string]string) error {
	ctl, ok := s.controller(id, agent)
	if !ok {
		return fmt.Errorf("swarm: unknown space/agent %q/%q", id, agent)
	}
	return ctl.RespondQuestion(reqID, ui.QuestionResponse{Answers: answers})
}

func (s *Service) Suspend(id, agent string) error  { return s.superCmd(id, agent, (*swarm.Supervisor).Suspend) }
func (s *Service) Resume(id, agent string) error   { return s.superCmd(id, agent, (*swarm.Supervisor).Resume) }
func (s *Service) Freeze(id, agent string) error   { return s.superCmd(id, agent, (*swarm.Supervisor).Freeze) }
func (s *Service) Unfreeze(id, agent string) error { return s.superCmd(id, agent, (*swarm.Supervisor).Unfreeze) }

func (s *Service) AddMember(id, agent string) error {
	return s.superCmd(id, agent, (*swarm.Supervisor).AddMember)
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

// controller resolves a member's Controller within a space.
func (s *Service) controller(id, agent string) (ui.Controller, bool) {
	ent, ok := s.entry(id)
	if !ok {
		return nil, false
	}
	return ent.space.Roster.Controller(agent)
}
