import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  reduceChat,
  groupTasks,
  textOf,
  approvalOf,
  questionOf,
  isApproval,
  consoleTurns,
  displayPhase,
  phaseClass,
  reducePhase,
  mailState,
  attentionKind,
  elapsed,
  attentionItems,
  relTime,
  TASK_STATES,
} from './events.ts'

const txt = (agent, s, chunk = true) => ({
  Kind: chunk ? 'text_chunk' : 'text',
  AgentID: agent,
  Text: { Text: s },
})

test('streaming text chunks coalesce into one assistant turn per agent', () => {
  let turns = []
  turns = reduceChat(turns, txt('leader', 'Hel'))
  turns = reduceChat(turns, txt('leader', 'lo'))
  assert.equal(turns.length, 1)
  assert.equal(turns[0].type, 'assistant')
  assert.equal(turns[0].text, 'Hello')
})

test('turn_end closes accumulation so the next text is a new turn', () => {
  let turns = []
  turns = reduceChat(turns, txt('leader', 'one'))
  turns = reduceChat(turns, { Kind: 'turn_end', AgentID: 'leader' })
  turns = reduceChat(turns, txt('leader', 'two'))
  assert.equal(turns.length, 2)
  assert.equal(turns[0].text, 'one')
  assert.equal(turns[1].text, 'two')
})

test('a different agent never appends to another agent open turn', () => {
  let turns = []
  turns = reduceChat(turns, txt('leader', 'L'))
  turns = reduceChat(turns, txt('worker', 'W'))
  assert.equal(turns.length, 2)
  assert.equal(turns[1].agentId, 'worker')
})

test('concurrent agents: interleaved deltas coalesce per agent, not by last turn', () => {
  const evs = [
    { Kind: 'thinking_chunk', AgentID: 'lead', Thinking: { Text: 'a' } },
    { Kind: 'thinking_chunk', AgentID: 'pm', Thinking: { Text: 'x' } },
    { Kind: 'thinking_chunk', AgentID: 'lead', Thinking: { Text: 'b' } },
    { Kind: 'thinking_chunk', AgentID: 'pm', Thinking: { Text: 'y' } },
    { Kind: 'text_chunk', AgentID: 'lead', Text: { Text: 'Hi' } },
    { Kind: 'text_chunk', AgentID: 'lead', Text: { Text: '!' } },
  ]
  let turns = []
  for (const e of evs) turns = reduceChat(turns, e)
  assert.equal(turns.length, 3) // lead thinking, pm thinking, lead assistant
  assert.deepEqual(
    consoleTurns(turns, 'lead', 'lead').map((t) => [t.type, t.text]),
    [
      ['thinking', 'ab'],
      ['assistant', 'Hi!'],
    ],
  )
  assert.deepEqual(
    consoleTurns(turns, 'pm', 'pm').map((t) => [t.type, t.text]),
    [['thinking', 'xy']],
  )
})

test('tool start then result resolves by ToolID', () => {
  let turns = []
  turns = reduceChat(turns, {
    Kind: 'tool_use_start',
    AgentID: 'leader',
    ToolUseStart: { Name: 'task_assign', ToolID: 't1', Input: {} },
  })
  assert.equal(turns[0].type, 'tool')
  assert.equal(turns[0].status, 'running')
  turns = reduceChat(turns, {
    Kind: 'tool_use_result',
    AgentID: 'leader',
    ToolUseResult: { ToolID: 't1', Content: 'ok', IsError: false },
  })
  assert.equal(turns[0].status, 'done')
  assert.equal(turns[0].result, 'ok')
})

test('an errored tool result marks the turn error', () => {
  let turns = [{ type: 'tool', toolId: 't9', status: 'running' }]
  turns = reduceChat(turns, {
    Kind: 'tool_use_result',
    ToolUseResult: { ToolID: 't9', Content: 'nope', IsError: true },
  })
  assert.equal(turns[0].status, 'error')
})

test('error event becomes an error turn', () => {
  let turns = []
  turns = reduceChat(turns, { Kind: 'error', AgentID: 'leader', Error: { Message: 'boom' } })
  assert.equal(turns[0].type, 'error')
  assert.equal(turns[0].text, 'boom')
})

test('empty text deltas and unknown kinds are ignored', () => {
  let turns = []
  turns = reduceChat(turns, txt('leader', ''))
  turns = reduceChat(turns, { Kind: 'usage', AgentID: 'leader' })
  assert.equal(turns.length, 0)
})

test('textOf is null-safe', () => {
  assert.equal(textOf(undefined), '')
  assert.equal(textOf({}), '')
  assert.equal(textOf({ Text: { Text: 'x' } }), 'x')
})

test('groupTasks buckets by status and drops unknowns', () => {
  const cols = groupTasks([
    { id: 1, status: 'pending' },
    { id: 2, status: 'running' },
    { id: 3, status: 'pending' },
    { id: 4, status: 'bogus' },
  ])
  assert.deepEqual(Object.keys(cols), TASK_STATES)
  assert.equal(cols.pending.length, 2)
  assert.equal(cols.running.length, 1)
  assert.equal(cols.completed.length, 0)
})

