import { defineStore } from 'pinia'

// Session = the operator's connection identity. For now that's just the service
// token (the `evva service` daemon prints it; the operator pastes it once). Auth
// hardening is out of scope for FE v2 (RP-4 §6).
const TOKEN_KEY = 'evva-swarm-token'

export const useSessionStore = defineStore('session', {
  state: () => ({ token: localStorage.getItem(TOKEN_KEY) || '' }),
  getters: {
    authed: (s): boolean => !!s.token,
  },
  actions: {
    connect(t: string) {
      this.token = t.trim()
      localStorage.setItem(TOKEN_KEY, this.token)
    },
    disconnect() {
      this.token = ''
      localStorage.removeItem(TOKEN_KEY)
    },
  },
})
