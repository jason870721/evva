package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm"
	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/common"
	"github.com/johnny1110/evva/pkg/event"
)

// sink is an httptest webhook endpoint: it records every request body (and
// the secret header) and answers with a scripted status sequence (default:
// all 200).
type sink struct {
	mu      sync.Mutex
	bodies  []string
	secrets []string
	status  []int // consumed one per request; empty = 200
	srv     *httptest.Server
}

func newSink(t *testing.T, status ...int) *sink {
	t.Helper()
	k := &sink{status: status}
	k.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		k.mu.Lock()
		k.bodies = append(k.bodies, string(b))
		k.secrets = append(k.secrets, r.Header.Get("X-Evva-Webhook-Secret"))
		code := http.StatusOK
		if len(k.status) > 0 {
			code = k.status[0]
			k.status = k.status[1:]
		}
		k.mu.Unlock()
		w.WriteHeader(code)
	}))
	t.Cleanup(k.srv.Close)
	return k
}

func (k *sink) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.bodies)
}

func (k *sink) body(i int) string {
	k.mu.Lock()
	defer k.mu.Unlock()
	if i >= len(k.bodies) {
		return ""
	}
	return k.bodies[i]
}

// testNotifier builds a notifier with compressed time knobs; cleanup closes it.
func testNotifier(t *testing.T, spec agentdef.NotifySpec) *notifier {
	t.Helper()
	if spec.Format == "" {
		spec.Format = agentdef.NotifyFormatJSON
	}
	if spec.RateLimit == 0 {
		spec.RateLimit = agentdef.DefaultNotifyRateLimit
	}
	n := newNotifier("sp-1", "web-team", "http://127.0.0.1:8888/?space=sp-1", spec)
	n.retryDelay = 20 * time.Millisecond
	n.cmdTimeout = 500 * time.Millisecond
	t.Cleanup(n.Close)
	return n
}

func approvalEvent(agent, reqID string) event.Event {
	return event.Event{Kind: event.KindApprovalNeeded, AgentID: agent,
		ApprovalNeeded: &event.ApprovalNeededPayload{RequestID: reqID, ToolName: "bash", InputDescription: "rm -rf dist/"}}
}

func waitCount(t *testing.T, what string, want int, got func() int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if got() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s = %d, want %d", what, got(), want)
}

// TestNotifierDeliversJSON: an approval gate becomes one POST with the
// documented payload shape, the secret header, and the console link.
func TestNotifierDeliversJSON(t *testing.T) {
	k := newSink(t)
	n := testNotifier(t, agentdef.NotifySpec{URL: k.srv.URL, Secret: "s3cret"})

	n.consider(approvalEvent("qa", "req-1"))
	waitCount(t, "sink hits", 1, k.count)

	var p notifPayload
	if err := json.Unmarshal([]byte(k.body(0)), &p); err != nil {
		t.Fatalf("payload not JSON: %v\n%s", err, k.body(0))
	}
	if p.Space != "web-team" || p.SpaceID != "sp-1" || p.Agent != "qa" || p.Kind != "approval_needed" {
		t.Fatalf("payload = %+v", p)
	}
	if !strings.Contains(p.Title, "qa is waiting for approval") || !strings.Contains(p.Body, "rm -rf dist/") {
		t.Fatalf("title/body = %q / %q", p.Title, p.Body)
	}
	if !strings.Contains(p.Console, "?space=sp-1") || p.At == "" {
		t.Fatalf("console/at = %q / %q", p.Console, p.At)
	}
	k.mu.Lock()
	secret := k.secrets[0]
	k.mu.Unlock()
	if secret != "s3cret" {
		t.Fatalf("secret header = %q", secret)
	}
	waitCount(t, "sent counter", 1, func() int { return int(n.Sent()) })
}

// TestNotifierSlackFormat: the same content folded into {"text": …}.
func TestNotifierSlackFormat(t *testing.T) {
	k := newSink(t)
	n := testNotifier(t, agentdef.NotifySpec{URL: k.srv.URL, Format: agentdef.NotifyFormatSlack})

	n.consider(event.Event{Kind: swarm.KindOpsAlert, AgentID: "w",
		Text: &event.TextPayload{Text: "⚠️ budget breaker: w frozen\nspent 2M tokens"}})
	waitCount(t, "sink hits", 1, k.count)

	var p map[string]string
	if err := json.Unmarshal([]byte(k.body(0)), &p); err != nil {
		t.Fatal(err)
	}
	text := p["text"]
	if !strings.Contains(text, "budget breaker") || !strings.Contains(text, "spent 2M tokens") || !strings.Contains(text, "?space=sp-1") {
		t.Fatalf("slack text = %q", text)
	}
}

