<script setup lang="ts">
import { computed, ref, watch, nextTick, onMounted } from 'vue'
import { useMailStore } from '@/stores/mail'
import { useSpaceStore } from '@/stores/space'
import { relTime, mailState } from '@/lib/events'
import { agentColor } from '@/lib/colors'

const props = defineProps<{ member: string }>()
const mail = useMailStore()
const space = useSpaceStore()
// Chronological (newest at the bottom, chat-log order) — same convention as
// the timeline and the streams.
const items = computed(() =>
  mail.messages.filter((m) => m.recipient === props.member || m.sender === props.member || m.recipient === 'all'),
)

// Follow-tail against the nearest scrollable ancestor (the inspector pane owns
// the scroll, not this list): pinned to the latest on entry and on new mail,
// but don't yank the user back while they're reading history.
const list = ref<HTMLElement | null>(null)
function scroller(): HTMLElement | null {
  let el: HTMLElement | null = list.value?.parentElement ?? null
  while (el) {
    if (/(auto|scroll)/.test(getComputedStyle(el).overflowY)) return el
    el = el.parentElement
  }
  return null
}
function atBottom(): boolean {
  const el = scroller()
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < 40
}
function scrollToEnd() {
  const el = scroller()
  if (el) el.scrollTop = el.scrollHeight
}
onMounted(() => {
  void nextTick(scrollToEnd)
})
watch(
  () => [items.value.length, props.member] as const,
  async ([, member], [, prevMember]) => {
    const stick = member !== prevMember || atBottom()
    await nextTick()
    if (stick) scrollToEnd()
  },
)
</script>

<template>
  <ul ref="list" class="mbox">
    <li v-for="m in items" :key="m.id" :class="mailState(m)">
      <div class="route">
        <span class="dot" :style="{ background: agentColor(m.sender) }" />{{ m.sender }}
        <span class="arrow">→</span>
        <span class="dot" :style="{ background: agentColor(m.recipient) }" />{{ m.recipient }}
        <span class="t">{{ relTime(m.createdAt, space.now) }}</span>
        <span class="st" :class="mailState(m)">{{ mailState(m) }}</span>
      </div>
      <div class="body">{{ m.subject ? m.subject + ' — ' : '' }}{{ m.body }}</div>
    </li>
    <li v-if="!items.length" class="dim">no messages</li>
  </ul>
</template>

<style scoped>
.mbox {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: var(--sp-2);
}
.mbox li {
  border: 1px solid var(--color-line);
  border-radius: var(--r-sm);
  padding: 0.35rem 0.5rem;
}
.route {
  display: flex;
  align-items: center;
  gap: 0.3rem;
  font-size: var(--fs-xs);
  font-family: var(--font-mono);
  color: var(--color-text-muted);
}
.dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
  display: inline-block;
}
.arrow {
  color: var(--color-text-faint);
}
.t {
  margin-left: auto;
  color: var(--color-text-faint);
}
.st.reading {
  color: var(--phase-thinking);
}
.st.read {
  color: var(--status-completed);
}
.body {
  margin-top: 0.2rem;
  font-size: var(--fs-sm);
  white-space: pre-wrap;
  word-break: break-word;
}
.dim {
  color: var(--color-text-muted);
  border: none !important;
}
.capped {
  border: none !important;
  text-align: center;
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
}
</style>
