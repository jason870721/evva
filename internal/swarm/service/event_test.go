package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/internal/swarm/webapi"
	"github.com/johnny1110/evva/pkg/common"
)

// loopbackAuth models the pre-RP-15 caller every legacy test assumed: a local
// process, no secret presented.
var loopbackAuth = webapi.EventAuth{Loopback: true}

// registerStubWithSecret brings up a stub space whose manifest demands a
// webhook secret (RP-15 fixture).
func registerStubWithSecret(t *testing.T, s *Service, secret string) string {
	t.Helper()
	m := stubManifest()
	m.Settings.WebhookSecret = secret
	id, err := s.register(common.GenUUID(), "stub-"+common.GenUUID()[:6], m, stubLoaded(), stubConfig(t))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return id
}

// TestIngestEventWakesLeader (RP-9): an external event rides the ordinary bus +
// drain, so the idle leader wakes, drains it, and the row settles read — tagged
// sender "webhook" and shaped with the external-event marker + the body.
func TestIngestEventWakesLeader(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc) // leader + worker

	mid, dup, err := svc.IngestEvent(id, webapi.EventIn{
		Title: "BTC spike", Body: "vol>3sigma", Source: "trader-engine",
		Data: json.RawMessage(`{"z":3.4}`),
	}, loopbackAuth)
	if err != nil || dup || mid == "" {
		t.Fatalf("ingest = id:%q dup:%v err:%v, want fresh delivery", mid, dup, err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var seen, read bool
	for time.Now().Before(deadline) {
		msgs, _ := svc.Messages(id)
		for _, m := range msgs {
			if m.Sender == "webhook" && m.Recipient == "leader" {
				seen = true
				if strings.Contains(m.Body, "external-event") && strings.Contains(m.Body, "vol>3sigma") && m.ReadAt != nil {
					read = true
				}
			}
		}
		if read {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !seen {
		t.Fatal("webhook event never reached the leader's mailbox")
	}
	if !read {
		t.Fatal("leader never drained the external event")
	}
}

// TestIngestEventDedup (RP-9): the same idempotency key delivers once.
func TestIngestEventDedup(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	mid, dup, err := svc.IngestEvent(id, webapi.EventIn{Body: "e", IdempotencyKey: "k9"}, loopbackAuth)
	if err != nil || dup {
		t.Fatalf("first = id:%q dup:%v err:%v", mid, dup, err)
	}
	mid2, dup2, err := svc.IngestEvent(id, webapi.EventIn{Body: "e retry", IdempotencyKey: "k9"}, loopbackAuth)
	if err != nil || !dup2 || mid2 != mid {
		t.Fatalf("retry = id:%q dup:%v err:%v, want same id + dup", mid2, dup2, err)
	}
}

// TestIngestEventErrors (RP-9): unknown space, stopped space, empty body, and an
// unknown recipient each fail — with "unknown"/"stopped" markers so the handler
// can map 404/409.
func TestIngestEventErrors(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	if _, _, err := svc.IngestEvent("nope", webapi.EventIn{Body: "x"}, loopbackAuth); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown space err = %v, want 'unknown'", err)
	}
	if _, _, err := svc.IngestEvent(id, webapi.EventIn{Body: "   "}, loopbackAuth); err == nil {
		t.Error("empty body should error")
	}
	if _, _, err := svc.IngestEvent(id, webapi.EventIn{Body: "x", To: "ghost"}, loopbackAuth); err == nil {
		t.Error("unknown recipient should error")
	}

	// A stopped space is distinct from unknown (→ 409, not 404).
	id2 := registerStub(t, svc)
	if err := svc.StopSpace(id2); err != nil {
		t.Fatalf("StopSpace: %v", err)
	}
	if _, _, err := svc.IngestEvent(id2, webapi.EventIn{Body: "x"}, loopbackAuth); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("stopped space err = %v, want 'stopped'", err)
	}
}

// TestIngestEventWebhookSecret (RP-15): a space with settings.webhook_secret
// demands a matching secret from EVERY caller (loopback included); with it,
// even a remote peer passes.
func TestIngestEventWebhookSecret(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStubWithSecret(t, svc, "s3cret")

	if _, _, err := svc.IngestEvent(id, webapi.EventIn{Body: "x"}, loopbackAuth); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("missing secret err = %v, want 'unauthorized'", err)
	}
	wrong := webapi.EventAuth{Secret: "nope", Loopback: true}
	if _, _, err := svc.IngestEvent(id, webapi.EventIn{Body: "x"}, wrong); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("wrong secret err = %v, want 'unauthorized'", err)
	}
	remote := webapi.EventAuth{Secret: "s3cret", Loopback: false}
	if mid, dup, err := svc.IngestEvent(id, webapi.EventIn{Body: "x"}, remote); err != nil || dup || mid == "" {
		t.Errorf("right secret from remote = id:%q dup:%v err:%v, want accepted", mid, dup, err)
	}
}

