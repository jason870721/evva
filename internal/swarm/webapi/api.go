package webapi

import (
	"crypto/subtle"
	"encoding/json"
	"io/fs"
	"net/http"
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

	// HasSpace reports whether a space id is registered (the WS subscribe guard).
	HasSpace(spaceID string) bool

	// Read snapshots. The bool is false when the space id is unknown.
	Spaces() []SpaceInfo
	Roster(spaceID string) ([]MemberInfo, bool)
	Tasks(spaceID string) ([]TaskInfo, bool)
	Messages(spaceID string) ([]MessageInfo, bool)
	Transcript(spaceID, agent string) ([]TranscriptEntry, bool)

	// Inbound commands. Run is asynchronous — it kicks off a turn whose events
	// stream back over the WebSocket; the rest are immediate.
	Run(spaceID, agent, prompt string) error
	RespondPermission(spaceID, agent, reqID, behavior, reason string) error
	RespondQuestion(spaceID, agent, reqID string, answers map[string]string) error
	Suspend(spaceID, agent string) error
	Resume(spaceID, agent string) error
	Freeze(spaceID, agent string) error
	Unfreeze(spaceID, agent string) error
	AddMember(spaceID, agent string) error
	HaltAll(spaceID string) error
}

// SpaceInfo is one row of GET /api/swarms.
type SpaceInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Workdir string `json:"workdir"`
	Members int    `json:"members"`
}

// MemberInfo mirrors swarm.MemberView on the wire (GET /api/swarm/:id).
type MemberInfo struct {
	Name        string `json:"name"`
	Role        string `json:"role"`
	Membership  string `json:"membership"`
	Run         string `json:"run"`
	CurrentTask int64  `json:"currentTask"`
	WhenToUse   string `json:"whenToUse,omitempty"`
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

// MessageInfo mirrors store.Message on the wire (GET /api/messages).
type MessageInfo struct {
	ID        string `json:"id"`
	Sender    string `json:"sender"`
	Recipient string `json:"recipient"`
	Subject   string `json:"subject,omitempty"`
	Body      string `json:"body"`
	RefTask   *int64 `json:"refTask,omitempty"`
	ReadAt    *int64 `json:"readAt,omitempty"`
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
	// don't need the token.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	guard := tokenGuard(b)

	// Read snapshots.
	mux.Handle("GET /api/swarms", guard(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, b.Spaces())
	}))
	mux.Handle("GET /api/swarm/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		if roster, ok := b.Roster(r.PathValue("id")); ok {
			writeJSON(w, http.StatusOK, roster)
		} else {
			http.Error(w, "unknown space", http.StatusNotFound)
		}
	}))
	mux.Handle("GET /api/tasks", guard(func(w http.ResponseWriter, r *http.Request) {
		if tasks, ok := b.Tasks(r.URL.Query().Get("space")); ok {
			writeJSON(w, http.StatusOK, tasks)
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
	mux.Handle("POST /api/members", guard(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Agent string `json:"agent"`
		}
		if !decode(w, r, &body) {
			return
		}
		respondErr(w, b.AddMember(r.URL.Query().Get("space"), body.Agent))
	}))
	mux.Handle("POST /api/halt", guard(func(w http.ResponseWriter, r *http.Request) {
		respondErr(w, b.HaltAll(r.URL.Query().Get("space")))
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
		dispatchInbound(b, spaceID, []byte(raw))
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
	Answers  map[string]string `json:"answers"`
}

func dispatchInbound(b Backend, spaceID string, raw []byte) {
	var cmd wsCommand
	if err := json.Unmarshal(raw, &cmd); err != nil {
		return
	}
	switch cmd.Type {
	case "run":
		_ = b.Run(spaceID, cmd.Agent, cmd.Prompt)
	case "respond_permission":
		_ = b.RespondPermission(spaceID, cmd.Agent, cmd.ReqID, cmd.Behavior, cmd.Reason)
	case "respond_question":
		_ = b.RespondQuestion(spaceID, cmd.Agent, cmd.ReqID, cmd.Answers)
	}
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
