// Wire types — mirror internal/swarm/webapi/api.go (*Info / *Spec) field-for-field
// (api.go:116-236). The Go JSON tags are the contract; if the backend changes a
// shape, the consuming TS goes red here instead of failing silently at runtime.

export type RunState = 'idle' | 'busy' | 'suspended'
export type TaskStatus = 'pending' | 'blocked' | 'running' | 'suspended' | 'verifying' | 'completed'

// SpaceInfo — GET /api/swarms (api.go:116). leader/busy are live-roster
// reads: present only for running spaces.
export interface SpaceInfo {
  id: string
  name: string
  workdir: string
  status: 'running' | 'stopped'
  members: number
  leader?: string
  busy?: number
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
  // PermissionMode is the member's effective permission stance (manifest member
  // override > space setting; RP-24): default | accept_edits | plan | bypass.
  permissionMode?: string
  cron?: string
  schedulePrompt?: string
  // Context-utilization meter (CTX bar): contextTokens is the input-token count
  // of the member's most recent turn (how full its prompt is now), contextLimit
  // its model's context window. contextLimit is 0 when the model is unknown.
  // Same pair evva's TUI status bar reads (LastTurnInputTokens / MODEL_CONTEXT_SIZE).
  contextTokens: number
  contextLimit: number
  // Token meter (RP-13): cumulative session input/output as of the member's last
  // run boundary, today's spend, and the effective daily budget (0 = unlimited).
  // All omitempty on the wire — absent means 0.
  tokensIn?: number
  tokensOut?: number
  tokensToday?: number
  tokensBudget?: number
  // Cost meter (CST): today's cache-class tokens and USD priced at meter
  // time. costUnpriced marks a member whose model has no rate card — its
  // dollars are MISSING from every $ figure, not zero.
  tokensCacheRead?: number
  tokensCacheWrite?: number
  costTodayUsd?: number
  costUnpriced?: boolean
  // DWF member_spawn clone: an ephemeral seat that retires itself when its
  // work completes, and the base member it was cloned from.
  ephemeral?: boolean
  spawnedFrom?: string
  // Worktree isolation (SWT): the member's own branch, work waiting to be
  // merged (ahead), staleness against base (behind), and uncommitted files
  // (dirty — a merge would refuse right now). worktreeBranch is absent for a
  // member on the shared workdir, which is every member unless the space
  // opted in.
  worktreeBranch?: string
  worktreeAhead?: number
  worktreeBehind?: number
  worktreeDirty?: number
}

// MemberSpec — POST /api/members add-agent form (api.go:148).
// model / effort are optional pins, fixed at creation ('' = configured default).
export interface MemberSpec {
  name: string
  systemPrompt: string
  whenToUse: string
  model: string
  effort: string
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
  // DWF task graph: the dependency edges holding a blocked task (dep badges),
  // and who settles verifying ('leader' | 'auto' | 'checks').
  dependsOn?: number[]
  verifyPolicy?: string
  // CHK machine evidence: the latest verify-time check run (absent = never
  // ran), whether one is queued/executing right now (the RUNNING chip), and
  // the creation-time opt-out.
  checks?: CheckInfo
  checkRunning?: boolean
  checkOff?: boolean
  createdAt: number
  updatedAt: number
}

// CheckInfo — one verify-time check run's evidence (CHK), mirrored from
// webapi.CheckInfo.
export interface CheckInfo {
  command: string
  exit: number
  timedOut?: boolean
  durationMs: number
  startedAt: number
  workdir?: string
  tail?: string
  truncated?: boolean
  pass: boolean
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

// ProposalInfo — GET /api/swarm/:id/proposals (RP-23), oldest-first (the
// leader's review queue). refTask is the task an accepted proposal became.
export interface ProposalInfo {
  id: number
  proposer: string
  title: string
  spec?: string
  suggestedAssignee?: string
  status: 'open' | 'accepted' | 'declined'
  decidedBy?: string
  decideNote?: string
  refTask?: number
  createdAt: number
  decidedAt?: number
}

// MemoryFileInfo — GET /api/agents/:name/memory (RP-25): one read-only memory
// file, dir-relative name + raw markdown. MEMORY.md (the index) comes first.
export interface MemoryFileInfo {
  name: string
  content: string
}

// BlackboardInfo — GET /api/swarm/:id/blackboard (BB): the leader-curated team
// blackboard. updatedAt is the file mtime in unix millis (0 = empty board); by
// is the last tool writer ("" after a restart or an operator disk edit).
export interface BlackboardInfo {
  content: string
  updatedAt: number
  by?: string
}

// MetricsInfo / MemberMetricsInfo — GET /api/swarm/:id/metrics (RP-17).
// RunSeconds buckets completed runs by wall-clock (lt10s/lt1m/lt10m/gte10m);
// RunTokens by per-run token cost (lt1k/lt10k/lt50k/gte50k, RP-28). TasksStale /
// MailboxStale count RP-22 workflow-watchdog notifications since space start.
export interface MemberMetricsInfo {
  wakesMessage: number
  wakesTimer: number
  runs: number
  aborts: number
  runSeconds: Record<string, number>
  runTokens: Record<string, number>
}
export interface MetricsInfo {
  uptimeSecs: number
  eventsLogged: number
  eventsDropped: number
  hintsDropped: number
  tasksStale: number
  mailboxStale: number
  // CST space-day cost aggregate + the daily ceiling (0 = axis off).
  spaceTokensToday: number
  spaceCostTodayUsd: number
  spaceCostUnpriced?: boolean
  ceilingTotalTokens?: number
  ceilingTotalUsd?: number
  ceilingTripped?: boolean
  members: Record<string, MemberMetricsInfo>
}
