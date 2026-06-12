<script setup lang="ts">
import { ref } from 'vue'
import type { MemberInfo } from '@/types/wire'
import { displayPhase, phaseClass, elapsed } from '@/lib/events'
import { describeCron } from '@/lib/cron'
import { agentColor } from '@/lib/colors'
import EvPill from '@/components/base/EvPill.vue'
import EvBadge from '@/components/base/EvBadge.vue'
import EvContextBar from '@/components/base/EvContextBar.vue'

// Calm resting card (RP-4 H10): identity / role / phase / task / schedule always
// visible; controls hidden behind a ⋯ menu. The leader is never removable.
defineProps<{ member: MemberInfo; selected: boolean; now: number }>()
const emit = defineEmits<{
  select: []
  freeze: []
  unfreeze: []
  suspend: []
  resume: []
  schedule: []
  skills: []
  clear: []
  remove: []
}>()
const menu = ref(false)

// Only a non-default stance earns a chip — the calm card stays calm for the
// common case, and "bypass" (fully autonomous) reads as the caution it is.
function permTone(mode: string): 'warning' | 'info' {
  return mode === 'bypass' ? 'warning' : 'info'
}
</script>

<template>
  <li class="card" :class="{ sel: selected }" @click="emit('select')">
    <div class="l1">
      <span class="name"><span class="dot" :style="{ background: agentColor(member.name) }" />{{ member.name }}</span>
      <span class="role" :class="member.role">{{ member.role }}</span>
      <button class="more" aria-label="member actions" @click.stop="menu = !menu">⋯</button>
    </div>
    <div class="l2">
      <EvBadge v-if="member.membership !== 'active'" :tone="member.membership === 'frozen' ? 'frozen' : 'warning'">
        {{ member.membership }}
      </EvBadge>
      <EvPill :tone="phaseClass(member)" :label="displayPhase(member) || member.run" />
      <EvBadge v-if="member.permissionMode && member.permissionMode !== 'default'" :tone="permTone(member.permissionMode)">
        {{ member.permissionMode }}
      </EvBadge>
      <span v-if="member.phaseSince && phaseClass(member) !== 'idle'" class="since">{{ elapsed(member.phaseSince, now) }}</span>
      <span v-if="member.currentTask" class="task">#{{ member.currentTask }}</span>
    </div>
    <div class="l3"><EvContextBar :used="member.contextTokens" :limit="member.contextLimit" /></div>
    <div v-if="member.tokensBudget" class="l3">
      <EvContextBar :used="member.tokensToday || 0" :limit="member.tokensBudget" label="BDG" noun="tokens today" />
    </div>
    <div v-if="member.cron" class="sched" :title="member.schedulePrompt">⏰ {{ describeCron(member.cron) }}</div>

    <div v-if="menu" class="menu" @click.stop>
      <button v-if="member.membership === 'active'" @click="emit('freeze'); menu = false">❄ freeze</button>
      <button v-else @click="emit('unfreeze'); menu = false">▶ unfreeze</button>
      <button v-if="member.run === 'busy'" @click="emit('suspend'); menu = false">⏸ suspend</button>
      <button v-else-if="member.run === 'suspended'" @click="emit('resume'); menu = false">▶ resume</button>
      <button @click="emit('schedule'); menu = false">⏰ schedule</button>
      <button @click="emit('skills'); menu = false">✦ skills</button>
      <button class="rm" @click="emit('clear'); menu = false">🧹 clear session</button>
      <button v-if="member.role !== 'leader'" class="rm" @click="emit('remove'); menu = false">🗑 remove</button>
    </div>
  </li>
</template>

<style scoped>
.card {
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  background: var(--color-bg);
  padding: 0.5rem 0.55rem;
  cursor: pointer;
}
.card:hover,
.card.sel {
  border-color: var(--color-accent);
}
.l1 {
  display: flex;
  align-items: center;
  gap: 0.4rem;
}
.name {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  font-weight: 600;
  font-size: var(--fs-sm);
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
.role.leader {
  color: var(--color-accent);
}
.more {
  margin-left: auto;
  background: transparent;
  border: none;
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: var(--fs-md);
  line-height: 1;
  padding: 0 0.2rem;
}
.more:hover {
  color: var(--color-text);
}
.l2 {
  display: flex;
  align-items: center;
  gap: 0.35rem;
  margin-top: 0.35rem;
}
.l3 {
  margin-top: 0.35rem;
}
.since,
.task {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.sched {
  margin-top: 0.35rem;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.menu {
  margin-top: 0.45rem;
  display: grid;
  gap: 0.2rem;
}
.menu button {
  text-align: left;
  background: var(--color-surface);
  border: 1px solid var(--color-line);
  border-radius: var(--r-sm);
  color: var(--color-text);
  cursor: pointer;
  font-size: var(--fs-xs);
  padding: 0.2rem 0.45rem;
}
.menu button:hover {
  border-color: var(--color-accent);
}
.menu .rm {
  color: var(--color-danger);
  border-color: color-mix(in srgb, var(--color-danger) 45%, transparent);
}
</style>
