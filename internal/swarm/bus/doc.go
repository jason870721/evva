// Package bus is the per-space message bus and mailboxes. Each agent has one
// mailbox channel that carries only message UUIDs (never payloads); the
// SQLite `messages` table is the durable source of truth. Delivery writes the
// message to the store first, then pushes the UUID onto the recipient's
// channel (ordering guarantee). "to: all" broadcasts to every active member.
//
// Carrying only UUIDs is what makes restart-resume trivial: on boot the
// Supervisor reloads unread UUIDs (store.UnreadFor) back onto the channels via
// Bus.Requeue and nothing in flight is lost.
//
// A "to: all" broadcast fans out into one durable row per active peer (each its
// own UUID and read_at), not a single recipient="all" row — that is what lets a
// broadcast restart-resume and track per-recipient read state exactly like a
// unicast message.
package bus
