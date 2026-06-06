<script setup>
// ApprovalOverlay is the BLOCKING gate surface (default mode): a modal scrim
// around one GateCard at a time (the head of the queue), forcing the operator to
// deal with it. The non-blocking alternative is ApprovalTray. Both share GateCard
// for the actual gate rendering (RP-4 UX-1b).
import { onMounted, onBeforeUnmount } from 'vue'
import GateCard from './GateCard.vue'

const props = defineProps({
  approval: { type: Object, default: null },
  question: { type: Object, default: null },
  pendingCount: { type: Number, default: 0 }, // total queued gates (approvals + questions)
})
const emit = defineEmits(['permission', 'question'])

// Keyboard shortcuts for the blocking modal: A = allow, D = deny the head
// approval (RP-4 UX-3). Questions need an explicit selection, so they have no
// shortcut. Ignored while typing in a field.
function onKey(e) {
  if (!props.approval) return
  const tag = (e.target && e.target.tagName) || ''
  if (tag === 'INPUT' || tag === 'TEXTAREA') return
  const k = e.key.toLowerCase()
  if (k === 'a') {
    e.preventDefault()
    emit('permission', { agent: props.approval.agentId, reqId: props.approval.requestId, behavior: 'allow' })
  } else if (k === 'd') {
    e.preventDefault()
    emit('permission', {
      agent: props.approval.agentId,
      reqId: props.approval.requestId,
      behavior: 'deny',
      reason: 'denied from web',
    })
  }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div v-if="approval || question" class="scrim">
    <div class="modal">
      <div v-if="pendingCount > 1" class="queued">
        {{ pendingCount }} pending — answer one at a time
      </div>
      <GateCard
        :approval="approval"
        :question="question"
        @permission="emit('permission', $event)"
        @question="emit('question', $event)"
      />
      <div v-if="approval" class="kbd">
        <kbd>A</kbd> allow · <kbd>D</kbd> deny
      </div>
    </div>
  </div>
</template>

<style scoped>
.scrim {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 50;
}
.modal {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 1.25rem 1.4rem;
  width: min(36rem, 92vw);
  max-height: 80vh;
  overflow: auto;
}
.queued {
  font-size: 0.72rem;
  color: #a855f7;
  border: 1px solid #a855f7;
  border-radius: 8px;
  padding: 0.2rem 0.5rem;
  margin-bottom: 0.7rem;
  display: inline-block;
}
.kbd {
  margin-top: 0.8rem;
  font-size: 0.72rem;
  color: var(--dim);
}
.kbd kbd {
  font-family: var(--mono);
  background: var(--bg);
  border: 1px solid var(--line);
  border-radius: 4px;
  padding: 0 0.3rem;
  color: #e6edf3;
}
</style>
