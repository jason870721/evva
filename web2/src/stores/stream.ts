import { defineStore } from 'pinia'
import { reduceChat, reducePhase, consoleTurns, type Turn, type PhaseMap } from '../lib/events'
import type { WireEvent } from '../types/events'
import type { MemberInfo, TranscriptEntry } from '../types/wire'
import { api } from '../lib/apiClient'
import { errMsg } from '../lib/util'
import { useConnectionStore } from './connection'

// collectSeeds reads each member's persisted transcript and flattens its
// assistant turns into a seed list — the persisted-truth view used to (re)hydrate
// the console. A per-member fetch failure is skipped so the rest still seed; the
// whole call resolves to whatever it could gather (possibly empty). Shared by the
// two hydrate entry points so seed-on-empty and replace-on-reconnect stay in step.
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
    // Best-effort: seed the console from each member's persisted transcript so a
    // reload doesn't show empty. Only seeds an EMPTY console (never clobbers turns
    // the live stream already delivered). Mirrors v1 hydrateConsole.
    async hydrateFromTranscripts(roster: MemberInfo[]) {
      const id = useConnectionStore().spaceId
      if (!id) return
      const seeded = await collectSeeds(id, roster)
      if (seeded.length && !this.turns.length) this.turns = seeded
    },
    // Reconnect path: after the WS drops and comes back (most visibly across a
    // `service stop && start`, which rebuilds the space and resumes agents that
    // then sit idle), the live turns are stale and the gap's events were never
    // replayed. REPLACE the console with the persisted truth — but only when the
    // transcripts actually returned something, so a reconnect BLIP (the WS opens,
    // then the service rejects it because the space isn't reconciled yet, so the
    // fetch fails/empties) leaves the existing turns intact instead of blanking
    // the stream. This is the non-destructive successor to reset()+hydrate, whose
    // empty window blanked the console on every flaky reconnect.
    async rehydrateFromTranscripts(roster: MemberInfo[]) {
      const id = useConnectionStore().spaceId
      if (!id) return
      const seeded = await collectSeeds(id, roster)
      if (seeded.length) this.turns = seeded
    },
  },
})
