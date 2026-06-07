<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useSpaceStore } from '@/stores/space'
import { useStreamStore } from '@/stores/stream'
import MemberStream from '@/components/stream/MemberStream.vue'
import TurnList from '@/components/stream/TurnList.vue'

const route = useRoute()
const space = useSpaceStore()
const stream = useStreamStore()
const member = computed(() => (route.params.member ? String(route.params.member) : ''))

// Firehose: all members interleaved on one stream, filterable by member.
const excluded = ref<Set<string>>(new Set())
function toggle(name: string) {
  const s = new Set(excluded.value)
  if (s.has(name)) s.delete(name)
  else s.add(name)
  excluded.value = s
}
const nameById = computed<Record<string, string>>(() => {
  const m: Record<string, string> = {}
  for (const r of space.roster) if (r.agentId) m[r.agentId] = r.name
  return m
})
const firehoseTurns = computed(() => {
  if (!excluded.value.size) return stream.turns
  return stream.turns.filter((t) => {
    const name = t.type === 'user' ? t.target : nameById.value[t.agentId]
    return !name || !excluded.value.has(name)
  })
})
</script>

<template>
  <MemberStream v-if="member" :member="member" />
  <div v-else class="firehose">
    <div class="filter">
      <span class="lbl">firehose · filter:</span>
      <button
        v-for="m in space.roster"
        :key="m.name"
        class="chip"
        :class="{ off: excluded.has(m.name) }"
        @click="toggle(m.name)"
      >
        {{ m.name }}
      </button>
    </div>
    <TurnList :turns="firehoseTurns" :show-agent="true" :name-by-id="nameById">
      <template #empty>No team activity yet. Open a member to message it.</template>
    </TurnList>
  </div>
</template>

<style scoped>
.firehose {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.filter {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--sp-2);
  padding-bottom: var(--sp-2);
}
.lbl {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
}
.chip {
  font-size: var(--fs-xs);
  padding: 0.1rem 0.5rem;
  border: 1px solid var(--color-line);
  border-radius: var(--r-pill);
  background: var(--color-surface);
  color: var(--color-text);
  cursor: pointer;
}
.chip.off {
  opacity: 0.4;
  text-decoration: line-through;
}
</style>
