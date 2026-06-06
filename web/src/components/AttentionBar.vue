<script setup>
// AttentionBar (RP-4 UX-1): the always-visible "what needs me?" strip above the
// workspace. It aggregates the members the operator should act on — blocked on
// approval/question ('act'), or errored/paused ('warn') — each as a clickable
// chip that focuses the member, with a live elapsed clock so a long stall is
// obvious. Quiet (a single "all clear") when nothing needs attention, so it
// earns its permanent place without adding noise.
defineProps({
  items: { type: Array, default: () => [] }, // from events.attentionItems(roster, now)
})
const emit = defineEmits(['focus'])
</script>

<template>
  <div class="attn" :class="{ quiet: !items.length }" role="status">
    <template v-if="items.length">
      <span class="lead">{{ items.length }} need{{ items.length === 1 ? 's' : '' }} you</span>
      <button
        v-for="it in items"
        :key="it.name + it.phase"
        :class="['chip', it.kind]"
        @click="emit('focus', it.name)"
        :title="`focus ${it.name}`"
      >
        <span class="glyph">{{ it.kind === 'act' ? '⏳' : '⚠' }}</span>
        <span class="who">{{ it.name }}</span>
        <span class="what">{{ it.phase }}<template v-if="it.tool">:{{ it.tool }}</template></span>
        <span v-if="it.elapsed" class="since">{{ it.elapsed }}</span>
      </button>
    </template>
    <span v-else class="clear">✓ all clear</span>
  </div>
</template>

<style scoped>
.attn {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  flex-wrap: wrap;
  padding: 0.4rem 0.55rem;
  border: 1px solid var(--line);
  border-radius: 8px;
  background: var(--panel);
  margin-bottom: 0.6rem;
  min-height: 2.1rem;
}
.attn.quiet {
  border-style: dashed;
  opacity: 0.7;
}
.lead {
  font-size: 0.74rem;
  font-weight: 600;
  color: #a855f7;
  margin-right: 0.2rem;
}
.clear {
  font-size: 0.74rem;
  color: var(--dim);
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.72rem;
  padding: 0.12rem 0.5rem;
  border-radius: 999px;
  border: 1px solid var(--line);
  background: var(--bg);
  cursor: pointer;
}
.chip:hover {
  border-color: var(--accent);
}
.chip.act {
  border-color: #a855f7;
  color: #d8b4fe;
}
.chip.warn {
  border-color: var(--danger);
  color: #fca5a5;
}
.chip .who {
  font-weight: 600;
}
.chip .what {
  color: var(--dim);
  font-family: var(--mono);
}
.chip .since {
  font-family: var(--mono);
  color: var(--dim);
}
</style>
