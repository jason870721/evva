<script setup>
import { computed } from 'vue'
import { agentColor } from '../colors.js'
import { relTime, mailState } from '../events.js'

const props = defineProps({
  agent: { type: String, default: '' },
  transcript: { type: Array, default: () => [] }, // [{role, text}]
  messages: { type: Array, default: () => [] }, // mailbox: [{sender,recipient,subject,body,readAt,claimedAt,createdAt}]
  now: { type: Number, default: 0 },
})
const emit = defineEmits(['close'])

// Each letter tagged with its unread→reading→read state (see events.mailState).
const mail = computed(() => props.messages.map((m) => ({ ...m, state: mailState(m) })))
</script>

<template>
  <div class="panel">
    <div class="phead">
      <span>{{ agent }}</span>
      <button class="ghost" @click="emit('close')">close</button>
    </div>

    <div class="section">history · transcript</div>
    <div class="transcript">
      <div v-for="(m, i) in transcript" :key="i" :class="['msg', m.role]">
        <span class="role">{{ m.role }}</span>
        <pre class="text">{{ m.text }}</pre>
      </div>
      <div v-if="!transcript.length" class="empty">no turns yet</div>
    </div>

    <div class="section">mailbox</div>
    <div class="mail">
      <div
        v-for="(m, i) in mail"
        :key="i"
        class="letter"
        :class="m.state"
        :style="{ borderLeftColor: agentColor(m.sender) }"
      >
        <div class="lhead">
          <span class="route">
            <span class="who" :style="{ color: agentColor(m.sender) }">
              <span class="dot" :style="{ background: agentColor(m.sender) }"></span>{{ m.sender }}
            </span>
            <span class="arrow">→</span>
            <span class="who" :style="{ color: agentColor(m.recipient) }">
              <span class="dot" :style="{ background: agentColor(m.recipient) }"></span>{{ m.recipient }}
            </span>
          </span>
          <span v-if="m.state !== 'read'" class="badge" :class="m.state">{{ m.state === 'reading' ? 'reading…' : 'unread' }}</span>
          <span class="time">{{ relTime(m.createdAt, now) }}</span>
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
  font-size: var(--fs-xs);
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
  border-left-width: 3px; /* coloured by sender (inline style) */
  border-radius: 6px;
  padding: 0.4rem 0.5rem;
  background: var(--panel);
}
.letter.unread {
  border-color: var(--accent); /* inline border-left-color keeps the sender stripe */
}
/* "reading" — folded into the recipient's in-flight run now. Sky blue, matching
   the roster's "thinking" pill; the inline left-color keeps the sender stripe. */
.letter.reading {
  border-color: #38bdf8;
}
.lhead {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.4rem;
  font-family: var(--mono);
  font-size: 0.7rem;
  color: var(--dim);
}
.route {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  min-width: 0;
  flex-wrap: wrap;
}
.who {
  display: inline-flex;
  align-items: center;
  gap: 0.28rem;
  font-weight: 600;
}
.dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  flex: none;
}
.arrow {
  color: var(--dim);
}
.badge {
  color: var(--accent);
}
.badge.reading {
  color: #38bdf8;
}
.time {
  margin-left: auto;
  font-family: var(--mono);
  font-size: var(--fs-xs);
  color: var(--dim);
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
