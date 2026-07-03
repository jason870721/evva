import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  reduceChat,
  clock,
  eventAt,
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
  orderRoster,
  relTime,
  contextUsage,
  humanTokens,
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

test('user_message (chatlog replay synthetic) folds into a user turn', () => {
  let turns = []
  turns = reduceChat(turns, {
    Kind: 'user_message',
    Time: '2026-06-01T10:00:00Z',
    UserMessage: { Sender: 'user', Recipient: 'qa', Body: 'please start' },
  })
  assert.equal(turns.length, 1)
  assert.deepEqual(
    consoleTurns(turns, 'a1', 'qa').map((t) => [t.type, t.text]),
    [['user', 'please start']],
  )
  assert.equal(turns[0].at, Date.parse('2026-06-01T10:00:00Z'))

  // A subject prefixes the body; an empty body+subject folds to nothing.
  turns = reduceChat(turns, {
    Kind: 'user_message',
    UserMessage: { Recipient: 'qa', Subject: 'standup', Body: 'notes' },
  })
  assert.equal(turns[1].text, 'standup — notes')
  turns = reduceChat(turns, { Kind: 'user_message', UserMessage: { Recipient: 'qa' } })
  assert.equal(turns.length, 2)
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

test('a turn stamps `at` from the event Time on first chunk, stable across appends', () => {
  const t0 = '2026-06-13T09:05:03.000Z'
  const t1 = '2026-06-13T09:05:04.500Z'
  let turns = []
  turns = reduceChat(turns, { ...txt('leader', 'Hel'), Time: t0 })
  turns = reduceChat(turns, { ...txt('leader', 'lo'), Time: t1 })
  assert.equal(turns.length, 1)
  assert.equal(turns[0].text, 'Hello')
  // First-chunk time wins — the stamp marks when the message began, not the last delta.
  assert.equal(turns[0].at, Date.parse(t0))
})

test('tool and error turns carry the event Time too', () => {
  const ts = '2026-06-13T09:05:03.000Z'
  let turns = reduceChat([], {
    Kind: 'tool_use_start',
    AgentID: 'leader',
    Time: ts,
    ToolUseStart: { Name: 'bash', ToolID: 't1', Input: {} },
  })
  assert.equal(turns[0].at, Date.parse(ts))
  turns = reduceChat([], { Kind: 'error', AgentID: 'leader', Time: ts, Error: { Message: 'boom' } })
  assert.equal(turns[0].at, Date.parse(ts))
})

test('a timeless event omits `at` entirely (pinned reducer fixtures stay clean)', () => {
  const turns = reduceChat([], txt('leader', 'hi'))
  assert.equal(turns[0].at, undefined)
  assert.ok(!('at' in turns[0]))
})

test('eventAt parses RFC3339, is null-safe, and rejects junk', () => {
  assert.equal(eventAt({ Kind: 'text', Time: '2026-06-13T09:05:03Z' }), Date.parse('2026-06-13T09:05:03Z'))
  assert.equal(eventAt({ Kind: 'text' }), undefined)
  assert.equal(eventAt(null), undefined)
  assert.equal(eventAt({ Kind: 'text', Time: 'not-a-date' }), undefined)
})

test('clock formats local HH:MM:SS, zero-padded, empty for no instant', () => {
  // Built from local components, so the expected string is timezone-independent.
  const ms = new Date(2026, 5, 13, 9, 5, 3).getTime()
  assert.equal(clock(ms), '09:05:03')
  assert.equal(clock(0), '')
  assert.equal(clock(undefined), '')
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

test('contextUsage derives a capped, known pct from the wire fields', () => {
  assert.deepEqual(contextUsage({ contextTokens: 180_000, contextLimit: 500_000 }), {
    used: 180_000,
    limit: 500_000,
    pct: 36,
    known: true,
  })
  // No turn yet → 0% but known (the window is known).
  assert.deepEqual(contextUsage({ contextTokens: 0, contextLimit: 200_000 }), {
    used: 0,
    limit: 200_000,
    pct: 0,
    known: true,
  })
})

test('contextUsage caps at 100 and treats an unknown/zero limit as not-known', () => {
  const over = contextUsage({ contextTokens: 600_000, contextLimit: 500_000 })
  assert.equal(over.pct, 100) // clamp, never > 100
  const unknown = contextUsage({ contextTokens: 12_000, contextLimit: 0 })
  assert.deepEqual(unknown, { used: 12_000, limit: 0, pct: 0, known: false })
})

test('contextUsage is null-safe and floors negatives to zero', () => {
  assert.deepEqual(contextUsage(null), { used: 0, limit: 0, pct: 0, known: false })
  assert.deepEqual(contextUsage({}), { used: 0, limit: 0, pct: 0, known: false })
  assert.deepEqual(contextUsage({ contextTokens: -5, contextLimit: -9 }), {
    used: 0,
    limit: 0,
    pct: 0,
    known: false,
  })
})

test('humanTokens matches the TUI k/M thresholds', () => {
  assert.equal(humanTokens(0), '0')
  assert.equal(humanTokens(-3), '0')
  assert.equal(humanTokens(512), '512')
  assert.equal(humanTokens(1_500), '1.5k')
  assert.equal(humanTokens(42_000), '42k')
  assert.equal(humanTokens(500_000), '500k')
  assert.equal(humanTokens(1_050_000), '1.1M')
})

const names = (ms) => ms.map((m) => m.name)

test('orderRoster: leader pins first, then attention → busy → idle → suspended → frozen', () => {
  const roster = [
    { name: 'finn', role: 'worker', run: 'idle', membership: 'frozen' },
    { name: 'erin', role: 'worker', run: 'idle', membership: 'active' },
    { name: 'dave', role: 'worker', run: 'busy', membership: 'active' },
    { name: 'bob', role: 'worker', run: 'busy', membership: 'active' },
    { name: 'gwen', role: 'worker', run: 'suspended', membership: 'active' },
    { name: 'lead', role: 'leader', run: 'idle', membership: 'active' },
  ]
  // bob needs attention (e.g. errored / stalled) — floats above the other busy.
  assert.deepEqual(names(orderRoster(roster, ['bob'])), ['lead', 'bob', 'dave', 'erin', 'gwen', 'finn'])
})

test('orderRoster: attention tier keeps the given urgency order, not alphabetical', () => {
  const roster = [
    { name: 'amy', role: 'worker', run: 'idle', membership: 'active' },
    { name: 'zed', role: 'worker', run: 'idle', membership: 'active' },
  ]
  // zed is more urgent (listed first) despite sorting after amy alphabetically.
  assert.deepEqual(names(orderRoster(roster, ['zed', 'amy'])), ['zed', 'amy'])
})

test('orderRoster: alphabetical within a tier and pure (no mutation)', () => {
  const roster = [
    { name: 'carol', role: 'worker', run: 'idle', membership: 'active' },
    { name: 'alice', role: 'worker', run: 'idle', membership: 'active' },
  ]
  const out = orderRoster(roster, [])
  assert.deepEqual(names(out), ['alice', 'carol'])
  assert.deepEqual(names(roster), ['carol', 'alice']) // input untouched
})
