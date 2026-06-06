<script setup>
// ConfirmDialog is the app's own confirmation modal (RP-4 UX-3), replacing the
// jarring native window.confirm. Destructive actions (reset, halt all) route
// through it so the operator gets a styled, consistent, double-checked prompt.
// Enter confirms, Esc cancels; the confirm button is focused on open.
import { onMounted, onBeforeUnmount, ref } from 'vue'

defineProps({
  title: { type: String, default: 'Confirm' },
  message: { type: String, default: '' },
  confirmLabel: { type: String, default: 'Confirm' },
  danger: { type: Boolean, default: false },
  // When set, an extra opt-in checkbox is shown; its state rides the confirm
  // event (e.g. RP-8 remove → "also delete the directory").
  checkboxLabel: { type: String, default: '' },
})
const emit = defineEmits(['confirm', 'cancel'])
const confirmBtn = ref(null)
const checked = ref(false)

function confirm() {
  emit('confirm', checked.value)
}
function onKey(e) {
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('cancel')
  } else if (e.key === 'Enter') {
    e.preventDefault()
    confirm()
  }
}
onMounted(() => {
  window.addEventListener('keydown', onKey)
  confirmBtn.value && confirmBtn.value.focus()
})
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <div class="scrim" role="dialog" aria-modal="true" @click.self="emit('cancel')">
    <div class="dialog">
      <h3>{{ title }}</h3>
      <p class="msg">{{ message }}</p>
      <label v-if="checkboxLabel" class="opt">
        <input type="checkbox" v-model="checked" />
        {{ checkboxLabel }}
      </label>
      <div class="row">
        <button class="ghost" @click="emit('cancel')">Cancel</button>
        <button ref="confirmBtn" :class="danger ? 'danger' : 'primary'" @click="confirm">
          {{ confirmLabel }}
        </button>
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
  width: min(30rem, 92vw);
}
h3 {
  margin: 0 0 0.6rem;
  font-size: 0.95rem;
}
.msg {
  color: var(--dim);
  font-size: 0.85rem;
  line-height: 1.45;
  margin: 0 0 1rem;
}
.opt {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  font-size: 0.82rem;
  color: var(--dim);
  margin: 0 0 1rem;
  cursor: pointer;
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
.danger {
  color: #fff;
  background: var(--danger);
  border-color: var(--danger);
}
</style>
