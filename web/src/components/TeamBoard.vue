<script setup>
import { computed } from 'vue'
import { groupTasks, TASK_STATES } from '../events.js'

const props = defineProps({
  tasks: { type: Array, default: () => [] },
})

const columns = computed(() => groupTasks(props.tasks))

const titles = {
  pending: 'Pending',
  running: 'Running',
  suspended: 'Suspended',
  verifying: 'Verifying',
  completed: 'Completed',
}
</script>

<template>
  <div class="board">
    <div v-for="s in TASK_STATES" :key="s" class="col">
      <div class="col-head">
        <span :class="['dot', s]"></span>{{ titles[s] }}
        <span class="count">{{ columns[s].length }}</span>
      </div>
      <div class="cards">
        <div v-for="t in columns[s]" :key="t.id" class="card">
          <div class="title">{{ t.title || ('task #' + t.id) }}</div>
          <div class="who">
            <span>#{{ t.id }}</span>
            <span class="assignee">→ {{ t.assignee }}</span>
          </div>
          <div v-if="t.verifyNote" class="note">{{ t.verifyNote }}</div>
        </div>
        <div v-if="!columns[s].length" class="empty">—</div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.board {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 0.6rem;
  height: 100%;
  overflow: auto;
}
.col {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 0.5rem;
  min-width: 0;
}
.col-head {
  font-size: 0.8rem;
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.2rem 0.3rem 0.5rem;
}
.count {
  margin-left: auto;
  color: var(--dim);
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  display: inline-block;
}
.dot.pending { background: #6b7280; }
.dot.running { background: #3b82f6; }
.dot.suspended { background: #f59e0b; }
.dot.verifying { background: #a855f7; }
.dot.completed { background: #22c55e; }
.cards {
  display: grid;
  gap: 0.4rem;
}
.card {
  background: var(--bg);
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.5rem 0.55rem;
}
.title {
  font-size: 0.82rem;
  line-height: 1.3;
}
.who {
  display: flex;
  gap: 0.4rem;
  font-family: var(--mono);
  font-size: 0.7rem;
  color: var(--dim);
  margin-top: 0.3rem;
}
.note {
  font-size: 0.72rem;
  color: var(--dim);
  margin-top: 0.3rem;
  font-style: italic;
}
.empty {
  color: var(--line);
  text-align: center;
  font-size: 0.8rem;
  padding: 0.4rem;
}
</style>
