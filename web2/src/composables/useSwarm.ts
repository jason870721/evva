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
    conn.close()
    stream.reset()
    gate.reset()
    space.reset()
    ledger.reset()
    mail.reset()
    proposals.reset()
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
}
