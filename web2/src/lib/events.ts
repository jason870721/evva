// Pure, framework-free reducers over the swarm event/REST shapes — ported from
// web/src/events.js with types. Kept out of components so they unit-test under
// `node --test` (no DOM, no build) and so the web's render semantics provably
// match evva's TUI event kinds. reducePhase/phaseFor are the JS twin of the Go
// phaseDeriver (internal/swarm/phase.go) and must stay in lockstep.

import type { WireEvent, QuestionItem } from '../types/events'
import type { TaskInfo, TaskStatus, MessageInfo } from '../types/wire'

export const TASK_STATES: readonly TaskStatus[] = [
  'pending',
  'running',
  'suspended',
  'verifying',
  'completed',
]

// ── Turn model ──────────────────────────────────────────────────────────────
export interface AssistantTurn {
  type: 'assistant'
  agentId: string
  text: string
  open: boolean
}
export interface ThinkingTurn {
  type: 'thinking'
  agentId: string
  text: string
  open: boolean
}
export interface ToolTurn {
  type: 'tool'
  agentId: string
  tool: string
  toolId: string
  input?: unknown
  status: 'running' | 'done' | 'error'
  result?: string
}
export interface ErrorTurn {
  type: 'error'
  agentId: string
  text: string
}
export interface UserTurn {
  type: 'user'
  target: string
  agentId: string
  text: string
}
export type StreamTurn = AssistantTurn | ThinkingTurn
export type Turn = StreamTurn | ToolTurn | ErrorTurn | UserTurn

// textOf / thinkingOf pull the renderable delta out of a text(-chunk) event.
export function textOf(ev?: WireEvent | null): string {
  return (ev && ev.Text && ev.Text.Text) || ''
}
export function thinkingOf(ev?: WireEvent | null): string {
  return (ev && ev.Thinking && ev.Thinking.Text) || ''
}

// closeAgentOpen closes ONE agent's open streaming turns, leaving others' in-flight
// turns open (members stream concurrently on one feed).
function closeAgentOpen(turns: Turn[], agent: string): void {
  for (const t of turns) {
    if ((t.type === 'assistant' || t.type === 'thinking') && t.open && t.agentId === agent) {
      t.open = false
    }
  }
}

// agentOpenTurn returns an agent's currently-streaming open text/thinking turn,
// scanning from the end so it skips other agents' interleaved turns.
function agentOpenTurn(turns: Turn[], agent: string): StreamTurn | null {
  for (let i = turns.length - 1; i >= 0; i--) {
    const t = turns[i]
    if (t.agentId === agent && (t.type === 'assistant' || t.type === 'thinking') && t.open) {
      return t
    }
  }
  return null
}

// appendChunk folds a delta into the agent's open turn of `type`, opening a fresh
// one when needed (closing the agent's other open streaming turn first).
function appendChunk(turns: Turn[], agent: string, type: StreamTurn['type'], text: string): Turn[] {
  if (!text) return turns
  const open = agentOpenTurn(turns, agent)
  if (open && open.type === type) {
    open.text += text
  } else {
    if (open) open.open = false
    turns.push({ type, agentId: agent, text, open: true } as StreamTurn)
  }
  return turns
}

