<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { useSessionStore } from '../stores/session'
import { useSpacesStore } from '../stores/spaces'
import { errMsg } from '../lib/util'
import EvButton from '../components/base/EvButton.vue'
import EvPanel from '../components/base/EvPanel.vue'
import EvBadge from '../components/base/EvBadge.vue'
import ConfirmDialog from '../components/safety/ConfirmDialog.vue'
import ThemeToggle from '../shell/ThemeToggle.vue'
import type { SpaceInfo } from '../types/wire'

const router = useRouter()
const session = useSessionStore()
const spaces = useSpacesStore()
const draft = ref('')
const busy = ref('') // space id with a lifecycle call in flight (disables its buttons)

// Graded confirm for the destructive per-card actions (the SpaceMenu pattern).
interface Confirm {
  title: string
  message: string
  confirmLabel: string
  action: () => Promise<void>
}
const confirm = ref<Confirm | null>(null)

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
async function lifecycle(s: SpaceInfo, action: () => Promise<void>) {
  busy.value = s.id
  try {
    await action()
  } catch (e) {
    spaces.error = errMsg(e)
  } finally {
    busy.value = ''
  }
}
const start = (s: SpaceInfo) => lifecycle(s, () => spaces.run(s.id))
const stop = (s: SpaceInfo) => lifecycle(s, () => spaces.stop(s.id))
function askReset(s: SpaceInfo) {
  confirm.value = {
    title: `Reset ${s.name || s.id}?`,
    message:
      'Wipes the task ledger, all messages, and every agent context, then rebuilds under the same id. This cannot be undone.',
    confirmLabel: 'Reset',
    action: () => lifecycle(s, () => spaces.reset(s.id)),
  }
}
function askRemove(s: SpaceInfo) {
  confirm.value = {
    title: `Remove ${s.name || s.id}?`,
    message:
      "Forgets the space entirely (its registration is dropped). The workdir's data stays on disk; register it again to bring it back.",
    confirmLabel: 'Remove',
    action: () => lifecycle(s, () => spaces.remove(s.id)),
  }
}
async function doConfirm() {
  const a = confirm.value?.action
  confirm.value = null
  if (a) await a()
}

onMounted(async () => {
  // Same-machine browsers auto-login via the loopback bootstrap (RP-15).
  if (!session.authed) await session.bootstrap()
  if (!session.authed) return
  await spaces.load()
  // Tokens rotate per service start, so a stored one goes stale on restart —
  // drop it and bootstrap once more before bothering the operator.
  if (spaces.error.includes('unauthorized')) {
    session.disconnect()
    await session.bootstrap()
    if (session.authed) await spaces.load()
  }
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
        On the service's machine this page logs in by itself. From another device, paste the
        session token from the file shown by <code>evva service status</code>.
      </p>
      <div class="row">
        <input v-model="draft" type="password" placeholder="session token" @keyup.enter="connect" />
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
          <div class="head">
            <span class="statusdot" :class="s.status" aria-hidden="true" />
            <span class="name">{{ s.name || s.id }}</span>
            <EvBadge :tone="s.status === 'running' ? 'success' : 'neutral'">{{ s.status }}</EvBadge>
            <EvBadge v-if="s.busy" tone="info">{{ s.busy }} busy</EvBadge>
            <span v-if="s.status === 'running'" class="open">open →</span>
          </div>
          <dl class="facts">
            <template v-if="s.status === 'running'">
              <div class="fact">
                <dt>members</dt>
                <dd>{{ s.members }}</dd>
              </div>
              <div v-if="s.leader" class="fact">
                <dt>leader</dt>
                <dd>👑 {{ s.leader }}</dd>
              </div>
            </template>
            <div class="fact wide">
              <dt>workdir</dt>
              <dd class="mono" :title="s.workdir">{{ s.workdir }}</dd>
            </div>
            <div class="fact wide">
              <dt>id</dt>
              <dd class="mono faint">{{ s.id }}</dd>
            </div>
          </dl>
          <div class="btnrow" @click.stop>
            <EvButton
              v-if="s.status === 'stopped'"
              size="sm"
              variant="primary"
              :loading="busy === s.id"
              @click="start(s)"
            >
              ▶ run
            </EvButton>
            <EvButton v-else size="sm" :loading="busy === s.id" @click="stop(s)">■ stop</EvButton>
            <EvButton size="sm" :disabled="busy === s.id" @click="askReset(s)">↺ reset</EvButton>
            <EvButton size="sm" variant="danger" :disabled="busy === s.id" @click="askRemove(s)">
              🗑 remove
            </EvButton>
          </div>
        </li>
      </ul>
    </template>

    <ConfirmDialog
      v-if="confirm"
      :title="confirm.title"
      :message="confirm.message"
      :confirm-label="confirm.confirmLabel"
      :danger="true"
      @confirm="doConfirm"
      @cancel="confirm = null"
    />
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
  height: 7rem;
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
  transition: border-color 120ms ease, box-shadow 120ms ease;
}
.spaces li:hover {
  border-color: var(--color-accent);
  box-shadow: 0 4px 18px rgba(0, 0, 0, 0.18);
}
.spaces li.stopped {
  opacity: 0.72;
  cursor: default;
}
.spaces li.stopped:hover {
  border-color: var(--color-line);
  box-shadow: none;
}
.head {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
}
.statusdot {
  width: 0.55rem;
  height: 0.55rem;
  border-radius: 50%;
  flex: none;
}
.statusdot.running {
  background: var(--color-success, #3fb950);
  box-shadow: 0 0 6px color-mix(in srgb, var(--color-success, #3fb950) 60%, transparent);
}
.statusdot.stopped {
  background: var(--color-text-faint);
}
.name {
  font-weight: 600;
}
.open {
  margin-left: auto;
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
  opacity: 0;
  transition: opacity 120ms ease;
}
.spaces li:hover .open {
  opacity: 1;
  color: var(--color-accent);
}
.facts {
  margin: var(--sp-2) 0 0;
  display: flex;
  flex-wrap: wrap;
  column-gap: var(--sp-5);
  row-gap: 0.2rem;
}
.fact {
  display: flex;
  align-items: baseline;
  gap: 0.45rem;
  min-width: 0;
}
.fact.wide {
  flex-basis: 100%;
}
.fact dt {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--color-text-faint);
  flex: none;
}
.fact dd {
  margin: 0;
  font-size: var(--fs-sm);
  color: var(--color-text-muted);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.mono {
  font-family: var(--font-mono);
}
.faint {
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
}
.btnrow {
  display: flex;
  gap: var(--sp-2);
  margin-top: var(--sp-3);
}
code {
  background: var(--color-surface-2);
  padding: 0.05rem 0.3rem;
  border-radius: var(--r-sm);
}
</style>
