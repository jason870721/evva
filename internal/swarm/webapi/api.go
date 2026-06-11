package webapi

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/net/websocket"
)

// Backend is everything the HTTP/WS layer needs from the swarm host. It is a
// narrow translation seam: handlers turn JSON into these calls and expose
// nothing beyond them (invariant #1). internal/swarm/service implements it over
// its SwarmSpace registry + per-space Supervisors. The DTOs below keep this
// package free of any agent/store/llm imports, so the wire shape is owned here,
// not leaked from the domain.
type Backend interface {
	// Token is the session token every /api and /ws request must present.
	Token() string

	// AllowRemote reports whether the host was started with the non-loopback
	// opt-in (RP-15). When true the loopback-trust conveniences are off: the
	// router hides the GET /api/auth/bootstrap endpoint entirely (behind a
	// reverse proxy every caller LOOKS loopback, so the endpoint would leak
	// the token to the world).
	AllowRemote() bool

	// HasSpace reports whether a space id is registered (the WS subscribe guard).
	HasSpace(spaceID string) bool

	// Lifecycle (Docker-style). Register brings a NEW space up from a workdir
	// with an optional explicit name (POST /api/swarms). StopSpace stops a running
	// space but KEEPS it as "stopped" (POST /api/swarm/:ref/stop); RunSpace
	// rebuilds a stopped one under its same id/name (POST /api/swarm/:ref/run);
	// RemoveSpace forgets it entirely (DELETE /api/swarm/:ref). ResetSpace wipes it
	// to a blank slate — fresh ledger + cleared agent context — under the SAME id
	// (POST /api/swarm/:ref/reset). For all of the above except Register, ref is a
	// space id OR its name. Register/Run/Reset return the (stable) space id.
	Register(workdir, name string) (string, error)
	StopSpace(ref string) error
	RunSpace(ref string) (string, error)
	RemoveSpace(ref string) error
	ResetSpace(ref string) (string, error)

	// Read snapshots. The bool is false when the space id is unknown.
	Spaces() []SpaceInfo
	Roster(spaceID string) ([]MemberInfo, bool)
	// Tasks is the board snapshot: every active (non-completed) task plus only
	// the most recent few completed; TaskPage.Total is the full completed count,
	// so the 2.5s board poll stays small however much history accumulates (RP-6).
	Tasks(spaceID string) (TaskPage, bool)
	// TasksByStatus is the on-demand paged view of one status (the Completed tab):
	// limit/offset page the rows (completed newest-first), Total is the full count.
	TasksByStatus(spaceID, status string, limit, offset int) (TaskPage, bool)
	Messages(spaceID string) ([]MessageInfo, bool)
	// Proposals lists the space's worker-filed work proposals (RP-23), every
	// status, oldest first — the web's bottom-up inbox.
	Proposals(spaceID string) ([]ProposalInfo, bool)
	Transcript(spaceID, agent string) ([]TranscriptEntry, bool)
	// PendingGates returns the space's outstanding approval/question gates as raw
	// wire events (same shape the WS sends), so a reconnecting browser re-renders
	// overlays for members still blocked instead of leaving them hung (RP-2 §3.3).
	PendingGates(spaceID string) ([]any, bool)

	// SendUserMessage delivers an operator message onto a member's mailbox as
	// sender "user" (or broadcasts when to == "all"). It rides the same bus +
	// drain path as inter-agent mail, so an idle member is woken and a busy one
	// folds it mid-run — flat operator↔member comms without disturbing the
	// workflow. See docs/roadmap/veronica/direction-flat-comms.md.
	SendUserMessage(spaceID, to, subject, body string) error

	// Inbound commands. Run is asynchronous — it kicks off a turn whose events
	// stream back over the WebSocket; the rest are immediate.
	Run(spaceID, agent, prompt string) error
	// RespondPermission delivers an approval reply. A non-empty ruleTool means
	// the operator picked "Always allow" — add a session-scope allow rule for
	// that tool so it stops re-prompting for the rest of the session.
	RespondPermission(spaceID, agent, reqID, behavior, reason, ruleTool string) error
	RespondQuestion(spaceID, agent, reqID string, answers map[string][]string) error
	Suspend(spaceID, agent string) error
	Resume(spaceID, agent string) error
	Freeze(spaceID, agent string) error
	Unfreeze(spaceID, agent string) error
	HaltAll(spaceID string) error

	// Schedule CRUD (RP-8). The web path has NO self-guard — the operator may
	// set/clear ANY member's schedule, including the leader's (the symmetric
	// complement to the leader tool, which refuses to reschedule itself, RP-7).
	// A bad cron is a validation error the handler maps to 400.
	SetSchedule(spaceID, agent, cron, prompt string) error
	ClearSchedule(spaceID, agent string) error

	// Membership editing (RP-8). CreateMember authors a new worker from a spec
	// (writes its dir, hot-loads it, records it in the manifest); RemoveMember
	// retires one (deleteDir also erases its on-disk definition). The leader is
	// unique — neither can target it. SelectableTools is the catalog the add-agent
	// form offers (collaboration tools excluded — they are role-injected).
	CreateMember(spaceID string, spec MemberSpec) error
	RemoveMember(spaceID, agent string, deleteDir bool) error
	SelectableTools() []string
	// SelectableModels is the model catalog the add-agent form offers for the
	// optional per-member model pin (every model of every built-in provider).
	SelectableModels() []string

	// IngestEvent delivers an external webhook event (RP-9) onto a member's
	// mailbox (default the leader), waking it through the ordinary bus path — a
	// webhook is just a message. duplicate is true when the idempotency key was
	// already seen (no second delivery/wake). Errors carry "unauthorized" /
	// "unknown space" / "stopped" so the handler can map 401 / 404 / 409. The
	// route skips the session-token guard; instead the backend authorizes from
	// auth (RP-15): a space-configured webhook_secret must match for everyone,
	// and without one only loopback peers pass (the RP-9 trust boundary).
	IngestEvent(ref string, evt EventIn, auth EventAuth) (messageID string, duplicate bool, err error)

	// Agent skills (RP-10). MemberSkills lists a member's authored skills (bool false
	// when the space/member is unknown); AddSkill writes a new SKILL.md and hot-reloads
	// that member's prompt; DeleteSkill removes one. Author path is User/web ONLY —
	// agents never author skills, only load them. Add/Delete errors carry "unknown" for
	// a missing space/member (→ 404); bad input (illegal/duplicate name, empty body) is
	// 400.
	MemberSkills(spaceID, agent string) ([]SkillInfo, bool)
	AddSkill(spaceID, agent string, spec SkillSpec) error
	DeleteSkill(spaceID, agent, skill string) error

	// Vacuum runs one ledger retention pass now (RP-16): archive-then-delete
	// messages read ≥ days ago and tasks completed ≥ days ago. days <= 0 uses
	// the space's configured window (or the default); dryRun only counts.
	Vacuum(ref string, days int, dryRun bool) (VacuumStats, error)

	// Metrics snapshots a space's scheduler counters (RP-17): per-member
	// wakes/runs/aborts + run-duration buckets, bus hint drops, and the event
	// log's logged/dropped counts. false = unknown or stopped space.
	Metrics(spaceID string) (MetricsInfo, bool)

	// Health is the unauthenticated liveness snapshot (RP-18): version,
	// uptime, and aggregate counts only — enough to tell "alive but idle"
	// from "in service" with one curl, while leaking no names or detail.
	Health() HealthInfo
}

