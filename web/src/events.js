// Pure, framework-free reducers over the swarm event/REST shapes. Kept out of
// the .vue components so they can be unit-tested with `node --test` (no DOM, no
// build step) and so the Web's render semantics provably match evva's TUI event
// kinds (SPRD-1-10 §4: "mirror evva's TUI event semantics").
//
// The wire event is the Go `event.Event` marshalled as-is: PascalCase fields
// (Kind/AgentID/Time) and one populated payload pointer named after its kind
// (Text, ToolUseStart, ApprovalNeeded, …). The service wraps it as
// {spaceId, event}.

export const TASK_STATES = ['pending', 'running', 'suspended', 'verifying', 'completed']

// textOf / thinkingOf pull the renderable delta out of a text(-chunk) event.
export function textOf(ev) {
  return (ev && ev.Text && ev.Text.Text) || ''
}
export function thinkingOf(ev) {
  return (ev && ev.Thinking && ev.Thinking.Text) || ''
}

function closeOpen(turns) {
  for (const t of turns) t.open = false
}

// reduceChat folds one agent event into the chat-turn list, in place, and
// returns it. Streaming text/thinking deltas coalesce into the open turn for
// that agent; tool calls become their own turns and resolve by ToolID; a
// turn_end/run_end closes the open accumulation so the next text starts fresh.
//
// A "turn" is one of:
//   {type:'assistant'|'thinking', agentId, text, open}
//   {type:'tool', agentId, tool, toolId, input, status:'running'|'done'|'error', result}
//   {type:'error', agentId, text}
export function reduceChat(turns, ev) {
  if (!ev || !ev.Kind) return turns
  const agent = ev.AgentID || ''
  const last = turns[turns.length - 1]

  switch (ev.Kind) {
    case 'text':
    case 'text_chunk': {
      const t = textOf(ev)
      if (!t) return turns
      if (last && last.type === 'assistant' && last.open && last.agentId === agent) {
        last.text += t
      } else {
        turns.push({ type: 'assistant', agentId: agent, text: t, open: true })
      }
      return turns
    }
    case 'thinking':
    case 'thinking_chunk': {
      const t = thinkingOf(ev)
      if (!t) return turns
      if (last && last.type === 'thinking' && last.open && last.agentId === agent) {
        last.text += t
      } else {
        turns.push({ type: 'thinking', agentId: agent, text: t, open: true })
      }
      return turns
    }
    case 'tool_use_start': {
      closeOpen(turns)
      const p = ev.ToolUseStart || {}
      turns.push({
        type: 'tool',
        agentId: agent,
        tool: p.Name || '',
        toolId: p.ToolID || '',
        input: p.Input,
        status: 'running',
      })
      return turns
    }
    case 'tool_use_result': {
      const p = ev.ToolUseResult || {}
      const tt = turns.find((x) => x.type === 'tool' && x.toolId === p.ToolID)
      if (tt) {
        tt.status = p.IsError ? 'error' : 'done'
        tt.result = p.Content || ''
      }
      return turns
    }
    case 'error': {
      closeOpen(turns)
      const msg = (ev.Error && ev.Error.Message) || 'error'
      turns.push({ type: 'error', agentId: agent, text: msg })
      return turns
    }
    case 'turn_end':
    case 'run_end':
      closeOpen(turns)
      return turns
    default:
      return turns
  }
}

// groupTasks buckets a task list into the 5 board columns, preserving order.
// Unknown statuses are dropped (defensive — the ledger only emits the 5).
export function groupTasks(tasks) {
  const cols = {}
  for (const s of TASK_STATES) cols[s] = []
  for (const t of tasks || []) {
    if (cols[t.status]) cols[t.status].push(t)
  }
  return cols
}

// consoleTurns selects the turns belonging to one member's console: that
// member's own agent turns (matched by AgentID), plus the operator's outgoing
// messages addressed to it (user turns matched by `target` member name). This is
// what powers the per-member view of the flat-comms UI — one mixed event stream,
// demuxed per member.
export function consoleTurns(turns, agentId, member) {
  return (turns || []).filter((t) =>
    t.type === 'user' ? t.target === member : agentId !== '' && t.agentId === agentId,
  )
}

