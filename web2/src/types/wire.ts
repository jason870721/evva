// Wire types — mirror internal/swarm/webapi/api.go (*Info / *Spec) field-for-field
// (api.go:116-236). The Go JSON tags are the contract; if the backend changes a
// shape, the consuming TS goes red here instead of failing silently at runtime.

export type RunState = 'idle' | 'busy' | 'suspended'
export type TaskStatus = 'pending' | 'running' | 'suspended' | 'verifying' | 'completed'

// SpaceInfo — GET /api/swarms (api.go:116).
export interface SpaceInfo {
  id: string
  name: string
  workdir: string
  status: 'running' | 'stopped'
  members: number
}

// MemberInfo — GET /api/swarm/:id (api.go:127). AgentID is the event-stream
// identity used to demux the per-(space,agent) WS feed.
export interface MemberInfo {
  name: string
  agentId: string
  role: string
  membership: string
  run: RunState
  phase?: string
  tool?: string
  phaseSince?: number
  currentTask: number
  whenToUse?: string
  cron?: string
  schedulePrompt?: string
}

// MemberSpec — POST /api/members add-agent form (api.go:148).
export interface MemberSpec {
  name: string
  systemPrompt: string
  whenToUse: string
  active: string[]
  deferred: string[]
  cron: string
  prompt: string
}

// SkillInfo / SkillSpec — GET/POST /api/agents/:name/skills (api.go:160,168).
export interface SkillInfo {
  name: string
  description: string
}
export interface SkillSpec {
  name: string
  description: string
  body: string
}

// TaskInfo — GET /api/tasks (api.go:175); TaskPage wraps a bounded slice + total
// (api.go:192).
export interface TaskInfo {
  id: number
  title: string
  spec: string
  status: TaskStatus
  assignee: string
  createdBy: string
  result?: string
  verifyNote?: string
  parentId?: number
  createdAt: number
  updatedAt: number
}
export interface TaskPage {
  tasks: TaskInfo[]
  total: number
}

// MessageInfo — GET /api/messages (api.go:214). ReadAt/ClaimedAt expose the
// unread→claimed→read lifecycle (store migration 0002).
export interface MessageInfo {
  id: string
  sender: string
  recipient: string
  subject?: string
  body: string
  refTask?: number
  readAt?: number
  claimedAt?: number
  createdAt: number
}

// TranscriptEntry — GET /api/agents/:name/transcript (api.go:233).
export interface TranscriptEntry {
  role: string
  text: string
}
