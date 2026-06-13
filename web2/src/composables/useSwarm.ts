import { onMounted, onBeforeUnmount, watch, type Ref } from 'vue'
import { useConnectionStore } from '../stores/connection'
import { useSpacesStore } from '../stores/spaces'
import { useSpaceStore } from '../stores/space'
import { useLedgerStore } from '../stores/ledger'
import { useMailStore } from '../stores/mail'
import { useProposalsStore } from '../stores/proposals'
import { useStreamStore } from '../stores/stream'
import { useGateStore } from '../stores/gate'

// useSwarm wires the active space's lifecycle for the duration of a component
// (WorkspaceView): initial snapshots → console hydrate → open the WS → start the
// 2.5s REST reconciliation poll + the 1s elapsed clock; tears it all down on
// unmount. This is the ONLY place IO lifecycle lives — components just read
// stores (FE-3 §9.1).
export function useSwarm(spaceId: Ref<string>) {
  const conn = useConnectionStore()
  const spaces = useSpacesStore()
  const space = useSpaceStore()
  const ledger = useLedgerStore()
  const mail = useMailStore()
  const proposals = useProposalsStore()
  const stream = useStreamStore()
  const gate = useGateStore()

  let poll: ReturnType<typeof setInterval> | null = null
  let clock: ReturnType<typeof setInterval> | null = null
  // Tracks whether the socket has already reached 'open' on this connection. A
  // later return to 'open' is therefore a RE-connect (the service bounced or the
  // link dropped) — the trigger for resync(). Cleared by stop() so a deliberate
  // restart's first 'open' is treated as initial, not a reconnect.
  let wasOpen = false

  async function start() {
    conn.setSpace(spaceId.value)
    await Promise.all([space.refresh(), ledger.refresh(), mail.refresh(), proposals.refresh()])
    await stream.hydrateFromTranscripts(space.roster)
    conn.open(spaceId.value)
    poll = setInterval(() => {
      void space.refresh()
      void ledger.refresh()
      void mail.refresh()
      void proposals.refresh()
    }, 2500)
    clock = setInterval(() => {
      space.now = Date.now()
    }, 1000)
  }

  function stop() {
    if (poll) clearInterval(poll)
    if (clock) clearInterval(clock)
    wasOpen = false
    conn.close()
    stream.reset()
    gate.reset()
    space.reset()
    ledger.reset()
    mail.reset()
    proposals.reset()
  }

  // resync re-pulls the read side after a reconnect. Events emitted while the
  // socket was down are NOT replayed (the hub only fans to connected clients, and
  // gates aside, nothing is rebroadcast on reconnect), so the live stream is stale
  // — most visibly after `service stop && start`, which rebuilds every space.
  // Re-fetch the snapshots, then reseed the console from each member's persisted
  // transcript (the same fidelity as a fresh page load), so the firehose and
  // roster reflect post-restart reality without the operator opening each member.
  // Gates are NOT touched here — connection.open()'s onStatus re-hydrates them on
  // every 'open', reconnect included.
  async function resync() {
    await Promise.all([space.refresh(), ledger.refresh(), mail.refresh(), proposals.refresh()])
    stream.reset()
    await stream.hydrateFromTranscripts(space.roster)
  }

  onMounted(start)
  onBeforeUnmount(stop)

  // A space reset rebuilds the swarm under the same id — restart the whole IO
  // lifecycle so stale turns / gates / roster agentIds don't linger on screen.
  watch(
    () => spaces.epoch,
    () => {
      stop()
      void start()
    },
  )

  // Switching the active space (the TopBar SpaceSwitcher) only changes the route
  // param — WorkspaceView is reused, not remounted, so onMounted never re-fires.
  // Tear the old space's IO down and bootstrap the new one, or the shell keeps
  // streaming the previous swarm while only the picker label changes.
  watch(spaceId, (next, prev) => {
    if (next === prev) return
    stop()
    void start()
  })

  // The socket reconnects itself after a drop (ws.ts backoff loop). A return to
  // 'open' AFTER a prior open means the service bounced or the link blipped while
  // the component stayed mounted — resync so the stale read side catches up. The
  // first 'open' of a fresh start() is the initial connect and must NOT resync
  // (start() already hydrated); stop() clears wasOpen so a deliberate restart's
  // open reads as initial too.
  watch(
    () => conn.status,
    (s) => {
      if (s !== 'open') return
      if (wasOpen) void resync()
      wasOpen = true
    },
  )
}
