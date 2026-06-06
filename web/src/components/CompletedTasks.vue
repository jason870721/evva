<script setup>
import { ref, computed, watch } from 'vue'
import { relTime } from '../events.js'
import { agentColor } from '../colors.js'

// On-demand paged view of the completed ledger (RP-6). Unlike the board, this is
// NOT driven by the 2.5s poll — it fetches a page only when the tab is opened or
// the operator pages, so a large completed history never rides the live refresh.
const props = defineProps({
  api: { type: Object, required: true },
  spaceId: { type: String, required: true },
  visible: { type: Boolean, default: false },
  now: { type: Number, default: 0 },
})

const PAGE = 20
const tasks = ref([])
const total = ref(0)
const offset = ref(0)
const loading = ref(false)
const err = ref('')
const open = ref(null)
function toggle(id) {
  open.value = open.value === id ? null : id
}

const rangeStart = computed(() => (tasks.value.length ? offset.value + 1 : 0))
const rangeEnd = computed(() => offset.value + tasks.value.length)
const hasPrev = computed(() => offset.value > 0)
const hasNext = computed(() => offset.value + tasks.value.length < total.value)

async function load(off) {
  loading.value = true
  err.value = ''
  try {
    const page = await props.api.tasksPage(props.spaceId, { status: 'completed', limit: PAGE, offset: off })
    tasks.value = (page && page.tasks) || []
    total.value = (page && page.total) || 0
    offset.value = off
  } catch (e) {
    err.value = String(e.message || e)
  } finally {
    loading.value = false
  }
}
function prev() {
  if (hasPrev.value) load(Math.max(0, offset.value - PAGE))
}
function next() {
  if (hasNext.value) load(offset.value + PAGE)
}

// Refetch from the top whenever the tab is (re)opened, so newly-completed tasks
// appear without a manual refresh. v-show keeps this mounted, hence the watch.
watch(
  () => props.visible,
  (v) => {
    if (v) load(0)
  },
  { immediate: true },
)
</script>

<template>
  <div class="completed">
    <div class="head">
      <span class="label">Completed</span>
      <span class="range" v-if="total">{{ rangeStart }}–{{ rangeEnd }} of {{ total }}</span>
      <span class="pager">
        <button :disabled="!hasPrev || loading" @click="prev">← newer</button>
        <button :disabled="!hasNext || loading" @click="next">older →</button>
      </span>
    </div>

    <p v-if="err" class="err">{{ err }}</p>
    <div v-if="!loading && !tasks.length && !err" class="empty">No completed tasks yet.</div>

    <div class="list">
      <div
        v-for="t in tasks"
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
      </div>
    </div>
  </div>
</template>

<style scoped>
.completed {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.head {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  padding: 0.2rem 0.1rem 0.5rem;
  font-size: 0.82rem;
}
.label {
  font-weight: 600;
}
.range {
  color: var(--dim);
  font-family: var(--mono);
  font-size: 0.72rem;
}
.pager {
  margin-left: auto;
  display: flex;
  gap: 0.3rem;
}
.pager button {
  font-size: 0.72rem;
  padding: 0.2rem 0.55rem;
  background: transparent;
  border: 1px solid var(--line);
  border-radius: 6px;
  color: var(--dim);
  cursor: pointer;
}
.pager button:disabled {
  opacity: 0.4;
  cursor: default;
}
.pager button:not(:disabled):hover {
  border-color: var(--accent);
  color: #e6edf3;
}
.err {
  color: var(--danger);
  font-size: var(--fs-sm);
  margin: 0 0 0.4rem;
}
.empty {
  color: var(--dim);
  text-align: center;
  font-size: 0.82rem;
  padding: 1rem;
}
.list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: grid;
  gap: 0.4rem;
  align-content: start;
}
.card {
  background: var(--bg);
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.5rem 0.55rem;
  cursor: pointer;
}
.card:hover,
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
  display: inline-block;
}
.who .time {
  margin-left: auto;
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
</style>
