<script setup lang="ts">
// Modal dialog base (FE-1 §8). Scrim + centered panel; Esc and scrim-click close.
// FE-6 (gates/confirm) and FE-7 (forms) build on this; full focus-trap lands in FE-8.
import { onMounted, onBeforeUnmount } from 'vue'

defineProps<{ title?: string; width?: string }>()
const emit = defineEmits<{ close: [] }>()

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') emit('close')
}
onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <Teleport to="body">
    <div class="scrim" @click.self="emit('close')">
      <div class="dialog" :style="{ width: width || '32rem' }" role="dialog" aria-modal="true">
        <header class="dhead">
          <slot name="title"><h3>{{ title }}</h3></slot>
          <button class="x" aria-label="close" @click="emit('close')">✕</button>
        </header>
        <div class="dbody"><slot /></div>
        <footer v-if="$slots.footer" class="dfoot"><slot name="footer" /></footer>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  background: var(--scrim);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--sp-4);
  z-index: 100;
}
.dialog {
  max-width: 92vw;
  max-height: 88vh;
  overflow: auto;
  background: var(--color-surface);
  border: 1px solid var(--color-line-strong);
  border-radius: var(--r-lg);
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.5);
}
.dhead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--sp-3) var(--sp-4);
  border-bottom: 1px solid var(--color-line);
}
.dhead h3 {
  font-size: var(--fs-lg);
}
.x {
  background: transparent;
  border: none;
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: var(--fs-md);
}
.x:hover {
  color: var(--color-text);
}
.dbody {
  padding: var(--sp-4);
}
.dfoot {
  padding: var(--sp-3) var(--sp-4);
  border-top: 1px solid var(--color-line);
  display: flex;
  justify-content: flex-end;
  gap: var(--sp-2);
}
</style>
