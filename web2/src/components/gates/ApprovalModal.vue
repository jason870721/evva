<script setup lang="ts">
// Blocking gate surface (default). Solid scrim forces the operator to deal with
// the head gate; a "N pending" badge shows the queue depth behind it. No close
// affordance — switch to the tray (TopBar toggle) to watch the team while deciding.
import type { ApprovalVM, QuestionVM } from '@/lib/events'
import type { PermissionReply, QuestionReply } from '@/stores/gate'
import GateCard from './GateCard.vue'

defineProps<{ approval: ApprovalVM | null; question: QuestionVM | null; pendingCount: number; error?: string }>()
const emit = defineEmits<{ permission: [d: PermissionReply]; question: [d: QuestionReply] }>()
</script>

<template>
  <div class="scrim">
    <div class="modal">
      <div v-if="pendingCount > 1" class="pending">{{ pendingCount }} pending</div>
      <GateCard
        :approval="approval"
        :question="question"
        :error="error"
        :active="true"
        @permission="emit('permission', $event)"
        @question="emit('question', $event)"
      />
    </div>
  </div>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  background: var(--scrim);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--sp-4);
  z-index: 120;
}
.modal {
  width: 34rem;
  max-width: 92vw;
  max-height: 88vh;
  overflow: auto;
  background: var(--color-surface);
  border: 1px solid var(--color-line-strong);
  border-radius: var(--r-lg);
  padding: var(--sp-4);
  box-shadow: 0 12px 48px rgba(0, 0, 0, 0.5);
}
.pending {
  font-size: var(--fs-xs);
  color: var(--phase-waiting);
  font-weight: 600;
  margin-bottom: var(--sp-2);
}
</style>
