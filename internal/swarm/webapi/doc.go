// Package webapi serves the swarm workstation API: REST snapshots
// (/api/swarms, /api/swarm/:id, /api/tasks, /api/agents/:name/transcript,
// /api/messages) and a WebSocket bridge that streams each agent's
// pkg/event.Event out to the browser, fanned out by (spaceID, AgentID).
// Inbound, the browser drives each agent's pkg/agent Controller (Run,
// RespondPermission, RespondQuestion) and the Supervisor (suspend, add,
// freeze, halt).
//
// The pkg/event doc already anticipates "a JSON-over-websocket bridge" — the
// Hub here is it: internal/swarm/service pumps each space's tagged event stream
// into Hub.Publish, which fans it out to the matching sockets.
//
// The package owns its own wire DTOs (SpaceInfo/MemberInfo/TaskInfo/…) and
// talks to the host only through the narrow Backend interface, so it imports no
// agent/store/llm types — the swarm domain maps into these shapes, not the
// reverse.
package webapi
