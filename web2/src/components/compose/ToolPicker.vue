<script setup lang="ts">
// Per-tool off / active / deferred selector for the add-agent form. v-model:active
// and v-model:deferred carry the two lists (collaboration tools are role-injected
// server-side, so they aren't in this catalog).
const props = defineProps<{ tools: string[]; active: string[]; deferred: string[] }>()
const emit = defineEmits<{ 'update:active': [v: string[]]; 'update:deferred': [v: string[]] }>()

function stateOf(t: string): 'off' | 'active' | 'deferred' {
  if (props.active.includes(t)) return 'active'
  if (props.deferred.includes(t)) return 'deferred'
  return 'off'
}
function setState(t: string, s: string) {
  const a = props.active.filter((x) => x !== t)
  const d = props.deferred.filter((x) => x !== t)
  if (s === 'active') a.push(t)
  else if (s === 'deferred') d.push(t)
  emit('update:active', a)
  emit('update:deferred', d)
}
</script>

<template>
  <div class="tp">
    <div v-for="t in tools" :key="t" class="row">
      <span class="tn">{{ t }}</span>
      <select :value="stateOf(t)" @change="setState(t, ($event.target as HTMLSelectElement).value)">
        <option value="off">off</option>
        <option value="active">active</option>
        <option value="deferred">deferred</option>
      </select>
    </div>
    <p v-if="!tools.length" class="dim">no tools available</p>
  </div>
</template>

<style scoped>
.tp {
  max-height: 12rem;
  overflow: auto;
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  padding: var(--sp-2);
  display: grid;
  gap: 0.2rem;
}
.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-2);
}
.tn {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}
select {
  font-size: var(--fs-xs);
  padding: 0.1rem 0.3rem;
}
.dim {
  color: var(--color-text-muted);
  font-size: var(--fs-xs);
}
</style>
