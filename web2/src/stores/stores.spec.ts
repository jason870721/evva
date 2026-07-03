import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useGateStore } from './gate'
import { useStreamStore } from './stream'
import { useConnectionStore } from './connection'
import { useLedgerStore } from './ledger'
import { useProposalsStore } from './proposals'
import { useSpaceStore } from './space'
import { api } from '@/lib/apiClient'
import type { AssistantTurn } from '@/lib/events'
import type { MemberInfo } from '@/types/wire'

// Store-layer glue around the (separately node-tested) pure reducers. Runs under
// Vitest so bundler resolution + aliases work (FE-8 testing finish).
beforeEach(() => setActivePinia(createPinia()))

describe('gate store', () => {
  const a = { agentId: 'a1', requestId: 'r1', tool: 'bash', description: '', reason: '', risk: '', plan: '', input: null }

  it('de-dups enqueue by (agentId, requestId)', () => {
    const g = useGateStore()
    g.enqueue('approval', a)
    g.enqueue('approval', a)
    g.enqueue('approval', { ...a, agentId: 'a2', requestId: 'r2' })
    expect(g.approvals.length).toBe(2)
    expect(g.pendingCount).toBe(2)
  })

  it('brings a member gate to the front, reports miss', () => {
    const g = useGateStore()
    g.enqueue('approval', a)
    g.enqueue('approval', { ...a, agentId: 'a2', requestId: 'r2' })
    expect(g.bringToFront('a2')).toBe(true)
    expect(g.approvals[0].agentId).toBe('a2')
    expect(g.bringToFront('nope')).toBe(false)
  })

  it('records and clears per-gate errors', () => {
    const g = useGateStore()
    g.noteError('r1', 'boom')
    expect(g.errors['r1']).toBe('boom')
    g.clearError('r1')
    expect(g.errors['r1']).toBeUndefined()
  })
})

describe('stream store', () => {
  it('coalesces a member text turn and tracks live phase', () => {
    const s = useStreamStore()
    s.foldChat({ Kind: 'text_chunk', AgentID: 'a1', Text: { Text: 'He' } })
    s.foldChat({ Kind: 'text_chunk', AgentID: 'a1', Text: { Text: 'llo' } })
    expect(s.turns.length).toBe(1)
    expect((s.turns[0] as AssistantTurn).text).toBe('Hello')
    s.foldPhase({ Kind: 'tool_use_start', AgentID: 'a1', ToolUseStart: { Name: 'bash' } })
    expect(s.livePhases['a1'].phase).toBe('executing')
    expect(s.livePhases['a1'].tool).toBe('bash')
  })
})

