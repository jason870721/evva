// This file is the NTF outbound notifier: the push half of swarm
// observability. RP-15 let external systems POST events INTO a space; this
// lets attention-worthy moments flow OUT — to a webhook (plain JSON or a
// Slack-compatible {"text"}), and/or a local command (desktop notify) — so
// an operator away from the console learns within seconds that a member is
// blocked on a gate, errored, paused, stalled, or budget-frozen.
//
// The discipline is the event log's, verbatim: the observer never slows the
// observed. The publish-side tap (consider) is filter + non-blocking offer
// into a bounded queue; one sender goroutine does the slow I/O; a dead
// endpoint shows up as a climbing dropped counter, never as backpressure.
// Delivery is best-effort by contract — one retry, then drop and count.

package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/webapi"
	"github.com/johnny1110/evva/pkg/common/proc"
	"github.com/johnny1110/evva/pkg/event"
)

const (
	// notifierBuffer absorbs bursts between sends. Far above the rate limit's
	// per-minute ceiling — overflow means a wedged sender, and dropping is
	// the contract then.
	notifierBuffer = 256
	// notifBodyCap bounds what leaves the machine: a notification is a
	// pager, not a transcript — title + a capped body tail; the console link
	// carries the rest.
	notifBodyCap = 500
	// Delivery timing: one POST attempt bounded at notifHTTPTimeout, one
	// retry after notifRetryDelay, then drop. A local command gets
	// notifCommandTimeout before its process tree is killed.
	notifHTTPTimeout    = 10 * time.Second
	notifRetryDelay     = 5 * time.Second
	notifCommandTimeout = 15 * time.Second
)

// notifier pushes one space's attention-worthy events out. Owned by the
// spaceEntry beside the event log; fed exclusively from the pump goroutine
// (consider), so its filter state — gate first-sighting keys, the token
// bucket, the suppression run — needs no locks. Counters are atomics (the
// metrics endpoint reads them from request goroutines).
type notifier struct {
	spaceID   string
	spaceName string
	console   string // console deep-link carried in every payload; "" = omitted
	spec      agentdef.NotifySpec
	groups    map[string]bool // expanded events filter: gates/errors/alerts

	ch     chan notifItem
	ctx    context.Context // cancelled by Close — aborts in-flight I/O and the retry wait
	cancel context.CancelFunc
	done   chan struct{}

	sent       atomic.Int64
	dropped    atomic.Int64
	suppressed atomic.Int64

	// Pump-goroutine-only state. seenGates keys a gate's FIRST sighting
	// (re-broadcasts and reconnect re-sends never re-notify) and is pruned
	// on run-terminal events, the gateTracker's own lifecycle rule.
	// suppressedRun counts rate-limited drops since the last delivered item,
	// so the next delivery can say "N suppressed" — silence is never
	// ambiguous.
	seenGates     map[string]string // requestID -> agentID
	bucket        tokenBucket
	suppressedRun int64

	client *http.Client
	// now / retryDelay / cmdTimeout are fields (defaulted from the consts)
	// so tests can compress time; production never touches them.
	now        func() time.Time
	retryDelay time.Duration
	cmdTimeout time.Duration
}

// notifItem is one queued notification, resolved to text at tap time (the
// event is not retained).
type notifItem struct {
	kind  string
	agent string
	title string
	body  string
	at    time.Time
}

