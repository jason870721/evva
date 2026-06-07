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

// ── Context meter colour ─────────────────────────────────────────────────────
// Port of the TUI's status.contextBarColor (pkg/ui/.../status/model.go): map a
// 0..100 context-utilization % onto the green→yellow→red spectrum so the swarm
// web's CTX bar reads the same hue as evva's TUI at the same load. Green ≤20%,
// yellow at 40–60%, red ≥80%, linearly interpolated through the transition bands.

const CTX_GREEN: [number, number, number] = [0x39, 0xff, 0x14]
const CTX_YELLOW: [number, number, number] = [0xfa, 0xfc, 0x4e]
const CTX_RED: [number, number, number] = [0xff, 0x00, 0x3c]

function lerpRGB(a: [number, number, number], b: [number, number, number], t: number): [number, number, number] {
  return [
    Math.round(a[0] + (b[0] - a[0]) * t),
    Math.round(a[1] + (b[1] - a[1]) * t),
    Math.round(a[2] + (b[2] - a[2]) * t),
  ]
}

const hex2 = (n: number): string => Math.max(0, Math.min(255, n)).toString(16).padStart(2, '0')

// contextColor returns a CSS hex for the given utilization %. Clamps out-of-range
// input; never throws.
export function contextColor(pct: number): string {
  const p = Math.max(0, Math.min(100, pct || 0))
  let c: [number, number, number]
  if (p <= 20) c = CTX_GREEN
  else if (p <= 40) c = lerpRGB(CTX_GREEN, CTX_YELLOW, (p - 20) / 20)
  else if (p <= 60) c = CTX_YELLOW
  else if (p <= 80) c = lerpRGB(CTX_YELLOW, CTX_RED, (p - 60) / 20)
  else c = CTX_RED
  return `#${hex2(c[0])}${hex2(c[1])}${hex2(c[2])}`
}
