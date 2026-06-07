<script setup lang="ts">
defineProps<{ size?: number }>()
</script>

<template>
  <span class="spinner" :style="{ width: (size || 16) + 'px', height: (size || 16) + 'px' }" aria-hidden="true" />
</template>

<style scoped>
.spinner {
  display: inline-block;
  border: 2px solid color-mix(in srgb, var(--phase-thinking) 28%, transparent);
  border-top-color: var(--phase-thinking);
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}
@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}
</style>
