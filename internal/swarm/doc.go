// Package swarm is the per-space coordination core of Veronica, evva's
// in-process multi-agent swarm subsystem.
//
// A SwarmSpace is one isolated "sub-cluster": a Leader plus Workers, each a
// long-lived agent.New(...) root agent, collaborating through a private
// message bus and a per-space .vero/ SQLite ledger. The Supervisor owns the
// space lifecycle (start/stop, dynamic membership, freeze, suspend/resume);
// the Scheduler turns wake sources (message / task / timer) into
// Controller.Run calls; the Roster is the single source of truth for "who is
// in this space and what are they doing", feeding both the list_members tool
// and the web API.
//
// The process-singleton multi-space host (the :8888 HTTP/WS server and the
// SwarmSpace registry) lives one layer up in internal/swarm/service; the
// leaf components (bus, store, agentdef, tools) live in their own
// subpackages.
//
// # Invariant: the multi-agent oracle (pkg-only for agent concerns)
//
// Everything under internal/swarm/** consumes agent functionality through
// the public pkg/* surface ONLY (agent.New, pkg/event, pkg/tools, pkg/skill,
// pkg/permission, ...) and never imports internal/agent or any other evva
// internal package. This is enforced by scripts/depcheck.sh in CI. The single
// sanctioned exception is the public inbox-drainer seam added to pkg/agent in
// SPRD-1-12 — which is itself public, not a private reach-in. Keeping the
// swarm pkg-pure makes it evva's multi-agent completeness oracle: if evva's
// own swarm can be built on pkg/* alone, a third party's can too.
package swarm
