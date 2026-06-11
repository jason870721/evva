<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useProposalsStore } from '@/stores/proposals'
import { useSpaceStore } from '@/stores/space'
import { splitProposals } from '@/lib/proposals'
import { relTime } from '@/lib/events'
import { agentColor } from '@/lib/colors'
import type { ProposalInfo } from '@/types/wire'
import EvPanel from '@/components/base/EvPanel.vue'
import EvBadge from '@/components/base/EvBadge.vue'
import EvButton from '@/components/base/EvButton.vue'

// The operator's read-only window onto the RP-23 bottom-up work queue: what
// workers filed with task_propose and how the leader ruled. Deciding stays with
// the leader's own tools (proposal_accept/decline) — no operator buttons here,
// by design: the ledger keeps a single writer.
const proposals = useProposalsStore()
const space = useSpaceStore()
const route = useRoute()
const router = useRouter()

const split = computed(() => splitProposals(proposals.list))

function tone(p: ProposalInfo): 'warning' | 'success' | 'neutral' {
  if (p.status === 'open') return 'warning'
  if (p.status === 'accepted') return 'success'
  return 'neutral'
}
// An accepted proposal links to the task it became — open it in the inspector.
function openTask(id: number) {
  router.push({ query: { ...route.query, t: String(id), m: undefined } })
}
</script>

<template>
  <EvPanel :title="`Proposals · ${split.open.length} open`" class="fill">
    <p class="disc">
      Workers file proposals with <code>task_propose</code>; the leader accepts (creating the linked task) or
      declines with a note. This view is read-only — the decision belongs to the leader.
    </p>

    <div class="scroll">
      <section>
        <h3>Open</h3>
        <ul class="list">
          <li v-for="p in split.open" :key="p.id" class="card">
            <div class="l1">
              <span class="id">#{{ p.id }}</span>
              <strong class="title">{{ p.title }}</strong>
              <EvBadge :tone="tone(p)">{{ p.status }}</EvBadge>
            </div>
            <div class="meta">
              <span class="who"><span class="dot" :style="{ background: agentColor(p.proposer) }" />{{ p.proposer }}</span>
              <span v-if="p.suggestedAssignee" class="sugg">→ {{ p.suggestedAssignee }}</span>
              <span class="time">{{ relTime(p.createdAt, space.now) }}</span>
            </div>
            <p v-if="p.spec" class="spec">{{ p.spec }}</p>
          </li>
          <li v-if="!split.open.length" class="dim">no open proposals — workers file them with task_propose</li>
        </ul>
      </section>

      <section>
        <h3>Decided</h3>
        <ul class="list">
          <li v-for="p in split.decided" :key="p.id" class="card decided">
            <div class="l1">
              <span class="id">#{{ p.id }}</span>
              <strong class="title">{{ p.title }}</strong>
              <EvBadge :tone="tone(p)">{{ p.status }}</EvBadge>
            </div>
            <div class="meta">
              <span class="who"><span class="dot" :style="{ background: agentColor(p.proposer) }" />{{ p.proposer }}</span>
              <span v-if="p.decidedBy" class="by">{{ p.status }} by {{ p.decidedBy }}</span>
              <span v-if="p.decidedAt" class="time">{{ relTime(p.decidedAt, space.now) }}</span>
            </div>
            <p v-if="p.decideNote" class="note">{{ p.decideNote }}</p>
            <EvButton v-if="p.refTask" size="sm" @click="openTask(p.refTask)">task #{{ p.refTask }} →</EvButton>
          </li>
          <li v-if="!split.decided.length" class="dim">nothing decided yet</li>
        </ul>
      </section>
    </div>
  </EvPanel>
</template>

<style scoped>
.fill {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.disc {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  margin-bottom: var(--sp-2);
}
.disc code {
  background: var(--color-surface-2);
  padding: 0 0.3rem;
  border-radius: var(--r-sm);
}
.scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: grid;
  gap: var(--sp-4);
  align-content: start;
}
h3 {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--color-text-muted);
  margin: 0 0 var(--sp-2);
}
.list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: var(--sp-2);
}
.card {
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  background: var(--color-bg);
  padding: 0.5rem 0.55rem;
  display: grid;
  gap: 0.35rem;
  justify-items: start;
}
.card.decided {
  opacity: 0.85;
}
.l1 {
  display: flex;
  align-items: center;
  gap: 0.45rem;
}
.id {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.title {
  font-size: var(--fs-sm);
}
.meta {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.who {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
}
.dot {
  width: 0.5rem;
  height: 0.5rem;
  border-radius: 50%;
}
.time {
  font-family: var(--font-mono);
}
.spec,
.note {
  margin: 0;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  white-space: pre-wrap;
  word-break: break-word;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.dim {
  color: var(--color-text-muted);
  font-size: var(--fs-sm);
}
</style>
