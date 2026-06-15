import { defineStore } from 'pinia'
import { attentionItems, type AttentionItem } from '../lib/events'
import { api } from '../lib/apiClient'
import { errMsg } from '../lib/util'
import type { MemberInfo, MemberSpec, MemoryFileInfo, MetricsInfo, SkillInfo, SkillSpec } from '../types/wire'
import { useConnectionStore } from './connection'
import { useStreamStore } from './stream'
import { useUiStore } from './ui'

// Outcome of a bulk action fanned out across members: the per-member endpoints
// are independent (the supervisor locks per member), so we run them concurrently
// and report which landed vs which were refused (e.g. a member that went busy
// mid-flight → 409).
export type BulkResult = { ok: string[]; failed: { name: string; error: string }[] }

// space holds the polled roster (structure) and overlays the live event-derived
// phase from the stream store (freshness) — the store-side twin of v1
// SpaceView.mergedRoster. `now` ticks (driven by useSwarm) so elapsed clocks stay
// live without each component owning a timer.
export const useSpaceStore = defineStore('space', {
  state: () => ({
    roster: [] as MemberInfo[],
    now: Date.now(),
    error: '',
    // Per-member compaction in-flight flags. A full compact is a multi-second
    // LLM call, so its busy state has to outlive whichever component triggered
    // it: the inspector is reused across members (no :key), so a component-local
    // flag bled onto whatever member you switched to mid-compact. Keying it by
    // member name here disables only the compacting member's own buttons.
    compacting: {} as Record<string, boolean>,
    // Per-member in-flight flags for the other roster ops, same rationale as
    // `compacting`: clear (a session wipe) and the lifecycle verbs
    // (suspend/resume/freeze/unfreeze) each get a member-keyed flag so a card
    // shows its own spinner during a bulk fan-out without bleeding onto siblings.
    clearing: {} as Record<string, boolean>,
    acting: {} as Record<string, boolean>,
  }),
  getters: {
    merged(state): MemberInfo[] {
      const lp = useStreamStore().livePhases
      return state.roster.map((m) => {
        const p = lp[m.agentId]
        return p ? { ...m, phase: p.phase, tool: p.tool, phaseSince: p.since } : m
      })
    },
    attention(): AttentionItem[] {
      const ui = useUiStore()
      return attentionItems(this.merged, this.now, { stallExecMs: ui.stallExecMs, stallThinkMs: ui.stallThinkMs })
    },
    leader(state): string {
      const m = state.roster.find((x) => x.role === 'leader')
      return m?.name || state.roster[0]?.name || ''
    },
    // True while the named member has a compaction request in flight. Reactive
    // per member, so switching the inspector never inherits another member's
    // busy state.
    isCompacting: (state) => (name: string) => !!state.compacting[name],
    isClearing: (state) => (name: string) => !!state.clearing[name],
    // Any roster op in flight for this member — drives the card's spinner.
    memberBusy: (state) => (name: string) =>
      !!state.compacting[name] || !!state.clearing[name] || !!state.acting[name],
  },
  actions: {
    async refresh() {
      const id = useConnectionStore().spaceId
      if (!id) return
      try {
        this.roster = (await api.roster(id)) || []
        this.error = ''
      } catch (e) {
        this.error = errMsg(e)
      }
    },
    async memberCmd(verb: 'suspend' | 'resume' | 'freeze' | 'unfreeze', name: string) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api[verb](id, name)
      await this.refresh()
    },
    // Clear one member's session (fresh context, new agent id). The refresh
    // re-reads the roster's new agentId, so the member's console naturally
    // switches to the (empty) new stream. Errors (409 busy) propagate to the
    // caller's confirm dialog.
    async clearMember(name: string) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.clearMember(id, name)
      await this.refresh()
    },
    // Compact one member's live context (micro = free tool-result elision, full =
    // one-LLM-call summary brief). The refresh re-reads the roster so the CTX bar
    // reflects the reduced context; errors (409 busy / 400 bad kind) propagate to
    // the caller for inline display.
    async compactMember(name: string, kind: 'micro' | 'full') {
      const id = useConnectionStore().spaceId
      if (!id) return
      this.compacting[name] = true
      try {
        await api.compactMember(id, name, kind)
        await this.refresh()
      } finally {
        this.compacting[name] = false
      }
    },
    // Fan `op` out across `names` concurrently (the per-member endpoints are
    // independent), refresh the roster once at the end, and report which
    // succeeded vs failed. Callers pre-filter by eligibility (a busy member
    // can't be cleared/compacted); allSettled still catches the member that
    // goes busy between the filter and the request → a 409 lands in `failed`.
    async bulkRun(names: string[], op: (name: string) => Promise<void>): Promise<BulkResult> {
      const settled = await Promise.allSettled(names.map((n) => op(n)))
      await this.refresh()
      const ok: string[] = []
      const failed: { name: string; error: string }[] = []
      settled.forEach((r, i) => {
        if (r.status === 'fulfilled') ok.push(names[i])
        else failed.push({ name: names[i], error: errMsg(r.reason) })
      })
      return { ok, failed }
    },
    bulkCompact(names: string[], kind: 'micro' | 'full'): Promise<BulkResult> {
      const id = useConnectionStore().spaceId
      if (!id) return Promise.resolve({ ok: [], failed: [] })
      return this.bulkRun(names, async (n) => {
        this.compacting[n] = true
        try {
          await api.compactMember(id, n, kind)
        } finally {
          this.compacting[n] = false
        }
      })
    },
    bulkClear(names: string[]): Promise<BulkResult> {
      const id = useConnectionStore().spaceId
      if (!id) return Promise.resolve({ ok: [], failed: [] })
      return this.bulkRun(names, async (n) => {
        this.clearing[n] = true
        try {
          await api.clearMember(id, n)
        } finally {
          this.clearing[n] = false
        }
      })
    },
    bulkCmd(verb: 'suspend' | 'resume' | 'freeze' | 'unfreeze', names: string[]): Promise<BulkResult> {
      const id = useConnectionStore().spaceId
      if (!id) return Promise.resolve({ ok: [], failed: [] })
      return this.bulkRun(names, async (n) => {
        this.acting[n] = true
        try {
          await api[verb](id, n)
        } finally {
          this.acting[n] = false
        }
      })
    },
    // Switch a member's permission stance (default | accept_edits | bypass).
    async setPermissionMode(name: string, mode: string) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.setPermissionMode(id, name, mode)
      await this.refresh()
    },
    // Membership editing (RP-8). Errors propagate to the caller (the dialog shows
    // them inline); a success refreshes the roster.
    async createMember(spec: MemberSpec) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.createMember(id, spec)
      await this.refresh()
    },
    async removeMember(name: string, deleteDir: boolean) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.removeMember(id, name, deleteDir)
      await this.refresh()
    },
    // Schedule CRUD (RP-7/RP-8): any member, incl. the leader.
    async setSchedule(name: string, cron: string, prompt: string) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.setSchedule(id, name, { cron, prompt })
      await this.refresh()
    },
    async clearSchedule(name: string) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.clearSchedule(id, name)
      await this.refresh()
    },
    // On-demand catalogs for the authoring dialogs (kept behind the store).
    fetchTools(): Promise<string[]> {
      const id = useConnectionStore().spaceId
      return id ? api.tools(id) : Promise.resolve([])
    },
    fetchModels(): Promise<string[]> {
      return api.models()
    },
    fetchSkills(name: string): Promise<SkillInfo[]> {
      const id = useConnectionStore().spaceId
      return id ? api.memberSkills(id, name) : Promise.resolve([])
    },
    async addSkill(name: string, spec: SkillSpec) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.addSkill(id, name, spec)
    },
    async deleteSkill(name: string, skill: string) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.deleteSkill(id, name, skill)
    },
    // Space-shared skills (RP-26): one copy every member loads; add/delete
    // hot-reloads the whole team.
    fetchSharedSkills(): Promise<SkillInfo[]> {
      const id = useConnectionStore().spaceId
      return id ? api.sharedSkills(id) : Promise.resolve([])
    },
    async addSharedSkill(spec: SkillSpec) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.addSharedSkill(id, spec)
    },
    async deleteSharedSkill(skill: string) {
      const id = useConnectionStore().spaceId
      if (!id) return
      await api.deleteSharedSkill(id, skill)
    },
    // Member long-term memory (RP-25), read-only.
    fetchMemory(name: string): Promise<MemoryFileInfo[]> {
      const id = useConnectionStore().spaceId
      return id ? api.memberMemory(id, name) : Promise.resolve([])
    },
    // Scheduler/watchdog/token counters (RP-17/22/28), fetched on demand.
    fetchMetrics(): Promise<MetricsInfo | null> {
      const id = useConnectionStore().spaceId
      return id ? api.metrics(id) : Promise.resolve(null)
    },
    reset() {
      this.roster = []
      this.compacting = {}
      this.clearing = {}
      this.acting = {}
    },
  },
})
