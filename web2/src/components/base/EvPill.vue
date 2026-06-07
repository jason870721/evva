<script setup lang="ts">
// Phase / status pill. Dual-encodes state as glyph (shape) + text + colour, so it
// stays legible without colour (a11y, RP-4 H5). `tone` maps to a semantic colour
// class; the glyph defaults per tone but can be overridden.
import { computed } from 'vue'

const props = defineProps<{
  tone?: string
  label: string
  glyph?: string
  title?: string
}>()

const GLYPHS: Record<string, string> = {
  idle: '○',
  ready: '○',
  thinking: '◇',
  busy: '▣',
  executing: '▶',
  waiting: '⏳',
  error: '✕',
  suspended: '⏸',
  pending: '○',
  running: '▶',
  verifying: '◆',
  completed: '✓',
  accent: '◆',
}

const glyph = computed(() => props.glyph ?? GLYPHS[props.tone || 'idle'] ?? '•')
</script>

<template>
  <span class="pill" :class="'t-' + (tone || 'idle')" :title="title || label">
    <span class="g" aria-hidden="true">{{ glyph }}</span>
    <span class="l">{{ label }}</span>
  </span>
</template>

<style scoped>
.pill {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  font-size: var(--fs-xs);
  padding: 0.05rem 0.45rem;
  border-radius: var(--r-pill);
  border: 1px solid var(--color-line);
  color: var(--color-text-muted);
  white-space: nowrap;
}
.g {
  font-size: 0.9em;
  line-height: 1;
}
.t-idle,
.t-ready {
  color: var(--phase-idle);
}
.t-thinking {
  color: var(--phase-thinking);
  border-color: color-mix(in srgb, var(--phase-thinking) 40%, transparent);
}
.t-executing {
  color: var(--phase-executing);
  border-color: color-mix(in srgb, var(--phase-executing) 40%, transparent);
}
.t-busy {
  color: var(--color-warning);
  border-color: color-mix(in srgb, var(--color-warning) 40%, transparent);
}
.t-waiting {
  color: var(--phase-waiting);
  border-color: var(--phase-waiting);
  font-weight: 600;
}
.t-error {
  color: var(--phase-error);
  border-color: color-mix(in srgb, var(--phase-error) 45%, transparent);
}
.t-suspended {
  color: var(--color-danger);
  border-color: color-mix(in srgb, var(--color-danger) 45%, transparent);
}
.t-pending {
  color: var(--status-pending);
}
.t-running {
  color: var(--status-running);
  border-color: color-mix(in srgb, var(--status-running) 40%, transparent);
}
.t-verifying {
  color: var(--status-verifying);
  border-color: color-mix(in srgb, var(--status-verifying) 40%, transparent);
}
.t-completed {
  color: var(--status-completed);
  border-color: color-mix(in srgb, var(--status-completed) 40%, transparent);
}
.t-accent {
  color: var(--color-accent);
  border-color: color-mix(in srgb, var(--color-accent) 40%, transparent);
}
</style>