// SpaceInfo is one row of GET /api/swarms. Status is "running" | "stopped"
// (the list is like `docker ps -a` — stopped spaces are shown too).
type SpaceInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Workdir string `json:"workdir"`
	Status  string `json:"status"`
	Members int    `json:"members"`
}

// MemberInfo mirrors swarm.MemberView on the wire (GET /api/swarm/:id).
// AgentID is the event-stream identity, so the web can demux the per-(space,
// agent) WS feed into a focused per-member console.
type MemberInfo struct {
	Name        string `json:"name"`
	AgentID     string `json:"agentId"`
	Role        string `json:"role"`
	Membership  string `json:"membership"`
	Run         string `json:"run"`                  // coarse lifecycle: idle | busy | suspended
	Phase       string `json:"phase,omitempty"`      // fine, event-derived sub-phase (RP-3)
	Tool        string `json:"tool,omitempty"`       // tool name for executing / waiting-approval
	PhaseSince  int64  `json:"phaseSince,omitempty"` // unix millis the phase was entered (RP-4 timing)
	CurrentTask int64  `json:"currentTask"`
	WhenToUse   string `json:"whenToUse,omitempty"`
	// ContextTokens / ContextLimit drive the per-member context-utilization meter
	// (the web roster's CTX bar). ContextTokens is the input-token count of the
	// member's most recent turn — how full its prompt is right now; ContextLimit
	// is its model's context window. They are the same pair evva's TUI status bar
	// reads (controller.LastTurnInputTokens / constant.MODEL_CONTEXT_SIZE; see
	// pkg/ui/.../status.SetContext). ContextLimit is 0 when the model is unknown
	// (custom/stub models absent from the context-size table) — the UI then shows
	// an unknown rail with no %. Not omitempty: 0 is meaningful (no turn yet /
	// unknown window) and the TS contract expects both fields always present.
	ContextTokens int `json:"contextTokens"`
	ContextLimit  int `json:"contextLimit"`
	// Token meter (RP-13): cumulative session input/output tokens as of the
	// member's last run boundary, today's spend, and the member's effective
	// daily budget (0 = unlimited). TokensToday vs TokensBudget is the budget
	// breaker's gauge; a frozen membership plus an exhausted gauge reads as
	// "frozen by the breaker".
	TokensIn     int `json:"tokensIn,omitempty"`
	TokensOut    int `json:"tokensOut,omitempty"`
	TokensToday  int `json:"tokensToday,omitempty"`
	TokensBudget int `json:"tokensBudget,omitempty"`
	// Cron / SchedulePrompt expose the member's recurring timer (RP-7/RP-8), read
	// live from the space's schedule map (the schedule's owner — it is NOT on
	// MemberView). Empty when the member has no schedule.
	Cron           string `json:"cron,omitempty"`
	SchedulePrompt string `json:"schedulePrompt,omitempty"`
}

