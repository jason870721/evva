import { defineStore } from 'pinia'
import { api } from '../lib/apiClient'
import type { MessageInfo } from '../types/wire'
import { useConnectionStore } from './connection'

// mail holds the space's messages (inter-agent + operator), polled with the
// roster/ledger. FE-4 (mailbox) and FE-5 (timeline) consume it; mailState()
// (FE-1) classifies unread→reading→read.
export const useMailStore = defineStore('mail', {
  state: () => ({
    messages: [] as MessageInfo[],
  }),
  actions: {
    async refresh() {
      const id = useConnectionStore().spaceId
      if (!id) return
      this.messages = (await api.messages(id)) || []
    },
    reset() {
      this.messages = []
    },
  },
})
