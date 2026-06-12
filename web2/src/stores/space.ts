import { defineStore } from 'pinia'
import { attentionItems, type AttentionItem } from '../lib/events'
import { api } from '../lib/apiClient'
import { errMsg } from '../lib/util'
import type { MemberInfo, MemberSpec, MemoryFileInfo, MetricsInfo, SkillInfo, SkillSpec } from '../types/wire'
import { useConnectionStore } from './connection'
import { useStreamStore } from './stream'
import { useUiStore } from './ui'

// space holds the polled roster (structure) and overlays the live event-derived
// phase from the stream store (freshness) — the store-side twin of v1
// SpaceView.mergedRoster. `now` ticks (driven by useSwarm) so elapsed clocks stay
// live without each component owning a timer.
export const useSpaceStore = defineStore('space', {
  state: () => ({
    roster: [] as MemberInfo[],
    now: Date.now(),
    error: '',
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
    },
  },
})
