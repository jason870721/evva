import { createI18n } from 'vue-i18n'

// i18n scaffold (FE-8): infrastructure + a curated message set so locale
// switching is demonstrably live. Full string extraction across every component
// is mechanical follow-up; this proves the seam (zh-Hant ↔ en) works.
export type Locale = 'zh-Hant' | 'en'

const messages = {
  'zh-Hant': {
    tabs: { board: '看板', timeline: '時間軸', stream: '串流', completed: '已完成' },
    common: { connect: '連線', refresh: '重新整理', changeToken: '更換 token', logout: '登出', members: '成員' },
    ws: { reconnecting: '即時連線中斷，重連中…（已退回 2.5 秒輪詢）' },
  },
  en: {
    tabs: { board: 'Board', timeline: 'Timeline', stream: 'Stream', completed: 'Completed' },
    common: { connect: 'Connect', refresh: 'Refresh', changeToken: 'Change token', logout: 'Logout', members: 'Members' },
    ws: { reconnecting: 'Live connection lost — reconnecting… (falling back to a 2.5s poll)' },
  },
}

const LOCALE_KEY = 'evva-locale'
function initialLocale(): Locale {
  const s = localStorage.getItem(LOCALE_KEY)
  if (s === 'en' || s === 'zh-Hant') return s
  return navigator.language?.startsWith('zh') ? 'zh-Hant' : 'en'
}

export const i18n = createI18n({
  legacy: false,
  locale: initialLocale(),
  fallbackLocale: 'en',
  messages,
})

export function setLocale(l: Locale) {
  i18n.global.locale.value = l
  localStorage.setItem(LOCALE_KEY, l)
}
export function currentLocale(): Locale {
  return i18n.global.locale.value as Locale
}
