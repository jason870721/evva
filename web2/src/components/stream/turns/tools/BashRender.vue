<script setup lang="ts">
import { computed } from 'vue'
import type { ToolTurn } from '@/lib/events'
import { toolField } from '@/lib/tools'

const props = defineProps<{ turn: ToolTurn }>()
const cmd = computed(() => toolField(props.turn.input, 'command') || toolField(props.turn.input, 'cmd'))
</script>

<template>
  <div class="bash">
    <pre v-if="cmd" class="cmd">$ {{ cmd }}</pre>
    <pre v-if="turn.result" class="out">{{ turn.result }}</pre>
  </div>
</template>

<style scoped>
pre {
  margin: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  word-break: break-word;
}
.cmd {
  color: var(--color-accent);
}
.out {
  margin-top: 0.3rem;
  max-height: 16rem;
  overflow: auto;
  color: var(--color-text-muted);
}
</style>
