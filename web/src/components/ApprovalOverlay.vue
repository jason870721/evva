<script setup>
import { ref, watch } from 'vue'

const props = defineProps({
  approval: { type: Object, default: null }, // {agentId, requestId, tool, description, reason, risk, plan}
  question: { type: Object, default: null }, // {agentId, requestId, questions:[{Question,Header,Options,MultiSelect}]}
})
const emit = defineEmits(['permission', 'question'])

// answers maps question text -> chosen option label (single-select MVP).
const answers = ref({})
watch(
  () => props.question,
  (q) => {
    answers.value = {}
    if (q) for (const item of q.questions) answers.value[item.Question] = ''
  },
)

function allow() {
  emit('permission', { agent: props.approval.agentId, reqId: props.approval.requestId, behavior: 'allow' })
}
function deny() {
  emit('permission', {
    agent: props.approval.agentId,
    reqId: props.approval.requestId,
    behavior: 'deny',
    reason: 'denied from web',
  })
}
function submitAnswers() {
  emit('question', {
    agent: props.question.agentId,
    reqId: props.question.requestId,
    answers: { ...answers.value },
  })
}
</script>

<template>
  <div v-if="approval || question" class="scrim">
    <div class="modal">
      <template v-if="approval">
        <h3>Permission requested</h3>
        <div class="tool">
          <code>{{ approval.tool }}</code>
          <span v-if="approval.risk" class="risk">{{ approval.risk }}</span>
          <span class="agent">{{ approval.agentId }}</span>
        </div>
        <p v-if="approval.description" class="desc">{{ approval.description }}</p>
        <p v-if="approval.reason" class="reason">{{ approval.reason }}</p>
        <pre v-if="approval.plan" class="plan">{{ approval.plan }}</pre>
        <div class="row">
          <button class="primary" @click="allow">Allow</button>
          <button class="danger" @click="deny">Deny</button>
        </div>
      </template>

      <template v-else-if="question">
        <h3>Question</h3>
        <div v-for="item in question.questions" :key="item.Question" class="q">
          <div class="qtext">{{ item.Question }}</div>
          <label v-for="opt in item.Options" :key="opt.Label" class="opt">
            <input type="radio" :value="opt.Label" v-model="answers[item.Question]" />
            <span>{{ opt.Label }}</span>
            <span v-if="opt.Description" class="odesc">— {{ opt.Description }}</span>
          </label>
        </div>
        <div class="row">
          <button class="primary" @click="submitAnswers">Submit</button>
        </div>
      </template>
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
h3 {
  margin: 0 0 0.75rem;
}
.tool {
  display: flex;
  gap: 0.5rem;
  align-items: center;
}
.risk {
  font-size: 0.7rem;
  color: var(--danger);
  border: 1px solid var(--danger);
  border-radius: 8px;
  padding: 0 0.35rem;
}
.agent {
  margin-left: auto;
  font-family: var(--mono);
  font-size: 0.72rem;
  color: var(--dim);
}
.desc {
  font-family: var(--mono);
  font-size: 0.85rem;
}
.reason {
  color: var(--dim);
  font-size: 0.85rem;
}
.plan {
  background: var(--bg);
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.6rem;
  font-size: 0.78rem;
  white-space: pre-wrap;
}
.row {
  display: flex;
  gap: 0.6rem;
  margin-top: 1rem;
}
.q {
  margin-bottom: 0.8rem;
}
.qtext {
  font-weight: 600;
  margin-bottom: 0.35rem;
}
.opt {
  display: flex;
  gap: 0.4rem;
  align-items: baseline;
  padding: 0.2rem 0;
  cursor: pointer;
}
.odesc {
  color: var(--dim);
  font-size: 0.8rem;
}
.primary {
  background: var(--accent);
  color: #fff;
  border-color: var(--accent);
}
.danger {
  color: var(--danger);
  border-color: var(--danger);
}
</style>
