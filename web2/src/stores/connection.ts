import { defineStore } from 'pinia'
import { openSocket, type Socket, type WsStatus } from '../lib/ws'
import { isApproval, isQuestion, approvalOf, questionOf, touchesLedger } from '../lib/events'
import type { WireEvent, CommandErrorFrame } from '../types/events'
import { useSessionStore } from './session'
import { useStreamStore } from './stream'
import { useGateStore } from './gate'
import { useLedgerStore } from './ledger'
import { useProposalsStore } from './proposals'

// The socket handle is kept module-local (not in reactive state) — it is an IO
// object, not data. Components/stores send through the store's send() action.
let sock: Socket | null = null

// connection owns the single WS to the active space + the ingest pipeline that
// fans one wire event out to the data stores (the store-side twin of v1
// SpaceView.onEvent). It also holds the active spaceId so the data stores know
// what to fetch.
export const useConnectionStore = defineStore('connection', {
  state: () => ({ status: 'closed' as WsStatus, lastError: '', spaceId: '' }),
  actions: {
    setSpace(id: string) {
      this.spaceId = id
    },
    open(spaceId: string) {
      this.close()
      this.spaceId = spaceId
      const token = useSessionStore().token
      sock = openSocket({
        token,
        spaceId,
        onEvent: (ev) => this.ingest(ev as WireEvent | CommandErrorFrame),
        onStatus: (s) => {
          this.status = s
          // On (re)connect, catch gates raised before/while we were disconnected.
          if (s === 'open') useGateStore().hydrate()
        },
      })
    },
    send(cmd: unknown) {
      sock?.send(cmd)
    },
    close() {
      sock?.close()
      sock = null
      this.status = 'closed'
    },
    // ingest routes one inbound frame. A command_error is a service frame (not an
    // event): surface it and re-hydrate gates so a reply that failed to route
    // doesn't strand the member (RP-2 §3.3).
    ingest(raw: WireEvent | CommandErrorFrame) {
      if ((raw as CommandErrorFrame).type === 'command_error') {
        const f = raw as CommandErrorFrame
        this.lastError = f.message || 'command failed'
        // Attach the failure to the specific gate (per-card retry), then re-pull
        // pending so the optimistically-removed gate reappears, answerable.
        if (f.reqId) useGateStore().noteError(f.reqId, f.message || 'command failed')
        useGateStore().hydrate()
        return
      }
      const ev = raw as WireEvent
      const stream = useStreamStore()
      // Every event may move a member's phase — fold it before the gate early-return.
      stream.foldPhase(ev)
      if (isApproval(ev)) {
        useGateStore().enqueue('approval', approvalOf(ev))
        return
      }
      if (isQuestion(ev)) {
        useGateStore().enqueue('question', questionOf(ev))
        return
      }
      stream.foldChat(ev)
      if (touchesLedger(ev)) {
        useLedgerStore().scheduleRefresh()
        useProposalsStore().scheduleRefresh()
      }
    },
  },
})
