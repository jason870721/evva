<script setup lang="ts">
// Graded confirmation for destructive ops (RP-4 §4.3, H2/H12). Built on EvDialog
// (Esc/scrim cancel). Optional checkbox (e.g. "also delete on-disk dir") and an
// optional type-to-confirm phrase that gates the highest-blast-radius actions
// (halt all). Replaces native window.confirm and the FE-2 placeholder.
import { ref, computed } from 'vue'
import EvDialog from '@/components/base/EvDialog.vue'
import EvButton from '@/components/base/EvButton.vue'

const props = defineProps<{
  title: string
  message: string
  confirmLabel: string
  danger?: boolean
  checkboxLabel?: string
  requireType?: string
}>()
const emit = defineEmits<{ confirm: [checked: boolean]; cancel: [] }>()

const checked = ref(false)
const typed = ref('')
const canConfirm = computed(() => !props.requireType || typed.value.trim() === props.requireType)

function confirm() {
  if (canConfirm.value) emit('confirm', checked.value)
}
</script>

<template>
  <EvDialog :title="title" width="28rem" @close="emit('cancel')">
    <p class="msg">{{ message }}</p>
    <label v-if="checkboxLabel" class="cb"><input v-model="checked" type="checkbox" /> {{ checkboxLabel }}</label>
    <div v-if="requireType" class="type">
      <label>Type <code>{{ requireType }}</code> to confirm</label>
      <input v-model="typed" :placeholder="requireType" @keyup.enter="confirm" />
    </div>
    <template #footer>
      <EvButton @click="emit('cancel')">Cancel</EvButton>
      <EvButton :variant="danger ? 'danger' : 'primary'" :disabled="!canConfirm" @click="confirm">{{ confirmLabel }}</EvButton>
    </template>
  </EvDialog>
</template>

<style scoped>
.msg {
  font-size: var(--fs-sm);
  line-height: 1.5;
}
.cb {
  display: flex;
  align-items: center;
  gap: 0.4rem;
  margin-top: var(--sp-3);
  font-size: var(--fs-sm);
}
.type {
  margin-top: var(--sp-3);
  display: grid;
  gap: 0.3rem;
  font-size: var(--fs-sm);
}
.type code {
  background: var(--color-surface-2);
  padding: 0 0.3rem;
  border-radius: var(--r-sm);
  color: var(--color-danger);
}
</style>
