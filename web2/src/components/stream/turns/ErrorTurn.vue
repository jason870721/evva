<script setup lang="ts">
import type { ErrorTurn } from '@/lib/events'
defineProps<{ turn: ErrorTurn }>()
</script>

<template>
  <div class="err">⚠ {{ turn.text }}</div>
</template>

<style scoped>
.err {
  color: var(--color-danger);
  border: 1px solid color-mix(in srgb, var(--color-danger) 45%, transparent);
  border-radius: var(--r-sm);
  padding: 0.4rem 0.5rem;
  font-size: var(--fs-sm);
  white-space: pre-wrap;
}
</style>
