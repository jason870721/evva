<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useSessionStore } from '../stores/session'
import { useSpacesStore } from '../stores/spaces'
import { errMsg } from '../lib/util'
import EvButton from '../components/base/EvButton.vue'
import EvPanel from '../components/base/EvPanel.vue'
import EvBadge from '../components/base/EvBadge.vue'
import ThemeToggle from '../shell/ThemeToggle.vue'
import type { SpaceInfo } from '../types/wire'

const router = useRouter()
const session = useSessionStore()
const spaces = useSpacesStore()
const draft = ref('')
const busy = ref('')

function connect() {
  const t = draft.value.trim()
  if (!t) return
  session.connect(t)
  spaces.load()
}
function changeToken() {
  session.disconnect()
  spaces.list = []
}
function enter(s: SpaceInfo) {
  if (s.status === 'stopped') return
  router.push({ name: 'board', params: { spaceId: s.id } })
}
async function start(s: SpaceInfo) {
  busy.value = s.id
  try {
    await spaces.run(s.id)
  } catch (e) {
    spaces.error = errMsg(e)
  } finally {
    busy.value = ''
  }
}

onMounted(() => {
  if (session.authed) spaces.load()
})
</script>

<template>
  <div class="landing">
    <header class="lhead">
      <h1><span class="logo">evva</span><span class="sep">·</span><span>swarm</span></h1>
      <div class="actions">
        <ThemeToggle />
        <template v-if="session.authed">
          <EvButton size="sm" @click="spaces.load()">↻ refresh</EvButton>
          <EvButton size="sm" @click="changeToken">change token</EvButton>
        </template>
      </div>
    </header>

    <EvPanel v-if="!session.authed" title="Connect" class="gate">
      <p class="dim">
        Enter the session token printed by <code>evva service start</code>. Default in dev: <code>root</code>.
      </p>
      <div class="row">
        <input v-model="draft" type="password" placeholder="session token (default: root)" @keyup.enter="connect" />
        <EvButton variant="primary" @click="connect">Connect</EvButton>
      </div>
    </EvPanel>

    <template v-else>
      <p v-if="spaces.error" class="err">{{ spaces.error }}</p>
      <div v-if="spaces.loading && !spaces.list.length" class="skel" aria-hidden="true">
        <div v-for="n in 3" :key="n" class="skelcard" />
      </div>
      <p v-if="!spaces.list.length && !spaces.loading" class="dim empty">
        No swarms registered. Start one with <code>evva swarm .</code> in a directory with an
        <code>evva-swarm.yml</code>.
      </p>
      <ul class="spaces">
        <li
          v-for="s in spaces.list"
          :key="s.id"
          :class="{ stopped: s.status === 'stopped' }"
          @click="enter(s)"
        >
          <div class="name">
            {{ s.name || s.id }}
            <EvBadge :tone="s.status === 'running' ? 'success' : 'neutral'">{{ s.status }}</EvBadge>
          </div>
          <div class="meta">
            <span>{{ s.members }} member{{ s.members === 1 ? '' : 's' }}</span>
            <span class="path">{{ s.workdir }}</span>
          </div>
          <div class="id">{{ s.id }}</div>
          <div v-if="s.status === 'stopped'" class="startrow" @click.stop>
            <EvButton size="sm" :loading="busy === s.id" @click="start(s)">▶ start</EvButton>
          </div>
        </li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.landing {
  max-width: 48rem;
  margin: 0 auto;
  padding: var(--sp-6) var(--sp-4);
}
.lhead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--sp-5);
}
h1 {
  font-size: var(--fs-xl);
}
.logo {
  color: var(--color-accent);
}
.sep {
  color: var(--color-accent-2);
}
.actions {
  display: flex;
  gap: var(--sp-2);
}
.gate .row {
  display: flex;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
}
.gate input {
  flex: 1;
}
.dim {
  color: var(--color-text-muted);
}
.empty {
  margin-top: var(--sp-4);
}
.skel {
  margin-top: var(--sp-4);
  display: grid;
  gap: var(--sp-3);
}
.skelcard {
  height: 4.5rem;
  border-radius: var(--r-lg);
  background: linear-gradient(90deg, var(--color-surface) 25%, var(--color-surface-2) 50%, var(--color-surface) 75%);
  background-size: 200% 100%;
  animation: shimmer 1.3s linear infinite;
}
@keyframes shimmer {
  to {
    background-position: -200% 0;
  }
}
.err {
  color: var(--color-danger);
  font-size: var(--fs-sm);
}
.spaces {
  list-style: none;
  padding: 0;
  margin-top: var(--sp-4);
  display: grid;
  gap: var(--sp-3);
}
.spaces li {
  border: 1px solid var(--color-line);
  border-radius: var(--r-lg);
  padding: var(--sp-3) var(--sp-4);
  cursor: pointer;
  background: var(--color-surface);
}
.spaces li:hover {
  border-color: var(--color-accent);
}
.spaces li.stopped {
  opacity: 0.65;
  cursor: default;
}
.spaces li.stopped:hover {
  border-color: var(--color-line);
}
.name {
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: var(--sp-2);
}
.meta {
  display: flex;
  gap: var(--sp-4);
  color: var(--color-text-muted);
  font-size: var(--fs-sm);
  margin-top: 0.2rem;
}
.path {
  font-family: var(--font-mono);
}
.id {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
  margin-top: 0.35rem;
}
.startrow {
  margin-top: var(--sp-2);
}
code {
  background: var(--color-surface-2);
  padding: 0.05rem 0.3rem;
  border-radius: var(--r-sm);
}
</style>
