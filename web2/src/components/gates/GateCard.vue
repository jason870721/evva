<script setup lang="ts">
// One pending gate — approval OR question — self-contained so it serves both the
// blocking modal and the non-blocking tray without duplicating logic (RP-4 UX-1b).
// Question answers cover single-select (radio), multi-select (checkbox), and a
// free-text "Other" per question; multi-select labels are joined into one string
// to fit the backend's map[string]string (FE-6 — native []string is a backend
// follow-up). When `active`, keyboard works: A=allow, D=deny, 1–9 toggle the first
// question's options, Enter submits.
import { ref, watch, computed, onMounted, onBeforeUnmount } from 'vue'
import type { ApprovalVM, QuestionVM } from '@/lib/events'
import type { QuestionItem } from '@/types/events'
import EvButton from '@/components/base/EvButton.vue'

const props = defineProps<{
  approval?: ApprovalVM | null
  question?: QuestionVM | null
  error?: string
  active?: boolean
}>()
const emit = defineEmits<{
  permission: [d: { agent: string; reqId: string; behavior: string; reason?: string; ruleTool?: string }]
  question: [d: { agent: string; reqId: string; answers: Record<string, string[]> }]
}>()

// Per-question selection sets + "other" text.
const sel = ref<Record<string, Set<string>>>({})
const other = ref<Record<string, string>>({})

watch(
  () => props.question,
  (q) => {
    sel.value = {}
    other.value = {}
    if (q) for (const item of q.questions) {
      sel.value[item.Question] = new Set()
      other.value[item.Question] = ''
    }
  },
  { immediate: true },
)

function toggle(q: QuestionItem, label: string) {
  const set = new Set(sel.value[q.Question] || [])
  if (q.MultiSelect) {
    if (set.has(label)) set.delete(label)
    else set.add(label)
  } else {
    set.clear()
    set.add(label)
  }
  sel.value = { ...sel.value, [q.Question]: set }
}
function isSel(q: QuestionItem, label: string): boolean {
  return sel.value[q.Question]?.has(label) ?? false
}

const allAnswered = computed(() => {
  if (!props.question) return false
  return props.question.questions.every(
    (q) => (sel.value[q.Question]?.size || 0) > 0 || (other.value[q.Question] || '').trim().length > 0,
  )
})

function buildAnswers(): Record<string, string[]> {
  const out: Record<string, string[]> = {}
  if (!props.question) return out
  for (const q of props.question.questions) {
    const labels = [...(sel.value[q.Question] || [])]
    const o = (other.value[q.Question] || '').trim()
    if (o) labels.push(o)
    out[q.Question] = labels
  }
  return out
}

function allow() {
  if (!props.approval) return
  emit('permission', { agent: props.approval.agentId, reqId: props.approval.requestId, behavior: 'allow' })
}
function alwaysAllow() {
  if (!props.approval) return
  emit('permission', {
    agent: props.approval.agentId,
    reqId: props.approval.requestId,
    behavior: 'allow',
    ruleTool: props.approval.tool,
  })
}
function deny() {
  if (!props.approval) return
  emit('permission', { agent: props.approval.agentId, reqId: props.approval.requestId, behavior: 'deny', reason: 'denied from web' })
}
function submit() {
  if (!props.question || !allAnswered.value) return
  emit('question', { agent: props.question.agentId, reqId: props.question.requestId, answers: buildAnswers() })
}

const dangerous = computed(() => !!props.approval?.risk)

