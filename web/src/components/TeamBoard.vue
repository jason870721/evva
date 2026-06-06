<script setup>
import { computed, ref } from 'vue'
import { groupTasks, TASK_STATES, relTime } from '../events.js'
import { agentColor } from '../colors.js'

const props = defineProps({
  tasks: { type: Array, default: () => [] },
  now: { type: Number, default: 0 },
  // Full completed count (the board only renders the newest few; the rest live
  // in the Completed tab). RP-6: keeps the column bounded as history grows.
  completedTotal: { type: Number, default: 0 },
})
const emit = defineEmits(['view-all'])

const columns = computed(() => groupTasks(props.tasks))
const open = ref(null) // id of the expanded card, or null
function toggle(id) {
  open.value = open.value === id ? null : id
}

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
        <span class="count">{{ s === 'completed' ? completedTotal : columns[s].length }}</span>
      </div>
      <div class="cards">
        <div
          v-for="t in columns[s]"
          :key="t.id"
          class="card"
          :class="{ open: open === t.id }"
          @click="toggle(t.id)"
        >
          <div class="title">{{ t.title || ('task #' + t.id) }}</div>
          <div class="who">
            <span>#{{ t.id }}</span>
            <span class="assignee">
              <span class="dot" :style="{ background: agentColor(t.assignee) }"></span>{{ t.assignee }}
            </span>
            <span class="time">{{ relTime(t.updatedAt, now) }}</span>
          </div>
          <div v-if="open === t.id" class="detail">
            <div v-if="t.spec" class="field"><span class="k">spec</span>{{ t.spec }}</div>
            <div v-if="t.result" class="field"><span class="k">result</span>{{ t.result }}</div>
            <div v-if="t.verifyNote" class="field"><span class="k">verify</span>{{ t.verifyNote }}</div>
            <div v-if="t.createdBy" class="field"><span class="k">by</span>{{ t.createdBy }}</div>
          </div>
          <div v-else-if="t.verifyNote" class="note">{{ t.verifyNote }}</div>
        </div>
        <div v-if="!columns[s].length" class="empty">—</div>
      </div>
      <button
        v-if="s === 'completed' && completedTotal > columns.completed.length"
        class="viewall"
        @click="emit('view-all')"
      >
        view all {{ completedTotal }} →
      </button>
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
  cursor: pointer;
}
.card:hover {
  border-color: var(--accent);
}
.card.open {
  border-color: var(--accent);
}
.title {
  font-size: 0.82rem;
  line-height: 1.3;
}
.who {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-family: var(--mono);
  font-size: 0.7rem;
  color: var(--dim);
  margin-top: 0.3rem;
}
.assignee {
  display: inline-flex;
  align-items: center;
  gap: 0.25rem;
}
.assignee .dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
}
.who .time {
  margin-left: auto;
}
.note {
  font-size: 0.72rem;
  color: var(--dim);
  margin-top: 0.3rem;
  font-style: italic;
}
.detail {
  margin-top: 0.4rem;
  display: grid;
  gap: 0.3rem;
}
.field {
  font-size: 0.74rem;
  line-height: 1.35;
  white-space: pre-wrap;
}
.field .k {
  display: inline-block;
  min-width: 3.2rem;
  margin-right: 0.4rem;
  color: var(--dim);
  font-family: var(--mono);
  font-size: var(--fs-xs);
  text-transform: uppercase;
}
.empty {
  color: var(--line);
  text-align: center;
  font-size: 0.8rem;
  padding: 0.4rem;
}
.viewall {
  margin-top: 0.4rem;
  width: 100%;
  background: transparent;
  border: 1px dashed var(--line);
  border-radius: 6px;
  color: var(--dim);
  font-size: 0.72rem;
  padding: 0.3rem;
  cursor: pointer;
}
.viewall:hover {
  border-color: var(--accent);
  color: #e6edf3;
}
</style>
