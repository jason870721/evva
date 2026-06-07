<script setup lang="ts">
import { computed } from 'vue'
import type { ToolTurn } from '@/lib/events'
import { toolField } from '@/lib/tools'

const props = defineProps<{ turn: ToolTurn }>()
const target = computed(() => toolField(props.turn.input, 'url') || toolField(props.turn.input, 'query'))
</script>

<template>
  <div class="web">
    <div v-if="target" class="target">{{ target }}</div>
    <pre v-if="turn.result" class="result">{{ turn.result }}</pre>
  </div>
</template>

<style scoped>
.target {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-info);
  margin-bottom: 0.3rem;
  word-break: break-all;
}
.result {
  margin: 0;
  max-height: 14rem;
  overflow: auto;
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--color-text-muted);
}
</style>
