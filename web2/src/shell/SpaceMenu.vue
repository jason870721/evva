<script setup lang="ts">
// The ⚙ space menu — the safe home for destructive lifecycle ops (RP-4 H2),
// each routed through the graded ConfirmDialog (FE-6). halt-all is the largest
// blast radius (whole team) so it requires type-to-confirm.
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useSpacesStore } from '../stores/spaces'
import { errMsg } from '../lib/util'
import ConfirmDialog from '../components/safety/ConfirmDialog.vue'
import SharedSkillsPanel from '../components/compose/SharedSkillsPanel.vue'
import MetricsPanel from '../components/ops/MetricsPanel.vue'

const props = defineProps<{ spaceId: string }>()
const spaces = useSpacesStore()
const router = useRouter()
const open = ref(false)
const err = ref('')
// Space-scoped management panes (read/author, not destructive): the shared
// skill library (RP-26) and the metrics counters (RP-17/22/28).
const showSkills = ref(false)
const showMetrics = ref(false)

interface Confirm {
  title: string
  message: string
  confirmLabel: string
  danger?: boolean
  requireType?: string
  action: () => Promise<void> | void
}
const confirm = ref<Confirm | null>(null)
const sp = computed(() => spaces.byId(props.spaceId))

function ask(c: Confirm) {
  open.value = false
  confirm.value = c
}
async function doConfirm() {
  const a = confirm.value?.action
  confirm.value = null
  if (!a) return
  try {
    await a()
  } catch (e) {
    err.value = errMsg(e)
  }
}
async function run() {
  open.value = false
  try {
    await spaces.run(props.spaceId)
  } catch (e) {
    err.value = errMsg(e)
  }
}
function haltAll() {
  ask({
    title: 'Halt the entire team?',
    message: 'Suspends every member and cancels all in-flight runs. Members come back individually via resume.',
    confirmLabel: 'Halt all',
    danger: true,
    requireType: 'halt',
    action: () => spaces.halt(props.spaceId),
  })
}
function reset() {
  ask({
    title: 'Reset this swarm?',
    message: 'Wipes the task ledger, all messages, and every agent context, then rebuilds under the same id. This cannot be undone.',
    confirmLabel: 'Reset',
    danger: true,
    action: () => spaces.reset(props.spaceId),
  })
}
function stop() {
  ask({
    title: 'Stop this swarm?',
    message: 'Stops all agents. The space is kept and can be started again.',
    confirmLabel: 'Stop',
    action: async () => {
      await spaces.stop(props.spaceId)
      router.push({ name: 'landing' })
    },
  })
}
function remove() {
  ask({
    title: 'Remove this swarm?',
    message: 'Forgets the space entirely (its registration is dropped).',
    confirmLabel: 'Remove',
    danger: true,
    action: async () => {
      await spaces.remove(props.spaceId)
      router.push({ name: 'landing' })
    },
  })
}
</script>

<template>
  <div class="menu-wrap">
    <button class="cog" title="space menu" aria-label="space menu" @click="open = !open">⚙</button>
    <template v-if="open">
      <div class="backdrop" @click="open = false" />
      <ul class="menu">
        <li @click="showSkills = true; open = false">✦ shared skills</li>
        <li @click="showMetrics = true; open = false">📊 metrics</li>
        <li class="sep" aria-hidden="true"></li>
        <li v-if="sp && sp.status === 'stopped'" @click="run()">▶ run</li>
        <li v-else @click="stop()">■ stop</li>
        <li @click="reset()">↺ reset</li>
        <li class="danger" @click="haltAll()">⏻ halt all</li>
        <li class="danger" @click="remove()">🗑 remove</li>
      </ul>
    </template>

    <SharedSkillsPanel v-if="showSkills" @close="showSkills = false" />
    <MetricsPanel v-if="showMetrics" @close="showMetrics = false" />

    <ConfirmDialog
      v-if="confirm"
      :title="confirm.title"
      :message="confirm.message"
      :confirm-label="confirm.confirmLabel"
      :danger="confirm.danger"
      :require-type="confirm.requireType"
      @confirm="doConfirm"
      @cancel="confirm = null"
    />

    <p v-if="err" class="err">{{ err }}</p>
  </div>
</template>

<style scoped>
.menu-wrap {
  position: relative;
}
.cog {
  background: var(--color-surface);
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  color: var(--color-text);
  cursor: pointer;
  padding: 0.2rem 0.5rem;
  font-size: var(--fs-md);
  line-height: 1.2;
}
.cog:hover {
  border-color: var(--color-accent);
}
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 40;
}
.menu {
  position: absolute;
  top: calc(100% + 4px);
  right: 0;
  min-width: 10rem;
  list-style: none;
  margin: 0;
  padding: 0.25rem;
  background: var(--color-surface);
  border: 1px solid var(--color-line-strong);
  border-radius: var(--r-md);
  box-shadow: 0 8px 28px rgba(0, 0, 0, 0.45);
  z-index: 41;
}
.menu li {
  padding: 0.35rem 0.5rem;
  border-radius: var(--r-sm);
  cursor: pointer;
  font-size: var(--fs-sm);
}
.menu li:hover {
  background: var(--color-surface-2);
}
.menu li.danger {
  color: var(--color-danger);
}
.menu li.sep {
  padding: 0;
  margin: 0.2rem 0;
  border-top: 1px solid var(--color-line);
  cursor: default;
}
.menu li.sep:hover {
  background: transparent;
}
.err {
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  font-size: var(--fs-xs);
  color: var(--color-danger);
}
</style>
