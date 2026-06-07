<script setup lang="ts">
defineProps<{ title?: string }>()
</script>

<template>
  <section class="panel">
    <header v-if="title || $slots.header" class="head">
      <slot name="header"><span class="title">{{ title }}</span></slot>
    </header>
    <div class="body"><slot /></div>
  </section>
</template>

<style scoped>
.panel {
  background: var(--panel-bg);
  border: 1px solid var(--panel-border);
  border-radius: var(--panel-radius);
  overflow: hidden;
}
.head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--sp-2) var(--sp-3);
  border-bottom: 1px solid var(--color-line);
}
.title {
  font-weight: 600;
  font-size: var(--fs-sm);
}
.body {
  padding: var(--sp-3);
}
</style>
