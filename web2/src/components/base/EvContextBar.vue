<script setup lang="ts">
import { computed } from 'vue'
import { contextUsage, humanTokens } from '@/lib/events'
import { contextColor } from '@/lib/colors'

// Per-member context-utilization meter — the web twin of evva's TUI CTX bar
// (pkg/ui/.../status.renderContextBar): segmented tally blocks (▰ filled, ▱ rail)
// coloured green→yellow→red by load, with the % beside it. used/limit are the
// member's wire fields; an unknown model window (limit 0) shows a dim rail + —
// instead of a misleading 0%. Shared by the roster card and the focused console
// header so the same agent reads the same gauge in both places.
const props = withDefaults(defineProps<{ used: number; limit: number; label?: string }>(), {
  label: 'CTX',
})

const WIDTH = 12 // matches the TUI's barWidth so the texture reads identically

const u = computed(() => contextUsage({ contextTokens: props.used, contextLimit: props.limit }))
// floor(pct * WIDTH / 100) — same fill math as the TUI (a small-but-nonzero load
// can still read empty, exactly as the terminal meter does).
const filled = computed(() => Math.min(WIDTH, Math.floor((u.value.pct * WIDTH) / 100)))
const fill = computed(() => '▰'.repeat(filled.value))
const rail = computed(() => '▱'.repeat(WIDTH - filled.value))
const color = computed(() => contextColor(u.value.pct))
const pctText = computed(() => `${Math.round(u.value.pct)}%`)
const title = computed(() =>
  u.value.known
    ? `context: ${humanTokens(u.value.used)} / ${humanTokens(u.value.limit)} tokens (${u.value.pct.toFixed(1)}%)`
    : `context: ${humanTokens(u.value.used)} tokens · model window unknown`,
)
</script>

<template>
  <span class="ctx" :title="title">
    <span class="k">{{ label }}</span>
    <span class="bar" aria-hidden="true"><span class="fill" :style="{ color }">{{ fill }}</span><span class="rail">{{ rail }}</span></span>
    <span v-if="u.known" class="pct">{{ pctText }}</span>
    <span v-else class="pct dim">—</span>
  </span>
</template>

<style scoped>
.ctx {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: nowrap;
  line-height: 1;
}
.k {
  color: var(--color-text-muted);
  letter-spacing: 0.04em;
}
.bar {
  letter-spacing: -0.05em; /* tighten the tally so 12 cells stay compact */
}
.rail {
  color: var(--color-line-strong);
}
.pct {
  color: var(--color-text-muted);
  font-variant-numeric: tabular-nums;
}
.pct.dim {
  opacity: 0.7;
}
</style>
