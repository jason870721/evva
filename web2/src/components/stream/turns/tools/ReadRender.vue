<script setup lang="ts">
import { computed } from 'vue'
import type { ToolTurn } from '@/lib/events'
import { toolField } from '@/lib/tools'

const props = defineProps<{ turn: ToolTurn }>()
const file = computed(() => toolField(props.turn.input, 'file_path') || toolField(props.turn.input, 'path'))
</script>

<template>
  <div class="read">
    <div v-if="file" class="file">{{ file }}</div>
    <pre v-if="turn.result" class="content">{{ turn.result }}</pre>
  </div>
</template>

<style scoped>
.file {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-tool-result);
  margin-bottom: 0.3rem;
}
.content {
  margin: 0;
  max-height: 16rem;
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