// reduceChat folds one agent event into the chat-turn list, in place, returning
// it. Streaming deltas coalesce into the EMITTING agent's own open turn (not the
// last turn — members interleave). Tool calls become turns and resolve by ToolID.
export function reduceChat(turns: Turn[], ev: WireEvent): Turn[] {
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
      const tt = turns.find((x): x is ToolTurn => x.type === 'tool' && x.toolId === p.ToolID)
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
// Unknown statuses are dropped (defensive).
export function groupTasks(tasks: TaskInfo[]): Record<TaskStatus, TaskInfo[]> {
  const cols = {} as Record<TaskStatus, TaskInfo[]>
  for (const s of TASK_STATES) cols[s] = []
  for (const t of tasks || []) {
    if (cols[t.status]) cols[t.status].push(t)
  }
  return cols
}

// consoleTurns selects one member's turns: that member's own agent turns (by
// AgentID) plus the operator's outgoing messages addressed to it (by target).
export function consoleTurns(turns: Turn[], agentId: string, member: string): Turn[] {
  return (turns || []).filter((t) =>
    t.type === 'user' ? t.target === member : agentId !== '' && t.agentId === agentId,
  )
}

// THINKING_PHASES collapse to one prominent "thinking" label (running between
// sub-phases, thinking reasoning, texting writing) so the operator sees "the
// model is working" instead of a flicker.
export const THINKING_PHASES = ['running', 'thinking', 'texting']

export interface PhaseLike {
  name?: string
  run?: string
  phase?: string
  tool?: string
  phaseSince?: number
}

// displayPhase composes coarse run + fine event-derived phase into one label —
// the JS twin of swarm.MemberView.DisplayPhase (RP-3).
export function displayPhase(m?: PhaseLike | null): string {
  if (!m) return ''
  if (m.run === 'suspended') return 'suspended'
  if (!m.phase) return m.run || ''
  if (THINKING_PHASES.includes(m.phase)) return 'thinking'
  return m.tool ? `${m.phase}:${m.tool}` : m.phase
}

// phaseClass maps a member to a CSS modifier so the pill can colour blocked/active
// phases distinctly (waiting-approval stands out — the operator must act on it).
export function phaseClass(m?: PhaseLike | null): string {
  if (!m) return ''
  if (m.run === 'suspended') return 'suspended'
  const p = m.phase || m.run || ''
  if (p === 'waiting-approval' || p === 'waiting-input') return 'waiting'
  if (p === 'error') return 'error'
  if (p === 'ready' || m.run === 'idle') return 'idle'
  if (THINKING_PHASES.includes(p)) return 'thinking'
  return 'busy'
}

interface PhaseDelta {
  phase: string
  tool: string
}

// phaseFor maps one wire event to the fine run sub-phase it implies, or null.
// JS twin of the Go phaseDeriver — keep in lockstep.
function phaseFor(ev: WireEvent): PhaseDelta | null {
  switch (ev.Kind) {
    case 'run_start':
    case 'run_resume':
    case 'turn_start':
    case 'turn_end':
    case 'tool_use_result':
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

export interface LivePhase {
  phase: string
  tool: string
  since: number
}
export type PhaseMap = Record<string, LivePhase>

// reducePhase folds one event into a per-agent live-phase map, returning a new
// map only on a real change (so Vue reactivity fires once). `since` restamps only
// when the PHASE changes (a tool-only change keeps the clock — matches roster.go
// setPhase).
export function reducePhase(map: PhaseMap, ev: WireEvent, now: number = Date.now()): PhaseMap {
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

// relTime formats an absolute unix-ms timestamp as a short relative age.
export function relTime(ms: number, now: number = Date.now()): string {
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

// attentionKind classifies whether a member needs the operator now (RP-4 UX-1):
// 'act' = blocked on a human; 'warn' = errored/paused; '' = fine.
export type AttentionKind = '' | 'act' | 'warn'
export function attentionKind(m?: PhaseLike | null): AttentionKind {
  if (!m) return ''
  const p = m.phase || ''
  if (p === 'waiting-approval' || p === 'waiting-input') return 'act'
  if (p === 'error' || p === 'paused') return 'warn'
  return ''
}

// elapsed formats a duration (now − sinceMs) as a compact clock.
export function elapsed(sinceMs: number, now: number = Date.now()): string {
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

export interface AttentionItem {
  name: string
  kind: 'act' | 'warn'
  phase: string
  tool: string
  since: number
  elapsed: string
  stalled?: boolean
}

// Stall thresholds: a member stuck in an active phase longer than this needs a
// look even though it isn't formally blocked (RP-4 H1). Defaults; overridable.
export interface AttentionOpts {
  stallExecMs?: number
  stallThinkMs?: number
}

// attentionItems distills the roster into what to act on, most-urgent first:
// 'act' (blocked on a human) before 'warn' (errored / paused / stalled), then
// longest-waiting first. A stall is an executing/thinking phase whose elapsed
// time exceeds the threshold — surfaced as 'warn' with stalled=true.
export function attentionItems(
  roster: PhaseLike[],
  now: number = Date.now(),
  opts: AttentionOpts = {},
): AttentionItem[] {
  const execMs = opts.stallExecMs ?? 5 * 60_000
  const thinkMs = opts.stallThinkMs ?? 3 * 60_000
  const items: AttentionItem[] = []
  for (const m of roster || []) {
    let kind = attentionKind(m)
    let stalled = false
    if (!kind) {
      const since = m.phaseSince || 0
      const age = since ? now - since : 0
      const p = m.phase || ''
      if (since && p === 'executing' && age > execMs) {
        kind = 'warn'
        stalled = true
      } else if (since && THINKING_PHASES.includes(p) && age > thinkMs) {
        kind = 'warn'
        stalled = true
      }
    }
    if (!kind) continue
    items.push({
      name: m.name || '',
      kind,
      phase: m.phase || '',
      tool: m.tool || '',
      since: m.phaseSince || 0,
      elapsed: elapsed(m.phaseSince || 0, now),
      stalled,
    })
  }
  items.sort((a, b) => (a.kind === b.kind ? a.since - b.since : a.kind === 'act' ? -1 : 1))
  return items
}

// isApproval / isQuestion classify the two interactive gate events.
export function isApproval(ev?: WireEvent | null): boolean {
  return !!ev && ev.Kind === 'approval_needed'
}
export function isQuestion(ev?: WireEvent | null): boolean {
  return !!ev && ev.Kind === 'question_needed'
}

export interface ApprovalVM {
  agentId: string
  requestId: string
  tool: string
  description: string
  reason: string
  risk: string
  plan: string
  input: unknown
}
export interface QuestionVM {
  agentId: string
  requestId: string
  questions: QuestionItem[]
}

// approvalOf / questionOf normalise a gate event into the overlay's view model.
export function approvalOf(ev: WireEvent): ApprovalVM {
  const p = ev.ApprovalNeeded || {}
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
export function questionOf(ev: WireEvent): QuestionVM {
  const p = ev.QuestionNeeded || {}
  return {
    agentId: p.AgentID || ev.AgentID || '',
    requestId: p.RequestID || '',
    questions: p.Questions || [],
  }
}

// touchesLedger reports whether an event likely changed the task ledger/roster,
// so the UI can refresh those REST snapshots promptly.
export function touchesLedger(ev?: WireEvent | null): boolean {
  if (!ev) return false
  if (ev.Kind === 'store_update') return true
  if (ev.Kind === 'tool_use_result') return true
  return false
}

// mailState classifies a message from the unread→claimed→read lifecycle.
export function mailState(m?: Pick<MessageInfo, 'readAt' | 'claimedAt'> | null): 'read' | 'reading' | 'unread' {
  if (!m) return 'unread'
  if (m.readAt) return 'read'
  if (m.claimedAt) return 'reading'
  return 'unread'
}
