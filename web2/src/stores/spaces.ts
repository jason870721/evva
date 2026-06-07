import { defineStore } from 'pinia'
import { api } from '../lib/apiClient'
import { errMsg } from '../lib/util'
import type { SpaceInfo } from '../types/wire'

// Navigation-tier store: the list of swarm spaces + their lifecycle. This is the
// only data the FE-2 shell needs (picker / switcher / space menu). The per-space
// BUSINESS data (roster / tasks / stream) lives in FE-3 stores, not here.
export const useSpacesStore = defineStore('spaces', {
  state: () => ({
    list: [] as SpaceInfo[],
    loading: false,
    error: '',
  }),
  getters: {
    byId:
      (s) =>
      (id: string): SpaceInfo | null =>
        s.list.find((x) => x.id === id) || null,
    running: (s): SpaceInfo[] => s.list.filter((x) => x.status === 'running'),
  },
  actions: {
    async load() {
      this.loading = true
      try {
        this.list = (await api.spaces()) || []
        this.error = ''
      } catch (e) {
        this.error = errMsg(e)
      } finally {
        this.loading = false
      }
    },
    async run(ref: string) {
      await api.runSpace(ref)
      await this.load()
    },
    async stop(ref: string) {
      await api.stopSpace(ref)
      await this.load()
    },
    async remove(ref: string) {
      await api.removeSpace(ref)
      await this.load()
    },
    async reset(id: string) {
      await api.reset(id)
      await this.load()
    },
    async halt(id: string) {
      await api.halt(id)
    },
  },
})
