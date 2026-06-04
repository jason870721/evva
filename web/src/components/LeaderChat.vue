<script setup>
import { ref, nextTick, watch } from 'vue'

const props = defineProps({
  turns: { type: Array, default: () => [] },
  leader: { type: String, default: '' },
  status: { type: String, default: '' }, // ws status
})
const emit = defineEmits(['send'])

const draft = ref('')
const scroller = ref(null)

function send() {
  const p = draft.value.trim()
  if (p) {
    emit('send', p)
    draft.value = ''
  }
}

watch(
  () => props.turns.length,
  async () => {
    await nextTick()
    if (scroller.value) scroller.value.scrollTop = scroller.value.scrollHeight
  },
)
</script>

<template>
  <div class="chat">
    <div class="chead">
      <span>Leader Chat</span>
      <span class="who">{{ leader }}</span>
      <span :class="['ws', status]">{{ status }}</span>
    </div>

    <div ref="scroller" class="stream">
      <div v-for="(t, i) in turns" :key="i" :class="['turn', t.type]">
        <div class="meta">
          <span class="agent">{{ t.agentId }}</span>
          <span class="tag">{{ t.type }}</span>
        </div>
        <div v-if="t.type === 'tool'" class="tool">
          <code>{{ t.tool }}</code>
          <span :class="['st', t.status]">{{ t.status }}</span>
          <pre v-if="t.result" class="result">{{ t.result }}</pre>
        </div>
        <pre v-else class="body" :class="{ think: t.type === 'thinking' }">{{ t.text }}</pre>
      </div>
      <div v-if="!turns.length" class="empty">Send a prompt to the leader to begin.</div>
    </div>

    <div class="input">
      <textarea
        v-model="draft"
        rows="2"
        placeholder="Message the leader…  (Enter to send, Shift+Enter for newline)"
        @keydown.enter.exact.prevent="send"
      ></textarea>
      <button class="primary" @click="send">Send</button>
    </div>
  </div>
</template>

<style scoped>
.chat {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.chead {
  display: flex;
  align-items: baseline;
  gap: 0.6rem;
  font-weight: 600;
  font-size: 0.85rem;
  padding-bottom: 0.5rem;
}
.who {
  font-family: var(--mono);
  color: var(--dim);
  font-size: 0.75rem;
}
.ws {
  margin-left: auto;
  font-size: 0.68rem;
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
  font-size: 0.62rem;
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
.turn.error .body {
  color: var(--danger);
}
.tool {
  margin-top: 0.15rem;
  font-size: 0.8rem;
}
.tool .st {
  margin-left: 0.4rem;
  font-size: 0.66rem;
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
