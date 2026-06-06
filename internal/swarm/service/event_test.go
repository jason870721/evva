package service

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/webapi"
)

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
	})
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

	mid, dup, err := svc.IngestEvent(id, webapi.EventIn{Body: "e", IdempotencyKey: "k9"})
	if err != nil || dup {
		t.Fatalf("first = id:%q dup:%v err:%v", mid, dup, err)
	}
	mid2, dup2, err := svc.IngestEvent(id, webapi.EventIn{Body: "e retry", IdempotencyKey: "k9"})
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

	if _, _, err := svc.IngestEvent("nope", webapi.EventIn{Body: "x"}); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Errorf("unknown space err = %v, want 'unknown'", err)
	}
	if _, _, err := svc.IngestEvent(id, webapi.EventIn{Body: "   "}); err == nil {
		t.Error("empty body should error")
	}
	if _, _, err := svc.IngestEvent(id, webapi.EventIn{Body: "x", To: "ghost"}); err == nil {
		t.Error("unknown recipient should error")
	}

	// A stopped space is distinct from unknown (→ 409, not 404).
	id2 := registerStub(t, svc)
	if err := svc.StopSpace(id2); err != nil {
		t.Fatalf("StopSpace: %v", err)
	}
	if _, _, err := svc.IngestEvent(id2, webapi.EventIn{Body: "x"}); err == nil || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("stopped space err = %v, want 'stopped'", err)
	}
}
