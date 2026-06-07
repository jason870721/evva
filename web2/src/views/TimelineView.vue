<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useMailStore } from '@/stores/mail'
import { useLedgerStore } from '@/stores/ledger'
import { useSpaceStore } from '@/stores/space'
import { buildTimeline, type TimelineItem } from '@/lib/timeline'
import { relTime } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import EvPanel from '@/components/base/EvPanel.vue'

const mail = useMailStore()
const ledger = useLedgerStore()
const space = useSpaceStore()
const route = useRoute()
const router = useRouter()
const filter = ref<'all' | 'message' | 'task'>('all')

const items = computed<TimelineItem[]>(() => {
  const all = buildTimeline(mail.messages, ledger.tasks)
  return filter.value === 'all' ? all : all.filter((i) => i.kind === filter.value)
})

function onClick(it: TimelineItem) {
  if (it.kind === 'task' && it.taskId) {
    router.push({ query: { ...route.query, t: String(it.taskId), m: undefined } })
  } else if (it.kind === 'message') {
    router.push({ query: { ...route.query, m: it.sender, t: undefined } })
  }
}
</script>

<template>
  <EvPanel :title="`Timeline · ${items.length}`" class="fill">
    <div class="filter">
      <button
        v-for="f in (['all', 'message', 'task'] as const)"
        :key="f"
        class="fchip"
        :class="{ on: filter === f }"
        @click="filter = f"
      >
        {{ f }}
      </button>
    </div>
    <ul class="feed">
      <li v-for="it in items" :key="it.id" :class="it.kind" @click="onClick(it)">
        <span class="t">{{ relTime(it.time, space.now) }}</span>
        <span class="g" aria-hidden="true">{{ it.kind === 'message' ? '✉' : '◆' }}</span>
        <span class="s">
          <span class="dot" :style="{ background: agentColor(it.sender) }" />{{ it.sender }}
          <template v-if="it.recipient"> → <span class="dot" :style="{ background: agentColor(it.recipient) }" />{{ it.recipient }}</template>
        </span>
        <span class="b">{{ it.title }}</span>
      </li>
      <li v-if="!items.length" class="dim">no activity yet</li>
    </ul>
    <p class="note">訊息 + 任務生命週期；gate / 成員 / 排程 / 外部事件來源由 FE-6/FE-7 接入此同一 feed。</p>
  </EvPanel>
</template>

<style scoped>
.fill {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.filter {
  display: flex;
  gap: var(--sp-1);
  margin-bottom: var(--sp-2);
}
.fchip {
  font-size: var(--fs-xs);
  padding: 0.05rem 0.5rem;
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
.feed {
  list-style: none;
  margin: 0;
  padding: 0;
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: grid;
  gap: 0.2rem;
  align-content: start;
}
.feed li {
  display: grid;
  grid-template-columns: 3rem 1rem auto 1fr;
  gap: var(--sp-2);
  align-items: baseline;
  font-size: var(--fs-sm);
  padding: 0.2rem 0.3rem;
  border-radius: var(--r-sm);
  cursor: pointer;
}
.feed li:hover {
  background: var(--color-surface);
}
.t {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
}
.g {
  color: var(--color-text-muted);
}
.s {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
}
.dot {
  width: 0.45rem;
  height: 0.45rem;
  border-radius: 50%;
  display: inline-block;
}
.b {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.dim {
  color: var(--color-text-muted);
}
.note {
  margin-top: var(--sp-2);
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
}
</style>
