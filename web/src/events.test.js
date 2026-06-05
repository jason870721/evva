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
  attentionKind,
  elapsed,
  attentionItems,
  TASK_STATES,
} from './events.js'

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
  // agentId "" (member not yet resolved) must not match every agent turn.
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
  assert.equal(displayPhase({ run: 'idle', phase: 'ready' }), 'ready')
  // coarse "suspended" wins even if the deriver moved the phase on after cancel.
  assert.equal(displayPhase({ run: 'suspended', phase: 'ready' }), 'suspended')
  // empty phase falls back to coarse run.
  assert.equal(displayPhase({ run: 'busy', phase: '' }), 'busy')
})

test('phaseClass flags waiting-approval distinctly', () => {
  assert.equal(phaseClass({ run: 'busy', phase: 'waiting-approval' }), 'waiting')
  assert.equal(phaseClass({ run: 'busy', phase: 'executing' }), 'busy')
  assert.equal(phaseClass({ run: 'suspended', phase: 'ready' }), 'suspended')
  assert.equal(phaseClass({ run: 'idle', phase: 'ready' }), 'idle')
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
