<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useLedgerStore } from '@/stores/ledger'
import { useSpaceStore } from '@/stores/space'
import { TASK_STATES } from '@/lib/events'
import type { TaskStatus } from '@/types/wire'
import TaskCard from './TaskCard.vue'
import EvPill from '@/components/base/EvPill.vue'

// Read-mostly 5-state board (the task store is agent-owned — no operator
// clear/cancel). Completed shows the newest few + a "view all" into the paged tab
// (RP-6). Assignee filter for locating tasks across columns.
const emit = defineEmits<{ viewAll: [] }>()
const ledger = useLedgerStore()
const space = useSpaceStore()
const route = useRoute()
const router = useRouter()
const assigneeFilter = ref('')

const titles: Record<TaskStatus, string> = {
  pending: 'Pending',
  running: 'Running',
  suspended: 'Suspended',
  verifying: 'Verifying',
  completed: 'Completed',
}
const groups = computed(() => ledger.groups)
function colTasks(s: TaskStatus) {
  const list = groups.value[s]
  return assigneeFilter.value ? list.filter((t) => t.assignee === assigneeFilter.value) : list
}
function openTask(id: number) {
  router.push({ query: { ...route.query, t: String(id), m: undefined } })
}
</script>

<template>
  <div class="board">
    <div class="filterbar">
      <span class="lbl">assignee:</span>
      <button class="fchip" :class="{ on: !assigneeFilter }" @click="assigneeFilter = ''">all</button>
      <button
        v-for="m in space.roster"
        :key="m.name"
        class="fchip"
        :class="{ on: assigneeFilter === m.name }"
        @click="assigneeFilter = m.name"
      >
        {{ m.name }}
      </button>
    </div>
    <div class="cols">
      <div v-for="s in TASK_STATES" :key="s" class="col">
        <div class="chead">
          <EvPill :tone="s" :label="titles[s]" />
          <span class="n">{{ s === 'completed' ? ledger.completedTotal : colTasks(s).length }}</span>
        </div>
        <div class="cards">
          <TaskCard v-for="t in colTasks(s)" :key="t.id" :task="t" :now="space.now" @open="openTask" />
          <div v-if="!colTasks(s).length" class="empty">—</div>
          <button
            v-if="s === 'completed' && ledger.completedTotal > groups.completed.length"
            class="viewall"
            @click="emit('viewAll')"
          >
            view all {{ ledger.completedTotal }} →
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.board {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.filterbar {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: var(--sp-1);
  padding-bottom: var(--sp-2);
}
.lbl {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  margin-right: 0.2rem;
}
.fchip {
  font-size: var(--fs-xs);
  padding: 0.05rem 0.45rem;
  border: 1px solid var(--color-line);
  border-radius: var(--r-pill);
  background: var(--color-surface);
  color: var(--color-text-muted);
  cursor: pointer;
}
.fchip.on {
  color: var(--color-text);
  border-color: var(--color-accent);
}
.cols {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: var(--sp-2);
  overflow: auto;
}
.col {
  background: var(--color-surface);
  border: 1px solid var(--color-line);
  border-radius: var(--r-lg);
  padding: var(--sp-2);
  min-width: 0;
  display: flex;
  flex-direction: column;
}
.chead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.1rem 0.2rem var(--sp-2);
}
.n {
  font-family: var(--font-mono);
  color: var(--color-text-muted);
}
.cards {
  display: grid;
  gap: var(--sp-2);
  overflow: auto;
  min-height: 0;
}
.empty {
  color: var(--color-line-strong);
  text-align: center;
  font-size: var(--fs-sm);
  padding: var(--sp-2);
}
.viewall {
  width: 100%;
  background: transparent;
  border: 1px dashed var(--color-line);
  border-radius: var(--r-md);
  color: var(--color-text-muted);
  font-size: var(--fs-xs);
  padding: 0.3rem;
  cursor: pointer;
}
.viewall:hover {
  border-color: var(--color-accent);
  color: var(--color-text);
}
</style>
