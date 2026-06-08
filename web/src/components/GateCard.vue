<script setup>
// GateCard renders ONE pending gate — an approval or a question — with its
// actions, and emits the operator's reply. It is self-contained (owns its own
// question-answer state) so it can be used both inside the blocking modal
// (ApprovalOverlay, one at a time) and stacked in the non-blocking tray
// (ApprovalTray, the whole queue), without duplicating the allow/deny/answer
// logic (RP-4 UX-1b).
import { ref, watch } from 'vue'

const props = defineProps({
  approval: { type: Object, default: null }, // {agentId, requestId, tool, description, reason, risk, plan}
  question: { type: Object, default: null }, // {agentId, requestId, questions:[{Question,Header,Options,MultiSelect}]}
})
const emit = defineEmits(['permission', 'question'])

// answers maps question text -> chosen option label (single-select MVP). Each
// card owns its own copy so a tray of several questions doesn't share state.
const answers = ref({})
watch(
  () => props.question,
  (q) => {
    answers.value = {}
    if (q) for (const item of q.questions) answers.value[item.Question] = ''
  },
  { immediate: true },
)

function allow() {
  emit('permission', { agent: props.approval.agentId, reqId: props.approval.requestId, behavior: 'allow' })
}
// Always allow: approve now AND seed a session-scope allow rule for this tool, so
// the same tool stops re-prompting for the rest of the session (ruleTool carries
// the tool name to the backend).
function alwaysAllow() {
  emit('permission', {
    agent: props.approval.agentId,
    reqId: props.approval.requestId,
    behavior: 'allow',
    ruleTool: props.approval.tool,
  })
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
  <div class="gate">
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
        <button class="primary" @click="allow">Allow once</button>
        <button class="primary ghost" @click="alwaysAllow" title="Allow this tool for the rest of the session">
          Always allow
        </button>
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
</template>

<style scoped>
h3 {
  margin: 0 0 0.75rem;
  font-size: 0.95rem;
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
  flex-wrap: wrap;
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