// MemberSpec is the wire shape of the web "add agent" form (RP-8): the operator
// authors a new worker. Collaboration tools are role-injected at construction, so
// they never appear here. Cron/Prompt are an optional recurring schedule.
type MemberSpec struct {
	Name         string   `json:"name"`
	SystemPrompt string   `json:"systemPrompt"`
	WhenToUse    string   `json:"whenToUse"`
	Model        string   `json:"model"`  // optional model pin; "" = configured default. Fixed at creation.
	Effort       string   `json:"effort"` // optional effort pin (low|medium|high|ultra); "" = default. Fixed at creation.
	Active       []string `json:"active"`
	Deferred     []string `json:"deferred"`
	Cron         string   `json:"cron"`
	Prompt       string   `json:"prompt"`
}

// SkillInfo is one row of GET /api/agents/{name}/skills (RP-10): a member's authored
// skill — name + description, the same pair the prompt's # Skills section lists.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// SkillSpec is the body of POST /api/agents/{name}/skills (RP-10): the operator
// authors a skill. The first line of the written SKILL.md becomes `# <name>
// <description>`; Body is the rest (the instructions the skill tool loads).
type SkillSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
}

// TaskInfo mirrors store.Task on the wire (GET /api/tasks).
type TaskInfo struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Spec       string `json:"spec"`
	Status     string `json:"status"`
	Assignee   string `json:"assignee"`
	CreatedBy  string `json:"createdBy"`
	Result     string `json:"result,omitempty"`
	VerifyNote string `json:"verifyNote,omitempty"`
	ParentID   *int64 `json:"parentId,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// TaskPage is a bounded slice of tasks plus the full match total, so a paged
// client can render "N of TOTAL" and decide whether to fetch more (RP-6). On the
// board snapshot, Total is the completed count (Tasks holds active + a preview).
type TaskPage struct {
	Tasks []TaskInfo `json:"tasks"`
	Total int        `json:"total"`
}

// EventIn is the body of POST /api/swarm/{id}/event — an external app's signal
// (RP-9). Only Body is required. Source/Title give the leader provenance; Data is
// carried verbatim; To defaults to the leader; IdempotencyKey collapses retries.
type EventIn struct {
	Title          string          `json:"title"`
	Body           string          `json:"body"`
	Source         string          `json:"source"`
	Data           json.RawMessage `json:"data"`
	To             string          `json:"to"`
	IdempotencyKey string          `json:"idempotency_key"`
}

// maxEventBytes bounds a webhook request body — the endpoint skips the session
// token (RP-9 loopback trust / RP-15 webhook secret), so cap the read defensively.
const maxEventBytes = 64 << 10

// WebhookSecretHeader carries a space's shared webhook secret on event POSTs
// (RP-15). Only consulted for spaces that set settings.webhook_secret.
const WebhookSecretHeader = "X-Evva-Webhook-Secret"

// EventAuth is what the transport observed about an event POST's caller: the
// presented shared secret (may be empty) and whether the TCP peer was a
// loopback address. The router only reports; the backend decides (RP-15).
type EventAuth struct {
	Secret   string
	Loopback bool
}

// VacuumStats is the result of POST /api/swarm/{id}/vacuum (RP-16): how many
// rows one retention pass archived + deleted (or, on a dry run, would have),
// the archive files appended, and the effective window used.
type VacuumStats struct {
	Messages int      `json:"messages"`
	Tasks    int      `json:"tasks"`
	Files    []string `json:"files,omitempty"`
	Days     int      `json:"days"`
	DryRun   bool     `json:"dryRun"`
}

// HealthInfo is GET /healthz (RP-18). The endpoint stays unauthenticated
// (liveness probes can't hold a token), so it deliberately carries no names,
// ids, or workdirs — per-space detail lives behind the guard
// (GET /api/swarms, /api/swarm/{id}/metrics).
type HealthInfo struct {
	Status        string `json:"status"` // always "ok" when the host answers
	Version       string `json:"version"`
	UptimeSecs    int64  `json:"uptimeSecs"`
	SpacesRunning int    `json:"spacesRunning"`
	SpacesStopped int    `json:"spacesStopped"`
	MembersActive int    `json:"membersActive"`
	MembersFrozen int    `json:"membersFrozen"`
}

// MetricsInfo is GET /api/swarm/{id}/metrics (RP-17): plain counters, no
// timeseries — the user-side exporter (if any) owns history.
type MetricsInfo struct {
	UptimeSecs    int64 `json:"uptimeSecs"`
	EventsLogged  int64 `json:"eventsLogged"`
	EventsDropped int64 `json:"eventsDropped"`
	HintsDropped  int64 `json:"hintsDropped"`
	// TasksStale / MailboxStale count RP-22 workflow-watchdog notifications
	// sent since the space started (stale-task reminders, backlog alerts).
	TasksStale   int64                        `json:"tasksStale"`
	MailboxStale int64                        `json:"mailboxStale"`
	Members      map[string]MemberMetricsInfo `json:"members"`
}

// MemberMetricsInfo is one member's scheduler counters. RunSeconds buckets
// completed runs by wall-clock duration: lt10s / lt1m / lt10m / gte10m.
type MemberMetricsInfo struct {
	WakesMessage int64            `json:"wakesMessage"`
	WakesTimer   int64            `json:"wakesTimer"`
	Runs         int64            `json:"runs"`
	Aborts       int64            `json:"aborts"`
	RunSeconds   map[string]int64 `json:"runSeconds"`
}

// ProposalInfo mirrors store.Proposal on the wire
// (GET /api/swarm/{id}/proposals, RP-23). Status: open | accepted | declined;
// RefTask is the task an accepted proposal became (0 = none yet).
type ProposalInfo struct {
	ID                int64  `json:"id"`
	Proposer          string `json:"proposer"`
	Title             string `json:"title"`
	Spec              string `json:"spec,omitempty"`
	SuggestedAssignee string `json:"suggestedAssignee,omitempty"`
	Status            string `json:"status"`
	DecidedBy         string `json:"decidedBy,omitempty"`
	DecideNote        string `json:"decideNote,omitempty"`
	RefTask           int64  `json:"refTask,omitempty"`
	CreatedAt         int64  `json:"createdAt"`
	DecidedAt         int64  `json:"decidedAt,omitempty"`
}

// MessageInfo mirrors store.Message on the wire (GET /api/messages).
type MessageInfo struct {
	ID        string `json:"id"`
	Sender    string `json:"sender"`
	Recipient string `json:"recipient"`
	Subject   string `json:"subject,omitempty"`
	Body      string `json:"body"`
	RefTask   *int64 `json:"refTask,omitempty"`
	// ReadAt / ClaimedAt expose the unread→claimed→read lifecycle (store
	// migration 0002). ReadAt is stamped only when a member's run ends cleanly
	// (SettleClaimed); ClaimedAt marks a message currently folded into an
	// in-flight run. Surfacing Claimed lets the UI show "reading…" for mail the
	// agent is actively processing, instead of it looking plain-unread until the
	// whole run settles.
	ReadAt    *int64 `json:"readAt,omitempty"`
	ClaimedAt *int64 `json:"claimedAt,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// TranscriptEntry is one conversation turn (GET /api/agents/:name/transcript).
