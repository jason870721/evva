<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { openSocket } from '../ws.js'
import { reduceChat, reducePhase, consoleTurns, isApproval, isQuestion, approvalOf, questionOf, touchesLedger, attentionItems } from '../events.js'
import MemberConsole from './MemberConsole.vue'
import TeamBoard from './TeamBoard.vue'
import CompletedTasks from './CompletedTasks.vue'
import Timeline from './Timeline.vue'
import Roster from './Roster.vue'
import AgentTranscript from './AgentTranscript.vue'
import ApprovalOverlay from './ApprovalOverlay.vue'
import ApprovalTray from './ApprovalTray.vue'
import AttentionBar from './AttentionBar.vue'
import ConfirmDialog from './ConfirmDialog.vue'
import NewAgentForm from './NewAgentForm.vue'
import SkillsPanel from './SkillsPanel.vue'

const props = defineProps({
  api: { type: Object, required: true },
  token: { type: String, required: true },
  space: { type: Object, required: true },
})
const emit = defineEmits(['leave'])

const roster = ref([])
const tasks = ref([]) // board snapshot: active + newest-few completed
const completedTotal = ref(0) // full completed count (RP-6) — board shows it, tab pages it
const messages = ref([])
const chat = ref([])
// Live per-agent sub-phase derived from the WS event stream (AgentID → {phase,
// tool, since}). Overlaid onto the 2.5s-polled roster so the status pill —
// notably the sky-blue "thinking" — tracks the agent loop in real time instead
// of lagging a poll behind.
const livePhases = ref({})
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
const centerTab = ref('board') // 'board' | 'completed' | 'timeline' | 'console' (RP-4 UX-2, RP-6)
const transcript = ref([])
const err = ref('')
const now = ref(Date.now()) // ticks every 1s so elapsed clocks stay live
// Custom confirmation (RP-4 UX-3): { title, message, confirmLabel, danger, action }
// or null. Replaces native window.confirm for destructive ops.
const confirmDialog = ref(null)
function askConfirm(opts) {
  confirmDialog.value = opts
}
function onConfirmYes(checked) {
  const action = confirmDialog.value && confirmDialog.value.action
  confirmDialog.value = null
  if (action) action(checked)
}

// Add-agent form state (RP-8): the dialog + the tool catalog it offers.
const showForm = ref(false)
const toolCatalog = ref([])
async function openForm() {
  try {
    toolCatalog.value = (await props.api.tools(props.space.id)) || []
  } catch (e) {
    toolCatalog.value = []
    err.value = String(e.message || e)
  }
  showForm.value = true
}
async function createMember(spec) {
  showForm.value = false
  try {
    await props.api.createMember(props.space.id, spec)
    await refreshSnapshots()
  } catch (e) {
    err.value = String(e.message || e)
  }
}
function removeMember(name) {
  askConfirm({
    title: `Remove ${name}?`,
    message: `${name} leaves the team: it stops running and the leader is asked to reassign its tasks. History is kept.`,
    confirmLabel: 'Remove',
    danger: true,
    checkboxLabel: 'Also delete its on-disk definition (cannot be re-added without recreating)',
    action: async (deleteDir) => {
      try {
        await props.api.removeMember(props.space.id, name, !!deleteDir)
        await refreshSnapshots()
      } catch (e) {
        err.value = String(e.message || e)
      }
    },
  })
}
async function setSchedule({ name, cron, prompt }) {
  try {
    await props.api.setSchedule(props.space.id, name, { cron, prompt })
    await refreshSnapshots()
  } catch (e) {
    err.value = String(e.message || e)
  }
}
async function clearSchedule(name) {
  try {
    await props.api.clearSchedule(props.space.id, name)
    await refreshSnapshots()
  } catch (e) {
    err.value = String(e.message || e)
  }
}

