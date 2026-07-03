import { defineStore } from 'pinia'
import { reduceChat, reducePhase, consoleTurns, type Turn, type PhaseMap } from '../lib/events'
import type { WireEvent } from '../types/events'
import type { MemberInfo, TranscriptEntry } from '../types/wire'
import { api } from '../lib/apiClient'
import { errMsg } from '../lib/util'
import { useConnectionStore } from './connection'

// collectHistory replays the space's durable chat log (GET /chatlog) through
// the SAME reducer the live WS feed uses, so a rebuilt console carries
// everything the live one showed: whole text/thinking turns, tool cards with
// results, errors, operator messages, timestamps, interleaved order. This is
// the primary (re)hydrate source — the live LLM context the transcript
// endpoint reads is a working buffer that compaction rewrites (the leader's
// most of all), which is exactly why re-seeding from it kept losing turns.
// Resolves [] when the log is unavailable (event_log: false) — callers fall
// back to transcript seeding.
async function collectHistory(id: string): Promise<Turn[]> {
  let evs: WireEvent[] = []
  try {
    evs = (await api.chatlog(id)) || []
  } catch {
    return []
  }
  let turns: Turn[] = []
  for (const ev of evs) turns = reduceChat(turns, ev)
  // The log can end mid-stream (member cut off); nothing further will close
  // those blocks, and an open cursor is a live-feed affordance.
  for (const t of turns) {
    if ((t.type === 'assistant' || t.type === 'thinking') && t.open) t.open = false
  }
  return turns
}

// collectSeeds reads each member's live transcript and flattens its assistant
// turns into a seed list — the FALLBACK (re)hydrate source for spaces that run
// with event_log: false. Lossy by nature: the live context omits tool/thinking/
// user turns and shrinks under compaction. A per-member fetch failure is
// skipped so the rest still seed; the whole call resolves to whatever it could
// gather (possibly empty). Shared by the two hydrate entry points so
// seed-on-empty and replace-on-reconnect stay in step.
async function collectSeeds(id: string, roster: MemberInfo[]): Promise<Turn[]> {
  const seeded: Turn[] = []
  for (const m of roster) {
    if (!m.agentId) continue
    let tr: TranscriptEntry[] = []
    try {
      tr = (await api.transcript(id, m.name)) || []
    } catch {
      continue
    }
    for (const e of tr) {
      if (e.role === 'assistant' && e.text) {
        seeded.push({ type: 'assistant', agentId: m.agentId, text: e.text, open: false })
      }
    }
  }
  return seeded
}

// stream holds the demuxed chat turns + the live per-agent phase map, both folded
// from the WS event stream by the FE-1 reducers (which are pinned by events.test).
export const useStreamStore = defineStore('stream', {
  state: () => ({
    turns: [] as Turn[],
    livePhases: {} as PhaseMap,
  }),
  getters: {
    // One mixed stream, demuxed to a member's console (FE-4 consumes this).
    forMember: (s) => (agentId: string, member: string): Turn[] => consoleTurns(s.turns, agentId, member),
  },
  actions: {
    foldChat(ev: WireEvent) {
      this.turns = [...reduceChat(this.turns, ev)]
    },
    foldPhase(ev: WireEvent) {
      this.livePhases = reducePhase(this.livePhases, ev)
    },
    pushUser(target: string, agentId: string, text: string) {
      this.turns = [...this.turns, { type: 'user', target, agentId, text, at: Date.now() }]
    },
    // Operator → member message (mail-mode flat comms). Optimistically shows the
    // user turn, then rides the bus + drain; the reply streams back over the WS
    // into this same console (RP-1). Errors surface on connection.lastError.
    async sendMessage(to: string, agentId: string, text: string) {
      this.pushUser(to, agentId, text)
      const conn = useConnectionStore()
      if (!conn.spaceId) return
      try {
        await api.message(conn.spaceId, to, text)
      } catch (e) {
        conn.lastError = errMsg(e)
      }
    },
    // On-demand transcript fetch for the inspector's History tab (kept behind a
    // store action so components don't touch the api client directly).
    async transcriptOf(member: string): Promise<TranscriptEntry[]> {
      const id = useConnectionStore().spaceId
      if (!id) return []
      try {
        return (await api.transcript(id, member)) || []
      } catch {
        return []
      }
    },
    reset() {
      this.turns = []
      this.livePhases = {}
    },
    // Best-effort: seed the console from the durable chat log (transcript
    // fallback for event_log: false spaces) so a reload doesn't show empty.
    // Only seeds an EMPTY console (never clobbers turns the live stream
    // already delivered).
    async hydrateHistory(roster: MemberInfo[]) {
      const id = useConnectionStore().spaceId
      if (!id) return
      let seeded = await collectHistory(id)
      if (!seeded.length) seeded = await collectSeeds(id, roster)
      if (seeded.length && !this.turns.length) this.turns = seeded
    },
    // Reconnect path: after the WS drops and comes back (most visibly across a
    // `service stop && start`, which rebuilds the space and resumes agents that
    // then sit idle), the live turns are stale and the gap's events were never
    // replayed. REPLACE the console with the durable chat log — but only when
    // the fetch actually returned something, so a reconnect BLIP (the WS opens,
    // then the service rejects it because the space isn't reconciled yet, so the
    // fetch fails/empties) leaves the existing turns intact instead of blanking
    // the stream. This is the non-destructive successor to reset()+hydrate, whose
    // empty window blanked the console on every flaky reconnect. One known gap:
    // a turn streaming ACROSS the reconnect shows only its post-boundary text
    // until its block lands in the log (coalesced at turn boundaries).
    async rehydrateHistory(roster: MemberInfo[]) {
      const id = useConnectionStore().spaceId
      if (!id) return
      let seeded = await collectHistory(id)
      if (!seeded.length) seeded = await collectSeeds(id, roster)
      if (seeded.length) this.turns = seeded
    },
  },
})