type TranscriptEntry struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

// NewRouter assembles the swarm workstation HTTP handler: token-gated REST
// snapshots + command endpoints, the token-gated WebSocket bridge fed by hub,
// an unauthenticated /healthz, and the embedded SPA at /. spa is the built
// vue tree (web/dist sub-FS); a nil spa skips the static mount (tests).
func NewRouter(b Backend, hub *Hub, spa fs.FS) http.Handler {
	mux := http.NewServeMux()

	// Health is unauthenticated so liveness probes (and the M0 smoke test)
	// don't need the token. Since RP-18 it answers JSON — status, version,
	// uptime, and aggregate counts; still a 200 for any probe that only
	// checks the status code.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, b.Health())
	})

	guard := tokenGuard(b)

	// Session bootstrap (RP-15): hands the minted token to LOCAL callers so the
	// embedded FE logs in without the operator copying anything — the same
	// loopback trust the whole service relied on pre-RP-15, now scoped to this
	// one endpoint. Two hard gates: it vanishes entirely in --allow-remote mode
	// (behind a reverse proxy every caller's TCP peer IS loopback, so it would
	// otherwise hand the token to the world), and the peer must be loopback.
	// 404 — not 401 — when gated, so its existence isn't advertised.
	mux.HandleFunc("GET /api/auth/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		if b.AllowRemote() || !peerIsLoopback(r.RemoteAddr) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": b.Token()})
	})

	// Read snapshots.
	mux.Handle("GET /api/swarms", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.Spaces())
	}))

	// Lifecycle: register a space from a workdir / stop one.
	mux.Handle("POST /api/swarms", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Workdir string `json:"workdir"`
			Name    string `json:"name"`
		}
		if !decode(w, r, &body) {
			return
		}
		id, err := b.Register(body.Workdir, body.Name)
		if err != nil {
			// Register failures (missing manifest, bad workdir, name clash) are
			// client errors.
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"id": id})
	}))
	// DELETE removes a space (Docker rm); the lifecycle stop/run pair below KEEPS
	// the record so a stopped space can be restarted by id or name.
	mux.Handle("DELETE /api/swarm/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		respondErr(w, b.RemoveSpace(r.PathValue("id")))
	}))
	mux.Handle("POST /api/swarm/{id}/stop", guard(func(w http.ResponseWriter, r *http.Request) {
		respondErr(w, b.StopSpace(r.PathValue("id")))
	}))
	mux.Handle("POST /api/swarm/{id}/run", guard(func(w http.ResponseWriter, r *http.Request) {
		id, err := b.RunSpace(r.PathValue("id"))
		if err != nil {
			respondErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id})
	}))
	mux.Handle("POST /api/swarm/{id}/reset", guard(func(w http.ResponseWriter, r *http.Request) {
		id, err := b.ResetSpace(r.PathValue("id"))
		if err != nil {
			respondErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id})
	}))
	// External-event webhook (RP-9/RP-15). DELIBERATELY outside the session-token
	// guard (HandleFunc, not guard): an external app should never hold the
	// operator token. Instead the backend authorizes per space: a configured
	// settings.webhook_secret (header X-Evva-Webhook-Secret) must match for
	// every caller; without one, loopback peers keep the RP-9 trust and remote
	// peers are refused. The body is size-capped since the route is reachable
	// pre-auth. new → 202, duplicate idempotency_key → 200, bad secret → 401,
	// missing body → 400, unknown space → 404, stopped → 409.
	mux.HandleFunc("POST /api/swarm/{id}/event", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxEventBytes)
		var evt EventIn
		if !decode(w, r, &evt) {
			return
		}
		auth := EventAuth{Secret: r.Header.Get(WebhookSecretHeader), Loopback: peerIsLoopback(r.RemoteAddr)}
		id, dup, err := b.IngestEvent(r.PathValue("id"), evt, auth)
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "unauthorized"):
				http.Error(w, err.Error(), http.StatusUnauthorized)
			case strings.Contains(err.Error(), "unknown space"):
				http.Error(w, err.Error(), http.StatusNotFound)
			case strings.Contains(err.Error(), "stopped"):
				http.Error(w, err.Error(), http.StatusConflict)
			default:
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
			return
		}
		code := http.StatusAccepted // 202 — fire-and-forget; the leader runs in its own loop
		if dup {
			code = http.StatusOK // 200 — already accepted under this key
		}
		writeJSON(w, code, map[string]string{"messageId": id})
	})
	mux.Handle("GET /api/swarm/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		if roster, ok := b.Roster(r.PathValue("id")); ok {
			writeJSON(w, http.StatusOK, roster)
		} else {
			http.Error(w, "unknown space", http.StatusNotFound)
		}
	}))
	mux.Handle("GET /api/tasks", guard(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		space := q.Get("space")
		// A status filter switches to the on-demand paged view (the Completed tab);
		// without it, return the board snapshot (active + recent completed). Both
		// shapes are a TaskPage so the client always reads {tasks,total} (RP-6).
		var (
			page TaskPage
			ok   bool
		)
		if status := q.Get("status"); status != "" {
			page, ok = b.TasksByStatus(space, status, queryInt(q.Get("limit")), queryInt(q.Get("offset")))
		} else {
			page, ok = b.Tasks(space)
		}
		if ok {
			writeJSON(w, http.StatusOK, page)
		} else {
			http.Error(w, "unknown space", http.StatusNotFound)
		}
	}))
	mux.Handle("GET /api/messages", guard(func(w http.ResponseWriter, r *http.Request) {
		if msgs, ok := b.Messages(r.URL.Query().Get("space")); ok {
			writeJSON(w, http.StatusOK, msgs)
		} else {
			http.Error(w, "unknown space", http.StatusNotFound)
		}
	}))
	mux.Handle("GET /api/agents/{name}/transcript", guard(func(w http.ResponseWriter, r *http.Request) {
		if tr, ok := b.Transcript(r.URL.Query().Get("space"), r.PathValue("name")); ok {
			writeJSON(w, http.StatusOK, tr)
		} else {
			http.Error(w, "unknown space or agent", http.StatusNotFound)
		}
	}))
	mux.Handle("GET /api/swarm/{id}/pending", guard(func(w http.ResponseWriter, r *http.Request) {
		if gates, ok := b.PendingGates(r.PathValue("id")); ok {
			writeJSON(w, http.StatusOK, gates)
		} else {
			http.Error(w, "unknown space", http.StatusNotFound)
		}
	}))
	// Bottom-up proposal inbox (RP-23).
	mux.Handle("GET /api/swarm/{id}/proposals", guard(func(w http.ResponseWriter, r *http.Request) {
		if ps, ok := b.Proposals(r.PathValue("id")); ok {
			writeJSON(w, http.StatusOK, ps)
		} else {
			http.Error(w, "unknown space", http.StatusNotFound)
		}
	}))
	// Scheduler counters (RP-17).
	mux.Handle("GET /api/swarm/{id}/metrics", guard(func(w http.ResponseWriter, r *http.Request) {
		if m, ok := b.Metrics(r.PathValue("id")); ok {
			writeJSON(w, http.StatusOK, m)
		} else {
			http.Error(w, "unknown space", http.StatusNotFound)
		}
	}))

	// Command endpoints (REST mirror of the WS inbound channel).
	mux.Handle("POST /api/agents/{name}/run", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Prompt string `json:"prompt"`
		}
		if !decode(w, r, &body) {
			return
		}
		respondErr(w, b.Run(r.URL.Query().Get("space"), r.PathValue("name"), body.Prompt))
	}))
	// Operator → member message (mail-mode flat comms). {name} may be "all".
	mux.Handle("POST /api/agents/{name}/message", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Subject string `json:"subject"`
			Body    string `json:"body"`
		}
		if !decode(w, r, &body) {
			return
		}
		respondErr(w, b.SendUserMessage(r.URL.Query().Get("space"), r.PathValue("name"), body.Subject, body.Body))
	}))
	for verb, fn := range map[string]func(string, string) error{
		"suspend":  b.Suspend,
		"resume":   b.Resume,
		"freeze":   b.Freeze,
		"unfreeze": b.Unfreeze,
	} {
		mux.Handle("POST /api/agents/{name}/"+verb, guard(func(w http.ResponseWriter, r *http.Request) {
			respondErr(w, fn(r.URL.Query().Get("space"), r.PathValue("name")))
		}))
	}
	// Author a new worker from the add-agent form (RP-8). Validation errors
	// (bad/duplicate name, etc.) are operator input → 400.
	mux.Handle("POST /api/members", guard(func(w http.ResponseWriter, r *http.Request) {
		var spec MemberSpec
		if !decode(w, r, &spec) {
			return
		}
		respondInputErr(w, b.CreateMember(r.URL.Query().Get("space"), spec))
	}))
	// Retire a worker (RP-8). ?deleteDir=true also erases its on-disk definition.
	// Leader-protected / unknown → operator error.
	mux.Handle("DELETE /api/agents/{name}", guard(func(w http.ResponseWriter, r *http.Request) {
		deleteDir := r.URL.Query().Get("deleteDir") == "true"
		respondInputErr(w, b.RemoveMember(r.URL.Query().Get("space"), r.PathValue("name"), deleteDir))
	}))
	// Schedule CRUD (RP-8). Operator may target ANY member, including the leader.
	mux.Handle("POST /api/agents/{name}/schedule", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Cron   string `json:"cron"`
			Prompt string `json:"prompt"`
		}
		if !decode(w, r, &body) {
			return
		}
		respondInputErr(w, b.SetSchedule(r.URL.Query().Get("space"), r.PathValue("name"), body.Cron, body.Prompt))
	}))
	mux.Handle("DELETE /api/agents/{name}/schedule", guard(func(w http.ResponseWriter, r *http.Request) {
		respondInputErr(w, b.ClearSchedule(r.URL.Query().Get("space"), r.PathValue("name")))
	}))
	// Agent skills CRUD (RP-10). User-only — guarded; agents never author skills. An
	// add/delete reloads ONLY that member's prompt (KV-cache miss accepted).
	mux.Handle("GET /api/agents/{name}/skills", guard(func(w http.ResponseWriter, r *http.Request) {
		skills, ok := b.MemberSkills(r.URL.Query().Get("space"), r.PathValue("name"))
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, skills)
	}))
	mux.Handle("POST /api/agents/{name}/skills", guard(func(w http.ResponseWriter, r *http.Request) {
		var spec SkillSpec
		if !decode(w, r, &spec) {
			return
		}
		respondInputErr(w, b.AddSkill(r.URL.Query().Get("space"), r.PathValue("name"), spec))
	}))
	mux.Handle("DELETE /api/agents/{name}/skills/{skill}", guard(func(w http.ResponseWriter, r *http.Request) {
		respondInputErr(w, b.DeleteSkill(r.URL.Query().Get("space"), r.PathValue("name"), r.PathValue("skill")))
	}))
	// The tool catalog the add-agent form offers (collaboration tools excluded).
	mux.Handle("GET /api/tools", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.SelectableTools())
	}))
	mux.Handle("GET /api/models", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.SelectableModels())
	}))
	mux.Handle("POST /api/halt", guard(func(w http.ResponseWriter, r *http.Request) {
		respondErr(w, b.HaltAll(r.URL.Query().Get("space")))
	}))
	// Ledger retention pass (RP-16). The body is optional — an empty POST runs
	// the space's configured window for real; {days, dry_run} override.
	mux.Handle("POST /api/swarm/{id}/vacuum", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Days   int  `json:"days"`
			DryRun bool `json:"dry_run"`
		}
		if r.ContentLength != 0 && !decode(w, r, &body) {
			return
		}
		stats, err := b.Vacuum(r.PathValue("id"), body.Days, body.DryRun)
		if err != nil {
			respondErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, stats)
	}))

	// WebSocket bridge — guarded by the same token (browsers pass it as ?token=).
	mux.Handle("GET /ws", guard(websocket.Handler(func(ws *websocket.Conn) {
		serveSocket(b, hub, ws)
	}).ServeHTTP))

	// SPA fallback: anything not matched above is served from the embedded tree.
	if spa != nil {
		mux.Handle("/", http.FileServerFS(spa))
	}

	return mux
}

