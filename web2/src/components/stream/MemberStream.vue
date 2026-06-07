<script setup lang="ts">
import { computed } from 'vue'
import { useSpaceStore } from '@/stores/space'
import { useStreamStore } from '@/stores/stream'
import { displayPhase, phaseClass } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import TurnList from './TurnList.vue'
import Composer from './Composer.vue'
import EvPill from '@/components/base/EvPill.vue'

// Focused single-member console: head identity + live phase pill, the member's
// demuxed turns, and a mail-mode composer. Same component for leader and worker
// (flat comms).
const props = defineProps<{ member: string }>()
const space = useSpaceStore()
const stream = useStreamStore()
const entry = computed(() => space.merged.find((m) => m.name === props.member) || null)
const agentId = computed(() => entry.value?.agentId || '')
const turns = computed(() => stream.forMember(agentId.value, props.member))

function send(text: string) {
  stream.sendMessage(props.member, agentId.value, text)
}
</script>

<template>
  <div class="mstream">
    <header class="head">
      <span class="who" :style="{ color: agentColor(member) }">
        <span class="dot" :style="{ background: agentColor(member) }" />{{ member }}
      </span>
      <span v-if="entry" class="role">{{ entry.role }}</span>
      <EvPill v-if="entry" :tone="phaseClass(entry)" :label="displayPhase(entry) || entry.run" />
      <span v-if="entry?.currentTask" class="task">#{{ entry.currentTask }}</span>
    </header>
    <TurnList :turns="turns">
      <template #empty>No activity yet. Send {{ member }} a message to begin.</template>
    </TurnList>
    <Composer :placeholder="`Message ${member}…`" @send="send" />
  </div>
</template>

<style scoped>
.mstream {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.head {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
  padding-bottom: var(--sp-2);
}
.who {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  font-weight: 600;
}
.dot {
  width: 0.6rem;
  height: 0.6rem;
  border-radius: 50%;
}
.role {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  color: var(--color-text-muted);
}
.task {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
</style>
