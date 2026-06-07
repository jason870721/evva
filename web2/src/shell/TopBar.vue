<script setup lang="ts">
import { useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useSessionStore } from '../stores/session'
import { useSpacesStore } from '../stores/spaces'
import { useConnectionStore } from '../stores/connection'
import { useUiStore } from '../stores/ui'
import { setLocale, type Locale } from '../lib/i18n'
import SpaceSwitcher from './SpaceSwitcher.vue'
import ThemeToggle from './ThemeToggle.vue'
import SpaceMenu from './SpaceMenu.vue'
import EvButton from '../components/base/EvButton.vue'
import EvIcon from '../components/base/EvIcon.vue'

defineProps<{ spaceId?: string }>()
const router = useRouter()
const session = useSessionStore()
const spaces = useSpacesStore()
const conn = useConnectionStore()
const ui = useUiStore()
const { t, locale } = useI18n()

function logout() {
  session.disconnect()
  router.push({ name: 'landing' })
}
</script>

<template>
  <header class="topbar">
    <div class="left">
      <button v-if="spaceId" class="ham" :aria-label="t('common.members')" @click="ui.toggleRoster()">☰</button>
      <RouterLink to="/" class="brand">
        <span class="logo">evva</span><span class="sep">·</span><span>swarm</span>
      </RouterLink>
      <SpaceSwitcher v-if="spaceId" :current="spaceId" />
    </div>
    <div class="right">
      <span v-if="spaceId" class="conn" :title="`live connection: ${conn.status}`">
        <span class="cdot" :class="conn.status" />{{ conn.status }}
      </span>
      <EvButton size="sm" :title="t('common.refresh')" @click="spaces.load()"><EvIcon name="refresh" :size="14" /></EvButton>
      <select class="loc" :value="locale" :title="'language'" @change="setLocale(($event.target as HTMLSelectElement).value as Locale)">
        <option value="zh-Hant">中</option>
        <option value="en">EN</option>
      </select>
      <ThemeToggle />
      <EvButton
        v-if="spaceId"
        size="sm"
        :title="ui.gateMode === 'modal' ? 'Approvals block the screen — switch to a side tray' : 'Approvals show in a side tray — switch to a blocking modal'"
        @click="ui.setGateMode(ui.gateMode === 'modal' ? 'tray' : 'modal')"
      >
        approvals: {{ ui.gateMode }}
      </EvButton>
      <SpaceMenu v-if="spaceId" :space-id="spaceId" />
      <EvButton size="sm" @click="logout">{{ t('common.logout') }}</EvButton>
    </div>
  </header>
</template>

<style scoped>
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: var(--sp-2) var(--sp-3);
  border-bottom: 1px solid var(--color-line);
  background: var(--color-surface);
}
.left,
.right {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
}
.ham {
  display: none;
  background: transparent;
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  color: var(--color-text);
  cursor: pointer;
  padding: 0.1rem 0.45rem;
  font-size: var(--fs-md);
}
.brand {
  display: inline-flex;
  align-items: baseline;
  gap: 0.25rem;
  font-weight: 700;
  font-size: var(--fs-lg);
}
.logo {
  color: var(--color-accent);
}
.sep {
  color: var(--color-accent-2);
}
.conn {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
}
.cdot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
  background: var(--color-text-muted);
}
.cdot.open {
  background: var(--status-completed);
}
.cdot.connecting {
  background: var(--status-suspended);
}
.cdot.closed {
  background: var(--color-danger);
}
.loc {
  font-size: var(--fs-xs);
  padding: 0.15rem 0.3rem;
}
@media (max-width: 860px) {
  .ham {
    display: inline-block;
  }
  /* keep the bar from overflowing on small screens */
  .conn {
    display: none;
  }
}
</style>
