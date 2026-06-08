<script setup lang="ts">
// Minimal inline-SVG icon set (FE-1 §8). Stroke = currentColor so icons inherit
// text colour. The markup constants are fixed/trusted (no user input), so v-html
// is safe here. FE-2+ can grow the set.
import { computed } from 'vue'

const props = defineProps<{ name: string; size?: number }>()

const PATHS: Record<string, string> = {
  check: '<path d="M5 13l4 4L19 7"/>',
  close: '<path d="M6 6l12 12M18 6L6 18"/>',
  play: '<path d="M8 5l11 7-11 7z"/>',
  pause: '<path d="M9 5v14M15 5v14"/>',
  square: '<rect x="6" y="6" width="12" height="12" rx="1"/>',
  clock: '<circle cx="12" cy="12" r="8"/><path d="M12 8v4l3 2"/>',
  shield: '<path d="M12 3l7 3v6c0 4-3 7-7 9-4-2-7-5-7-9V6z"/>',
  bolt: '<path d="M13 3L4 14h6l-1 7 9-11h-6z"/>',
  dots: '<circle cx="6" cy="12" r="1.4"/><circle cx="12" cy="12" r="1.4"/><circle cx="18" cy="12" r="1.4"/>',
  warning: '<path d="M12 4l9 16H3z"/><path d="M12 10v4"/><path d="M12 17v.5"/>',
  snowflake: '<path d="M12 3v18M4.5 7.5l15 9M19.5 7.5l-15 9"/>',
  chevron: '<path d="M9 6l6 6-6 6"/>',
  refresh: '<path d="M20 11a8 8 0 0 0-14-5L4 8M4 13a8 8 0 0 0 14 5l2-2"/>',
}

const inner = computed(() => PATHS[props.name] || PATHS.dots)
const px = computed(() => (props.size || 16) + 'px')
</script>

<template>
  <svg
    class="icon"
    :style="{ width: px, height: px }"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    stroke-width="2"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
    v-html="inner"
  />
</template>

<style scoped>
.icon {
  display: inline-block;
  vertical-align: -0.15em;
  flex: none;
}
</style>
