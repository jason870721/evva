// Package tools holds Veronica's swarm-specific custom tools, written against
// pkg/tools.Tool and attached to agents via pkg/agent.WithCustomTool:
//
//   - task_create / task_assign / task_update_status / task_verify /
//     task_list  — Leader-only task-ledger writes.
//   - my_tasks / task_get                              — Worker read-only views.
//   - send_message                                     — built per agent so the
//     sender identity is baked into the closure (the recipient must know who
//     wrote); writes the durable message, pushes the UUID to the mailbox.
//   - list_members                                     — read-only roster view
//     for "who is on, who is the right expert" before sending mail.
//
// The Leader and Worker tool sets differ — the permission boundary IS the tool
// boundary. This package implements the ToolSet seam that SwarmSpace assembly
// (SPRD-1-4) injects, so the space has no hard dependency on this package's
// concrete tools.
//
// Per-agent identity (the sender name, the space) reaches each tool via the
// member's Config (swarm.MemberContext), NOT a factory closure:
// pkg/agent.WithCustomTool registers one factory per tool name process-wide, so
// the same factory serves every agent and reads its identity from the per-agent
// Config it is built against. Write-class ledger tools are gated by
// pkg/permission (the read/self tools are registered into its auto-allow
// safelist in init); the leader-only guard itself lives in the store.
package tools
