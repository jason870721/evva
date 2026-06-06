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

// closeAgentOpen closes the open streaming turns of ONE agent, leaving other
// agents' in-flight turns open. Swarm members stream concurrently, so their
// chunk events interleave on the single event stream; closing ALL open turns at
// a phase/turn boundary would cut another member mid-stream. Boundaries are
// per-agent, so this is too.
function closeAgentOpen(turns, agent) {
  for (const t of turns) if (t.open && t.agentId === agent) t.open = false
}

// agentOpenTurn returns an agent's currently-streaming (open) text/thinking turn,
// scanning from the end so it skips other agents' interleaved turns — null when
// the agent has none. This is what makes coalescing correct under concurrent
// streaming: a member's deltas accrete into THAT member's open block, not into
// whichever turn happens to be last (which, mid-swarm, is usually another
// member's). Coalescing by `last` alone shattered each stream into one block per
// token once streaming was enabled.
function agentOpenTurn(turns, agent) {
  for (let i = turns.length - 1; i >= 0; i--) {
    const t = turns[i]
    if (t.agentId === agent && t.open && (t.type === 'assistant' || t.type === 'thinking')) return t
  }
  return null
}

// appendChunk folds a text/thinking delta into the agent's open turn of `type`,
// opening a fresh one when needed — and closing the agent's other open streaming
// turn first (the thinking→texting switch) so the two don't tangle.
function appendChunk(turns, agent, type, text) {
  if (!text) return turns
  const open = agentOpenTurn(turns, agent)
  if (open && open.type === type) {
    open.text += text
  } else {
    if (open) open.open = false
    turns.push({ type, agentId: agent, text, open: true })
  }
  return turns
}

