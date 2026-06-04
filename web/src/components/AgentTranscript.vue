<script setup>
defineProps({
  agent: { type: String, default: '' },
  transcript: { type: Array, default: () => [] }, // [{role, text}]
  messages: { type: Array, default: () => [] }, // mailbox: [{sender,recipient,subject,body,readAt}]
})
const emit = defineEmits(['close'])
</script>

<template>
  <div class="panel">
    <div class="phead">
      <span>{{ agent }}</span>
      <button class="ghost" @click="emit('close')">close</button>
    </div>

    <div class="section">transcript</div>
    <div class="transcript">
      <div v-for="(m, i) in transcript" :key="i" :class="['msg', m.role]">
        <span class="role">{{ m.role }}</span>
        <pre class="text">{{ m.text }}</pre>
      </div>
      <div v-if="!transcript.length" class="empty">no turns yet</div>
    </div>

    <div class="section">mailbox</div>
    <div class="mail">
      <div v-for="(m, i) in messages" :key="i" class="letter" :class="{ unread: !m.readAt }">
        <div class="lhead">
          <span>{{ m.sender }} → {{ m.recipient }}</span>
          <span v-if="!m.readAt" class="badge">unread</span>
        </div>
        <div v-if="m.subject" class="subj">{{ m.subject }}</div>
        <pre class="lbody">{{ m.body }}</pre>
      </div>
      <div v-if="!messages.length" class="empty">no messages</div>
    </div>
  </div>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  height: 100%;
  overflow: auto;
}
.phead {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
  font-family: var(--mono);
  font-size: 0.85rem;
  padding-bottom: 0.5rem;
}
.section {
  font-size: 0.65rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  color: var(--dim);
  margin: 0.6rem 0 0.3rem;
}
.transcript,
.mail {
  display: grid;
  gap: 0.4rem;
}
.msg {
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.4rem 0.5rem;
  background: var(--panel);
}
.role {
  font-size: 0.6rem;
  text-transform: uppercase;
  color: var(--dim);
}
.text,
.lbody {
  white-space: pre-wrap;
  margin: 0.2rem 0 0;
  font-family: inherit;
  font-size: 0.8rem;
}
.letter {
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.4rem 0.5rem;
  background: var(--panel);
}
.letter.unread {
  border-color: var(--accent);
}
.lhead {
  display: flex;
  justify-content: space-between;
  font-family: var(--mono);
  font-size: 0.7rem;
  color: var(--dim);
}
.badge {
  color: var(--accent);
}
.subj {
  font-weight: 600;
  font-size: 0.78rem;
  margin-top: 0.2rem;
}
.empty {
  color: var(--dim);
  font-size: 0.78rem;
}
</style>