// queryInt parses a non-negative int query param; missing or invalid → 0 (the
// callers treat 0 as "use the default / no offset", so a junk value degrades
// safely rather than erroring the request).
func queryInt(s string) int {
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return n
	}
	return 0
}

// tokenGuard returns a middleware that rejects any request not carrying the
// backend's session token (Authorization: Bearer <t> or ?token=<t>). The
// constant-time compare avoids leaking the token via timing.
func tokenGuard(b Backend) func(http.HandlerFunc) http.Handler {
	return func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			want := b.Token()
			got := bearer(r)
			if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next(w, r)
		})
	}
}

// peerIsLoopback reports whether a request's TCP peer is a loopback address.
// RemoteAddr is kernel-reported (never a header), so a remote caller cannot
// spoof it; behind a reverse proxy it is the PROXY's address — which is exactly
// why remote-facing deployments must run --allow-remote (killing the loopback
// trust paths) rather than proxying a default loopback-trusting instance.
func peerIsLoopback(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// bearer extracts the presented token from the Authorization header or the
// `token` query parameter (the latter is how a browser authenticates a WS
// handshake, which cannot set custom headers).
func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return r.URL.Query().Get("token")
}

// serveSocket subscribes one browser connection to a space (and optionally one
// agent) and runs its inbound command loop until the socket closes. The
// subscription gates the outbound fan-out the hub performs.
func serveSocket(b Backend, hub *Hub, ws *websocket.Conn) {
	q := ws.Request().URL.Query()
	spaceID := q.Get("space")
	if spaceID == "" || !b.HasSpace(spaceID) {
		return // closes the socket; nothing to subscribe to
	}
	c := &wsConn{ws: ws, spaceID: spaceID, agentID: q.Get("agent")}
	hub.add(c)
	defer hub.remove(c)

	for {
		var raw string
		if err := websocket.Message.Receive(ws, &raw); err != nil {
			return // EOF / closed
		}
		if reqID, err := dispatchInbound(b, spaceID, []byte(raw)); err != nil {
			// A command that failed to route (e.g. an approval reply that hit no
			// controller) must NOT be swallowed — that is exactly how a blocked
			// agent hangs invisibly. Echo it back (with the reqId) so the browser
			// can recover the specific gate. c.send serialises against the hub.
			_ = c.send(commandErrorFrame(reqID, err))
		}
	}
}

