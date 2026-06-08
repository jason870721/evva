// Package agentdef is the re-callable loader that turns one on-disk agent
// directory (agents/{main,sub}/{name}/: system_prompt.md, tools/active.yml,
// tools/deferr.yml, profile.yml, skills/*) into the public SDK objects needed
// to construct a live agent — a pkg/agent.AgentDefinition plus a
// *pkg/skill.Registry — using pkg/* only. It also parses the swarm manifest
// (evva-swarm.yml) and surfaces each agent's optional Schedule (cron /
// interval) for the Scheduler's timer wakes.
//
// Re-callability is the point: "read one dir -> Loaded" is a pure function, so
// dynamic hot-load (SPRD-1-6) and restart-rebuild (SPRD-1-11) are just another
// call. Note: in Veronica both main and sub agents are ROOT agents — the
// main/sub split is a leader/worker role marker, not evva's spawn semantics.
//
// TODO(SPRD-1-3): implement LoadManifest, Build (one dir), and BuildAll.
package agentdef