// Agent skills dialog (RP-10): view/add/delete one member's skills. skillsFor is the
// member whose dialog is open ('' = closed); skillList is its current catalog. An
// add/delete reloads that member's prompt server-side, then we refetch the list.
const skillsFor = ref('')
const skillList = ref([])
async function openSkills(name) {
  try {
    skillList.value = (await props.api.memberSkills(props.space.id, name)) || []
    skillsFor.value = name
  } catch (e) {
    err.value = String(e.message || e)
  }
}
async function refetchSkills() {
  if (!skillsFor.value) return
  try {
    skillList.value = (await props.api.memberSkills(props.space.id, skillsFor.value)) || []
  } catch (e) {
    err.value = String(e.message || e)
  }
}
async function addSkill(spec) {
  try {
    await props.api.addSkill(props.space.id, skillsFor.value, spec)
    await refetchSkills()
  } catch (e) {
    err.value = String(e.message || e)
  }
}
function confirmDeleteSkill(skillName) {
  const member = skillsFor.value
  askConfirm({
    title: `Delete skill "${skillName}"?`,
    message: `${member} will no longer see or be able to load "${skillName}". Its prompt reloads on the next run.`,
    confirmLabel: 'Delete',
    danger: true,
    action: async () => {
      try {
        await props.api.deleteSkill(props.space.id, member, skillName)
        await refetchSkills()
      } catch (e) {
        err.value = String(e.message || e)
      }
    },
  })
}

let sock = null
let poll = null
let clock = null

// The polled roster with each member's live event-derived phase overlaid (by
// agentId). The poll stays the source of truth for structure (membership, role,
// task, coarse run); the WS stream supplies the fresh sub-phase + tool + clock.
const mergedRoster = computed(() =>
  roster.value.map((m) => {
    const lp = livePhases.value[m.agentId]
    return lp ? { ...m, phase: lp.phase, tool: lp.tool, phaseSince: lp.since } : m
  }),
)

// What needs the operator: blocked-on-approval/question or errored/paused
// members, most-urgent first, with live elapsed times (RP-4 UX-1). Built off the
// merged roster so a waiting-approval lights up the instant the event arrives.
const attention = computed(() => attentionItems(mergedRoster.value, now.value))

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
    // /api/tasks now returns the board snapshot { tasks, total } (RP-6): active
    // tasks + the newest-few completed, plus the full completed count.
    tasks.value = (t && t.tasks) || []
    completedTotal.value = (t && t.total) || 0
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
    // A reply that failed to route left the member still blocked — re-sync the
    // pending gates so it reappears and can be retried (RP-2 §3.3 / RP-4 UX-3).
    hydratePending()
    return
  }
  // Every real event may move a member's sub-phase — including the gate events
  // (→ waiting-approval / waiting-input). Fold it into the live phase map before
  // the gate early-returns below so the roster pill reflects it immediately.
  livePhases.value = reducePhase(livePhases.value, ev)
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

// Halt the whole team — a destructive control, so it goes through the confirm
// dialog (the old one-click no-confirm was a misclick waiting to happen).
function haltAll() {
  askConfirm({
    title: 'Halt the entire team?',
    message:
      'This suspends every member and cancels all in-flight runs. Members come back individually via resume.',
    confirmLabel: 'Halt all',
    danger: true,
    action: () => memberCmd('halt', undefined),
  })
}

