<script setup>
// Timeline is the space-wide Team Activity feed (RP-4 UX-2): every message across
// the swarm, time-ordered, coloured by sender→recipient. It is the cross-member
// view the per-member console can't give — "see the whole team talking" — and it
// subsumes RP-1 §3.6 (inter-agent messages were previously only visible in a
// member's mailbox). Messages are the durable, timestamped, cross-member signal,
// so the feed is built from them.
import { ref, computed, watch, nextTick } from 'vue'
import { agentColor } from '../colors.js'
import { relTime, mailState } from '../events.js'

const props = defineProps({
  messages: { type: Array, default: () => [] }, // [{id,sender,recipient,subject,body,refTask,readAt,claimedAt,createdAt}]
  now: { type: Number, default: 0 },
})

// Tag each message with its unread→reading→read state once (avoids re-deriving
// it in three template bindings per row).
const rows = computed(() => props.messages.map((m) => ({ ...m, state: mailState(m) })))

const scroller = ref(null)
// Keep the newest activity in view (the REST list is oldest-first).
watch(
  () => props.messages.length,
  async () => {
    await nextTick()
    if (scroller.value) scroller.value.scrollTop = scroller.value.scrollHeight
  },
)
</script>

<template>
  <div class="timeline">
    <div class="thead">
      Team activity <span class="dim">— every message across the swarm</span>
    </div>
    <div ref="scroller" class="feed">
      <div v-for="m in rows" :key="m.id" class="evt" :class="m.state">
        <div class="line1">
          <span class="route">
            <span class="dot" :style="{ background: agentColor(m.sender) }"></span>
            <span class="who" :style="{ color: agentColor(m.sender) }">{{ m.sender }}</span>
            <span class="arr">→</span>
            <span class="who" :style="{ color: agentColor(m.recipient) }">{{ m.recipient }}</span>
          </span>
          <span v-if="m.refTask" class="ref">task #{{ m.refTask }}</span>
          <span v-if="m.state !== 'read'" class="badge" :class="m.state">{{ m.state === 'reading' ? 'reading…' : 'unread' }}</span>
          <span class="time">{{ relTime(m.createdAt, now) }}</span>
        </div>
        <div v-if="m.subject" class="subj">{{ m.subject }}</div>
        <div class="body">{{ m.body }}</div>
      </div>
      <div v-if="!messages.length" class="empty">
        No team activity yet. Messages between members — and your own — show up here.
      </div>
    </div>
  </div>
</template>

<style scoped>
.timeline {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.thead {
  font-size: 0.8rem;
  font-weight: 600;
  padding: 0 0.2rem 0.5rem;
}
.thead .dim {
  color: var(--dim);
  font-weight: 400;
}
.feed {
  flex: 1;
  overflow: auto;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  padding: 0.6rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.evt {
  border: 1px solid var(--line);
  border-left-width: 3px;
  border-radius: 6px;
  padding: 0.4rem 0.55rem;
  background: var(--bg);
}
.evt.unread {
  border-color: var(--accent);
}
/* "reading" — the recipient has this message folded into a run right now. Sky
   blue, the same language as the roster's "thinking" pill. */
.evt.reading {
  border-color: #38bdf8;
}
.line1 {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.74rem;
}
.route {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-weight: 600;
}
.dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  flex: none;
}
.arr {
  color: var(--dim);
  font-weight: 400;
}
.ref {
  font-family: var(--mono);
  font-size: var(--fs-xs);
  color: var(--dim);
}
.badge {
  font-size: var(--fs-xs);
  color: var(--accent);
  border: 1px solid var(--accent);
  border-radius: 8px;
  padding: 0 0.3rem;
}
.badge.reading {
  color: #38bdf8;
  border-color: #38bdf8;
}
.time {
  margin-left: auto;
  font-family: var(--mono);
  font-size: var(--fs-xs);
  color: var(--dim);
}
.subj {
  font-weight: 600;
  font-size: 0.8rem;
  margin-top: 0.25rem;
}
.body {
  white-space: pre-wrap;
  font-size: 0.82rem;
  line-height: 1.4;
  margin-top: 0.15rem;
}
.empty {
  color: var(--dim);
  font-size: 0.82rem;
}
</style>
