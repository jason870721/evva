<script setup lang="ts">
// Non-blocking gate surface: a floating side rail listing the whole queue, so the
// operator can keep watching the team while deciding one at a time (RP-4 UX-1b).
import type { ApprovalVM, QuestionVM } from '@/lib/events'
import type { PermissionReply, QuestionReply } from '@/stores/gate'
import GateCard from './GateCard.vue'

defineProps<{ approvals: ApprovalVM[]; questions: QuestionVM[]; errors: Record<string, string> }>()
const emit = defineEmits<{ permission: [d: PermissionReply]; question: [d: QuestionReply] }>()
</script>

<template>
  <aside class="tray">
    <div class="thead">{{ approvals.length + questions.length }} pending</div>
    <div class="list">
      <div v-for="a in approvals" :key="'a' + a.requestId" class="item">
        <GateCard :approval="a" :error="errors[a.requestId]" @permission="emit('permission', $event)" />
      </div>
      <div v-for="q in questions" :key="'q' + q.requestId" class="item">
        <GateCard :question="q" :error="errors[q.requestId]" @question="emit('question', $event)" />
      </div>
    </div>
  </aside>
</template>

<style scoped>
.tray {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  width: 26rem;
  max-width: 92vw;
  background: var(--color-surface);
  border-left: 1px solid var(--color-line-strong);
  box-shadow: -8px 0 32px rgba(0, 0, 0, 0.45);
  z-index: 110;
  display: flex;
  flex-direction: column;
}
.thead {
  padding: var(--sp-2) var(--sp-3);
  border-bottom: 1px solid var(--color-line);
  font-size: var(--fs-xs);
  font-weight: 600;
  color: var(--phase-waiting);
}
.list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: var(--sp-3);
  display: grid;
  gap: var(--sp-3);
}
.item {
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  padding: var(--sp-3);
  background: var(--color-bg);
}
</style>