// TestNotifierRetryOnceThenDrop: a failing endpoint gets exactly one retry
// per item; persistent failure counts a drop, recovery counts a send.
func TestNotifierRetryOnceThenDrop(t *testing.T) {
	// First item: 500 then 200 (retry succeeds). Second item: 500, 500 (drop).
	k := newSink(t, 500, 200, 500, 500)
	n := testNotifier(t, agentdef.NotifySpec{URL: k.srv.URL})

	n.consider(approvalEvent("a", "r1"))
	waitCount(t, "sent after retry", 1, func() int { return int(n.Sent()) })
	n.consider(approvalEvent("a", "r2"))
	waitCount(t, "dropped after two failures", 1, func() int { return int(n.Dropped()) })
	if got := k.count(); got != 4 {
		t.Fatalf("endpoint hits = %d, want 4 (2 per item)", got)
	}
}

// TestNotifierGateFirstSighting (NTF-4): re-broadcasts of the same gate never
// re-notify; a run-terminal event prunes the key so a NEXT run's gate does.
func TestNotifierGateFirstSighting(t *testing.T) {
	k := newSink(t)
	n := testNotifier(t, agentdef.NotifySpec{URL: k.srv.URL})

	n.consider(approvalEvent("qa", "req-1"))
	n.consider(approvalEvent("qa", "req-1")) // reconnect re-send
	n.consider(approvalEvent("qa", "req-1")) // re-broadcast
	waitCount(t, "sink hits", 1, k.count)
	time.Sleep(50 * time.Millisecond)
	if got := k.count(); got != 1 {
		t.Fatalf("gate notified %d times, want once", got)
	}

	// The run ends; a fresh gate in the next run is a fresh ask.
	n.consider(event.Event{Kind: event.KindRunEnd, AgentID: "qa"})
	n.consider(approvalEvent("qa", "req-1"))
	waitCount(t, "sink hits after new run", 2, k.count)
}

// TestNotifierGroupFilter: an alerts-only config drops gate/error events
// before the queue — no sends, no drops, no suppressions.
func TestNotifierGroupFilter(t *testing.T) {
	k := newSink(t)
	n := testNotifier(t, agentdef.NotifySpec{URL: k.srv.URL, Events: []string{agentdef.NotifyGroupAlerts}})

	n.consider(approvalEvent("qa", "r1"))
	n.consider(event.Event{Kind: event.KindError, AgentID: "qa", Error: &event.ErrorPayload{Message: "boom"}})
	n.consider(event.Event{Kind: event.KindIterLimit, AgentID: "qa"})
	n.consider(event.Event{Kind: swarm.KindOpsAlert, AgentID: "w", Text: &event.TextPayload{Text: "⏳ stall: w busy\nbody"}})
	waitCount(t, "sink hits", 1, k.count)
	time.Sleep(50 * time.Millisecond)
	if k.count() != 1 || n.Dropped() != 0 || n.Suppressed() != 0 {
		t.Fatalf("filter leaked: hits=%d dropped=%d suppressed=%d", k.count(), n.Dropped(), n.Suppressed())
	}
	if !strings.Contains(k.body(0), "stall") {
		t.Fatalf("delivered = %q, want the ops alert", k.body(0))
	}
}

// TestNotifierRateLimit (NTF-4): a 50-event burst at rate 5 sends 5, counts
// 45 suppressed, and the next delivery after the bucket refills is preceded
// by ONE suppression notice.
func TestNotifierRateLimit(t *testing.T) {
	k := newSink(t)
	n := testNotifier(t, agentdef.NotifySpec{URL: k.srv.URL, RateLimit: 5})
	clock := time.Unix(1_800_000_000, 0)
	n.now = func() time.Time { return clock }

	for i := range 50 {
		n.consider(approvalEvent("qa", fmt.Sprintf("req-%d", i)))
	}
	waitCount(t, "burst sends", 5, k.count)
	if got := n.Suppressed(); got != 45 {
		t.Fatalf("suppressed = %d, want 45", got)
	}

	// A minute later the bucket refills: the next item is delivered with one
	// "N suppressed" notice in front of it.
	clock = clock.Add(time.Minute)
	n.consider(approvalEvent("qa", "req-fresh"))
	waitCount(t, "post-refill sends", 7, k.count)
	if !strings.Contains(k.body(5), "45 notifications suppressed") {
		t.Fatalf("first post-refill delivery = %q, want the suppression notice", k.body(5))
	}
	if !strings.Contains(k.body(6), "req-fresh") && !strings.Contains(k.body(6), "waiting for approval") {
		t.Fatalf("second post-refill delivery = %q, want the fresh gate", k.body(6))
	}
}

