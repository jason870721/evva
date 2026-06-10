package swarm

import (
	"errors"
	"testing"
	"time"

	"github.com/johnny1110/evva/internal/swarm/agentdef"
	"github.com/johnny1110/evva/internal/swarm/store"
)

// RP-16: with retention_days set, the supervisor's daily sweep — which also
// fires once right after startup — archives-and-deletes consumed history.
func TestRetentionSweepAtStartup(t *testing.T) {
	sp, _ := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	sp.settings.RetentionDays = 30
	old := time.Now().AddDate(0, 0, -40).UnixMilli()
	if err := sp.Store.PutMessage(store.Message{
		ID: "m-old", Sender: "a", Recipient: "w", Body: "x", CreatedAt: old, ReadAt: &old,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	startSup(t, sp)
	waitFor(t, 5*time.Second, "old read message vacuumed by the startup sweep", func() bool {
		_, err := sp.Store.GetMessage("m-old")
		return errors.Is(err, store.ErrMessageNotFound)
	})
}

// RP-16: the zero-value (retention off — what a Go-built unit space gets, and
// what `retention_days: "0"` declares) keeps the pre-RP-16 never-delete
// behavior: the sweep never touches the ledger.
func TestRetentionSweepDisabledByZero(t *testing.T) {
	sp, _ := ctlSpace(t, map[string]agentdef.Role{"w": agentdef.RoleWorker})
	old := time.Now().AddDate(0, 0, -40).UnixMilli()
	if err := sp.Store.PutMessage(store.Message{
		ID: "m-old", Sender: "a", Recipient: "w", Body: "x", CreatedAt: old, ReadAt: &old,
	}); err != nil {
		t.Fatalf("put: %v", err)
	}

	startSup(t, sp)
	time.Sleep(60 * time.Millisecond) // many 5ms ticks
	if _, err := sp.Store.GetMessage("m-old"); err != nil {
		t.Fatalf("retention-off sweep touched the ledger: %v", err)
	}
}
