<script setup lang="ts">
import { computed } from 'vue'
import type { AssistantTurn } from '@/lib/events'
import { splitFences } from '@/lib/segments'

const props = defineProps<{ turn: AssistantTurn }>()
const segs = computed(() => splitFences(props.turn.text))
</script>

<template>
  <div class="assistant">
    <template v-for="(s, i) in segs" :key="i">
      <pre v-if="s.code" class="code">{{ s.text }}</pre>
      <span v-else class="txt">{{ s.text }}</span>
    </template>
    <span v-if="turn.open" class="caret" aria-hidden="true" />
  </div>
</template>

<style scoped>
.assistant {
  font-size: var(--fs-md);
  line-height: 1.5;
}
.txt {
  white-space: pre-wrap;
  word-break: break-word;
}
.code {
  display: block;
  margin: 0.35rem 0;
  padding: 0.5rem 0.6rem;
  background: var(--color-bg);
  border: 1px solid var(--color-line);
  border-radius: var(--r-sm);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  overflow-x: auto;
}
.caret {
  display: inline-block;
  width: 0.5rem;
  height: 1em;
  margin-left: 1px;
  vertical-align: -0.15em;
  background: var(--color-cursor);
  animation: blink 1s step-end infinite;
}
@keyframes blink {
  50% {
    opacity: 0;
  }
}
</style>
