<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { openSocket } from '../ws.js'
import { reduceChat, isApproval, isQuestion, approvalOf, questionOf, touchesLedger } from '../events.js'
import LeaderChat from './LeaderChat.vue'
import TeamBoard from './TeamBoard.vue'
import Roster from './Roster.vue'
import AgentTranscript from './AgentTranscript.vue'
import ApprovalOverlay from './ApprovalOverlay.vue'

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
const approval = ref(null)
const question = ref(null)
const selected = ref('')
const transcript = ref([])
const err = ref('')

let sock = null
let poll = null

const leader = computed(() => {
  const m = roster.value.find((x) => x.role === 'leader')
  return m ? m.name : roster.value[0] ? roster.value[0].name : ''
})
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
  if (isApproval(ev)) {
    approval.value = approvalOf(ev)
    return
  }
  if (isQuestion(ev)) {
    question.value = questionOf(ev)
    return
  }
  chat.value = [...reduceChat(chat.value, ev)]
  if (touchesLedger(ev)) refreshSnapshots()
}

function send(prompt) {
  // Interactive turn over the socket so its events stream straight back.
  sock && sock.send({ type: 'run', agent: leader.value, prompt })
  chat.value = [...chat.value, { type: 'user', agentId: 'you', text: prompt }]
}

function onPermission(d) {
  sock && sock.send({ type: 'respond_permission', agent: d.agent, reqId: d.reqId, behavior: d.behavior, reason: d.reason || '' })
  approval.value = null
}
function onQuestion(d) {
  sock && sock.send({ type: 'respond_question', agent: d.agent, reqId: d.reqId, answers: d.answers })
  question.value = null
}

async function memberCmd(verb, name) {
  try {
    await props.api[verb](props.space.id, name)
    await refreshSnapshots()
  } catch (e) {
    err.value = String(e.message || e)
  }
}

async function selectMember(name) {
  selected.value = name
  try {
    transcript.value = (await props.api.transcript(props.space.id, name)) || []
  } catch (e) {
    transcript.value = []
    err.value = String(e.message || e)
  }
}

onMounted(async () => {
  await refreshSnapshots()
  sock = openSocket({
    token: props.token,
    spaceId: props.space.id,
    onEvent,
    onStatus: (s) => (wsStatus.value = s),
  })
  poll = setInterval(refreshSnapshots, 2500)
})

onBeforeUnmount(() => {
  if (sock) sock.close()
  if (poll) clearInterval(poll)
})
</script>

<template>
  <div class="space">
    <header class="bar">
      <button class="ghost" @click="emit('leave')">← spaces</button>
      <span class="title">{{ space.name || space.id }}</span>
      <span class="sid">{{ space.id }}</span>
      <button class="danger ghost halt" @click="memberCmd('halt', undefined)">halt all</button>
    </header>

    <p v-if="err" class="err">{{ err }}</p>

    <div class="grid">
      <aside class="left">
        <Roster
          :members="roster"
          :selected="selected"
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
          <LeaderChat :turns="chat" :leader="leader" :status="wsStatus" @send="send" />
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
      :approval="approval"
      :question="question"
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
.halt {
  margin-left: auto;
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

