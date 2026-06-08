<script setup lang="ts">
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useSpacesStore } from '../stores/spaces'
import EvIcon from '../components/base/EvIcon.vue'

const props = defineProps<{ current?: string }>()
const router = useRouter()
const spaces = useSpacesStore()
const open = ref(false)

const label = computed(() => {
  const s = props.current ? spaces.byId(props.current) : null
  return s?.name || props.current || 'select space'
})

function pick(id: string) {
  open.value = false
  router.push({ name: 'board', params: { spaceId: id } })
}
</script>

<template>
  <div class="switcher">
    <button class="trigger" :class="{ open }" @click="open = !open">
      <span class="lbl">{{ label }}</span>
      <EvIcon name="chevron" :size="14" />
    </button>
    <template v-if="open">
      <div class="backdrop" @click="open = false" />
      <ul class="menu" role="listbox">
        <li
          v-for="s in spaces.running"
          :key="s.id"
          :class="{ cur: s.id === current }"
          role="option"
          @click="pick(s.id)"
        >
          <span class="name">{{ s.name || s.id }}</span>
          <span class="mc">{{ s.members }}m</span>
        </li>
        <li v-if="!spaces.running.length" class="empty">no running spaces</li>
      </ul>
    </template>
  </div>
</template>

<style scoped>
.switcher {
  position: relative;
}
.trigger {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  background: var(--color-surface);
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  padding: 0.25rem 0.55rem;
  color: var(--color-text);
  cursor: pointer;
  font-size: var(--fs-sm);
}
.trigger:hover {
  border-color: var(--color-accent);
}
.lbl {
  font-weight: 600;
}
.backdrop {
  position: fixed;
  inset: 0;
  z-index: 40;
}
.menu {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  min-width: 12rem;
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
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-3);
  padding: 0.35rem 0.5rem;
  border-radius: var(--r-sm);
  cursor: pointer;
  font-size: var(--fs-sm);
}
.menu li:hover {
  background: var(--color-surface-2);
}
.menu li.cur {
  color: var(--color-accent);
}
.mc {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.empty {
  color: var(--color-text-muted);
  cursor: default;
  font-size: var(--fs-xs);
}
</style>
