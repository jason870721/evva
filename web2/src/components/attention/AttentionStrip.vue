<script setup lang="ts">
// The always-visible "what needs me?" strip (FE-5 / RP-4 UX-1). Aggregates the
// members the operator should act on: 'act' (blocked on approval/question), or
// 'warn' (errored / paused / stalled). Quiet "all clear" otherwise. Updates the
// document title so a backgrounded tab still shows the count; `!` jumps to the
// first item. Clicking a chip focuses that member (FE-6 will route 'act' chips
// straight to the gate).
import { onMounted, onBeforeUnmount, watch } from 'vue'
import type { AttentionItem } from '@/lib/events'

const props = defineProps<{ items: AttentionItem[] }>()
const emit = defineEmits<{ focus: [name: string] }>()

function onKey(e: KeyboardEvent) {
  if (e.key === '!' && props.items.length) emit('focus', props.items[0].name)
}
onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onKey)
  document.title = 'evva · swarm'
})
watch(
  () => props.items.length,
  (n) => {
    document.title = n ? `(${n}) evva · swarm` : 'evva · swarm'
  },
  { immediate: true },
)

function glyph(it: AttentionItem): string {
  if (it.kind === 'act') return '⏳'
  return it.stalled ? '◷' : '⚠'
}
</script>

<template>
  <div class="attn" :class="{ quiet: !items.length }" role="status">
    <template v-if="items.length">
      <span class="lead">{{ items.length }} need{{ items.length === 1 ? 's' : '' }} you</span>
      <button
        v-for="it in items"
        :key="it.name + it.phase"
        class="chip"
        :class="[it.kind, { stall: it.stalled }]"
        :title="`focus ${it.name}`"
        @click="emit('focus', it.name)"
      >
        <span class="g" aria-hidden="true">{{ glyph(it) }}</span>
        <span class="who">{{ it.name }}</span>
        <span class="what">{{ it.phase }}<template v-if="it.tool">:{{ it.tool }}</template><template v-if="it.stalled"> · stalled</template></span>
        <span v-if="it.elapsed" class="since">{{ it.elapsed }}</span>
      </button>
    </template>
    <span v-else class="clear">✓ all clear</span>
  </div>
</template>

<style scoped>
.attn {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  flex-wrap: wrap;
  padding: var(--sp-2) var(--sp-3);
  border-bottom: 1px solid var(--color-line);
  background: var(--color-surface-2);
  min-height: 2.1rem;
}
.attn.quiet {
  opacity: 0.7;
}
.lead {
  font-size: var(--fs-xs);
  font-weight: 600;
  color: var(--phase-waiting);
}
.clear {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: var(--fs-xs);
  padding: 0.12rem 0.5rem;
  border-radius: var(--r-pill);
  border: 1px solid var(--color-line);
  background: var(--color-bg);
  color: var(--color-text);
  cursor: pointer;
  font-family: var(--font-mono);
}
.chip.act {
  border-color: var(--phase-waiting);
  color: var(--phase-waiting);
}
.chip.warn {
  border-color: var(--color-danger);
  color: var(--color-danger);
}
.chip.warn.stall {
  border-color: var(--status-suspended);
  color: var(--status-suspended);
}
.chip .who {
  font-weight: 600;
}
.chip .what,
.chip .since {
  color: var(--color-text-muted);
}
</style>
