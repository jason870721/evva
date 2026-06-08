import { defineStore } from 'pinia'

// Shipped themes. Adding one = a new tokens.primitive.<name>.css + a name here.
export const THEMES = ['neon-tokyo', 'midnight'] as const
export type ThemeName = (typeof THEMES)[number]
export type GateMode = 'modal' | 'tray'

const THEME_KEY = 'evva-theme'
const GATE_KEY = 'evva-swarm-gate-mode'

function readInitialTheme(): ThemeName {
  const saved = localStorage.getItem(THEME_KEY) || ''
  return (THEMES as readonly string[]).includes(saved) ? (saved as ThemeName) : 'neon-tokyo'
}

export const useUiStore = defineStore('ui', {
  state: () => ({
    theme: readInitialTheme(),
    // Gate surface preference (RP-4 UX-1b): blocking modal vs non-blocking tray.
    gateMode: (localStorage.getItem(GATE_KEY) === 'tray' ? 'tray' : 'modal') as GateMode,
    hideThinking: false,
    // Stall thresholds (FE-5): a phase stuck longer than this surfaces in the
    // Attention strip even when not formally blocked.
    stallExecMs: 5 * 60_000,
    stallThinkMs: 3 * 60_000,
    // Narrow-screen roster drawer (FE-8 RWD).
    rosterDrawer: false,
  }),
  actions: {
    applyTheme() {
      document.documentElement.dataset.theme = this.theme
    },
    setTheme(t: ThemeName) {
      this.theme = t
      localStorage.setItem(THEME_KEY, t)
      this.applyTheme()
    },
    toggleTheme() {
      const i = THEMES.indexOf(this.theme)
      this.setTheme(THEMES[(i + 1) % THEMES.length])
    },
    setGateMode(m: GateMode) {
      this.gateMode = m
      localStorage.setItem(GATE_KEY, m)
    },
    toggleRoster() {
      this.rosterDrawer = !this.rosterDrawer
    },
    closeRoster() {
      this.rosterDrawer = false
    },
  },
})