// displayPhase composes a roster member's coarse run status and fine,
// event-derived phase into one label — the JS twin of swarm.MemberView.DisplayPhase
// (RP-3). A suspended member reads "suspended" (coarse wins); otherwise the fine
// phase, with the tool appended for executing/waiting-approval ("executing:bash");
// an empty phase falls back to the coarse run. Lets the roster show WHAT a member
// is doing (thinking / executing a tool / blocked on approval) instead of a flat
// "busy".
export function displayPhase(m) {
  if (!m) return ''
  if (m.run === 'suspended') return 'suspended'
  if (!m.phase) return m.run || ''
  return m.tool ? `${m.phase}:${m.tool}` : m.phase
}

// phaseClass maps a member to a CSS modifier so the roster pill can colour
// blocked/active phases distinctly (waiting-approval stands out — it is the
// state an operator must act on).
export function phaseClass(m) {
  if (!m) return ''
  if (m.run === 'suspended') return 'suspended'
  const p = m.phase || m.run || ''
  if (p === 'waiting-approval' || p === 'waiting-input') return 'waiting'
  if (p === 'error') return 'error'
  if (p === 'ready' || m.run === 'idle') return 'idle'
  return 'busy'
}

// attentionKind classifies whether a roster member needs the operator's
// attention right now, and how urgently (RP-4 UX-1): 'act' = blocked waiting for
// a human (approval / question); 'warn' = errored or paused (likely needs a
// look); '' = fine. The Attention Bar aggregates these so the operator's first
// question — "what needs me?" — is answered without scanning the whole roster.
export function attentionKind(m) {
  if (!m) return ''
  const p = m.phase || ''
  if (p === 'waiting-approval' || p === 'waiting-input') return 'act'
  if (p === 'error' || p === 'paused') return 'warn'
  return ''
}

// elapsed formats a duration (now − sinceMs) as a compact clock: "12s", "2:41",
// "1:03:20". Empty for a missing/zero timestamp. Used to surface "stuck for N".
export function elapsed(sinceMs, now = Date.now()) {
  if (!sinceMs) return ''
  let s = Math.floor((now - sinceMs) / 1000)
  if (s < 0) s = 0
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  const sec = String(s % 60).padStart(2, '0')
  if (m < 60) return `${m}:${sec}`
  const h = Math.floor(m / 60)
  return `${h}:${String(m % 60).padStart(2, '0')}:${sec}`
}

// attentionItems distills the roster into what the operator should act on,
// most-urgent first: 'act' before 'warn', then longest-waiting first. Each item
// carries what the Attention Bar needs to render a chip and focus the member.
export function attentionItems(roster, now = Date.now()) {
  const items = []
  for (const m of roster || []) {
    const kind = attentionKind(m)
    if (!kind) continue
    items.push({
      name: m.name,
      kind,
      phase: m.phase,
      tool: m.tool || '',
      since: m.phaseSince || 0,
      elapsed: elapsed(m.phaseSince, now),
    })
  }
  items.sort((a, b) => (a.kind === b.kind ? a.since - b.since : a.kind === 'act' ? -1 : 1))
  return items
}

// isApproval / isQuestion classify the two interactive gate events the Leader
// Chat surfaces as overlays.
export function isApproval(ev) {
  return ev && ev.Kind === 'approval_needed'
}
export function isQuestion(ev) {
  return ev && ev.Kind === 'question_needed'
}

// approvalOf / questionOf normalise a gate event into the overlay's view model.
export function approvalOf(ev) {
  const p = (ev && ev.ApprovalNeeded) || {}
  return {
    agentId: ev.AgentID || '',
    requestId: p.RequestID || '',
    tool: p.ToolName || '',
    description: p.InputDescription || '',
    reason: p.Reason || '',
    risk: p.RiskHint || '',
    plan: p.PlanContent || '',
    input: p.ToolInput,
  }
}
export function questionOf(ev) {
  const p = (ev && ev.QuestionNeeded) || {}
  return {
    agentId: p.AgentID || ev.AgentID || '',
    requestId: p.RequestID || '',
    questions: p.Questions || [],
  }
}

// touchesLedger reports whether an event likely changed the task ledger or
// roster, so the UI can refresh those REST snapshots promptly (the swarm task
// store is separate from evva's event-emitting stores, so we trigger on the
// Leader's task_* / membership tool results rather than store_update events).
export function touchesLedger(ev) {
  if (!ev) return false
  if (ev.Kind === 'store_update') return true
  if (ev.Kind === 'tool_use_result') {
    // The result event carries no tool name; pair via the open tool turn is
    // overkill here — the caller refreshes on any tool result, which on a
    // localhost workstation is cheap and keeps the board honest.
    return true
  }
  return false
}
