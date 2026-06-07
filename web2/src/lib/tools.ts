// Tool-call rendering helpers (FE-4). toolFamily picks the ToolCard body renderer
// from a tool name; toolField safely reads a string field out of the opaque tool
// Input payload.
export type ToolFamily = 'bash' | 'diff' | 'read' | 'web' | 'generic'

export function toolFamily(name: string): ToolFamily {
  const n = (name || '').toLowerCase()
  if (n.includes('bash') || n === 'shell' || n.includes('exec')) return 'bash'
  if (n.includes('edit') || n.includes('write') || n.includes('patch') || n.includes('str_replace')) return 'diff'
  if (n === 'read' || n.includes('read_file') || n.includes('cat')) return 'read'
  if (n.includes('web') || n.includes('fetch') || n.includes('search') || n.includes('tavily')) return 'web'
  return 'generic'
}

export function toolField(input: unknown, key: string): string {
  if (input && typeof input === 'object' && key in input) {
    const v = (input as Record<string, unknown>)[key]
    if (v == null) return ''
    return typeof v === 'string' ? v : JSON.stringify(v)
  }
  return ''
}

export function toolInputJson(input: unknown): string {
  if (input == null) return ''
  try {
    return JSON.stringify(input, null, 2)
  } catch {
    return String(input)
  }
}
