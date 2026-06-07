// Minimal 5-field cron support (minute hour dom month dow) for the schedule
// editor (FE-7): validate, describe in human terms, and compute the next fire.
// Pure + framework-free (node --test). Supports *, */n, a-b, a-b/n, and lists.
// dow 0/7 = Sunday; dom/dow follow cron's OR rule when both are restricted.

export interface Cron {
  minute: number[]
  hour: number[]
  dom: number[]
  month: number[]
  dow: number[]
  domStar: boolean
  dowStar: boolean
}

function parseField(field: string, min: number, max: number): number[] | null {
  const out = new Set<number>()
  for (const part of field.split(',')) {
    let step = 1
    let range = part
    const slash = part.indexOf('/')
    if (slash >= 0) {
      step = Number(part.slice(slash + 1))
      range = part.slice(0, slash)
      if (!Number.isInteger(step) || step < 1) return null
    }
    let lo = min
    let hi = max
    if (range === '*') {
      // full range
    } else if (range.includes('-')) {
      const [a, b] = range.split('-').map(Number)
      if (!Number.isInteger(a) || !Number.isInteger(b)) return null
      lo = a
      hi = b
    } else {
      const n = Number(range)
      if (!Number.isInteger(n)) return null
      lo = n
      hi = n
    }
    if (lo < min || hi > max || lo > hi) return null
    for (let v = lo; v <= hi; v += step) out.add(v)
  }
  return [...out].sort((a, b) => a - b)
}

export function parseCron(expr: string): Cron | null {
  const f = (expr || '').trim().split(/\s+/)
  if (f.length !== 5) return null
  const minute = parseField(f[0], 0, 59)
  const hour = parseField(f[1], 0, 23)
  const dom = parseField(f[2], 1, 31)
  const month = parseField(f[3], 1, 12)
  let dow = parseField(f[4], 0, 7)
  if (!minute || !hour || !dom || !month || !dow) return null
  dow = [...new Set(dow.map((d) => (d === 7 ? 0 : d)))].sort((a, b) => a - b)
  return { minute, hour, dom, month, dow, domStar: f[2] === '*', dowStar: f[4] === '*' }
}

export function isValidCron(expr: string): boolean {
  return parseCron(expr) !== null
}

function matches(c: Cron, d: Date): boolean {
  const domMatch = c.dom.includes(d.getDate())
  const dowMatch = c.dow.includes(d.getDay())
  let dayOk: boolean
  if (c.domStar && c.dowStar) dayOk = true
  else if (c.domStar) dayOk = dowMatch
  else if (c.dowStar) dayOk = domMatch
  else dayOk = domMatch || dowMatch
  return (
    c.minute.includes(d.getMinutes()) &&
    c.hour.includes(d.getHours()) &&
    c.month.includes(d.getMonth() + 1) &&
    dayOk
  )
}

export function nextFire(expr: string, from: number = Date.now(), capDays = 366): number | null {
  const c = parseCron(expr)
  if (!c) return null
  const d = new Date(from)
  d.setSeconds(0, 0)
  d.setMinutes(d.getMinutes() + 1)
  const cap = from + capDays * 86_400_000
  while (d.getTime() <= cap) {
    if (matches(c, d)) return d.getTime()
    d.setMinutes(d.getMinutes() + 1)
  }
  return null
}

const DAYS = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat']

export function describeCron(expr: string): string {
  if (!parseCron(expr)) return 'invalid cron'
  const f = expr.trim().split(/\s+/)
  const star = (i: number) => f[i] === '*'
  const everyN = /^\*\/(\d+)$/.exec(f[0])
  if (everyN && star(1) && star(2) && star(3) && star(4)) return `every ${everyN[1]} min`
  if (star(0) && star(1) && star(2) && star(3) && star(4)) return 'every minute'
  const mm = /^\d+$/.test(f[0]) ? f[0].padStart(2, '0') : null
  const hh = /^\d+$/.test(f[1]) ? f[1].padStart(2, '0') : null
  if (mm && star(1) && star(2) && star(3) && star(4)) return `hourly at :${mm}`
  if (mm && hh && star(2) && star(3) && star(4)) return `daily at ${hh}:${mm}`
  if (mm && hh && star(2) && star(3) && /^\d$/.test(f[4])) return `weekly on ${DAYS[Number(f[4]) % 7]} at ${hh}:${mm}`
  return `cron: ${expr.trim()}`
}