// Reset wipes the whole space — task ledger, all messages, and every agent's
// context — and rebuilds it under the SAME id. Destructive, so confirm first.
function resetSpace() {
  askConfirm({
    title: 'Reset this swarm?',
    message:
      "This wipes the task ledger, all messages, and every agent's context, then restarts the team from scratch. This cannot be undone.",
    confirmLabel: 'Reset',
    danger: true,
    action: doReset,
  })
}
// doReset runs after confirmation: drop the now-stale local view (live stream,
// open transcript, pending gates) and re-pull the fresh (empty) snapshots.
async function doReset() {
  try {
    await props.api.reset(props.space.id)
    chat.value = []
    livePhases.value = {}
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

// hydrateConsole rebuilds the console from each member's persisted transcript so
// a stop→run (or a plain page reload / reconnect after the live stream was lost)
// doesn't show an empty console — the conversation that already happened is back.
// Best-effort: the transcript carries role+text only, so tool-call cards from the
// old run aren't reconstructed (the live stream renders those going forward); we
// seed the agents' assistant turns, which is the history worth keeping.
async function hydrateConsole() {
  try {
    const seeded = []
    for (const m of roster.value) {
      if (!m.agentId) continue
      const tr = await props.api.transcript(props.space.id, m.name)
      for (const e of tr || []) {
        if (e.role === 'assistant' && e.text) {
          seeded.push({ type: 'assistant', agentId: m.agentId, text: e.text, open: false })
        }
      }
    }
    // Only replace an empty console — never clobber turns the live stream already
    // delivered while we were fetching.
    if (seeded.length && !chat.value.length) chat.value = seeded
  } catch {
    /* non-fatal — the live stream still populates the console from here on */
  }
}

onMounted(async () => {
  await refreshSnapshots()
  await hydrateConsole()
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
      <button class="danger ghost" @click="haltAll">halt all</button>
      <button class="danger ghost" @click="resetSpace" title="Wipe ledger + all agent context and restart the team (same id)">reset</button>
    </header>

    <div v-if="wsStatus !== 'open'" class="wsbanner" role="status">
      ⚠ live connection {{ wsStatus }} — reconnecting… (the view falls back to a 2.5s refresh)
    </div>
    <p v-if="err" class="err">{{ err }}</p>

    <AttentionBar :items="attention" @focus="selectMember" />

    <div class="grid">
      <aside class="left">
        <Roster
          :members="mergedRoster"
          :selected="selected"
          :now="now"
          @select="selectMember"
          @freeze="(n) => memberCmd('freeze', n)"
          @unfreeze="(n) => memberCmd('unfreeze', n)"
          @suspend="(n) => memberCmd('suspend', n)"
          @resume="(n) => memberCmd('resume', n)"
          @open-form="openForm"
          @remove="removeMember"
          @set-schedule="setSchedule"
          @clear-schedule="clearSchedule"
          @open-skills="openSkills"
        />
      </aside>

      <main class="center">
        <nav class="tabs">
          <button :class="{ active: centerTab === 'board' }" @click="centerTab = 'board'">Board</button>
          <button :class="{ active: centerTab === 'completed' }" @click="centerTab = 'completed'">
            Completed<span v-if="completedTotal" class="who-tab"> · {{ completedTotal }}</span>
          </button>
          <button :class="{ active: centerTab === 'timeline' }" @click="centerTab = 'timeline'">Timeline</button>
          <button :class="{ active: centerTab === 'console' }" @click="centerTab = 'console'">
            Console<span v-if="focusedMember" class="who-tab"> · {{ focusedMember }}</span>
          </button>
        </nav>
        <section class="tabbody">
          <TeamBoard
            v-show="centerTab === 'board'"
            :tasks="tasks"
            :now="now"
            :completed-total="completedTotal"
            @view-all="centerTab = 'completed'"
          />
          <CompletedTasks
            v-show="centerTab === 'completed'"
            :api="api"
            :space-id="space.id"
            :visible="centerTab === 'completed'"
            :now="now"
          />
          <Timeline v-show="centerTab === 'timeline'" :messages="messages" :now="now" />
          <MemberConsole
            v-show="centerTab === 'console'"
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
          :now="now"
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

    <ConfirmDialog
      v-if="confirmDialog"
      :title="confirmDialog.title"
      :message="confirmDialog.message"
      :confirm-label="confirmDialog.confirmLabel"
      :danger="confirmDialog.danger"
      :checkbox-label="confirmDialog.checkboxLabel || ''"
      @confirm="onConfirmYes"
      @cancel="confirmDialog = null"
    />

    <NewAgentForm
      v-if="showForm"
      :tools="toolCatalog"
      @create="createMember"
      @cancel="showForm = false"
    />

    <SkillsPanel
      v-if="skillsFor"
      :member="skillsFor"
      :skills="skillList"
      @add="addSkill"
      @delete="confirmDeleteSkill"
      @close="skillsFor = ''"
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
  font-size: var(--fs-sm);
}
.wsbanner {
  background: #f59e0b22;
  border: 1px solid #f59e0b88;
  color: #fbbf24;
  border-radius: 6px;
  padding: 0.3rem 0.6rem;
  margin-bottom: 0.5rem;
  font-size: var(--fs-sm);
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
/* Narrow screens: drop to a single column and hide the detail rail; the center
   tabs still give Board / Timeline / Console. (RP-4 UX-4 RWD.) */
@media (max-width: 860px) {
  .grid,
  .grid:has(.right) {
    grid-template-columns: 1fr;
  }
  .right {
    display: none;
  }
}
.left,
.right {
  min-height: 0;
  overflow: hidden;
}
.center {
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.tabs {
  display: flex;
  gap: 0.3rem;
  margin-bottom: 0.5rem;
}
.tabs button {
  font-size: 0.78rem;
  padding: 0.25rem 0.7rem;
  background: transparent;
  border: 1px solid var(--line);
  border-radius: 6px;
  color: var(--dim);
}
.tabs button.active {
  color: #e6edf3;
  border-color: var(--accent);
  background: var(--panel);
}
.tabs .who-tab {
  color: var(--dim);
  font-family: var(--mono);
  font-size: 0.7rem;
}
.tabbody {
  flex: 1;
  min-height: 0;
}
.tabbody > * {
  height: 100%;
}
</style>

