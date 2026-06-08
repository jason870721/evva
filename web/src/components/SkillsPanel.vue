<script setup>
// SkillsPanel is the operator's view/add/delete dialog for ONE member's skills
// (RP-10). Skills are User-authored only — agents load skills with the skill tool
// but never write them — so this dialog is the single authoring path. An add or
// delete hot-reloads that member's system prompt at its next run boundary.
import { ref, onMounted, onBeforeUnmount } from 'vue'

defineProps({
  member: { type: String, default: '' },
  skills: { type: Array, default: () => [] }, // [{ name, description }]
})
const emit = defineEmits(['add', 'delete', 'close'])

const name = ref('')
const description = ref('')
const body = ref('')
const err = ref('')

function submit() {
  const n = name.value.trim()
  if (!n) {
    err.value = 'name is required'
    return
  }
  if (!body.value.trim()) {
    err.value = 'body is required'
    return
  }
  emit('add', { name: n, description: description.value.trim(), body: body.value })
  name.value = ''
  description.value = ''
  body.value = ''
  err.value = ''
}

function onKey(e) {
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('close')
  }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="scrim" role="dialog" aria-modal="true" @click.self="emit('close')">
    <div class="dialog">
      <h3>Skills · {{ member }}</h3>
      <p class="hint">
        The member sees these in its prompt and loads them with the <code>skill</code> tool. Authoring is
        operator-only.
      </p>
      <p v-if="err" class="err">{{ err }}</p>

      <ul class="list">
        <li v-for="s in skills" :key="s.name">
          <div class="sk">
            <span class="sname">{{ s.name }}</span>
            <span v-if="s.description" class="sdesc">{{ s.description }}</span>
          </div>
          <button class="rm" @click="emit('delete', s.name)">delete</button>
        </li>
        <li v-if="!skills.length" class="empty">No skills yet.</li>
      </ul>

      <div class="add">
        <span class="addhd">Add a skill</span>
        <label class="fld">
          <span>Name</span>
          <input v-model="name" placeholder="e.g. pnl-report" />
        </label>
        <label class="fld">
          <span>Description</span>
          <input v-model="description" placeholder="One line shown in the prompt list" />
        </label>
        <label class="fld">
          <span>Body</span>
          <textarea v-model="body" rows="6" placeholder="The instructions the skill tool loads…"></textarea>
        </label>
      </div>

      <div class="row">
        <button class="ghost" @click="emit('close')">Close</button>
        <button class="primary" @click="submit">Add skill</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* z-index below ConfirmDialog (60) so the delete-confirm stacks on top of this. */
.scrim {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.55);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 58;
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
  margin: 0 0 0.4rem;
  font-size: 0.95rem;
}
.hint {
  color: var(--dim);
  font-size: var(--fs-xs);
  margin: 0 0 0.7rem;
}
.hint code {
  font-family: var(--mono);
}
.err {
  color: var(--danger);
  font-size: 0.82rem;
  margin: 0 0 0.6rem;
}
.list {
  list-style: none;
  margin: 0 0 0.9rem;
  padding: 0;
  display: grid;
  gap: 0.3rem;
}
.list li {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.35rem 0.5rem;
}
.sk {
  flex: 1;
  min-width: 0;
}
.sname {
  font-family: var(--mono);
  font-size: 0.82rem;
}
.sdesc {
  display: block;
  color: var(--dim);
  font-size: var(--fs-xs);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.list .rm {
  font-size: var(--fs-xs);
  padding: 0.1rem 0.45rem;
  color: var(--danger);
  border-color: var(--danger);
  background: transparent;
}
.list .empty {
  display: block;
  color: var(--dim);
  font-size: var(--fs-xs);
  text-align: center;
  border-style: dashed;
}
.add {
  border-top: 1px solid var(--line);
  padding-top: 0.7rem;
}
.addhd {
  display: block;
  font-size: 0.8rem;
  font-weight: 600;
  margin-bottom: 0.5rem;
}
.fld {
  display: block;
  margin: 0 0 0.6rem;
}
.fld > span {
  display: block;
  font-size: 0.78rem;
  color: var(--dim);
  margin-bottom: 0.25rem;
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
