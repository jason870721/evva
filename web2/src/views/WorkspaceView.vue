<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useSwarm } from '../composables/useSwarm'
import { useSpaceStore } from '../stores/space'
import { useGateStore } from '../stores/gate'
import { useConnectionStore } from '../stores/connection'
import { useUiStore } from '../stores/ui'
import TopBar from '../shell/TopBar.vue'
import AppLayout from '../shell/AppLayout.vue'
import Inspector from '../shell/Inspector.vue'
import AttentionStrip from '../components/attention/AttentionStrip.vue'
import GateLayer from '../components/gates/GateLayer.vue'
import Roster from '../components/roster/Roster.vue'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const space = useSpaceStore()
const gate = useGateStore()
const conn = useConnectionStore()
const ui = useUiStore()
const spaceId = computed(() => String(route.params.spaceId || ''))
const hasInspector = computed(() => !!(route.query.m || route.query.t))
const activeTab = computed(() => {
  const n = String(route.name || '')
  return n === 'stream-member' ? 'stream' : n
})
const tabs = computed(() => [
  { name: 'board', label: t('tabs.board') },
  { name: 'timeline', label: t('tabs.timeline') },
  { name: 'stream', label: t('tabs.stream') },
  { name: 'completed', label: t('tabs.completed') },
])

useSwarm(spaceId)

// Roster click goes straight into the member's live stream (center) and keeps
// the inspector (?m) in sync — focused and selected move together here.
function openMember(name: string) {
  ui.closeRoster()
  router.push({
    name: 'stream-member',
    params: { spaceId: spaceId.value, member: name },
    query: { ...route.query, m: name, t: undefined },
  })
}
function onAttention(name: string) {
  const m = space.roster.find((r) => r.name === name)
  if (m && gate.bringToFront(m.agentId)) return
  openMember(name)
}
</script>

<template>
  <div class="ws">
    <a class="skip" href="#main">Skip to main content</a>
    <TopBar :space-id="spaceId" />

    <div v-if="conn.status !== 'open'" class="wsbanner" role="alert">⚠ {{ t('ws.reconnecting') }}</div>

    <AttentionStrip :items="space.attention" @focus="onAttention" />

    <AppLayout :has-inspector="hasInspector" :drawer-open="ui.rosterDrawer" @close-drawer="ui.closeRoster()">
      <template #left>
        <Roster @select="openMember" />
      </template>
      <template #center>
        <nav class="tabs" aria-label="views">
          <RouterLink
            v-for="tb in tabs"
            :key="tb.name"
            :to="{ name: tb.name, params: { spaceId }, query: route.query }"
            class="tab"
            :class="{ active: activeTab === tb.name }"
          >
            {{ tb.label }}
          </RouterLink>
        </nav>
        <main id="main" class="centerbody"><RouterView /></main>
      </template>
      <template #inspector><Inspector /></template>
    </AppLayout>

    <GateLayer />
  </div>
</template>

<style scoped>
.ws {
  height: 100vh;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.skip {
  position: absolute;
  left: -999px;
  top: 0;
  z-index: 200;
  background: var(--color-accent);
  color: var(--btn-primary-fg);
  padding: 0.3rem 0.6rem;
  border-radius: var(--r-sm);
}
.skip:focus {
  left: 0.5rem;
  top: 0.5rem;
}
.wsbanner {
  background: color-mix(in srgb, var(--status-suspended) 16%, transparent);
  border-bottom: 1px solid color-mix(in srgb, var(--status-suspended) 50%, transparent);
  color: var(--status-suspended);
  padding: var(--sp-1) var(--sp-3);
  font-size: var(--fs-xs);
}
.tabs {
  display: flex;
  gap: var(--sp-1);
  margin-bottom: var(--sp-2);
}
.tab {
  font-size: var(--fs-sm);
  padding: 0.25rem 0.7rem;
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  color: var(--color-text-muted);
}
.tab:hover {
  border-color: var(--color-accent);
}
.tab.active {
  color: var(--color-text);
  border-color: var(--color-accent);
  background: var(--color-surface);
}
.centerbody {
  flex: 1;
  min-height: 0;
}
.centerbody > * {
  height: 100%;
}
</style>
