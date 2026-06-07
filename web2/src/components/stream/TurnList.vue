<script setup lang="ts">
import { ref, watch, nextTick, computed, onMounted } from 'vue'
import type { Turn } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import AssistantTurn from './turns/AssistantTurn.vue'
import ThinkingTurn from './turns/ThinkingTurn.vue'
import ErrorTurn from './turns/ErrorTurn.vue'
import ToolCard from './turns/ToolCard.vue'

// TurnList renders a turn stream with follow-tail behaviour: it auto-scrolls to
// the bottom while the user is at the bottom, but the moment they scroll up it
// stops and surfaces a "↓ N new · jump to latest" pill instead of yanking them
// back (the v1 console force-scrolled — RP-4). In firehose mode (showAgent) each
// turn is tagged with its sender's stable colour.
const props = defineProps<{
  turns: Turn[]
  showAgent?: boolean
  nameById?: Record<string, string>
}>()

const scroller = ref<HTMLElement | null>(null)
const following = ref(true)
const newCount = ref(0)

// Render cap (perf-lite, FE-8): keep the DOM bounded on very long streams. Full
// windowed virtualization is a follow-on; a tail cap covers the common case.
const CAP = 400
const visible = computed(() => (props.turns.length > CAP ? props.turns.slice(-CAP) : props.turns))

function displayName(t: Turn): string {
  if (t.type === 'user') return 'you'
  return (props.nameById && props.nameById[t.agentId]) || t.agentId
}
function colorOf(t: Turn): string {
  return agentColor(t.type === 'user' ? 'user' : displayName(t))
}

function atBottom(): boolean {
  const el = scroller.value
  if (!el) return true
  return el.scrollHeight - el.scrollTop - el.clientHeight < 40
}
function onScroll() {
  if (atBottom()) {
    following.value = true
    newCount.value = 0
  } else {
    following.value = false
  }
}
function scrollToEnd() {
  const el = scroller.value
  if (el) el.scrollTop = el.scrollHeight
}
function jump() {
  following.value = true
  newCount.value = 0
  nextTick(scrollToEnd)
}

// Pin to the latest on entry: without this, opening a stream that already has
// turns (stream tab / "open live stream") lands at the top showing the oldest.
onMounted(() => {
  void nextTick(scrollToEnd)
})

// Watch array identity, not just length: every reducer fold replaces the array,
// so streaming text growth inside a turn also keeps the tail pinned.
watch(
  () => props.turns,
  async (now, prev) => {
    if (following.value) {
      await nextTick()
      scrollToEnd()
    } else {
      const grew = now.length - (prev?.length ?? 0)
      if (grew > 0) newCount.value += grew
    }
  },
)
</script>

<template>
  <div class="turnlist">
    <div ref="scroller" class="scroll" role="log" aria-live="polite" aria-relevant="additions" @scroll="onScroll">
      <p v-if="turns.length > visible.length" class="capped">showing last {{ visible.length }} of {{ turns.length }}</p>
      <div v-for="(t, i) in visible" :key="i" class="row">
        <div v-if="showAgent" class="who" :style="{ color: colorOf(t) }">
          <span class="dot" :style="{ background: colorOf(t) }" />{{ displayName(t) }}
        </div>
        <div class="turn">
          <AssistantTurn v-if="t.type === 'assistant'" :turn="t" />
          <ThinkingTurn v-else-if="t.type === 'thinking'" :turn="t" />
          <ToolCard v-else-if="t.type === 'tool'" :turn="t" />
          <ErrorTurn v-else-if="t.type === 'error'" :turn="t" />
          <div v-else class="userturn"><span class="me">you →</span> {{ t.text }}</div>
        </div>
      </div>
      <div v-if="!turns.length" class="empty"><slot name="empty">No activity yet.</slot></div>
    </div>
    <button v-if="!following && newCount" class="jump" @click="jump">↓ {{ newCount }} new · jump to latest</button>
  </div>
</template>

<style scoped>
.turnlist {
  position: relative;
  flex: 1;
  min-height: 0;
  display: flex;
}
.scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  border: 1px solid var(--color-line);
  border-radius: var(--r-lg);
  background: var(--console-bg);
  padding: var(--sp-3);
  display: flex;
  flex-direction: column;
  gap: var(--sp-3);
}
.row {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}
.who {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: var(--fs-xs);
  font-weight: 600;
  font-family: var(--font-mono);
}
.dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
}
.userturn {
  border-left: 2px solid var(--color-accent);
  padding-left: 0.5rem;
  color: var(--color-text);
  white-space: pre-wrap;
}
.me {
  color: var(--color-accent);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}
.empty {
  color: var(--color-text-muted);
  font-size: var(--fs-sm);
}
.capped {
  text-align: center;
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
}
.jump {
  position: absolute;
  bottom: var(--sp-3);
  left: 50%;
  transform: translateX(-50%);
  background: var(--color-accent);
  color: var(--btn-primary-fg);
  border: none;
  border-radius: var(--r-pill);
  padding: 0.25rem 0.75rem;
  font-size: var(--fs-xs);
  cursor: pointer;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
}
</style>
