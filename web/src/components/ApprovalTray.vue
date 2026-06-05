<script setup>
// ApprovalTray is the NON-BLOCKING gate surface (RP-4 UX-1b): a floating rail
// that stacks every pending approval/question so the operator can keep watching
// the team while deciding, instead of being locked behind a full-screen modal.
// Same GateCard rendering as the modal; the mode is an operator preference.
import GateCard from './GateCard.vue'

defineProps({
  approvals: { type: Array, default: () => [] },
  questions: { type: Array, default: () => [] },
})
const emit = defineEmits(['permission', 'question'])
</script>

<template>
  <div v-if="approvals.length || questions.length" class="tray">
    <div class="thead">
      <span class="t">Approvals</span>
      <span class="n">{{ approvals.length + questions.length }}</span>
    </div>
    <div class="cards">
      <div v-for="a in approvals" :key="'a-' + a.agentId + a.requestId" class="card">
        <GateCard :approval="a" @permission="emit('permission', $event)" @question="emit('question', $event)" />
      </div>
      <div v-for="q in questions" :key="'q-' + q.agentId + q.requestId" class="card">
        <GateCard :question="q" @permission="emit('permission', $event)" @question="emit('question', $event)" />
      </div>
    </div>
  </div>
</template>

<style scoped>
.tray {
  position: fixed;
  right: 0.8rem;
  bottom: 0.8rem;
  width: min(24rem, 92vw);
  max-height: 78vh;
  display: flex;
  flex-direction: column;
  background: var(--panel);
  border: 1px solid #a855f7;
  border-radius: 10px;
  box-shadow: 0 10px 30px rgba(0, 0, 0, 0.5);
  z-index: 40;
  overflow: hidden;
}
.thead {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.5rem 0.7rem;
  border-bottom: 1px solid var(--line);
  font-weight: 600;
  font-size: 0.8rem;
}
.thead .t {
  color: #d8b4fe;
}
.thead .n {
  margin-left: auto;
  background: #a855f7;
  color: #fff;
  border-radius: 999px;
  padding: 0 0.45rem;
  font-size: 0.7rem;
}
.cards {
  overflow: auto;
  padding: 0.5rem 0.7rem;
}
.card {
  padding: 0.55rem 0;
}
.card + .card {
  border-top: 1px solid var(--line);
}
</style>
