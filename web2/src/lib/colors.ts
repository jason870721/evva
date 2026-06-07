// Stable per-agent colours — ported from web/src/colors.js. The same member
// reads the same hue everywhere (roster dot, mailbox routing, console header) so
// "who sent what to whom" is scannable. Pure + framework-free so it unit-tests
// under `node --test`.
//
// PALETTE was curated for a dark bg (#0e1116); the hues stay legible on the
// NEON TOKYO bg (#0A0A14) too. FE-4 re-verifies contrast and may re-tune.

const PALETTE = [
  '#f59e0b', // amber
  '#22c55e', // green
  '#60a5fa', // blue
  '#c084fc', // purple
  '#2dd4bf', // teal
  '#f472b6', // pink
  '#a3e635', // lime
  '#fb923c', // orange
  '#38bdf8', // sky
  '#e879f9', // fuchsia
]

// Fixed hues for the two non-member pseudo-recipients: the human operator stands
// out near-white, a broadcast reads neutral/dim.
const FIXED: Record<string, string> = {
  user: '#e6edf3',
  all: '#8a929c',
}

const NEUTRAL = '#8a929c'

// agentColor maps a member name to a stable colour: fixed names win, otherwise an
// FNV-1a hash picks a palette slot. Case-insensitive and trimmed. Empty/unknown
// → neutral grey, never throws.
export function agentColor(name?: string | null): string {
  const key = String(name || '')
    .trim()
    .toLowerCase()
  if (!key) return NEUTRAL
  if (FIXED[key]) return FIXED[key]
  let h = 2166136261
  for (let i = 0; i < key.length; i++) {
    h ^= key.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return PALETTE[Math.abs(h) % PALETTE.length]
}
