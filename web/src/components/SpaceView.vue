<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { openSocket } from '../ws.js'
import { reduceChat, consoleTurns, isApproval, isQuestion, approvalOf, questionOf, touchesLedger, attentionItems } from '../events.js'
import MemberConsole from './MemberConsole.vue'
import TeamBoard from './TeamBoard.vue'
import Roster from './Roster.vue'
import AgentTranscript from './AgentTranscript.vue'
import ApprovalOverlay from './ApprovalOverlay.vue'
import ApprovalTray from './ApprovalTray.vue'
import AttentionBar from './AttentionBar.vue'

const props = defineProps({
  api: { type: Object, required: true },
  token: { type: String, required: true },
  space: { type: Object, required: true },
})
const emit = defineEmits(['leave'])

const roster = ref([])
const tasks = ref([])
const messages = ref([])
const chat = ref([])
const wsStatus = ref('connecting')
// Pending gates are QUEUES, not single slots: in a swarm several members can
// block on approval at once, and a single ref would let the second event clobber
// the first — stranding the first member's blocked tool with no way to answer it
// (RP-2 §3.2). We surface the head of each queue and keep the rest behind it.
const approvals = ref([])
const questions = ref([])
const approval = computed(() => approvals.value[0] || null)
const question = computed(() => questions.value[0] || null)
// Gate surface preference (RP-4 UX-1b): "modal" (blocking, default — forces the
// operator to deal with it) vs "tray" (non-blocking rail — keep watching the team
// while deciding). Persisted per browser.
const GATE_MODE_KEY = 'evva-swarm-gate-mode'
const gateMode = ref(localStorage.getItem(GATE_MODE_KEY) === 'tray' ? 'tray' : 'modal')
function toggleGateMode() {
  gateMode.value = gateMode.value === 'modal' ? 'tray' : 'modal'
  localStorage.setItem(GATE_MODE_KEY, gateMode.value)
}
const focused = ref('') // the member whose console is in the center pane
const selected = ref('') // the member whose transcript+mailbox is in the right pane
const transcript = ref([])
const err = ref('')
const now = ref(Date.now()) // ticks every 1s so elapsed clocks stay live

let sock = null
let poll = null
let clock = null

// What needs the operator: blocked-on-approval/question or errored/paused
// members, most-urgent first, with live elapsed times (RP-4 UX-1).
const attention = computed(() => attentionItems(roster.value, now.value))

const leader = computed(() => {
  const m = roster.value.find((x) => x.role === 'leader')
  return m ? m.name : roster.value[0] ? roster.value[0].name : ''
})
// The member the console is focused on — explicit focus, else the leader.
const focusedMember = computed(() => focused.value || leader.value)
const focusedEntry = computed(() => roster.value.find((m) => m.name === focusedMember.value) || {})
const focusedAgentId = computed(() => focusedEntry.value.agentId || '')
// One mixed event stream, demuxed to the focused member.
const focusedTurns = computed(() => consoleTurns(chat.value, focusedAgentId.value, focusedMember.value))
const selectedMail = computed(() =>
  messages.value.filter((m) => m.recipient === selected.value || m.sender === selected.value || m.recipient === 'all'),
)

async function refreshSnapshots() {
  try {
    const [r, t, m] = await Promise.all([
      props.api.roster(props.space.id),
      props.api.tasks(props.space.id),
      props.api.messages(props.space.id),
    ])
    roster.value = r || []
    tasks.value = t || []
    messages.value = m || []
    err.value = ''
  } catch (e) {
    err.value = String(e.message || e)
  }
}

function onEvent(ev) {
  // A command_error frame is the backend telling us an inbound command (e.g. an
  // approval reply) failed to route, instead of silently dropping it. Surface it.
  if (ev && ev.type === 'command_error') {
    err.value = ev.message || 'command failed'
    return
  }
  if (isApproval(ev)) {
    enqueueGate(approvals, approvalOf(ev))
    return
  }
  if (isQuestion(ev)) {
    enqueueGate(questions, questionOf(ev))
    return
  }
  chat.value = [...reduceChat(chat.value, ev)]
  if (touchesLedger(ev)) refreshSnapshots()
}