// Reconnect re-hydrate (the `service stop && start` fix): rehydrate must REPLACE
// the stale console with durable truth — the /chatlog event-log replay, falling
// back to live transcripts for event_log: false spaces — but never blank it on
// a reconnect blip (WS opens before the space is reconciled → REST reads
// fail/empty).
describe('stream store · reconnect rehydrate', () => {
  const roster: MemberInfo[] = [
    { name: 'qa', agentId: 'a1', role: 'worker', membership: 'active', run: 'idle', currentTask: 0, contextTokens: 0, contextLimit: 0 },
  ]
  beforeEach(() => useConnectionStore().setSpace('S1'))
  afterEach(() => vi.restoreAllMocks())

  it('replaces the console with the durable chat log on a real reconnect', async () => {
    const s = useStreamStore()
    s.turns = [{ type: 'assistant', agentId: 'a1', text: 'partial…', open: true }]
    vi.spyOn(api, 'chatlog').mockResolvedValue([
      { Kind: 'text', AgentID: 'a1', Text: { Text: 'complete answer' } },
      { Kind: 'tool_use_start', AgentID: 'a1', ToolUseStart: { Name: 'bash', ToolID: 't1' } },
      { Kind: 'user_message', UserMessage: { Recipient: 'qa', Body: 'thanks' } },
    ])
    await s.rehydrateHistory(roster)
    expect(s.turns.length).toBe(3)
    expect((s.turns[0] as AssistantTurn).text).toBe('complete answer')
    expect((s.turns[0] as AssistantTurn).open).toBe(false) // replay never leaves cursors open
    expect(s.turns[1].type).toBe('tool')
    expect(s.turns[2]).toMatchObject({ type: 'user', target: 'qa', text: 'thanks' })
  })

  it('falls back to transcripts when the chat log is empty (event_log: false)', async () => {
    const s = useStreamStore()
    vi.spyOn(api, 'chatlog').mockResolvedValue([])
    vi.spyOn(api, 'transcript').mockResolvedValue([
      { role: 'user', text: 'go' },
      { role: 'assistant', text: 'complete answer' },
    ])
    await s.rehydrateHistory(roster)
    expect(s.turns.length).toBe(1)
    expect((s.turns[0] as AssistantTurn).text).toBe('complete answer')
  })

  it('keeps existing turns when the blip reads throw (space not reconciled yet)', async () => {
    const s = useStreamStore()
    s.turns = [{ type: 'assistant', agentId: 'a1', text: 'keep me', open: false }]
    vi.spyOn(api, 'chatlog').mockRejectedValue(new Error('404 not running'))
    vi.spyOn(api, 'transcript').mockRejectedValue(new Error('404 not running'))
    await s.rehydrateHistory(roster)
    expect(s.turns.length).toBe(1)
    expect((s.turns[0] as AssistantTurn).text).toBe('keep me')
  })

  it('keeps existing turns when history and transcripts come back empty (no blanking)', async () => {
    const s = useStreamStore()
    s.turns = [{ type: 'assistant', agentId: 'a1', text: 'keep me', open: false }]
    vi.spyOn(api, 'chatlog').mockResolvedValue([])
    vi.spyOn(api, 'transcript').mockResolvedValue([])
    await s.rehydrateHistory(roster)
    expect((s.turns[0] as AssistantTurn).text).toBe('keep me')
  })
})

describe('ledger + space getters', () => {
  it('groups tasks; merges live phase into the roster', () => {
    const led = useLedgerStore()
    led.tasks = [
      { id: 1, title: 't', spec: '', status: 'running', assignee: 'qa', createdBy: 'lead', createdAt: 0, updatedAt: 0 },
    ]
    expect(led.groups.running.length).toBe(1)
    expect(led.groups.completed.length).toBe(0)

    const sp = useSpaceStore()
    sp.roster = [
      { name: 'qa', agentId: 'a1', role: 'worker', membership: 'active', run: 'idle', currentTask: 0, contextTokens: 0, contextLimit: 0 },
    ]
    useStreamStore().foldPhase({ Kind: 'tool_use_start', AgentID: 'a1', ToolUseStart: { Name: 'bash' } })
    expect(sp.merged[0].phase).toBe('executing')
  })
})

// The bug this guards: the inspector is reused across members (no :key), so a
// component-local busy flag bled onto whoever you switched to mid-compact. The
// flag now lives in the store keyed by member.
describe('space store · per-member compaction', () => {
  beforeEach(() => useConnectionStore().setSpace('S1'))
  afterEach(() => vi.restoreAllMocks())

  it('marks only the compacting member busy, then clears it', async () => {
    const sp = useSpaceStore()
    vi.spyOn(api, 'roster').mockResolvedValue([])
    let release!: () => void
    vi.spyOn(api, 'compactMember').mockReturnValue(new Promise<null>((res) => (release = () => res(null))))

    const done = sp.compactMember('qa', 'full')
    expect(sp.isCompacting('qa')).toBe(true)
    expect(sp.isCompacting('dev')).toBe(false) // a sibling stays clickable

    release()
    await done
    expect(sp.isCompacting('qa')).toBe(false)
  })

  it('clears the busy flag even when the compact is refused (409)', async () => {
    const sp = useSpaceStore()
    vi.spyOn(api, 'compactMember').mockRejectedValue(new Error('409 busy'))
    await expect(sp.compactMember('qa', 'full')).rejects.toThrow('busy')
    expect(sp.isCompacting('qa')).toBe(false)
  })
})

