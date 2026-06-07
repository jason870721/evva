<script setup lang="ts">
import { computed } from 'vue'
import type { ToolTurn } from '@/lib/events'
import { toolField } from '@/lib/tools'

// Best-effort diff: edit/write tools carry old_string/new_string in the input.
// Rendered as filled −/+ blocks using the TUI diff tokens (white text on solid
// green/red, palette.go M2 look). Falls back to the raw result otherwise.
const props = defineProps<{ turn: ToolTurn }>()
const file = computed(() => toolField(props.turn.input, 'file_path') || toolField(props.turn.input, 'path'))
const oldS = computed(() => toolField(props.turn.input, 'old_string'))
const newS = computed(() => toolField(props.turn.input, 'new_string'))
const hasDiff = computed(() => !!(oldS.value || newS.value))
</script>

<template>
  <div class="diff">
    <div v-if="file" class="file">{{ file }}</div>
    <template v-if="hasDiff">
      <pre v-if="oldS" class="del">{{ oldS.split('\n').map((l) => '- ' + l).join('\n') }}</pre>
      <pre v-if="newS" class="add">{{ newS.split('\n').map((l) => '+ ' + l).join('\n') }}</pre>
    </template>
    <pre v-else-if="turn.result" class="res">{{ turn.result }}</pre>
  </div>
</template>

<style scoped>
.file {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  margin-bottom: 0.3rem;
}
pre {
  margin: 0;
  padding: 0.3rem 0.5rem;
  border-radius: var(--r-sm);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  word-break: break-word;
}
.del {
  background: var(--diff-del-bg);
  color: var(--diff-fg);
}
.add {
  background: var(--diff-add-bg);
  color: var(--diff-fg);
  margin-top: 0.2rem;
}
.res {
  background: var(--color-bg);
  border: 1px solid var(--color-line);
  color: var(--color-text-muted);
}
</style>