// enqueueGate appends a pending gate, de-duped by (agentId, requestId) so a
// re-delivered event (reconnect replay, double WS) can't double-queue it.
function enqueueGate(queue, g) {
  if (!queue.value.some((x) => x.agentId === g.agentId && x.requestId === g.requestId)) {
    queue.value = [...queue.value, g]
  }
}
function dequeueGate(queue, agent, reqId) {
  queue.value = queue.value.filter((x) => !(x.agentId === agent && x.requestId === reqId))
}

async function send(text) {
  // Mail-mode: deliver the operator's message onto the focused member's mailbox.
  // It rides the same bus + drain path as inter-agent mail, so an idle member is
  // woken and a busy one folds it mid-run — no disruption to the workflow. Its
  // reply streams back over the event feed and lands in this same console.
  const to = focusedMember.value
  chat.value = [...chat.value, { type: 'user', target: to, agentId: focusedAgentId.value, text }]
  try {
    await props.api.message(props.space.id, to, text)
  } catch (e) {
    err.value = String(e.message || e)
  }
}

function onPermission(d) {
  sock &&
    sock.send({
      type: 'respond_permission',
      agent: d.agent,
      reqId: d.reqId,
      behavior: d.behavior,
      reason: d.reason || '',
      ruleTool: d.ruleTool || '', // non-empty on "Always allow" → backend seeds a session rule
    })
  // Remove only the answered gate; the next queued one surfaces automatically.
  dequeueGate(approvals, d.agent, d.reqId)
}
function onQuestion(d) {
  sock && sock.send({ type: 'respond_question', agent: d.agent, reqId: d.reqId, answers: d.answers })
  dequeueGate(questions, d.agent, d.reqId)
}

async function memberCmd(verb, name) {
  try {
    await props.api[verb](props.space.id, name)
    await refreshSnapshots()
  } catch (e) {
    err.value = String(e.message || e)
  }
}

// Reset wipes the whole space — task ledger, all messages, and every agent's
// context — and rebuilds it under the SAME id. Destructive, so confirm first;
// then drop the now-stale local view (the live event-stream accumulation, the
// open transcript, any pending overlay) and re-pull the fresh (empty) snapshots.
async function resetSpace() {
  const ok = window.confirm(
    'Reset this swarm?\n\nThis wipes the task ledger, all messages, and every ' +
      "agent's context, then restarts the team from scratch. This cannot be undone.",
  )
  if (!ok) return
  try {
    await props.api.reset(props.space.id)
    chat.value = []
    transcript.value = []
    approvals.value = []
    questions.value = []
    selected.value = ''
    focused.value = ''
    await refreshSnapshots()
  } catch (e) {
    err.value = String(e.message || e)
  }
}

async function selectMember(name) {
  // Clicking a member both focuses the live console on it (center) and opens its
  // transcript + mailbox (right) — flat comms: any member is one click away.
  focused.value = name
  selected.value = name
  try {
    transcript.value = (await props.api.transcript(props.space.id, name)) || []
  } catch (e) {
    transcript.value = []
    err.value = String(e.message || e)
  }
}

// hydratePending re-renders overlays for gates raised before we connected (or
// during a reconnect gap): the service replays the outstanding approval/question
// events, and onEvent enqueues them (de-duped), so a blocked member is always
// answerable (RP-2 §3.3).
async function hydratePending() {
  try {
    const gates = await props.api.pending(props.space.id)
    for (const ev of gates || []) onEvent(ev)
  } catch {
    /* non-fatal — the live stream still delivers new gates */
  }
}