// TestNotifierCommandMode: the JSON payload arrives on the command's stdin;
// a hung command is tree-killed at the timeout and counts as a drop.
func TestNotifierCommandMode(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "notif.json")
	n := testNotifier(t, agentdef.NotifySpec{Command: fmt.Sprintf("cat > %q", filepath.ToSlash(out))})

	n.consider(approvalEvent("qa", "r1"))
	waitCount(t, "command sends", 1, func() int { return int(n.Sent()) })
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("command output: %v", err)
	}
	var p notifPayload
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("stdin payload not JSON: %v\n%s", err, b)
	}
	if p.Kind != "approval_needed" || p.Agent != "qa" {
		t.Fatalf("payload = %+v", p)
	}

	// Hung command: killed at the (compressed) timeout, counted as dropped.
	hung := testNotifier(t, agentdef.NotifySpec{Command: "sleep 30"})
	start := time.Now()
	hung.consider(approvalEvent("qa", "r2"))
	waitCount(t, "hung command drops", 1, func() int { return int(hung.Dropped()) })
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("tree kill took %s", took)
	}
}

// TestNotifierCloseDropsQueue: Close never drains against a dead endpoint —
// it aborts in-flight work, drops the queue, and returns promptly.
func TestNotifierCloseDropsQueue(t *testing.T) {
	k := newSink(t, 500, 500, 500, 500, 500, 500, 500, 500)
	n := newNotifier("sp", "s", "", agentdef.NotifySpec{URL: k.srv.URL, Format: agentdef.NotifyFormatJSON, RateLimit: 100})
	n.retryDelay = 10 * time.Second // a live retry wait for Close to abort

	for i := range 4 {
		n.consider(approvalEvent("qa", fmt.Sprintf("r%d", i)))
	}
	waitCount(t, "first attempt", 1, k.count) // sender is mid-item (in the retry wait)
	start := time.Now()
	n.Close()
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("Close took %s — it must not drain against a dead endpoint", took)
	}
	if n.Sent() != 0 {
		t.Fatalf("sent = %d, want 0", n.Sent())
	}
}

// TestNotifyEndToEndBudgetTrip is the full pipe: a space registered with a
// 1-token daily budget and an alerts-only notifier trips the breaker on its
// leader's first metered run — supervisor notifyOps → ops_alert event →
// pump tap → notifier → webhook. No component is called directly.
func TestNotifyEndToEndBudgetTrip(t *testing.T) {
	k := newSink(t)
	svc := New("127.0.0.1:0")
	defer svc.Stop()

	m := stubManifest()
	m.Settings.DailyBudgetTokens = 1 // the stub burns 150/turn — trips on run one
	m.Settings.Notify = &agentdef.NotifySpec{
		URL: k.srv.URL, Format: agentdef.NotifyFormatJSON,
		Events: []string{agentdef.NotifyGroupAlerts}, RateLimit: agentdef.DefaultNotifyRateLimit,
	}
	id, err := svc.register(common.GenUUID(), "ntf-"+common.GenUUID()[:6], m, stubLoaded(), stubConfig(t), false)
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	// Wake the leader with durable mail; its stub run meters 150 tokens.
	ent, _ := svc.entry(id)
	if _, err := ent.space.Bus.Send(store.Message{Sender: "user", Recipient: "leader", Body: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	waitCount(t, "webhook deliveries", 1, k.count)
	var p notifPayload
	if err := json.Unmarshal([]byte(k.body(0)), &p); err != nil {
		t.Fatalf("payload: %v\n%s", err, k.body(0))
	}
	if p.Kind != "ops_alert" || !strings.Contains(p.Title, "budget breaker") || p.Agent != "leader" {
		t.Fatalf("payload = %+v, want the leader's budget-breaker alert", p)
	}

	// Metrics carry the send; teardown is prompt with the notifier attached.
	mi, ok := svc.Metrics(id)
	if !ok || mi.NotifsSent != 1 {
		t.Fatalf("metrics notifsSent = %d (ok=%v), want 1", mi.NotifsSent, ok)
	}
	start := time.Now()
	if err := svc.StopSpace(id); err != nil {
		t.Fatalf("StopSpace: %v", err)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("teardown with notifier took %s", took)
	}
}
