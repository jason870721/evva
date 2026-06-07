import { defineStore } from 'pinia'
import { groupTasks } from '../lib/events'
import { api } from '../lib/apiClient'
import type { TaskInfo, TaskStatus, TaskPage } from '../types/wire'
import { useConnectionStore } from './connection'

// Trailing-debounce timer: tool_use_result events are high-frequency, so the
// ingest pipeline calls scheduleRefresh() rather than refresh() per event — the
// board still reaches the same final state, with far fewer REST hits (FE-3 §5).
let debounceT: ReturnType<typeof setTimeout> | null = null

export const useLedgerStore = defineStore('ledger', {
  state: () => ({
    tasks: [] as TaskInfo[], // board snapshot: active + newest-few completed (RP-6)
    completedTotal: 0,
  }),
  getters: {
    groups: (s): Record<TaskStatus, TaskInfo[]> => groupTasks(s.tasks),
  },
  actions: {
    async refresh() {
      const id = useConnectionStore().spaceId
      if (!id) return
      const p: TaskPage = await api.tasks(id)
      this.tasks = p?.tasks || []
      this.completedTotal = p?.total || 0
    },
    scheduleRefresh() {
      if (debounceT) clearTimeout(debounceT)
      debounceT = setTimeout(() => {
        debounceT = null
        void this.refresh()
      }, 300)
    },
    // Paged view of one status (the Completed tab, FE-5).
    page(status: TaskStatus, limit: number, offset: number): Promise<TaskPage> {
      const id = useConnectionStore().spaceId
      return api.tasksPage(id, { status, limit, offset })
    },
    reset() {
      this.tasks = []
      this.completedTotal = 0
    },
  },
})
