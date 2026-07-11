// Package client is the attach TUI's service client: thin authenticated REST
// calls for state snapshots and one WebSocket for the live feed + the
// interactive gate replies — exactly the surface the web console consumes
// (process model A: the service builds the agents, this client only reads
// state and POSTs intent). No internal/swarm runtime package is linked; the
// webapi import is types-only (the wire DTOs).
package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/johnny1110/evva/internal/swarm/tui/wire"
	"github.com/johnny1110/evva/internal/swarm/webapi"
)

// Client issues authenticated REST calls against one service address.
type Client struct {
	Addr  string // host:port
	Token string
	HTTP  *http.Client
}

// New builds a client with the standard 10s timeout (servicectl's norm).
func New(addr, token string) *Client {
	return &Client{Addr: addr, Token: token, HTTP: &http.Client{Timeout: 10 * time.Second}}
}

func (c *Client) do(method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, "http://"+c.Addr+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach evva service at %s (is it running? `evva service start`): %w", c.Addr, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

func (c *Client) get(path string, out any) error   { return c.do("GET", path, nil, out) }
func (c *Client) post(path string, body any) error { return c.do("POST", path, body, nil) }

// Spaces lists every registered space (running + stopped).
func (c *Client) Spaces() ([]webapi.SpaceInfo, error) {
	var out []webapi.SpaceInfo
	err := c.get("/api/swarms", &out)
	return out, err
}

// ResolveSpace turns a ref (id or name) into its SpaceInfo, erroring with the
// available refs when it matches nothing — the CLI convention.
func (c *Client) ResolveSpace(ref string) (webapi.SpaceInfo, error) {
	spaces, err := c.Spaces()
	if err != nil {
		return webapi.SpaceInfo{}, err
	}
	var names []string
	for _, s := range spaces {
		if s.ID == ref || s.Name == ref {
			return s, nil
		}
		names = append(names, s.Name)
	}
	if len(names) == 0 {
		return webapi.SpaceInfo{}, fmt.Errorf("no spaces registered — `evva swarm .` in a workdir first")
	}
	return webapi.SpaceInfo{}, fmt.Errorf("no space %q — available: %s", ref, strings.Join(names, ", "))
}

// Roster reads the space's member list (GET /api/swarm/{id}).
func (c *Client) Roster(space string) ([]webapi.MemberInfo, error) {
	var out []webapi.MemberInfo
	err := c.get("/api/swarm/"+url.PathEscape(space), &out)
	return out, err
}

// Tasks reads the board snapshot (active + newest few completed).
func (c *Client) Tasks(space string) (webapi.TaskPage, error) {
	var out webapi.TaskPage
	err := c.get("/api/tasks?space="+url.QueryEscape(space), &out)
	return out, err
}

// ChatLog replays the durable event log as wire events, oldest-first — the
// hydration source that survives compaction (RP-17). Unparseable elements are
// skipped (forward-compatible), never fatal.
func (c *Client) ChatLog(space string, limit int) ([]*wire.Event, error) {
	path := "/api/swarm/" + url.PathEscape(space) + "/chatlog"
	if limit > 0 {
		path += fmt.Sprintf("?limit=%d", limit)
	}
	var raw []json.RawMessage
	if err := c.get(path, &raw); err != nil {
		return nil, err
	}
	out := make([]*wire.Event, 0, len(raw))
	for _, r := range raw {
		if ev, _ := wire.ParseEvent(r); ev != nil {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Pending reads the outstanding approval/question gates (raw event shape) so
// gates raised while detached are answerable on attach.
func (c *Client) Pending(space string) ([]*wire.Event, error) {
	var raw []json.RawMessage
	if err := c.get("/api/swarm/"+url.PathEscape(space)+"/pending", &raw); err != nil {
		return nil, err
	}
	out := make([]*wire.Event, 0, len(raw))
	for _, r := range raw {
		if ev, _ := wire.ParseEvent(r); ev != nil {
			out = append(out, ev)
		}
	}
	return out, nil
}

// Message sends an operator mail to one member (sender "user" — the web
// composer's endpoint). to may be "all".
func (c *Client) Message(space, to, body string) error {
	return c.post("/api/agents/"+url.PathEscape(to)+"/message?space="+url.QueryEscape(space),
		map[string]string{"body": body})
}

// Verb fires one lifecycle verb (suspend | resume | freeze | unfreeze) at a
// member — thin POSTs, same endpoints as the web roster buttons.
func (c *Client) Verb(space, member, verb string) error {
	return c.post("/api/agents/"+url.PathEscape(member)+"/"+verb+"?space="+url.QueryEscape(space), nil)
}

// HaltAll cancels every in-flight run in the space.
func (c *Client) HaltAll(space string) error {
	return c.post("/api/halt?space="+url.QueryEscape(space), nil)
}

// Health probes /healthz (unauthenticated liveness + version skew check).
func (c *Client) Health() (webapi.HealthInfo, error) {
	var out webapi.HealthInfo
	err := c.get("/healthz", &out)
	return out, err
}
