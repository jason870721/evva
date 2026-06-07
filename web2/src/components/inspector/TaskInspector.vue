<script setup lang="ts">
import { computed } from 'vue'
import { useLedgerStore } from '@/stores/ledger'
import { useMailStore } from '@/stores/mail'
import { useSpaceStore } from '@/stores/space'
import { relTime } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import EvPill from '@/components/base/EvPill.vue'

// Task detail rail (FE-5): the task plus its related messages (refTask). The task
// store is agent-owned, so this is read-only. Only board-snapshot tasks resolve
// here; a completed task off the snapshot shows a gentle note.
const props = defineProps<{ taskId: number }>()
const ledger = useLedgerStore()
const mail = useMailStore()
const space = useSpaceStore()
const task = computed(() => ledger.tasks.find((t) => t.id === props.taskId) || null)
const related = computed(() => mail.messages.filter((m) => m.refTask === props.taskId))
</script>

<template>
  <div v-if="task" class="ti">
    <div class="title">{{ task.title || 'task #' + task.id }}</div>
    <div class="row">
      <EvPill :tone="task.status" :label="task.status" />
      <span class="as"><span class="dot" :style="{ background: agentColor(task.assignee) }" />{{ task.assignee || '—' }}</span>
      <span class="t">{{ relTime(task.updatedAt, space.now) }}</span>
    </div>
    <div v-if="task.spec" class="f"><span class="k">spec</span>{{ task.spec }}</div>
    <div v-if="task.result" class="f"><span class="k">result</span>{{ task.result }}</div>
    <div v-if="task.verifyNote" class="f"><span class="k">verify</span>{{ task.verifyNote }}</div>
    <div v-if="task.parentId" class="f"><span class="k">parent</span>#{{ task.parentId }}</div>

    <div class="sub">related messages</div>
    <ul class="rel">
      <li v-for="m in related" :key="m.id">
        <span class="dot" :style="{ background: agentColor(m.sender) }" />{{ m.sender }} → {{ m.recipient }}: {{ m.body }}
      </li>
      <li v-if="!related.length" class="dim">none</li>
    </ul>
  </div>
  <div v-else class="dim">task #{{ taskId }} is not in the current board snapshot.</div>
</template>

<style scoped>
.title {
  font-weight: 600;
  margin-bottom: var(--sp-2);
}
.row {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  margin-bottom: var(--sp-2);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.as {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}
.dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
  display: inline-block;
}
.t {
  margin-left: auto;
  font-family: var(--font-mono);
}
.f {
  font-size: var(--fs-xs);
  line-height: 1.35;
  white-space: pre-wrap;
  word-break: break-word;
  margin-bottom: 0.3rem;
}
.f .k {
  display: inline-block;
  min-width: 3.2rem;
  margin-right: 0.4rem;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  text-transform: uppercase;
}
.sub {
  margin: var(--sp-3) 0 var(--sp-1);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.rel {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: 0.3rem;
}
.rel li {
  font-size: var(--fs-sm);
  word-break: break-word;
}
.dim {
  color: var(--color-text-muted);
  font-size: var(--fs-sm);
}
</style>
