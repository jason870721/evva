<script setup lang="ts">
// Design-system button. Reads only semantic/component tokens — never raw colour.
defineProps<{
  variant?: 'primary' | 'ghost' | 'danger'
  size?: 'sm' | 'md'
  loading?: boolean
  disabled?: boolean
}>()
</script>

<template>
  <button
    class="btn"
    :class="[variant || 'ghost', size || 'md', { 'is-loading': loading }]"
    :disabled="disabled || loading"
  >
    <span v-if="loading" class="sp" aria-hidden="true" />
    <slot />
  </button>
</template>

<style scoped>
.btn {
  display: inline-flex;
  align-items: center;
  gap: var(--sp-2);
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
  transition: border-color var(--dur-fast) var(--ease-out), background var(--dur-fast) var(--ease-out);
}
.btn.md {
  padding: 0.35rem 0.7rem;
  font-size: var(--fs-sm);
}
.btn.sm {
  padding: 0.15rem 0.5rem;
  font-size: var(--fs-xs);
}
.btn:hover:not(:disabled) {
  border-color: var(--color-accent);
}
.btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
.btn.ghost {
  background: transparent;
}
.btn.primary {
  background: var(--btn-primary-bg);
  border-color: var(--btn-primary-bg);
  color: var(--btn-primary-fg);
  font-weight: 600;
}
.btn.danger {
  background: transparent;
  color: var(--btn-danger-fg);
  border-color: var(--color-danger);
}
.sp {
  width: 0.8em;
  height: 0.8em;
  border: 2px solid currentColor;
  border-right-color: transparent;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