test('groupTasks preserves order within a column', () => {
  const cols = groupTasks([
    { id: 5, status: 'pending' },
    { id: 1, status: 'pending' },
  ])
  assert.deepEqual(cols.pending.map((t) => t.id), [5, 1])
})

test('consoleTurns demuxes one mixed stream per member', () => {
  const turns = [
    { type: 'assistant', agentId: 'AID-leader', text: 'hi' },
    { type: 'tool', agentId: 'AID-worker', tool: 'bash' },
    { type: 'user', target: 'worker', text: 'status?' },
    { type: 'user', target: 'leader', text: 'go' },
    { type: 'assistant', agentId: 'AID-worker', text: 'done' },
  ]
  const w = consoleTurns(turns, 'AID-worker', 'worker')
  assert.equal(w.length, 3) // worker tool + user→worker + worker assistant
  assert.ok(w.every((t) => (t.type === 'user' ? t.target === 'worker' : t.agentId === 'AID-worker')))

  const l = consoleTurns(turns, 'AID-leader', 'leader')
  assert.equal(l.length, 2) // leader assistant + user→leader
})

test('consoleTurns with an unknown agentId shows only operator turns', () => {
  const turns = [
    { type: 'assistant', agentId: 'AID-x', text: 'a' },
    { type: 'user', target: 'worker', text: 'b' },
  ]
  assert.deepEqual(consoleTurns(turns, '', 'worker'), [{ type: 'user', target: 'worker', text: 'b' }])
})

test('approvalOf and questionOf normalise the gate payloads', () => {
  const ev = {
    Kind: 'approval_needed',
    AgentID: 'leader',
    ApprovalNeeded: { RequestID: 'r1', ToolName: 'bash', Reason: 'risky', InputDescription: 'rm' },
  }
  assert.ok(isApproval(ev))
  const a = approvalOf(ev)
  assert.equal(a.requestId, 'r1')
  assert.equal(a.tool, 'bash')
  assert.equal(a.description, 'rm')

  const q = questionOf({
    Kind: 'question_needed',
    QuestionNeeded: { RequestID: 'q1', AgentID: 'leader', Questions: [{ Question: 'pick?' }] },
  })
  assert.equal(q.requestId, 'q1')
  assert.equal(q.questions.length, 1)
})

test('displayPhase composes coarse run + fine phase (RP-3)', () => {
  assert.equal(displayPhase({ run: 'busy', phase: 'executing', tool: 'bash' }), 'executing:bash')
  assert.equal(displayPhase({ run: 'busy', phase: 'waiting-approval', tool: 'bash' }), 'waiting-approval:bash')
  assert.equal(displayPhase({ run: 'busy', phase: 'thinking' }), 'thinking')
  assert.equal(displayPhase({ run: 'busy', phase: 'running' }), 'thinking')
  assert.equal(displayPhase({ run: 'busy', phase: 'texting' }), 'thinking')
  assert.equal(displayPhase({ run: 'idle', phase: 'ready' }), 'ready')
  assert.equal(displayPhase({ run: 'suspended', phase: 'ready' }), 'suspended')
  assert.equal(displayPhase({ run: 'busy', phase: '' }), 'busy')
})

test('phaseClass flags waiting-approval distinctly and groups thinking', () => {
  assert.equal(phaseClass({ run: 'busy', phase: 'waiting-approval' }), 'waiting')
  assert.equal(phaseClass({ run: 'busy', phase: 'executing' }), 'busy')
  assert.equal(phaseClass({ run: 'busy', phase: 'thinking' }), 'thinking')
  assert.equal(phaseClass({ run: 'busy', phase: 'running' }), 'thinking')
  assert.equal(phaseClass({ run: 'busy', phase: 'texting' }), 'thinking')
  assert.equal(phaseClass({ run: 'suspended', phase: 'ready' }), 'suspended')
  assert.equal(phaseClass({ run: 'idle', phase: 'ready' }), 'idle')
})

test('reducePhase derives live per-agent phase from the event stream', () => {
  let m = {}
  m = reducePhase(m, { Kind: 'turn_start', AgentID: 'a1' }, 1000)
  assert.deepEqual(m.a1, { phase: 'running', tool: '', since: 1000 })
  m = reducePhase(m, { Kind: 'thinking_chunk', AgentID: 'a1', Thinking: { Text: '…' } }, 1500)
  assert.deepEqual(m.a1, { phase: 'thinking', tool: '', since: 1500 })
  m = reducePhase(m, { Kind: 'tool_use_start', AgentID: 'a1', ToolUseStart: { Name: 'bash' } }, 2000)
  assert.deepEqual(m.a1, { phase: 'executing', tool: 'bash', since: 2000 })
  m = reducePhase(m, { Kind: 'approval_needed', AgentID: 'a1', ApprovalNeeded: { ToolName: 'bash' } }, 2500)
  assert.deepEqual(m.a1, { phase: 'waiting-approval', tool: 'bash', since: 2500 })
})