// reduceChat folds one agent event into the chat-turn list, in place, and
// returns it. Streaming text/thinking deltas coalesce into the EMITTING agent's
// own open turn — not merely the last turn, because members stream concurrently
// and their deltas interleave (see agentOpenTurn). Tool calls become their own
// turns and resolve by ToolID; a turn_end/run_end closes that agent's open
// accumulation so its next text starts fresh.
//
// A "turn" is one of:
//   {type:'assistant'|'thinking', agentId, text, open}
//   {type:'tool', agentId, tool, toolId, input, status:'running'|'done'|'error', result}
//   {type:'error', agentId, text}
export function reduceChat(turns, ev) {
  if (!ev || !ev.Kind) return turns
  const agent = ev.AgentID || ''

  switch (ev.Kind) {
    case 'text':
    case 'text_chunk':
      return appendChunk(turns, agent, 'assistant', textOf(ev))
    case 'thinking':
    case 'thinking_chunk':
      return appendChunk(turns, agent, 'thinking', thinkingOf(ev))
    case 'tool_use_start': {
      closeAgentOpen(turns, agent)
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
      closeAgentOpen(turns, agent)
      const msg = (ev.Error && ev.Error.Message) || 'error'
      turns.push({ type: 'error', agentId: agent, text: msg })
      return turns
    }
    case 'turn_end':
    case 'run_end':
      closeAgentOpen(turns, agent)
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

// THINKING_PHASES are the sub-phases where the LLM is actively generating with
// no tool running and nothing blocked: running (between sub-phases), thinking
// (reasoning), texting (writing the answer). The roster collapses all three into
// one prominent "thinking" label (sky blue, see Roster.vue) so the operator sees
// "the model is working" at a glance — instead of a flicker of
// running→thinking→texting — and visibly distinct from executing:<tool> and the
// blocked waiting-* phases.
export const THINKING_PHASES = ['running', 'thinking', 'texting']

// displayPhase composes a roster member's coarse run status and fine,
// event-derived phase into one label — the JS twin of swarm.MemberView.DisplayPhase
// (RP-3). A suspended member reads "suspended" (coarse wins); the LLM-generating
// phases collapse to "thinking"; otherwise the fine phase, with the tool appended
// for executing/waiting-approval ("executing:bash"); an empty phase falls back to
// the coarse run. Lets the roster show WHAT a member is doing (thinking /
// executing a tool / blocked on approval) instead of a flat "busy".
export function displayPhase(m) {
  if (!m) return ''
  if (m.run === 'suspended') return 'suspended'
  if (!m.phase) return m.run || ''
  if (THINKING_PHASES.includes(m.phase)) return 'thinking'
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
  if (THINKING_PHASES.includes(p)) return 'thinking'
  return 'busy'
}

// phaseFor maps one wire event to the fine run sub-phase it implies, or null if
// the event doesn't move the phase. The JS twin of the Go phaseDeriver
// (internal/swarm/phase.go) — kept in lockstep so the web can derive a member's
// live phase from the SAME event stream the backend roster does. Returns
// { phase, tool }.
function phaseFor(ev) {
  switch (ev.Kind) {
    case 'run_start':
    case 'run_resume':
    case 'turn_start':
    case 'turn_end':
    case 'tool_use_result': // tool done — back to generic running until the next sub-phase
    case 'compacting_end':
      return { phase: 'running', tool: '' }
    case 'run_end':
    case 'idle':
    case 'run_cancelled':
      return { phase: 'ready', tool: '' }
    case 'thinking':
    case 'thinking_chunk':
      return { phase: 'thinking', tool: '' }
    case 'text':
    case 'text_chunk':
      return { phase: 'texting', tool: '' }
    case 'tool_use_start':
      return { phase: 'executing', tool: (ev.ToolUseStart && ev.ToolUseStart.Name) || '' }
    case 'approval_needed':
      return { phase: 'waiting-approval', tool: (ev.ApprovalNeeded && ev.ApprovalNeeded.ToolName) || '' }
    case 'question_needed':
      return { phase: 'waiting-input', tool: '' }
    case 'draining_info':
    case 'drain_inbox':
    case 'drain_background_task':
    case 'drain_monitor_events':
      return { phase: 'draining', tool: '' }
    case 'compacting':
      return { phase: 'compacting', tool: '' }
    case 'iter_limit':
      return { phase: 'paused', tool: '' }
    case 'error':
      return { phase: 'error', tool: '' }
    default:
      return null
  }
}

// reducePhase folds one event into a per-agent live-phase map, in place-ish
// (returns a new map only on a real change so Vue reactivity fires once), keyed
// by AgentID → { phase, tool, since }. `since` is restamped only when the PHASE
// changes (a tool-only change keeps the clock — matches roster.go setPhase), so
// a pill's elapsed time stays meaningful. Events that don't bear on phase are
// no-ops. SpaceView overlays this onto the polled roster so the status pill —
// notably the sky-blue "thinking" — updates the instant the event lands, not on
// the 2.5s REST poll.
export function reducePhase(map, ev, now = Date.now()) {
  if (!ev || !ev.Kind) return map
  const agent = ev.AgentID || ''
  if (!agent) return map
  const next = phaseFor(ev)
  if (!next) return map
  const cur = map[agent]
  if (cur && cur.phase === next.phase && cur.tool === next.tool) return map
  const since = cur && cur.phase === next.phase ? cur.since : now
  return { ...map, [agent]: { phase: next.phase, tool: next.tool, since } }
}

// relTime formats an absolute unix-ms timestamp as a short relative age — "now",
// "8s", "3m", "2h", "4d" — for board cards and the activity timeline.
export function relTime(ms, now = Date.now()) {
  if (!ms) return ''
  const s = Math.floor((now - ms) / 1000)
  if (s < 5) return 'now'
  if (s < 60) return `${s}s`
  const m = Math.floor(s / 60)
  if (m < 60) return `${m}m`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h`
  return `${Math.floor(h / 24)}d`
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

// mailState classifies a message for the timeline/mailbox from the
// unread→claimed→read lifecycle (store migration 0002; webapi.MessageInfo):
//   'read'    — readAt set: the recipient's run settled cleanly.
//   'reading' — claimedAt set, not yet read: folded into an in-flight run RIGHT
//               NOW — the agent has it and is acting on it.
//   'unread'  — neither: sitting in the mailbox, untouched.
// The 'reading' state is why a message the agent is actively processing no
// longer looks identical to one never opened until the whole run ends.
export function mailState(m) {
  if (!m) return 'unread'
  if (m.readAt) return 'read'
  if (m.claimedAt) return 'reading'
  return 'unread'
}
