package bus

import (
	"log/slog"
	"strings"
	"sync"

	"github.com/johnny1110/evva/internal/swarm/store"
	"github.com/johnny1110/evva/pkg/common"
)

// inboxBuffer is the per-agent mailbox capacity. The channel only carries
// message UUIDs (a "you have mail" hint), so a generous buffer is cheap; once
// it fills, Send drops only the hint and the row stays durable in the store,
// recoverable via store.UnreadFor (DB is truth, the chan is a hint).
const inboxBuffer = 256

// Membership is the minimal roster view the bus needs to expand a "to: all"
// broadcast. It is declared here (not imported from package swarm) so the bus
// never imports swarm — the swarm Roster implements this structurally, keeping
// the dependency one-directional (swarm -> bus).
type Membership interface {
	// ActiveMembers returns the names of in-service members; frozen/cold-stored
	// members are excluded so a broadcast never reaches them.
	ActiveMembers() []string
}

// Bus is one per-space inter-agent transport: a mailbox channel per agent that
// carries message UUIDs, backed by the durable messages table. Delivery writes
// the row first, then signals the channel (the §6.2 persist-before-signal
// invariant), so any drain that reads a UUID always finds its row. A Bus
// belongs to exactly one SwarmSpace; recipients resolve only within it
// (invariant #2 — no cross-space delivery).
type Bus struct {
	mu      sync.RWMutex
	inboxes map[string]chan string
	store   *store.Store
	roster  Membership
}

// New builds a Bus over a space's store and roster view. Register each member's
// mailbox before the scheduler selects on it.
func New(st *store.Store, roster Membership) *Bus {
	return &Bus{
		inboxes: make(map[string]chan string),
		store:   st,
		roster:  roster,
	}
}

// Register creates name's mailbox. It is idempotent: a second call keeps the
// existing channel (and any UUIDs already queued on it) rather than replacing
// it, so re-registering an active member never drops pending mail.
func (b *Bus) Register(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.inboxes[name]; ok {
		return
	}
	b.inboxes[name] = make(chan string, inboxBuffer)
}

// Deregister drops name's mailbox (e.g. when a member is frozen). The channel
// is deleted, not closed: closing it would make a scheduler's select spin on
// the zero value, and risks a send-on-closed panic under a racing Send.
// Undrained UUIDs stay durable in the store and reload via Requeue +
// store.UnreadFor when the member is re-registered.
func (b *Bus) Deregister(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.inboxes, name)
}

// Inbox returns name's mailbox for the scheduler to select on. It returns nil
// for an unregistered name — a receive on a nil channel blocks forever, the
// safe no-op — so Register before Inbox.
func (b *Bus) Inbox(name string) <-chan string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.inboxes[name]
}

// Send delivers m. For a single recipient it persists one row then signals that
// mailbox; for store.RecipientAll it fans out to every active peer (one durable
// row each — see broadcast). The bus assigns every delivered row a fresh UUID
// (it is the single source of message identity, §6.2), so any ID on m is
// ignored. The returned uuid is the delivered row's id for a single recipient,
// and empty for a broadcast (which has no single identity). Send never blocks
// the calling agent's tool execution.
func (b *Bus) Send(m store.Message) (uuid string, err error) {
	if m.Recipient == store.RecipientAll {
		return "", b.broadcast(m)
	}
	return b.deliver(m, m.Recipient)
}

// SendExternal delivers an external-event message (RP-9) to one recipient with
// optional idempotency. With an empty key it is plain deliver. With a key it
// collapses retries: a key already seen returns the original message id and
// dup=true WITHOUT re-inserting or re-signalling — so a jittery engine that
// retries the same event can't wake (and trigger) the leader twice. Like Send it
// never blocks the caller. Unicast only (the webhook addresses one member).
func (b *Bus) SendExternal(m store.Message, key string) (id string, dup bool, err error) {
	if strings.TrimSpace(key) == "" {
		id, err = b.deliver(m, m.Recipient)
		return id, false, err
	}
	m.ID = common.GenUUID()
	inserted, existingID, err := b.store.PutMessageIfNew(m, key)
	if err != nil {
		return "", false, err
	}
	if !inserted {
		return existingID, true, nil // duplicate — already delivered/processing, don't re-wake
	}
	b.signal(m.Recipient, m.ID)
	return m.ID, false, nil
}

// broadcast fans m out to every active member except the sender — one durable
// row per recipient, each with its own UUID and read_at. Per-recipient rows are
// what make a broadcast behave exactly like unicast for restart-resume
// (store.UnreadFor(name) finds it) and per-recipient read tracking; a single
// shared recipient="all" row could do neither. The sender is skipped because a
// broadcast goes to peers — an agent does not mail (and so wake) itself.
func (b *Bus) broadcast(m store.Message) error {
	for _, name := range b.roster.ActiveMembers() {
		if name == m.Sender {
			continue
		}
		if _, err := b.deliver(m, name); err != nil {
			return err
		}
	}
	return nil
}

// deliver persists one row addressed to `to` (fresh UUID), then signals that
// recipient's mailbox. PutMessage commits before signal pushes — the
// persist-before-signal ordering (§6.2). m is taken by value, so the broadcast
// loop's per-recipient mutations don't leak across rows.
func (b *Bus) deliver(m store.Message, to string) (string, error) {
	m.ID = common.GenUUID()
	m.Recipient = to
	if err := b.store.PutMessage(m); err != nil {
		return "", err
	}
	b.signal(to, m.ID)
	return m.ID, nil
}

// signal pushes a UUID onto a recipient's mailbox without ever blocking. A
// missing inbox (unregistered or deregistered/frozen) or a full buffer drops
// only the hint — the row is already durable, so the reader recovers it via
// store.UnreadFor on its next cycle.
func (b *Bus) signal(to, uuid string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ch, ok := b.inboxes[to]
	if !ok {
		return
	}
	select {
	case ch <- uuid:
	default:
		// Buffer full: the hint is dropped but the row is durable, so the
		// recipient still recovers it via store.UnreadFor on its next cycle.
		// Worth a debug line — a chronically full mailbox is a real stall signal.
		slog.Debug("swarm bus: mailbox hint dropped (buffer full)", "recipient", to, "id", uuid)
	}
}

// Requeue re-pushes a list of unread UUIDs onto name's mailbox in order. The
// restart path (SPRD-1-11) calls it on boot with store.UnreadFor(name) (already
// created_at-ordered) after re-registering the member. Non-blocking like Send:
// if the buffer fills, the remainder stay recoverable via store.UnreadFor.
func (b *Bus) Requeue(name string, uuids []string) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	ch, ok := b.inboxes[name]
	if !ok {
		return
	}
	for _, id := range uuids {
		select {
		case ch <- id:
		default:
			return
		}
	}
}
