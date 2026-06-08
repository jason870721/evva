<script setup>
import { ref, nextTick, watch } from 'vue'
import { agentColor } from '../colors.js'

// A per-member console: the live, focused view of ONE member — its streamed
// turns and tool calls, plus an input that sends the operator a message to it
// (mail-mode flat comms). The same component serves the leader and every
// worker, so "talking to a worker" is identical to "talking to the leader".
const props = defineProps({
  member: { type: String, default: '' },
  role: { type: String, default: '' },
  currentTask: { type: Number, default: 0 },
  turns: { type: Array, default: () => [] }, // already filtered to this member
  status: { type: String, default: '' }, // ws status
})
const emit = defineEmits(['send'])

const draft = ref('')
const scroller = ref(null)

function send() {
  const t = draft.value.trim()
  if (t) {
    emit('send', t)
    draft.value = ''
  }
}

function onEnterKey(e) {
  if (e.isComposing) return
  send()
}

watch(
  () => [props.turns.length, props.member],
  async () => {
    await nextTick()
    if (scroller.value) scroller.value.scrollTop = scroller.value.scrollHeight
  },
)
</script>

<template>
  <div class="console">
    <div class="chead">
      <span class="who" :style="{ color: agentColor(member) }">
        <span class="dot" :style="{ background: agentColor(member) }"></span>{{ member || '—' }}
      </span>
      <span v-if="role" class="role" :class="role">{{ role }}</span>
      <span v-if="currentTask" class="task">task #{{ currentTask }}</span>
      <span :class="['ws', status]">{{ status }}</span>
    </div>

    <div ref="scroller" class="stream">
      <div v-for="(t, i) in turns" :key="i" :class="['turn', t.type]">
        <div class="meta">
          <span
            class="agent"
            :style="{ color: t.type === 'user' ? agentColor('user') : agentColor(member) }"
          >{{ t.type === 'user' ? 'you' : member }}</span>
          <span class="tag">{{ t.type }}</span>
        </div>
        <div v-if="t.type === 'tool'" class="tool">
          <code>{{ t.tool }}</code>
          <span :class="['st', t.status]">{{ t.status }}</span>
          <pre v-if="t.result" class="result">{{ t.result }}</pre>
        </div>
        <pre v-else class="body" :class="{ think: t.type === 'thinking', mine: t.type === 'user' }">{{ t.text }}</pre>
      </div>
      <div v-if="!turns.length" class="empty">
        No activity yet. Send {{ member || 'this member' }} a message to begin.
      </div>
    </div>

    <div class="input">
      <textarea
        v-model="draft"
        rows="2"
        :placeholder="`Message ${member || 'member'}…  (Enter to send, Shift+Enter for newline)`"
        @keydown.enter.exact.prevent="onEnterKey"
      ></textarea>
      <button class="primary" @click="send">Send</button>
    </div>
  </div>
</template>

<style scoped>
.console {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.chead {
  display: flex;
  align-items: baseline;
  gap: 0.6rem;
  font-size: 0.85rem;
  padding-bottom: 0.5rem;
}
.who {
  font-weight: 600;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.dot {
  width: 0.55rem;
  height: 0.55rem;
  border-radius: 50%;
  flex: none;
}
.agent {
  font-weight: 600;
}
.role {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  color: var(--dim);
}
.role.leader {
  color: var(--accent);
}
.task {
  font-family: var(--mono);
  font-size: 0.7rem;
  color: var(--dim);
}
.ws {
  margin-left: auto;
  font-size: var(--fs-xs);
  color: var(--dim);
}
.ws.open { color: #22c55e; }
.ws.closed, .ws.connecting { color: #f59e0b; }
.stream {
  flex: 1;
  overflow: auto;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  padding: 0.6rem;
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
}
.turn .meta {
  display: flex;
  gap: 0.5rem;
  font-size: var(--fs-xs);
  color: var(--dim);
  font-family: var(--mono);
}
.body {
  white-space: pre-wrap;
  margin: 0.15rem 0 0;
  font-family: inherit;
  font-size: 0.88rem;
  line-height: 1.45;
}
.body.think {
  color: var(--dim);
  font-style: italic;
}
.body.mine {
  color: #cdd9e5;
  border-left: 2px solid var(--accent);
  padding-left: 0.5rem;
}
.turn.error .body {
  color: var(--danger);
}
.tool {
  margin-top: 0.15rem;
  font-size: 0.8rem;
}
.tool .st {
  margin-left: 0.4rem;
  font-size: var(--fs-xs);
  color: var(--dim);
}
.tool .st.done { color: #22c55e; }
.tool .st.error { color: var(--danger); }
.result {
  white-space: pre-wrap;
  background: var(--bg);
  border: 1px solid var(--line);
  border-radius: 5px;
  padding: 0.4rem;
  margin-top: 0.3rem;
  font-size: 0.74rem;
  max-height: 12rem;
  overflow: auto;
}
.empty {
  color: var(--dim);
  font-size: 0.85rem;
}
.input {
  display: flex;
  gap: 0.5rem;
  margin-top: 0.5rem;
}
.input textarea {
  flex: 1;
  resize: vertical;
  font-family: inherit;
}
.primary {
  background: var(--accent);
  color: #fff;
  border-color: var(--accent);
}
</style>
