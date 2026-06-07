<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSpaceStore } from '@/stores/space'
import { useStreamStore } from '@/stores/stream'
import { displayPhase, phaseClass } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import type { TranscriptEntry } from '@/types/wire'
import EvPill from '@/components/base/EvPill.vue'
import EvButton from '@/components/base/EvButton.vue'
import MailboxList from './MailboxList.vue'

// Member detail rail (FE-4): Live (phase + jump to stream), History (persisted
// transcript), Mailbox (routed messages). Clearly separates "live" from
// "history" (RP-4 H7).
const props = defineProps<{ member: string }>()
const route = useRoute()
const router = useRouter()
const space = useSpaceStore()
const stream = useStreamStore()
const tab = ref<'live' | 'history' | 'mailbox'>('live')
const entry = computed(() => space.merged.find((m) => m.name === props.member) || null)
const transcript = ref<TranscriptEntry[]>([])

async function loadHistory() {
  transcript.value = await stream.transcriptOf(props.member)
}
watch(
  () => [props.member, tab.value],
  () => {
    if (tab.value === 'history') loadHistory()
  },
)

function openStream() {
  router.push({
    name: 'stream-member',
    params: { spaceId: String(route.params.spaceId), member: props.member },
    query: route.query,
  })
}
</script>

<template>
  <div class="mi">
    <div class="who">
      <span class="dot" :style="{ background: agentColor(member) }" />
      <strong>{{ member }}</strong>
      <span v-if="entry" class="role">{{ entry.role }}</span>
    </div>

    <nav class="tabs">
      <button :class="{ active: tab === 'live' }" @click="tab = 'live'">Live</button>
      <button :class="{ active: tab === 'history' }" @click="tab = 'history'">History</button>
      <button :class="{ active: tab === 'mailbox' }" @click="tab = 'mailbox'">Mailbox</button>
    </nav>

    <section v-if="tab === 'live'" class="pane">
      <EvPill v-if="entry" :tone="phaseClass(entry)" :label="displayPhase(entry) || entry.run" />
      <p v-if="entry?.currentTask" class="row">current task <code>#{{ entry.currentTask }}</code></p>
      <p v-if="entry?.whenToUse" class="muted">{{ entry.whenToUse }}</p>
      <EvButton size="sm" @click="openStream">open live stream →</EvButton>
    </section>

    <section v-else-if="tab === 'history'" class="pane">
      <ul class="tr">
        <li v-for="(e, i) in transcript" :key="i" :class="e.role">
          <span class="r">{{ e.role }}</span>
          <span class="x">{{ e.text }}</span>
        </li>
        <li v-if="!transcript.length" class="dim">no transcript</li>
      </ul>
    </section>

    <section v-else class="pane">
      <MailboxList :member="member" />
    </section>
  </div>
</template>

<style scoped>
.mi {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.who {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  margin-bottom: var(--sp-2);
}
.dot {
  width: 0.7rem;
  height: 0.7rem;
  border-radius: 50%;
}
.role {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  color: var(--color-text-muted);
}
.tabs {
  display: flex;
  gap: var(--sp-1);
  margin-bottom: var(--sp-2);
}
.tabs button {
  font-size: var(--fs-xs);
  padding: 0.15rem 0.5rem;
  background: transparent;
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  color: var(--color-text-muted);
  cursor: pointer;
}
.tabs button.active {
  color: var(--color-text);
  border-color: var(--color-accent);
}
.pane {
  display: grid;
  gap: var(--sp-2);
  align-content: start;
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.row {
  font-size: var(--fs-sm);
}
.muted {
  color: var(--color-text-faint);
  font-size: var(--fs-xs);
}
.tr {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: var(--sp-2);
}
.tr li {
  font-size: var(--fs-sm);
}
.tr .r {
  display: block;
  font-size: var(--fs-xs);
  font-family: var(--font-mono);
  color: var(--color-text-muted);
}
.tr .x {
  white-space: pre-wrap;
  word-break: break-word;
}
.dim {
  color: var(--color-text-muted);
}
code {
  background: var(--color-surface-2);
  padding: 0 0.3rem;
  border-radius: var(--r-sm);
}
</style>