function onKey(e: KeyboardEvent) {
  if (!props.active) return
  const tag = (e.target as HTMLElement | null)?.tagName
  if (tag === 'INPUT' || tag === 'TEXTAREA') return // don't hijack typing
  if (props.approval) {
    if (e.key === 'a' || e.key === 'A') {
      if (!dangerous.value) allow() // dangerous tools require an explicit click
    } else if (e.key === 'd' || e.key === 'D') {
      deny()
    }
  } else if (props.question) {
    if (e.key === 'Enter') {
      submit()
    } else if (/^[1-9]$/.test(e.key)) {
      const q = props.question.questions[0]
      const opt = q?.Options?.[Number(e.key) - 1]
      if (q && opt) toggle(q, opt.Label)
    }
  }
}
onMounted(() => document.addEventListener('keydown', onKey))
onBeforeUnmount(() => document.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="gate" :class="{ danger: dangerous }">
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
      <p v-if="error" class="gerr">⚠ {{ error }} — reloaded; try again.</p>
      <div class="row">
        <EvButton variant="primary" @click="allow">Allow once <kbd v-if="!dangerous">A</kbd></EvButton>
        <EvButton @click="alwaysAllow" title="Allow this tool for the rest of the session">Always allow</EvButton>
        <EvButton variant="danger" @click="deny">Deny <kbd>D</kbd></EvButton>
      </div>
    </template>

    <template v-else-if="question">
      <h3>Question</h3>
      <div v-for="item in question.questions" :key="item.Question" class="q">
        <div v-if="item.Header" class="qhead">{{ item.Header }}</div>
        <div class="qtext">{{ item.Question }}<span v-if="item.MultiSelect" class="multi">· choose any</span></div>
        <label v-for="(opt, oi) in item.Options || []" :key="opt.Label" class="opt">
          <input
            :type="item.MultiSelect ? 'checkbox' : 'radio'"
            :checked="isSel(item, opt.Label)"
            @change="toggle(item, opt.Label)"
          />
          <span class="ol"><kbd v-if="oi < 9">{{ oi + 1 }}</kbd> {{ opt.Label }}</span>
          <span v-if="opt.Description" class="odesc">— {{ opt.Description }}</span>
        </label>
        <input v-model="other[item.Question]" class="otherin" placeholder="Other… (free text)" />
      </div>
      <p v-if="error" class="gerr">⚠ {{ error }} — reloaded; try again.</p>
      <div class="row">
        <EvButton variant="primary" :disabled="!allAnswered" @click="submit">Submit <kbd>⏎</kbd></EvButton>
      </div>
    </template>
  </div>
</template>

<style scoped>
.gate {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.gate.danger {
  outline: 1px solid color-mix(in srgb, var(--color-danger) 50%, transparent);
  outline-offset: 0.4rem;
  border-radius: var(--r-sm);
}
h3 {
  font-size: var(--fs-md);
  margin: 0;
}
.tool {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.tool code {
  background: var(--color-surface-2);
  padding: 0.05rem 0.35rem;
  border-radius: var(--r-sm);
  color: var(--phase-executing);
}
.risk {
  font-size: var(--fs-xs);
  color: var(--color-danger);
  border: 1px solid var(--color-danger);
  border-radius: var(--r-pill);
  padding: 0 0.35rem;
}
.agent {
  margin-left: auto;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.desc {
  font-family: var(--font-mono);
  font-size: var(--fs-sm);
}
.reason {
  color: var(--color-text-muted);
  font-size: var(--fs-sm);
}
.plan {
  background: var(--color-bg);
  border: 1px solid var(--color-line);
  border-radius: var(--r-sm);
  padding: 0.5rem;
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  max-height: 14rem;
  overflow: auto;
}
.gerr {
  color: var(--color-danger);
  font-size: var(--fs-xs);
}
.q {
  margin-bottom: 0.5rem;
}
.qhead {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  color: var(--color-text-muted);
}
.qtext {
  font-weight: 600;
  margin-bottom: 0.25rem;
}
.multi {
  margin-left: 0.4rem;
  font-weight: 400;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.opt {
  display: flex;
  align-items: baseline;
  gap: 0.4rem;
  padding: 0.15rem 0;
  cursor: pointer;
}
.odesc {
  color: var(--color-text-muted);
  font-size: var(--fs-sm);
}
.otherin {
  width: 100%;
  margin-top: 0.25rem;
  font-size: var(--fs-sm);
}
.row {
  display: flex;
  gap: 0.5rem;
  margin-top: 0.5rem;
  flex-wrap: wrap;
}
kbd {
  font-family: var(--font-mono);
  font-size: 0.7em;
  border: 1px solid var(--color-line-strong);
  border-radius: 3px;
  padding: 0 0.25rem;
  color: var(--color-text-muted);
}
</style>
