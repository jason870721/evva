<script setup lang="ts">
import { computed, ref } from 'vue'
import type { ToolTurn } from '@/lib/events'
import { toolInputJson } from '@/lib/tools'

const props = defineProps<{ turn: ToolTurn }>()
const inputStr = computed(() => toolInputJson(props.turn.input))
const showInput = ref(false)
</script>

<template>
  <div class="generic">
    <button v-if="inputStr" class="toggle" @click="showInput = !showInput">
      {{ showInput ? '▾' : '▸' }} input
    </button>
    <pre v-if="showInput && inputStr" class="input">{{ inputStr }}</pre>
    <pre v-if="turn.result" class="result">{{ turn.result }}</pre>
  </div>
</template>

<style scoped>
.toggle {
  background: transparent;
  border: none;
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: var(--fs-xs);
  padding: 0;
  font-family: var(--font-mono);
}
pre {
  margin: 0.3rem 0 0;
  max-height: 14rem;
  overflow: auto;
  background: var(--color-bg);
  border: 1px solid var(--color-line);
  border-radius: var(--r-sm);
  padding: 0.4rem;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--color-text-muted);
}
</style>
