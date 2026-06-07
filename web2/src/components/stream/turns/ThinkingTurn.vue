<script setup lang="ts">
import { ref, watch } from 'vue'
import type { ThinkingTurn } from '@/lib/events'
import { useUiStore } from '@/stores/ui'
import EvSpinner from '@/components/base/EvSpinner.vue'

const props = defineProps<{ turn: ThinkingTurn }>()
const ui = useUiStore()
const expanded = ref(true)

// Collapse to a one-line summary once the thinking stream ends.
watch(
  () => props.turn.open,
  (open) => {
    if (!open) expanded.value = false
  },
)
</script>

<template>
  <div v-if="!ui.hideThinking" class="thinking">
    <button class="toggle" @click="expanded = !expanded">
      <EvSpinner v-if="turn.open" :size="12" />
      <span v-else aria-hidden="true">💭</span>
      <span>{{ turn.open ? 'thinking…' : 'thought' }}</span>
      <span class="chev" aria-hidden="true">{{ expanded ? '▾' : '▸' }}</span>
    </button>
    <pre v-if="expanded" class="body">{{ turn.text }}</pre>
  </div>
</template>

<style scoped>
.thinking {
  color: var(--phase-thinking);
}
.toggle {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  background: transparent;
  border: none;
  color: var(--phase-thinking);
  cursor: pointer;
  font-size: var(--fs-xs);
  padding: 0;
}
.chev {
  color: var(--color-text-faint);
}
.body {
  margin: 0.25rem 0 0;
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--color-text-faint);
  font-style: italic;
  font-size: var(--fs-sm);
  border-left: 2px solid color-mix(in srgb, var(--phase-thinking) 40%, transparent);
  padding-left: 0.5rem;
}
</style>
