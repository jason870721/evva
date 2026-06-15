<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useSpaceStore } from '@/stores/space'
import { useStreamStore } from '@/stores/stream'
import { displayPhase, phaseClass, humanTokens } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import { errMsg } from '@/lib/util'
import type { MemoryFileInfo, TranscriptEntry } from '@/types/wire'
import EvPill from '@/components/base/EvPill.vue'
import EvBadge from '@/components/base/EvBadge.vue'
import EvButton from '@/components/base/EvButton.vue'
import EvContextBar from '@/components/base/EvContextBar.vue'
import MailboxList from './MailboxList.vue'

// Member detail rail (FE-4): Live (phase + jump to stream), History (persisted
// transcript), Mailbox (routed messages), Memory (RP-25 long-term notes,
// read-only). Clearly separates "live" from "history" (RP-4 H7).
const props = defineProps<{ member: string }>()
const route = useRoute()
const router = useRouter()
const space = useSpaceStore()
const stream = useStreamStore()
const tab = ref<'live' | 'history' | 'mailbox' | 'memory'>('live')
const entry = computed(() => space.merged.find((m) => m.name === props.member) || null)
const transcript = ref<TranscriptEntry[]>([])
const memory = ref<MemoryFileInfo[]>([])
// Compact control (Live tab): the backend refuses a member with a run in flight
// (409), so pre-disable while busy; `full` is lossy (it replaces the transcript
// with a summary brief) and so asks to confirm first. The in-flight flag lives
// in the store keyed by member — this inspector is reused across members (no
// :key), so a local flag would bleed onto whoever you switched to mid-compact.
const cBusy = computed(() => space.isCompacting(props.member))
const cErr = ref('')
const confirmFull = ref(false)
const running = computed(() => entry.value?.run === 'busy')

async function loadHistory() {
  transcript.value = await stream.transcriptOf(props.member)
}
async function loadMemory() {
  memory.value = (await space.fetchMemory(props.member)) || []
}
watch(
  () => [props.member, tab.value],
  () => {
    if (tab.value === 'history') loadHistory()
    if (tab.value === 'memory') loadMemory()
    // Drop any half-finished full-compact confirm + stale error when the
    // inspector retargets or the operator leaves the Live tab.
    confirmFull.value = false
    cErr.value = ''
  },
)

function openStream() {
  router.push({
    name: 'stream-member',
    params: { spaceId: String(route.params.spaceId), member: props.member },
    query: route.query,
  })
}

async function doCompact(kind: 'micro' | 'full') {
  const target = props.member // capture: the inspector may retarget mid-call
  cErr.value = ''
  try {
    await space.compactMember(target, kind)
    if (props.member === target) confirmFull.value = false
  } catch (e) {
    if (props.member === target) cErr.value = errMsg(e)
  }
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
      <button :class="{ active: tab === 'memory' }" @click="tab = 'memory'">Memory</button>
    </nav>

    <section v-if="tab === 'live'" class="pane">
      <div class="pills">
        <EvPill v-if="entry" :tone="phaseClass(entry)" :label="displayPhase(entry) || entry.run" />
        <EvBadge v-if="entry?.permissionMode" :tone="entry.permissionMode === 'bypass' ? 'warning' : 'info'">
          perm {{ entry.permissionMode }}
        </EvBadge>
      </div>
      <p v-if="entry?.currentTask" class="row">current task <code>#{{ entry.currentTask }}</code></p>
      <p v-if="entry?.whenToUse" class="muted">{{ entry.whenToUse }}</p>
      <div v-if="entry" class="gauges">
        <EvContextBar :used="entry.contextTokens" :limit="entry.contextLimit" />
        <EvContextBar
          v-if="entry.tokensBudget"
          :used="entry.tokensToday || 0"
          :limit="entry.tokensBudget"
          label="BDG"
          noun="tokens today"
        />
        <p class="muted io">
          session {{ humanTokens(entry.tokensIn || 0) }} in · {{ humanTokens(entry.tokensOut || 0) }} out
          <template v-if="!entry.tokensBudget"> · today {{ humanTokens(entry.tokensToday || 0) }}</template>
        </p>
      </div>
      <div v-if="entry" class="compact">
        <div class="chead">
          <span class="clabel">🗜 compact context</span>
          <span v-if="cErr" class="cerr">{{ cErr }}</span>
        </div>
        <div class="copts">
          <button
            :disabled="running || cBusy"
            title="Elide older tool results — free, instant, no LLM call"
            @click="doCompact('micro')"
          >
            micro
          </button>
          <button
            v-if="!confirmFull"
            class="risky"
            :disabled="running || cBusy"
            title="Summarize the whole transcript into a brief — one LLM call, lossy"
            @click="confirmFull = true"
          >
            full…
          </button>
          <template v-else>
            <button class="risky" :disabled="cBusy" @click="doCompact('full')">⚠ replace transcript</button>
            <button :disabled="cBusy" @click="confirmFull = false">cancel</button>
          </template>
        </div>
        <p v-if="running" class="muted">running — suspend it first to compact</p>
      </div>
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

    <section v-else-if="tab === 'mailbox'" class="pane">
      <MailboxList :member="member" />
    </section>

    <section v-else class="pane">
      <p class="muted">read-only — {{ member }} curates its own notes; only this member can write them.</p>
      <div v-for="f in memory" :key="f.name" class="memfile">
        <div class="fname">{{ f.name }}</div>
        <pre class="fbody">{{ f.content }}</pre>
      </div>
      <p v-if="!memory.length" class="dim">no memory yet</p>
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
.pills {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  flex-wrap: wrap;
}
.gauges {
  display: grid;
  gap: 0.4rem;
  justify-items: start;
}
.io {
  font-family: var(--font-mono);
}
.compact {
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  padding: 0.3rem 0.45rem 0.4rem;
  background: var(--color-surface);
  display: grid;
  gap: 0.3rem;
  width: 100%;
}
.chead {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 0.4rem;
}
.clabel {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.cerr {
  font-size: var(--fs-xs);
  color: var(--color-danger);
  text-align: right;
  word-break: break-word;
}
.copts {
  display: flex;
  gap: 0.25rem;
}
.copts button {
  flex: 1;
  text-align: center;
  background: var(--color-bg);
  border: 1px solid var(--color-line);
  border-radius: var(--r-sm);
  color: var(--color-text);
  cursor: pointer;
  font-size: var(--fs-xs);
  padding: 0.2rem 0.3rem;
  white-space: nowrap;
}
.copts button:hover:not(:disabled) {
  border-color: var(--color-accent);
}
.copts button:disabled {
  opacity: 0.5;
  cursor: default;
}
.copts button.risky:not(:disabled) {
  color: var(--color-warning, #d29922);
}
.memfile {
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  overflow: hidden;
}
.fname {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  background: var(--color-surface);
  padding: 0.25rem 0.5rem;
  border-bottom: 1px solid var(--color-line);
}
.fbody {
  margin: 0;
  padding: 0.4rem 0.5rem;
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  word-break: break-word;
  max-height: 16rem;
  overflow: auto;
}
</style>
