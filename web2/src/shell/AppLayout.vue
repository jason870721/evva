<script setup lang="ts">
// The three-region console grid (LEFT | CENTER | INSPECTOR). Widths come from
// --layout-* tokens (RP-4 H13). On narrow screens the LEFT roster becomes an
// overlay drawer toggled from the TopBar (FE-8 RWD).
defineProps<{ hasInspector?: boolean; drawerOpen?: boolean }>()
const emit = defineEmits<{ closeDrawer: [] }>()
</script>

<template>
  <div class="layout" :class="{ inspect: hasInspector }">
    <aside class="left" :class="{ 'drawer-open': drawerOpen }"><slot name="left" /></aside>
    <div v-if="drawerOpen" class="drawer-scrim" @click="emit('closeDrawer')" />
    <main class="center"><slot name="center" /></main>
    <aside v-if="hasInspector" class="inspector"><slot name="inspector" /></aside>
  </div>
</template>

<style scoped>
.layout {
  flex: 1;
  display: grid;
  grid-template-columns: var(--layout-left) 1fr;
  gap: var(--sp-3);
  min-height: 0;
  padding: var(--sp-3);
}
.layout.inspect {
  grid-template-columns: var(--layout-left) 1fr var(--layout-inspector);
}
.left,
.inspector {
  min-height: 0;
  overflow: auto;
}
.center {
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.drawer-scrim {
  display: none;
}

/* Mid screens: the inspector floats over the workspace instead of taking a column. */
@media (max-width: 1200px) {
  .layout.inspect {
    grid-template-columns: var(--layout-left) 1fr;
  }
  .inspector {
    position: fixed;
    top: 0;
    right: 0;
    bottom: 0;
    width: var(--layout-inspector);
    background: var(--color-surface);
    border-left: 1px solid var(--color-line-strong);
    box-shadow: -8px 0 32px rgba(0, 0, 0, 0.4);
    padding: var(--sp-3);
    z-index: 50;
  }
}
/* Narrow: single column; the roster becomes a toggled overlay drawer. */
@media (max-width: 860px) {
  .layout,
  .layout.inspect {
    grid-template-columns: 1fr;
  }
  .left {
    display: none;
  }
  .left.drawer-open {
    display: block;
    position: fixed;
    top: 0;
    left: 0;
    bottom: 0;
    width: 18rem;
    max-width: 86vw;
    background: var(--color-surface);
    border-right: 1px solid var(--color-line-strong);
    box-shadow: 8px 0 32px rgba(0, 0, 0, 0.4);
    padding: var(--sp-3);
    z-index: 60;
  }
  .drawer-scrim {
    display: block;
    position: fixed;
    inset: 0;
    background: var(--scrim);
    z-index: 55;
  }
}
</style>
