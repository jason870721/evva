<script setup lang="ts">
import { computed, ref } from 'vue'
import { useSpaceStore } from '@/stores/space'
import { relTime } from '@/lib/events'
import { agentColor } from '@/lib/colors'

// Read-only team blackboard (BB): the leader-curated standing picture every
// member's wake brief carries. Renders only when the board has content (a
// dormant board costs zero pixels); content refreshes on the space store's
// reconciliation poll, so a blackboard_updated event shows within one tick.
// Write access is deliberately absent — the leader curates via its tool, the
// operator edits .vero/blackboard.md on disk (BB open question #1).
const space = useSpaceStore()
const collapsed = ref(false)

const board = computed(() => space.blackboard)
const hasContent = computed(() => !!board.value?.content)
</script>

<template>
  <div v-if="hasContent" class="bb" :class="{ collapsed }">
    <div class="head" @click="collapsed = !collapsed">
      <span class="chev">{{ collapsed ? '▸' : '▾' }}</span>
      <span class="ttl">Team blackboard</span>
      <span v-if="board!.by" class="by"
        ><span class="dot" :style="{ background: agentColor(board!.by) }" />{{ board!.by }}</span
      >
      <span class="time">updated {{ relTime(board!.updatedAt, space.now) }}</span>
    </div>
    <pre v-if="!collapsed" class="body">{{ board!.content }}</pre>
  </div>
</template>

<style scoped>
.bb {
  border: 1px solid var(--color-line);
  border-radius: var(--r-lg);
  background: var(--color-surface);
  margin-bottom: var(--sp-2);
}
.head {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  padding: 0.35rem var(--sp-2);
  cursor: pointer;
  user-select: none;
}
.chev {
  color: var(--color-text-muted);
  font-size: var(--fs-xs);
}
.ttl {
  font-size: var(--fs-sm);
  font-weight: 600;
}
.by {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-size: var(--fs-xs);
  font-family: var(--font-mono);
  color: var(--color-text-muted);
}
.dot {
  width: 0.55rem;
  height: 0.55rem;
  border-radius: 50%;
}
.time {
  margin-left: auto;
  font-size: var(--fs-xs);
  font-family: var(--font-mono);
  color: var(--color-text-muted);
}
.body {
  margin: 0;
  padding: 0 var(--sp-2) var(--sp-2);
  max-height: 30vh;
  overflow: auto;
  font-size: var(--fs-sm);
  font-family: var(--font-mono);
  white-space: pre-wrap;
  word-break: break-word;
  color: var(--color-text);
}
</style>
