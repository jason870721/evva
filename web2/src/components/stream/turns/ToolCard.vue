<script setup lang="ts">
import { computed } from 'vue'
import type { ToolTurn } from '@/lib/events'
import { toolFamily } from '@/lib/tools'
import EvSpinner from '@/components/base/EvSpinner.vue'
import BashRender from './tools/BashRender.vue'
import DiffRender from './tools/DiffRender.vue'
import ReadRender from './tools/ReadRender.vue'
import WebRender from './tools/WebRender.vue'
import GenericRender from './tools/GenericRender.vue'

const props = defineProps<{ turn: ToolTurn }>()
const family = computed(() => toolFamily(props.turn.tool))
const glyph = computed(() => ({ bash: '$', diff: '±', read: '▤', web: '🌐', generic: '▸' })[family.value])
</script>

<template>
  <div class="tool" :class="[turn.status]">
    <div class="head">
      <span class="g" aria-hidden="true">{{ glyph }}</span>
      <code class="name">{{ turn.tool }}</code>
      <span class="status" :class="turn.status">
        <EvSpinner v-if="turn.status === 'running'" :size="12" />
        <span v-else aria-hidden="true">{{ turn.status === 'done' ? '✓' : '✕' }}</span>
        {{ turn.status }}
      </span>
    </div>
    <div class="body">
      <BashRender v-if="family === 'bash'" :turn="turn" />
      <DiffRender v-else-if="family === 'diff'" :turn="turn" />
      <ReadRender v-else-if="family === 'read'" :turn="turn" />
      <WebRender v-else-if="family === 'web'" :turn="turn" />
      <GenericRender v-else :turn="turn" />
    </div>
  </div>
</template>

<style scoped>
.tool {
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  background: var(--color-surface-2);
  overflow: hidden;
}
.tool.error {
  border-color: color-mix(in srgb, var(--color-danger) 55%, transparent);
}
.head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.3rem 0.5rem;
  border-bottom: 1px solid var(--color-line);
}
.g {
  color: var(--phase-executing);
  font-family: var(--font-mono);
  font-weight: 700;
}
.name {
  color: var(--phase-executing);
  background: transparent;
  font-size: var(--fs-sm);
}
.status {
  margin-left: auto;
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.status.done {
  color: var(--status-completed);
}
.status.error {
  color: var(--color-danger);
}
.body {
  padding: 0.4rem 0.5rem;
}
</style>
