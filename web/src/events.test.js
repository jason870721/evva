import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  reduceChat,
  groupTasks,
  textOf,
  approvalOf,
  questionOf,
  isApproval,
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
