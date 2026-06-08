package webapi

import (
	"sync"
	"time"

	"golang.org/x/net/websocket"
)

// wsWriteTimeout bounds how long a single frame write may block. Without it a
// half-open browser socket wedges the per-space event pump (Publish → send is
// synchronous), which stops draining the space's event channel and — once it
// fills — blocks EVERY member's Emit, freezing the whole space (including the
// approval events needed to unblock it). On timeout the write errors, Publish
// drops the connection, and the browser reconnects. (RP-2 §3.5.)
const wsWriteTimeout = 5 * time.Second

// Hub fans each space's event stream out to the browser. Every WebSocket client
// subscribes to exactly one space — and optionally one agent within it — and the
// hub delivers only events whose (spaceID, AgentID) match that subscription.
// This is the on-the-wire half of per-space isolation (invariant #2, AC#3): a
// client watching space A never receives space B's events.
//
// golang.org/x/net/websocket connections are not safe for concurrent writes, so
// each conn serialises its sends under its own mutex; the hub itself only holds
// its registry lock long enough to snapshot the target set.
type Hub struct {
	mu    sync.RWMutex
	conns map[*wsConn]struct{}
}

// NewHub returns an empty hub.
func NewHub() *Hub { return &Hub{conns: make(map[*wsConn]struct{})} }

// wsConn is one live browser connection and the filter it subscribed with.
// agentID == "" means "every agent in the space".
type wsConn struct {
	ws      *websocket.Conn
	spaceID string
	agentID string

	sendMu sync.Mutex
}

func (h *Hub) add(c *wsConn) {
	h.mu.Lock()
	h.conns[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *wsConn) {
	h.mu.Lock()
	delete(h.conns, c)
	h.mu.Unlock()
}

// Publish delivers a marshalled event to every connection whose subscription
// matches (spaceID, agentID). A connection with no agent filter matches all of
// its space's agents. A failed write tears down that connection — the browser
// will reconnect — without blocking the others.
func (h *Hub) Publish(spaceID, agentID string, payload []byte) {
	h.mu.RLock()
	targets := make([]*wsConn, 0, len(h.conns))
	for c := range h.conns {
		if c.spaceID != spaceID {
			continue
		}
		if c.agentID != "" && c.agentID != agentID {
			continue
		}
		targets = append(targets, c)
	}
	h.mu.RUnlock()

	for _, c := range targets {
		if err := c.send(payload); err != nil {
			h.remove(c)
			// Close so the connection's receive loop unblocks too — a write
			// timeout means the socket is wedged; leaving it half-open would keep
			// serveSocket parked in Receive.
			_ = c.ws.Close()
		}
	}
}

// send writes one frame, serialised against any other writer on this conn, under
// a write deadline so a stuck socket fails fast instead of blocking the pump.
func (c *wsConn) send(payload []byte) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
	return websocket.Message.Send(c.ws, string(payload))
}

// Connections returns the current subscriber count — used by tests to wait for
// a client's subscription to land before driving the agent that should reach it.
func (h *Hub) Connections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}