onMounted(async () => {
  await refreshSnapshots()
  sock = openSocket({
    token: props.token,
    spaceId: props.space.id,
    onEvent,
    onStatus: (s) => {
      wsStatus.value = s
      if (s === 'open') hydratePending() // catch gates raised before/while disconnected
    },
  })
  poll = setInterval(refreshSnapshots, 2500)
  clock = setInterval(() => (now.value = Date.now()), 1000)
})

onBeforeUnmount(() => {
  if (sock) sock.close()
  if (poll) clearInterval(poll)
  if (clock) clearInterval(clock)
})
</script>

<template>
  <div class="space">
    <header class="bar">
      <button class="ghost" @click="emit('leave')">← spaces</button>
      <span class="title">{{ space.name || space.id }}</span>
      <span class="sid">{{ space.id }}</span>
      <button
        class="ghost mode"
        @click="toggleGateMode"
        :title="gateMode === 'modal' ? 'Approvals block the screen — click for a non-blocking tray' : 'Approvals show in a side tray — click for a blocking modal'"
      >
        approvals: {{ gateMode }}
      </button>
      <button class="danger ghost" @click="memberCmd('halt', undefined)">halt all</button>
      <button class="danger ghost" @click="resetSpace" title="Wipe ledger + all agent context and restart the team (same id)">reset</button>
    </header>

    <p v-if="err" class="err">{{ err }}</p>

    <AttentionBar :items="attention" @focus="selectMember" />

    <div class="grid">
      <aside class="left">
        <Roster
          :members="roster"
          :selected="selected"
          :now="now"
          @select="selectMember"
          @freeze="(n) => memberCmd('freeze', n)"
          @unfreeze="(n) => memberCmd('unfreeze', n)"
          @suspend="(n) => memberCmd('suspend', n)"
          @resume="(n) => memberCmd('resume', n)"
          @add="(n) => memberCmd('addMember', n)"
        />
      </aside>

      <main class="center">
        <section class="board-wrap">
          <TeamBoard :tasks="tasks" />
        </section>
        <section class="chat-wrap">
          <MemberConsole
            :member="focusedMember"
            :role="focusedEntry.role || ''"
            :current-task="focusedEntry.currentTask || 0"
            :turns="focusedTurns"
            :status="wsStatus"
            @send="send"
          />
        </section>
      </main>

      <aside class="right" v-if="selected">
        <AgentTranscript
          :agent="selected"
          :transcript="transcript"
          :messages="selectedMail"
          @close="selected = ''"
        />
      </aside>
    </div>

    <ApprovalOverlay
      v-if="gateMode === 'modal'"
      :approval="approval"
      :question="question"
      :pending-count="approvals.length + questions.length"
      @permission="onPermission"
      @question="onQuestion"
    />
    <ApprovalTray
      v-else
      :approvals="approvals"
      :questions="questions"
      @permission="onPermission"
      @question="onQuestion"
    />
  </div>
</template>

<style scoped>
.space {
  height: 100vh;
  display: flex;
  flex-direction: column;
  padding: 0.6rem 0.8rem;
  box-sizing: border-box;
}
.bar {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding-bottom: 0.5rem;
}
.title {
  font-weight: 600;
}
.sid {
  font-family: var(--mono);
  font-size: 0.7rem;
  color: var(--dim);
}
.mode {
  margin-left: auto;
  font-size: 0.72rem;
  font-family: var(--mono);
}
.err {
  color: var(--danger);
  margin: 0 0 0.4rem;
  font-size: 0.85rem;
}
.grid {
  flex: 1;
  display: grid;
  grid-template-columns: 16rem 1fr;
  gap: 0.7rem;
  min-height: 0;
}
.grid:has(.right) {
  grid-template-columns: 16rem 1fr 22rem;
}
.left,
.right {
  min-height: 0;
  overflow: hidden;
}
.center {
  display: grid;
  grid-template-rows: 40% 60%;
  gap: 0.7rem;
  min-height: 0;
}
.board-wrap,
.chat-wrap {
  min-height: 0;
}
</style>

