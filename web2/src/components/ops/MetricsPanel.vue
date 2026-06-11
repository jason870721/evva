<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useSpaceStore } from '@/stores/space'
import { errMsg } from '@/lib/util'
import type { MetricsInfo } from '@/types/wire'
import EvDialog from '@/components/base/EvDialog.vue'
import EvButton from '@/components/base/EvButton.vue'

// Space metrics (RP-17): scheduler counters per member, the RP-22 watchdog
// tallies, and the RP-28 run-token histograms — the "is per-run cost creeping
// up with history length?" gauge. Plain counters since space start; refresh on
// demand, no timeseries.
const emit = defineEmits<{ close: [] }>()
const space = useSpaceStore()
const m = ref<MetricsInfo | null>(null)
const err = ref('')
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    m.value = await space.fetchMetrics()
    err.value = ''
  } catch (e) {
    err.value = errMsg(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

function uptime(secs: number): string {
  const d = Math.floor(secs / 86400)
  const h = Math.floor((secs % 86400) / 3600)
  const min = Math.floor((secs % 3600) / 60)
  if (d > 0) return `${d}d${h}h`
  if (h > 0) return `${h}h${min}m`
  return `${min}m`
}

// Bucket orders match the Go metrics maps (runSeconds / runTokens).
const SEC_BUCKETS: [string, string][] = [
  ['lt10s', '<10s'],
  ['lt1m', '<1m'],
  ['lt10m', '<10m'],
  ['gte10m', '≥10m'],
]
const TOK_BUCKETS: [string, string][] = [
  ['lt1k', '<1k'],
  ['lt10k', '<10k'],
  ['lt50k', '<50k'],
  ['gte50k', '≥50k'],
]
function dist(buckets: [string, string][], counts?: Record<string, number>): string {
  return buckets.map(([k, label]) => `${label} ${counts?.[k] || 0}`).join(' · ')
}
</script>

<template>
  <EvDialog title="Space metrics" width="46rem" @close="emit('close')">
    <template v-if="m">
      <div class="counters">
        <div class="c"><span class="k">uptime</span><span class="v">{{ uptime(m.uptimeSecs) }}</span></div>
        <div class="c"><span class="k">events logged</span><span class="v">{{ m.eventsLogged }}</span></div>
        <div class="c"><span class="k">events dropped</span><span class="v" :class="{ bad: m.eventsDropped }">{{ m.eventsDropped }}</span></div>
        <div class="c"><span class="k">hints dropped</span><span class="v" :class="{ bad: m.hintsDropped }">{{ m.hintsDropped }}</span></div>
        <div class="c"><span class="k">stale-task alerts</span><span class="v" :class="{ bad: m.tasksStale }">{{ m.tasksStale }}</span></div>
        <div class="c"><span class="k">mailbox alerts</span><span class="v" :class="{ bad: m.mailboxStale }">{{ m.mailboxStale }}</span></div>
      </div>

      <table class="members">
        <thead>
          <tr>
            <th>member</th>
            <th title="wakes: by message / by timer">wakes ✉/⏰</th>
            <th>runs</th>
            <th>aborts</th>
            <th>run time</th>
            <th title="per-run input+output token cost (RP-28)">run tokens</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="(mm, name) in m.members" :key="name">
            <td class="name">{{ name }}</td>
            <td class="num">{{ mm.wakesMessage }}/{{ mm.wakesTimer }}</td>
            <td class="num">{{ mm.runs }}</td>
            <td class="num" :class="{ bad: mm.aborts }">{{ mm.aborts }}</td>
            <td class="dist">{{ dist(SEC_BUCKETS, mm.runSeconds) }}</td>
            <td class="dist">{{ dist(TOK_BUCKETS, mm.runTokens) }}</td>
          </tr>
          <tr v-if="!Object.keys(m.members || {}).length">
            <td colspan="6" class="dim">no member activity yet</td>
          </tr>
        </tbody>
      </table>
      <p class="hint">
        run tokens bucket each completed run's input+output cost — a histogram drifting right means a long-lived
        member's per-run context is growing. Exact per-run usage is on every run_end line in
        <code>.vero/events/</code>.
      </p>
    </template>
    <p v-else-if="!err" class="dim">loading…</p>
    <p v-if="err" class="err">{{ err }}</p>

    <template #footer>
      <EvButton :loading="loading" @click="load">refresh</EvButton>
      <EvButton variant="primary" @click="emit('close')">Close</EvButton>
    </template>
  </EvDialog>
</template>

<style scoped>
.counters {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: var(--sp-2);
  margin-bottom: var(--sp-3);
}
.c {
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  padding: 0.35rem 0.5rem;
  display: grid;
}
.k {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.v {
  font-family: var(--font-mono);
  font-size: var(--fs-md);
}
.v.bad,
.num.bad {
  color: var(--color-warning);
}
.members {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--fs-xs);
}
.members th {
  text-align: left;
  color: var(--color-text-muted);
  font-weight: 500;
  padding: 0.25rem 0.4rem;
  border-bottom: 1px solid var(--color-line);
  white-space: nowrap;
}
.members td {
  padding: 0.3rem 0.4rem;
  border-bottom: 1px solid var(--color-line);
  vertical-align: top;
}
.name {
  font-weight: 600;
}
.num {
  font-family: var(--font-mono);
}
.dist {
  font-family: var(--font-mono);
  white-space: nowrap;
}
.hint {
  margin-top: var(--sp-2);
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
}
.hint code {
  background: var(--color-surface-2);
  padding: 0 0.3rem;
  border-radius: var(--r-sm);
}
.err {
  color: var(--color-danger);
  font-size: var(--fs-sm);
}
.dim {
  color: var(--color-text-muted);
}
</style>
