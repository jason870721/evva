// Stable per-agent colors. The same member reads the same hue everywhere it
// appears — the roster dot (the legend), the mailbox sender→recipient line, the
// console header — so "who sent what to whom" is scannable at a glance. Pure and
// framework-free so it unit-tests under `node --test`, like events.js.

// Curated for the dark theme (bg #0e1116): vibrant but legible, visually
// distinct from each other and from the accent blue used for selection/links.
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

// Fixed hues for the two non-member pseudo-recipients so they never blend into a
// real member's colour: the human operator stands out near-white, a broadcast
// reads neutral/dim.
const FIXED = {
  user: '#e6edf3', // operator (messages show sender "user")
  all: '#8a929c', // broadcast recipient
}

const NEUTRAL = '#8a929c'

// agentColor maps a member name to a stable colour: fixed names win, otherwise an
// FNV-1a hash picks a palette slot. Case-insensitive and trimmed so "Lead" and
// "lead " resolve the same. Empty/unknown → neutral grey.
export function agentColor(name) {
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
