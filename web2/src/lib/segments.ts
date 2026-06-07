// splitFences breaks assistant text into plain/code segments on ``` fences. Pure
// and SAFE — it builds text segments the template renders as text nodes (no
// v-html, no injection), giving basic code-block rendering without a markdown lib.
// A trailing unclosed fence (mid-stream) renders as code, which is the desired
// live behaviour.
export interface Segment {
  code: boolean
  lang?: string
  text: string
}

export function splitFences(input: string): Segment[] {
  const out: Segment[] = []
  const parts = (input || '').split('```')
  for (let i = 0; i < parts.length; i++) {
    const p = parts[i]
    if (i % 2 === 0) {
      if (p) out.push({ code: false, text: p })
    } else {
      const nl = p.indexOf('\n')
      if (nl >= 0) {
        const lang = p.slice(0, nl).trim()
        out.push({ code: true, lang: lang || undefined, text: p.slice(nl + 1) })
      } else {
        out.push({ code: true, text: p })
      }
    }
  }
  return out
}
