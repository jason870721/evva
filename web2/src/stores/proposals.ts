import { defineStore } from 'pinia'
import { api } from '../lib/apiClient'
import { countOpen } from '../lib/proposals'
import type { ProposalInfo } from '../types/wire'
import { useConnectionStore } from './connection'

// Trailing-debounce timer, the ledger pattern: proposal changes arrive as
// store_update hints, so the ingest pipeline batches REST refreshes rather than
// hitting the API once per event.
let debounceT: ReturnType<typeof setTimeout> | null = null

// proposals holds the RP-23 bottom-up work queue: what workers filed with
// task_propose and what the leader did about it. The web is a read-only window
// — accept/decline live behind the leader's own tools, not operator buttons.
export const useProposalsStore = defineStore('proposals', {
  state: () => ({
    list: [] as ProposalInfo[], // service order: oldest-first (the review queue)
  }),
  getters: {
    openCount: (s): number => countOpen(s.list),
  },
  actions: {
    async refresh() {
      const id = useConnectionStore().spaceId
      if (!id) return
      this.list = (await api.proposals(id)) || []
    },
    scheduleRefresh() {
      if (debounceT) clearTimeout(debounceT)
      debounceT = setTimeout(() => {
        debounceT = null
        void this.refresh()
      }, 300)
    },
    reset() {
      this.list = []
    },
  },
})