test('reducePhase ignores non-phase events and no-ops when unchanged', () => {
  const base = reducePhase({}, { Kind: 'turn_start', AgentID: 'a1' }, 1000)
  assert.equal(reducePhase(base, { Kind: 'usage', AgentID: 'a1' }, 2000), base)
  assert.equal(reducePhase(base, { Kind: 'store_update', AgentID: 'a1' }, 2000), base)
  assert.equal(reducePhase(base, { Kind: 'turn_start' }, 2000), base) // no AgentID
  assert.equal(reducePhase(base, { Kind: 'turn_end', AgentID: 'a1' }, 9000), base)
})

test('reducePhase keeps the clock on a tool-only change, resets on phase change', () => {
  let m = reducePhase({}, { Kind: 'tool_use_start', AgentID: 'a1', ToolUseStart: { Name: 'bash' } }, 1000)
  m = reducePhase(m, { Kind: 'tool_use_start', AgentID: 'a1', ToolUseStart: { Name: 'read' } }, 5000)
  assert.deepEqual(m.a1, { phase: 'executing', tool: 'read', since: 1000 })
})

test('reducePhase isolates agents', () => {
  let m = {}
  m = reducePhase(m, { Kind: 'thinking', AgentID: 'a1' }, 1000)
  m = reducePhase(m, { Kind: 'tool_use_start', AgentID: 'a2', ToolUseStart: { Name: 'bash' } }, 1000)
  assert.equal(m.a1.phase, 'thinking')
  assert.equal(m.a2.phase, 'executing')
})

test('mailState classifies the unread→reading→read lifecycle', () => {
  assert.equal(mailState({ readAt: 123 }), 'read')
  assert.equal(mailState({ readAt: 123, claimedAt: 100 }), 'read') // read wins
  assert.equal(mailState({ claimedAt: 100 }), 'reading')
  assert.equal(mailState({}), 'unread')
  assert.equal(mailState(null), 'unread')
})

test('attentionKind: blocked = act, errored/paused = warn (RP-4)', () => {
  assert.equal(attentionKind({ phase: 'waiting-approval' }), 'act')
  assert.equal(attentionKind({ phase: 'waiting-input' }), 'act')
  assert.equal(attentionKind({ phase: 'error' }), 'warn')
  assert.equal(attentionKind({ phase: 'paused' }), 'warn')
  assert.equal(attentionKind({ phase: 'executing' }), '')
  assert.equal(attentionKind({ phase: 'ready' }), '')
})

test('elapsed formats a compact clock', () => {
  const now = 1_000_000
  assert.equal(elapsed(0, now), '')
  assert.equal(elapsed(now - 12_000, now), '12s')
  assert.equal(elapsed(now - 161_000, now), '2:41')
  assert.equal(elapsed(now - 3_800_000, now), '1:03:20')
  assert.equal(elapsed(now + 5000, now), '0s') // future clamps to 0
})

test('attentionItems sorts act before warn, then longest-waiting first', () => {
  const now = 100_000
  const roster = [
    { name: 'fe', phase: 'executing', phaseSince: now - 1000 }, // not attention
    { name: 'qa', phase: 'waiting-approval', tool: 'bash', phaseSince: now - 5000 },
    { name: 'be', phase: 'error', phaseSince: now - 9000 },
    { name: 'pm', phase: 'waiting-input', phaseSince: now - 20000 }, // oldest act
  ]
  const items = attentionItems(roster, now)
  assert.deepEqual(items.map((i) => i.name), ['pm', 'qa', 'be'])
  assert.equal(items[0].kind, 'act')
  assert.equal(items[2].kind, 'warn')
  assert.equal(items[1].elapsed, '5s')
})

test('relTime formats a short relative age (RP-4 UX-2)', () => {
  const now = 10_000_000
  assert.equal(relTime(0, now), '')
  assert.equal(relTime(now - 2000, now), 'now')
  assert.equal(relTime(now - 40_000, now), '40s')
  assert.equal(relTime(now - 5 * 60_000, now), '5m')
  assert.equal(relTime(now - 3 * 3_600_000, now), '3h')
  assert.equal(relTime(now - 2 * 86_400_000, now), '2d')
})

test('attentionItems flags a long-running phase as a stall (warn)', () => {
  const now = 10 * 60_000
  const roster = [
    { name: 'a', phase: 'executing', tool: 'bash', phaseSince: now - 6 * 60_000 }, // 6m > 5m → stall
    { name: 'b', phase: 'executing', tool: 'bash', phaseSince: now - 1000 }, // fresh → not attention
    { name: 'c', phase: 'thinking', phaseSince: now - 4 * 60_000 }, // 4m > 3m → stall
  ]
  const items = attentionItems(roster, now)
  const names = items.map((i) => i.name)
  assert.ok(names.includes('a'))
  assert.ok(!names.includes('b'))
  assert.ok(names.includes('c'))
  assert.equal(items.find((i) => i.name === 'a').stalled, true)
})
