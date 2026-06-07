// Typed REST client over the swarm service API — ported from web/src/api.js.
// Every call carries the session token as a Bearer header. Return types come
// from types/wire.ts so a backend contract change surfaces at compile time.

import type {
  SpaceInfo,
  MemberInfo,
  MemberSpec,
  MessageInfo,
  TaskPage,
  TranscriptEntry,
  SkillInfo,
  SkillSpec,
} from '../types/wire'
import type { WireEvent } from '../types/events'

export type Api = ReturnType<typeof createApi>

export function createApi(getToken: () => string) {
  async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = { Authorization: 'Bearer ' + (getToken() || '') }
    const opts: RequestInit = { method, headers }
    if (body !== undefined) {
      headers['Content-Type'] = 'application/json'
      opts.body = JSON.stringify(body)
    }
    const resp = await fetch(path, opts)
    if (resp.status === 401) throw new Error('unauthorized — check the session token')
    if (!resp.ok) {
      const text = await resp.text().catch(() => '')
      throw new Error(`${method} ${path} → ${resp.status} ${text}`.trim())
    }
    if (resp.status === 204) return null as unknown as T
    const ct = resp.headers.get('Content-Type') || ''
    return (ct.includes('application/json') ? await resp.json() : await resp.text()) as T
  }

  const enc = encodeURIComponent
  return {
    // snapshots
    spaces: () => req<SpaceInfo[]>('GET', '/api/swarms'),
    roster: (id: string) => req<MemberInfo[]>('GET', `/api/swarm/${enc(id)}`),
    // Board snapshot: { tasks: [active… + newest-few completed], total: completed count }.
    tasks: (id: string) => req<TaskPage>('GET', `/api/tasks?space=${enc(id)}`),
    // On-demand paged view of one status (the Completed tab).
    tasksPage: (
      id: string,
      { status, limit, offset }: { status?: string; limit?: number; offset?: number } = {},
    ) => {
      const p = new URLSearchParams({ space: id })
      if (status) p.set('status', status)
      if (limit != null) p.set('limit', String(limit))
      if (offset != null) p.set('offset', String(offset))
      return req<TaskPage>('GET', `/api/tasks?${p.toString()}`)
    },
    messages: (id: string) => req<MessageInfo[]>('GET', `/api/messages?space=${enc(id)}`),
    transcript: (id: string, agent: string) =>
      req<TranscriptEntry[]>('GET', `/api/agents/${enc(agent)}/transcript?space=${enc(id)}`),
    // Outstanding approval/question gates (raw event shape), re-rendered on (re)connect.
    pending: (id: string) => req<WireEvent[]>('GET', `/api/swarm/${enc(id)}/pending`),

    // commands
    run: (id: string, agent: string, prompt: string) =>
      req<null>('POST', `/api/agents/${enc(agent)}/run?space=${enc(id)}`, { prompt }),
    // Operator → member message (flat comms). `to` may be "all".
    message: (id: string, to: string, body: string, subject?: string) =>
      req<null>('POST', `/api/agents/${enc(to)}/message?space=${enc(id)}`, { body, subject }),
    suspend: (id: string, agent: string) => req<null>('POST', `/api/agents/${enc(agent)}/suspend?space=${enc(id)}`),
    resume: (id: string, agent: string) => req<null>('POST', `/api/agents/${enc(agent)}/resume?space=${enc(id)}`),
    freeze: (id: string, agent: string) => req<null>('POST', `/api/agents/${enc(agent)}/freeze?space=${enc(id)}`),
    unfreeze: (id: string, agent: string) => req<null>('POST', `/api/agents/${enc(agent)}/unfreeze?space=${enc(id)}`),
    // Membership editing (RP-8). The leader is unique — neither targets it.
    createMember: (id: string, spec: MemberSpec) => req<null>('POST', `/api/members?space=${enc(id)}`, spec),
    removeMember: (id: string, agent: string, deleteDir: boolean) =>
      req<null>('DELETE', `/api/agents/${enc(agent)}?space=${enc(id)}&deleteDir=${deleteDir ? 'true' : 'false'}`),
    // Tool catalog the add-agent form offers (collaboration tools excluded).
    tools: (id: string) => req<string[]>('GET', `/api/tools?space=${enc(id)}`),
    // Schedule CRUD (RP-7/RP-8). The operator may target ANY member, incl. the leader.
    setSchedule: (id: string, agent: string, { cron, prompt }: { cron: string; prompt: string }) =>
      req<null>('POST', `/api/agents/${enc(agent)}/schedule?space=${enc(id)}`, { cron, prompt }),
    clearSchedule: (id: string, agent: string) =>
      req<null>('DELETE', `/api/agents/${enc(agent)}/schedule?space=${enc(id)}`),
    // Agent skills (RP-10). User-only view/add/delete; an add/delete hot-reloads the prompt.
    memberSkills: (id: string, agent: string) =>
      req<SkillInfo[]>('GET', `/api/agents/${enc(agent)}/skills?space=${enc(id)}`),
    addSkill: (id: string, agent: string, spec: SkillSpec) =>
      req<null>('POST', `/api/agents/${enc(agent)}/skills?space=${enc(id)}`, spec),
    deleteSkill: (id: string, agent: string, skill: string) =>
      req<null>('DELETE', `/api/agents/${enc(agent)}/skills/${enc(skill)}?space=${enc(id)}`),
    halt: (id: string) => req<null>('POST', `/api/halt?space=${enc(id)}`),
    // Lifecycle (ref = id or name): stop KEEPS the space (run restarts it).
    stopSpace: (ref: string) => req<null>('POST', `/api/swarm/${enc(ref)}/stop`),
    runSpace: (ref: string) => req<null>('POST', `/api/swarm/${enc(ref)}/run`),
    removeSpace: (ref: string) => req<null>('DELETE', `/api/swarm/${enc(ref)}`),
    // Wipe the space and rebuild under the same id; returns { id }. Destructive.
    reset: (id: string) => req<{ id: string }>('POST', `/api/swarm/${enc(id)}/reset`),
  }
}
