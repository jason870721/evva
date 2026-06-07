// errMsg normalises an unknown thrown value into a display string.
export function errMsg(e: unknown): string {
  if (e instanceof Error) return e.message
  return String((e as { message?: string })?.message ?? e)
}