// notifPayload is the wire shape (format "json"). Slack format folds the
// same content into {"text": …}.
type notifPayload struct {
	Space   string `json:"space"`
	SpaceID string `json:"spaceId"`
	Agent   string `json:"agent,omitempty"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	At      string `json:"at"`
	Console string `json:"console,omitempty"`
}

func newNotifier(spaceID, spaceName, console string, spec agentdef.NotifySpec) *notifier {
	groups := map[string]bool{}
	if len(spec.Events) == 0 {
		groups[agentdef.NotifyGroupGates] = true
		groups[agentdef.NotifyGroupErrors] = true
		groups[agentdef.NotifyGroupAlerts] = true
	} else {
		for _, g := range spec.Events {
			groups[g] = true
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	n := &notifier{
		spaceID:    spaceID,
		spaceName:  spaceName,
		console:    console,
		spec:       spec,
		groups:     groups,
		ch:         make(chan notifItem, notifierBuffer),
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
		seenGates:  map[string]string{},
		bucket:     tokenBucket{capacity: float64(spec.RateLimit)},
		client:     &http.Client{Timeout: notifHTTPTimeout},
		now:        time.Now,
		retryDelay: notifRetryDelay,
		cmdTimeout: notifCommandTimeout,
	}
	go n.run()
	return n
}

// Close aborts any in-flight delivery (and its retry wait), drops whatever
// is still queued, and waits for the sender to exit. Unlike the event log,
// Close does NOT drain: delivery is slow network I/O against a possibly-dead
// endpoint, and teardown must never wait on one.
func (n *notifier) Close() {
	n.cancel()
	<-n.done
}

// Sent / Dropped / Suppressed report the lifetime counters (metrics endpoint).
func (n *notifier) Sent() int64       { return n.sent.Load() }
func (n *notifier) Dropped() int64    { return n.dropped.Load() }
func (n *notifier) Suppressed() int64 { return n.suppressed.Load() }

// consider is the publish tap: called for every space event, on the pump
// goroutine only. It reduces the event to a notification (or nothing),
// applies the group filter, gate first-sighting, and the rate limit, and
// offers the result to the sender without ever blocking.
func (n *notifier) consider(e event.Event) {
	it, ok := n.itemFor(e)
	if !ok {
		return
	}
	if !n.bucket.allow(n.now()) {
		n.suppressed.Add(1)
		n.suppressedRun++
		return
	}
	if n.suppressedRun > 0 {
		n.offer(notifItem{
			kind:  "rate_limit",
			title: fmt.Sprintf("rate limit: %d notifications suppressed", n.suppressedRun),
			body:  fmt.Sprintf("The space crossed notify.rate_limit (%d/min); %d notifications were dropped. The console has the full picture.", n.spec.RateLimit, n.suppressedRun),
			at:    n.now(),
		})
		n.suppressedRun = 0
	}
	n.offer(it)
}

func (n *notifier) offer(it notifItem) {
	select {
	case n.ch <- it:
	default:
		n.dropped.Add(1)
	}
}

// itemFor reduces one event to its notification. The five notifiable kinds
// are the complete attention surface (NTF §4): the two gates, the two
// failure kinds, and the promoted ops alert — each already deduped at its
// source; gates additionally keyed here on first sighting. Run-terminal
// events prune the gate keys (the gateTracker lifecycle rule) and notify
// nothing themselves — except error, which does both.
func (n *notifier) itemFor(e event.Event) (notifItem, bool) {
	none := notifItem{}
	switch e.Kind {
	case event.KindApprovalNeeded:
		p := e.ApprovalNeeded
		if p == nil || p.RequestID == "" || !n.groups[agentdef.NotifyGroupGates] {
			return none, false
		}
		if _, dup := n.seenGates[p.RequestID]; dup {
			return none, false
		}
		n.seenGates[p.RequestID] = e.AgentID
		body := "tool: " + p.ToolName
		if p.InputDescription != "" {
			body += " — " + p.InputDescription
		}
		if p.Reason != "" {
			body += " (" + p.Reason + ")"
		}
		return notifItem{kind: string(e.Kind), agent: e.AgentID, at: n.now(),
			title: fmt.Sprintf("%s is waiting for approval", e.AgentID), body: capBody(body)}, true

	case event.KindQuestionNeeded:
		p := e.QuestionNeeded
		if p == nil || p.RequestID == "" || !n.groups[agentdef.NotifyGroupGates] {
			return none, false
		}
		if _, dup := n.seenGates[p.RequestID]; dup {
			return none, false
		}
		n.seenGates[p.RequestID] = e.AgentID
		body := ""
		if len(p.Questions) > 0 {
			body = p.Questions[0].Question
			if len(p.Questions) > 1 {
				body += fmt.Sprintf(" (+%d more)", len(p.Questions)-1)
			}
		}
		return notifItem{kind: string(e.Kind), agent: e.AgentID, at: n.now(),
			title: fmt.Sprintf("%s asked a question", e.AgentID), body: capBody(body)}, true

	case event.KindError:
		n.pruneGates(e.AgentID) // the run died — its gates die with it
		if !n.groups[agentdef.NotifyGroupErrors] {
			return none, false
		}
		body := ""
		if e.Error != nil {
			body = e.Error.Message
		}
		return notifItem{kind: string(e.Kind), agent: e.AgentID, at: n.now(),
			title: fmt.Sprintf("%s errored", e.AgentID), body: capBody(body)}, true

	case event.KindIterLimit:
		if !n.groups[agentdef.NotifyGroupErrors] {
			return none, false
		}
		return notifItem{kind: string(e.Kind), agent: e.AgentID, at: n.now(),
			title: fmt.Sprintf("%s paused at its iteration limit", e.AgentID),
			body:  "Resume it from the console, or raise settings.max_iterations."}, true

	case swarm.KindOpsAlert:
		if !n.groups[agentdef.NotifyGroupAlerts] {
			return none, false
		}
		text := ""
		if e.Text != nil {
			text = e.Text.Text
		}
		title, body, _ := strings.Cut(text, "\n")
		if title == "" {
			return none, false
		}
		return notifItem{kind: string(e.Kind), agent: e.AgentID, at: n.now(),
			title: title, body: capBody(body)}, true

	case event.KindRunEnd, event.KindRunCancelled:
		n.pruneGates(e.AgentID)
	}
	return none, false
}

// pruneGates forgets an agent's gate keys when its run terminates — a gate
// that re-fires in a later run is a fresh ask and notifies again.
func (n *notifier) pruneGates(agentID string) {
	for id, ag := range n.seenGates {
		if ag == agentID {
			delete(n.seenGates, id)
		}
	}
}

func capBody(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= notifBodyCap {
		return s
	}
	return s[:notifBodyCap] + "…"
}

// run is the single sender: webhook first, then the local command — either
// succeeding counts the item as sent. On Close it drops the remaining queue
// and exits.
func (n *notifier) run() {
	defer close(n.done)
	for {
		select {
		case <-n.ctx.Done():
			for {
				select {
				case <-n.ch:
					n.dropped.Add(1)
				default:
					return
				}
			}
		case it := <-n.ch:
			n.deliver(it)
		}
	}
}

func (n *notifier) deliver(it notifItem) {
	payload, err := json.Marshal(n.payloadFor(it))
	if err != nil {
		n.dropped.Add(1)
		return
	}
	delivered := false
	if n.spec.URL != "" {
		if n.post(payload) {
			delivered = true
		} else {
			select {
			case <-time.After(n.retryDelay):
				if n.post(payload) {
					delivered = true
				}
			case <-n.ctx.Done():
			}
		}
	}
	if n.spec.Command != "" && n.execCommand(payload) {
		delivered = true
	}
	if delivered {
		n.sent.Add(1)
	} else {
		n.dropped.Add(1)
	}
}

func (n *notifier) payloadFor(it notifItem) any {
	if n.spec.Format == agentdef.NotifyFormatSlack {
		// Lowest common denominator: works for Slack, Discord-compatible
		// relays, and most chat webhooks.
		text := it.title
		if it.body != "" {
			text += "\n" + it.body
		}
		if n.console != "" {
			text += "\n" + n.console
		}
		return map[string]string{"text": text}
	}
	return notifPayload{
		Space: n.spaceName, SpaceID: n.spaceID, Agent: it.agent, Kind: it.kind,
		Title: it.title, Body: it.body, At: it.at.UTC().Format(time.RFC3339),
		Console: n.console,
	}
}

func (n *notifier) post(payload []byte) bool {
	req, err := http.NewRequestWithContext(n.ctx, http.MethodPost, n.spec.URL, bytes.NewReader(payload))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if n.spec.Secret != "" {
		// The inbound webhook's header, reused outbound for symmetry (RP-15).
		req.Header.Set(webapi.WebhookSecretHeader, n.spec.Secret)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// execCommand runs the operator's local command with the JSON payload on
// stdin — the bash tool's process discipline (resolved shell, process group,
// tree kill, bounded Wait). Operator-authored manifest config: the same
// trust class as permission_mode: bypass.
func (n *notifier) execCommand(payload []byte) bool {
	shell, err := proc.Shell()
	if err != nil {
		return false
	}
	cctx, cancel := context.WithTimeout(n.ctx, n.cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, shell, "-c", n.spec.Command)
	cmd.Stdin = bytes.NewReader(payload)
	proc.Group(cmd)
	cmd.Cancel = func() error {
		_ = proc.KillTree(cmd)
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd.Run() == nil
}

// tokenBucket is the per-space send limiter: capacity tokens, refilled at
// capacity per minute. Zero-valued until the first allow, which starts it
// full — the first burst of a quiet space always gets through.
type tokenBucket struct {
	capacity float64
	tokens   float64
	last     time.Time
}

func (b *tokenBucket) allow(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = b.capacity
	} else {
		b.tokens = min(b.capacity, b.tokens+b.capacity*now.Sub(b.last).Minutes())
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}