// Bulk ops fan the per-member endpoints out concurrently (the supervisor locks
// per member), refresh once, and report ok vs failed so a member that goes busy
// mid-flight (409) is surfaced rather than silently dropped.
describe('space store · bulk ops', () => {
  beforeEach(() => useConnectionStore().setSpace('S1'))
  afterEach(() => vi.restoreAllMocks())

  it('fans bulkCompact across members and reports all ok', async () => {
    const sp = useSpaceStore()
    vi.spyOn(api, 'roster').mockResolvedValue([])
    const spy = vi.spyOn(api, 'compactMember').mockResolvedValue(null)
    const r = await sp.bulkCompact(['a', 'b', 'c'], 'micro')
    expect([...r.ok].sort()).toEqual(['a', 'b', 'c'])
    expect(r.failed).toEqual([])
    expect(spy).toHaveBeenCalledTimes(3)
    expect(sp.memberBusy('a')).toBe(false) // flags cleared after
  })

  it('reports per-member failures from the fan-out (409 busy)', async () => {
    const sp = useSpaceStore()
    vi.spyOn(api, 'roster').mockResolvedValue([])
    vi.spyOn(api, 'clearMember').mockImplementation((_id, name) =>
      name === 'b' ? Promise.reject(new Error('409 busy')) : Promise.resolve(null),
    )
    const r = await sp.bulkClear(['a', 'b'])
    expect(r.ok).toEqual(['a'])
    expect(r.failed).toEqual([{ name: 'b', error: '409 busy' }])
  })

  it('marks each member busy during a bulk fan-out, then clears them', async () => {
    const sp = useSpaceStore()
    vi.spyOn(api, 'roster').mockResolvedValue([])
    let release!: () => void
    vi.spyOn(api, 'suspend').mockReturnValue(new Promise<null>((res) => (release = () => res(null))))
    const done = sp.bulkCmd('suspend', ['a', 'b'])
    expect(sp.memberBusy('a')).toBe(true)
    expect(sp.memberBusy('b')).toBe(true)
    release()
    await done
    expect(sp.memberBusy('a')).toBe(false)
    expect(sp.memberBusy('b')).toBe(false)
  })

  it('fires onSettled per member as each lands (ok and error)', async () => {
    const sp = useSpaceStore()
    vi.spyOn(api, 'roster').mockResolvedValue([])
    vi.spyOn(api, 'compactMember').mockImplementation((_id, name) =>
      name === 'b' ? Promise.reject(new Error('409 busy')) : Promise.resolve(null),
    )
    const events: Record<string, string> = {}
    const r = await sp.bulkCompact(['a', 'b'], 'full', (name, error) => {
      events[name] = error ?? 'ok'
    })
    expect(events).toEqual({ a: 'ok', b: '409 busy' })
    expect(r.ok).toEqual(['a'])
    expect(r.failed).toEqual([{ name: 'b', error: '409 busy' }])
  })
})

describe('proposals store', () => {
  it('counts only open proposals for the tab badge', () => {
    const p = useProposalsStore()
    p.list = [
      { id: 1, proposer: 'qa', title: 'a', status: 'open', createdAt: 1 },
      { id: 2, proposer: 'qa', title: 'b', status: 'accepted', createdAt: 2, decidedAt: 3, refTask: 9 },
      { id: 3, proposer: 'dev', title: 'c', status: 'declined', createdAt: 4, decidedAt: 5 },
    ]
    expect(p.openCount).toBe(1)
    p.reset()
    expect(p.openCount).toBe(0)
  })
})
