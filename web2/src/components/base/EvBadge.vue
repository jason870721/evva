<script setup lang="ts">
// Small status chip. `tone` selects a semantic colour; content is the slot.
defineProps<{
  tone?: 'neutral' | 'accent' | 'success' | 'danger' | 'warning' | 'info' | 'frozen'
}>()
</script>

<template>
  <span class="badge" :class="tone || 'neutral'">
    <slot />
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  font-size: var(--fs-xs);
  padding: 0.05rem 0.4rem;
  border-radius: var(--r-pill);
  border: 1px solid var(--color-line);
  color: var(--color-text-muted);
}
.accent {
  color: var(--color-accent);
  border-color: color-mix(in srgb, var(--color-accent) 45%, transparent);
}
.success {
  color: var(--color-success);
  border-color: color-mix(in srgb, var(--color-success) 45%, transparent);
}
.danger {
  color: var(--color-danger);
  border-color: color-mix(in srgb, var(--color-danger) 45%, transparent);
}
.warning {
  color: var(--color-warning);
  border-color: color-mix(in srgb, var(--color-warning) 45%, transparent);
}
.info {
  color: var(--color-info);
  border-color: color-mix(in srgb, var(--color-info) 45%, transparent);
}
.frozen {
  color: var(--color-tool-result);
  border-color: color-mix(in srgb, var(--color-tool-result) 45%, transparent);
}
</style>
