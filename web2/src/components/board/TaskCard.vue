<script setup lang="ts">
import { ref } from 'vue'
import type { TaskInfo } from '@/types/wire'
import { relTime } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import EvButton from '@/components/base/EvButton.vue'

defineProps<{ task: TaskInfo; now: number }>()
const emit = defineEmits<{ open: [id: number] }>()
const expanded = ref(false)
</script>

<template>
  <div class="card" :class="{ open: expanded }">
    <div class="title" @click="expanded = !expanded">{{ task.title || 'task #' + task.id }}</div>
    <div class="meta">
      <span class="id">#{{ task.id }}</span>
      <span class="assignee"><span class="dot" :style="{ background: agentColor(task.assignee) }" />{{ task.assignee || '—' }}</span>
      <span v-if="task.parentId" class="parent">↳#{{ task.parentId }}</span>
      <span class="time">{{ relTime(task.updatedAt, now) }}</span>
    </div>
    <div v-if="expanded" class="detail">
      <div v-if="task.spec" class="f"><span class="k">spec</span>{{ task.spec }}</div>
      <div v-if="task.result" class="f"><span class="k">result</span>{{ task.result }}</div>
      <div v-if="task.verifyNote" class="f"><span class="k">verify</span>{{ task.verifyNote }}</div>
      <div v-if="task.createdBy" class="f"><span class="k">by</span>{{ task.createdBy }}</div>
      <EvButton size="sm" @click="emit('open', task.id)">open in inspector →</EvButton>
    </div>
    <div v-else-if="task.verifyNote" class="note">{{ task.verifyNote }}</div>
  </div>
</template>

<style scoped>
.card {
  background: var(--card-bg);
  border: 1px solid var(--card-border);
  border-radius: var(--r-md);
  padding: 0.5rem 0.55rem;
}
.card:hover,
.card.open {
  border-color: var(--color-accent);
}
.title {
  font-size: var(--fs-sm);
  line-height: 1.3;
  cursor: pointer;
}
.meta {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  margin-top: 0.3rem;
}
.assignee {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}
.dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
}
.time {
  margin-left: auto;
}
.note {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  margin-top: 0.3rem;
  font-style: italic;
}
.detail {
  margin-top: 0.4rem;
  display: grid;
  gap: 0.3rem;
}
.f {
  font-size: var(--fs-xs);
  line-height: 1.35;
  white-space: pre-wrap;
  word-break: break-word;
}
.f .k {
  display: inline-block;
  min-width: 3.2rem;
  margin-right: 0.4rem;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  text-transform: uppercase;
}
</style>