// TestIngestEventRemoteNeedsSecret (RP-15): a space WITHOUT a webhook secret
// keeps the RP-9 loopback trust but refuses non-loopback peers — --allow-remote
// must not silently expose an unauthenticated wake endpoint.
func TestIngestEventRemoteNeedsSecret(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	if _, _, err := svc.IngestEvent(id, webapi.EventIn{Body: "x"}, webapi.EventAuth{Loopback: false}); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("remote without secret err = %v, want 'unauthorized'", err)
	}
	if mid, _, err := svc.IngestEvent(id, webapi.EventIn{Body: "x"}, loopbackAuth); err != nil || mid == "" {
		t.Errorf("loopback without secret = id:%q err:%v, want accepted (legacy trust)", mid, err)
	}
}

// RP-16: the service's manual vacuum resolves the window (explicit days > the
// space's setting > the default), reports dry-run and real counts identically,
// and 404-maps an unknown ref.
func TestServiceVacuum(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()
	id := registerStub(t, svc)

	ent, ok := svc.entry(id)
	if !ok {
		t.Fatal("no entry for registered space")
	}
	old := time.Now().AddDate(0, 0, -40).UnixMilli()
	if err := ent.space.Store.PutMessage(store.Message{
		ID: "m-old", Sender: "a", Recipient: "leader", Body: "x", CreatedAt: old, ReadAt: &old,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	// The stub manifest has retention off (0), so days<=0 resolves to the default.
	dry, err := svc.Vacuum(id, 0, true)
	if err != nil {
		t.Fatalf("dry vacuum: %v", err)
	}
	if dry.Messages != 1 || dry.Days != agentdef.DefaultRetentionDays || !dry.DryRun {
		t.Fatalf("dry = %+v, want 1 message at the %d-day default", dry, agentdef.DefaultRetentionDays)
	}
	real, err := svc.Vacuum(id, 0, false)
	if err != nil {
		t.Fatalf("vacuum: %v", err)
	}
	if real.Messages != dry.Messages || len(real.Files) == 0 {
		t.Fatalf("real = %+v, want dry-matching counts + archive files", real)
	}

	// An explicit window narrower than the data's age clears nothing.
	if again, err := svc.Vacuum(id, 365, false); err != nil || again.Messages != 0 || again.Days != 365 {
		t.Fatalf("365-day vacuum = %+v, %v — want 0 rows at days=365", again, err)
	}

	if _, err := svc.Vacuum("ghost", 0, false); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("unknown ref err = %v, want 'unknown'", err)
	}
}

// RP-18: /healthz aggregates — running vs stopped spaces, active vs frozen
// members — with zero names or ids in the payload.
func TestHealthCounts(t *testing.T) {
	svc := New("127.0.0.1:0")
	defer svc.Stop()

	idA := registerStub(t, svc) // leader + worker
	idB := registerStub(t, svc)
	if err := svc.Freeze(idA, "worker"); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	if err := svc.StopSpace(idB); err != nil {
		t.Fatalf("stop: %v", err)
	}

	h := svc.Health()
	if h.Status != "ok" || h.Version == "" || h.UptimeSecs < 0 {
		t.Fatalf("health basics = %+v", h)
	}
	if h.SpacesRunning != 1 || h.SpacesStopped != 1 {
		t.Fatalf("spaces = %d running / %d stopped, want 1 / 1", h.SpacesRunning, h.SpacesStopped)
	}
	if h.MembersActive != 1 || h.MembersFrozen != 1 {
		t.Fatalf("members = %d active / %d frozen, want 1 / 1", h.MembersActive, h.MembersFrozen)
	}
}