// wsCommand is the inbound JSON envelope a browser sends over the socket. The
// live socket carries the interactive turns — leader chat (run) and the two
// approval replies; lifecycle commands go through the REST endpoints.
type wsCommand struct {
	Type     string            `json:"type"`
	Agent    string            `json:"agent"`
	Prompt   string            `json:"prompt"`
	ReqID    string            `json:"reqId"`
	Behavior string            `json:"behavior"`
	Reason   string            `json:"reason"`
	RuleTool string            `json:"ruleTool"` // "Always allow": tool to session-allow ("" = one-shot)
	Answers  map[string][]string `json:"answers"` // question text → chosen labels (native multi-select)
}

// dispatchInbound routes one inbound WS command, returning the command's reqId
// (for gate replies) and its error. A malformed frame is ignored (nil error); a
// command that ran but failed returns its error so serveSocket can echo it back
// instead of silently dropping it.
func dispatchInbound(b Backend, spaceID string, raw []byte) (string, error) {
	var cmd wsCommand
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return "", nil // not a command we can act on; ignore the frame
	}
	switch cmd.Type {
	case "run":
		return "", b.Run(spaceID, cmd.Agent, cmd.Prompt)
	case "respond_permission":
		return cmd.ReqID, b.RespondPermission(spaceID, cmd.Agent, cmd.ReqID, cmd.Behavior, cmd.Reason, cmd.RuleTool)
	case "respond_question":
		return cmd.ReqID, b.RespondQuestion(spaceID, cmd.Agent, cmd.ReqID, cmd.Answers)
	}
	return "", nil
}

// commandErrorFrame is the JSON pushed back to the browser when an inbound WS
// command fails to apply. type:"command_error" is distinct from the event
// envelope, so the event reducers ignore it; the UI surfaces it and re-hydrates
// the pending gates so a reply that failed to route doesn't strand the member.
func commandErrorFrame(reqID string, err error) []byte {
	b, _ := json.Marshal(map[string]string{"type": "command_error", "reqId": reqID, "message": err.Error()})
	return b
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// decode reads a JSON request body into v, writing a 400 and returning false on
// malformed input.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return false
	}
	return true
}

// respondErr maps a backend command result to a status: 204 on success, 404
// when the target space/agent was unknown (the typical caller error), 500
// otherwise.
func respondErr(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case strings.Contains(err.Error(), "unknown") || strings.Contains(err.Error(), "no controller"):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// respondInputErr is respondErr for operator-input endpoints (schedule CRUD,
// member create/remove): an unknown space/member is still 404, but every other
// failure is the operator's bad input (bad cron, duplicate/illegal name,
// leader-protected) → 400, not a 500.
func respondInputErr(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case strings.Contains(err.Error(), "unknown") || strings.Contains(err.Error(), "no controller"):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
}
