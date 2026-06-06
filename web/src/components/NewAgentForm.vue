<script setup>
// NewAgentForm is the operator's "add agent" dialog (RP-8): author a new worker
// — name, system prompt, when-to-use, the tools it gets (active vs deferred), and
// an optional recurring schedule. Collaboration tools (send_message, task_*, …)
// are NOT in the catalog: they are injected by role at construction, invisibly.
import { ref, onMounted, onBeforeUnmount } from 'vue'

defineProps({
  tools: { type: Array, default: () => [] }, // selectable catalog from GET /api/tools
})
const emit = defineEmits(['create', 'cancel'])

const name = ref('')
const whenToUse = ref('')
const systemPrompt = ref('')
const active = ref([])
const deferred = ref([])
const cron = ref('')
const prompt = ref('')
const err = ref('')

// active / deferred are mutually exclusive per tool: setting one clears the other.
function setTool(kind, t) {
  const a = active.value.filter((x) => x !== t)
  const d = deferred.value.filter((x) => x !== t)
  if (kind === 'active' && !active.value.includes(t)) a.push(t)
  if (kind === 'deferred' && !deferred.value.includes(t)) d.push(t)
  active.value = a
  deferred.value = d
}

function submit() {
  const n = name.value.trim()
  if (!n) {
    err.value = 'name is required'
    return
  }
  if (!systemPrompt.value.trim()) {
    err.value = 'system prompt is required'
    return
  }
  emit('create', {
    name: n,
    systemPrompt: systemPrompt.value,
    whenToUse: whenToUse.value.trim(),
    active: active.value,
    deferred: deferred.value,
    cron: cron.value.trim(),
    prompt: prompt.value.trim(),
  })
}

function onKey(e) {
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('cancel')
  }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="scrim" role="dialog" aria-modal="true" @click.self="emit('cancel')">
    <div class="dialog">
      <h3>Add agent</h3>
      <p v-if="err" class="err">{{ err }}</p>

      <label class="fld">
        <span>Name</span>
        <input v-model="name" placeholder="e.g. qa-bot" />
      </label>
      <label class="fld">
        <span>When to use</span>
        <input v-model="whenToUse" placeholder="One line: when the leader should hand this agent work" />
      </label>
      <label class="fld">
        <span>System prompt</span>
        <textarea v-model="systemPrompt" rows="6" placeholder="Who this agent is and how it works…"></textarea>
      </label>

      <div class="fld">
        <span>Tools <em>(collaboration tools are added automatically)</em></span>
        <div class="tools">
          <div v-for="t in tools" :key="t" class="toolrow">
            <span class="tname">{{ t }}</span>
            <button type="button" :class="{ on: active.includes(t) }" @click="setTool('active', t)">active</button>
            <button type="button" :class="{ on: deferred.includes(t) }" @click="setTool('deferred', t)">deferred</button>
          </div>
          <div v-if="!tools.length" class="empty">No tools available.</div>
        </div>
      </div>

      <details class="sched">
        <summary>Schedule (optional)</summary>
        <label class="fld">
          <span>Cron</span>
          <input v-model="cron" placeholder='e.g. "*/30 * * * *"' />
        </label>
        <label class="fld">
          <span>Wake prompt</span>
          <input v-model="prompt" placeholder="What it should do each time it fires" />
        </label>
      </details>

      <div class="row">
        <button class="ghost" @click="emit('cancel')">Cancel</button>
        <button class="primary" @click="submit">Create</button>
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
  z-index: 60;
}
.dialog {
  background: var(--panel);
  border: 1px solid var(--line);
  border-radius: 10px;
  padding: 1.2rem 1.3rem;
  width: min(34rem, 94vw);
  max-height: 90vh;
  overflow: auto;
}
h3 {
  margin: 0 0 0.6rem;
  font-size: 0.95rem;
}
.err {
  color: var(--danger);
  font-size: 0.82rem;
  margin: 0 0 0.6rem;
}
.fld {
  display: block;
  margin: 0 0 0.7rem;
}
.fld > span {
  display: block;
  font-size: 0.78rem;
  color: var(--dim);
  margin-bottom: 0.25rem;
}
.fld em {
  font-style: normal;
  opacity: 0.7;
}
.fld input,
.fld textarea {
  width: 100%;
  box-sizing: border-box;
}
.fld textarea {
  resize: vertical;
  font-family: var(--mono);
  font-size: 0.82rem;
}
.tools {
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.4rem;
  max-height: 11rem;
  overflow: auto;
  display: grid;
  gap: 0.25rem;
}
.toolrow {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: var(--fs-xs);
}
.tname {
  flex: 1;
  font-family: var(--mono);
}
.toolrow button {
  font-size: var(--fs-xs);
  padding: 0.05rem 0.4rem;
  background: transparent;
  border: 1px solid var(--line);
  border-radius: 5px;
  color: var(--dim);
  cursor: pointer;
}
.toolrow button.on {
  border-color: var(--accent);
  color: #e6edf3;
  background: var(--accent);
}
.empty {
  color: var(--dim);
  font-size: var(--fs-xs);
  text-align: center;
  padding: 0.4rem;
}
.sched {
  margin: 0 0 0.8rem;
  font-size: 0.82rem;
}
.sched summary {
  cursor: pointer;
  color: var(--dim);
  margin-bottom: 0.5rem;
}
.row {
  display: flex;
  justify-content: flex-end;
  gap: 0.6rem;
}
.primary {
  background: var(--accent);
  color: #fff;
  border-color: var(--accent);
}
</style>
