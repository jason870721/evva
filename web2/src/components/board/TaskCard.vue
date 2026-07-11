<script setup lang="ts">
import { ref } from 'vue'
import type { CheckInfo, TaskInfo } from '@/types/wire'
import { relTime } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import EvButton from '@/components/base/EvButton.vue'

defineProps<{ task: TaskInfo; now: number }>()
const emit = defineEmits<{ open: [id: number] }>()
const expanded = ref(false)

// One-line verdict for the evidence row — mirrors store.CheckEvidence.Outcome.
function checkOutcome(ev: CheckInfo): string {
  const secs = Math.round(ev.durationMs / 1000)
  const dur = secs >= 60 ? `${Math.floor(secs / 60)}m${secs % 60}s` : `${secs}s`
  if (ev.timedOut) return `TIMEOUT after ${dur}`
  return ev.pass ? `PASS in ${dur}` : `FAIL (exit ${ev.exit}) in ${dur}`
}
</script>

<template>
  <div class="card" :class="{ open: expanded }">
    <div class="title" @click="expanded = !expanded">{{ task.title || 'task #' + task.id }}</div>
    <div class="meta">
      <span class="id">#{{ task.id }}</span>
      <span class="assignee"><span class="dot" :style="{ background: agentColor(task.assignee) }" />{{ task.assignee || '—' }}</span>
      <span v-if="task.parentId" class="parent">↳#{{ task.parentId }}</span>
      <span v-if="task.dependsOn?.length" class="deps" title="dependencies — the engine dispatches this task when they complete">⛓ {{ task.dependsOn.map((d) => '#' + d).join(' ') }}</span>
      <span v-if="task.verifyPolicy === 'auto'" class="auto" title="verify: auto — completes the instant the worker reports done">auto</span>
      <span v-if="task.verifyPolicy === 'checks'" class="auto" title="verify: checks — the space check gates it: green auto-completes, red escalates to the leader">checks</span>
      <span v-if="task.checkRunning" class="chip running" title="verify-time check executing">checks…</span>
      <span v-else-if="task.checks" class="chip" :class="task.checks.pass ? 'pass' : 'fail'" :title="checkOutcome(task.checks)">{{ task.checks.timedOut ? '⏱ timeout' : task.checks.pass ? '✓ pass' : '✗ fail' }}</span>
      <span class="time">{{ relTime(task.updatedAt, now) }}</span>
    </div>
    <div v-if="expanded" class="detail">
      <div v-if="task.spec" class="f"><span class="k">spec</span>{{ task.spec }}</div>
      <div v-if="task.result" class="f"><span class="k">result</span>{{ task.result }}</div>
      <div v-if="task.verifyNote" class="f"><span class="k">verify</span>{{ task.verifyNote }}</div>
      <div v-if="task.checks" class="f"><span class="k">checks</span>{{ checkOutcome(task.checks) }} — {{ task.checks.command }}</div>
      <pre v-if="task.checks && !task.checks.pass && task.checks.tail" class="tail">{{ task.checks.tail }}</pre>
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
.deps {
  color: var(--color-text-faint);
}
.auto {
  border: 1px solid var(--color-text-faint);
  border-radius: var(--r-sm);
  padding: 0 0.25rem;
  font-size: var(--fs-xxs, var(--fs-xs));
  color: var(--color-text-muted);
}
.chip {
  border-radius: var(--r-sm);
  padding: 0 0.25rem;
  font-size: var(--fs-xxs, var(--fs-xs));
}
.chip.pass {
  color: var(--color-success);
  border: 1px solid var(--color-success);
}
.chip.fail {
  color: var(--color-danger);
  border: 1px solid var(--color-danger);
}
.chip.running {
  color: var(--color-warning);
  border: 1px solid var(--color-warning);
  animation: chip-pulse 1.2s ease-in-out infinite;
}
@keyframes chip-pulse {
  50% {
    opacity: 0.45;
  }
}
.tail {
  margin: 0;
  padding: 0.35rem 0.45rem;
  max-height: 10rem;
  overflow: auto;
  font-family: var(--font-mono);
  font-size: var(--fs-xxs, var(--fs-xs));
  line-height: 1.35;
  white-space: pre-wrap;
  word-break: break-word;
  background: var(--color-surface-2);
  border-radius: var(--r-sm);
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
