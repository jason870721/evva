<script setup lang="ts">
import { ref, computed } from 'vue'
import { describeCron, isValidCron, nextFire } from '@/lib/cron'
import EvDialog from '@/components/base/EvDialog.vue'
import EvButton from '@/components/base/EvButton.vue'

// Schedule any member (incl. the leader, RP-7/8). Live human-readable + next-fire
// preview from the cron helper; Set disabled until the expression is valid.
const props = defineProps<{ member: string; cron?: string; prompt?: string }>()
const emit = defineEmits<{ set: [d: { cron: string; prompt: string }]; clear: []; cancel: [] }>()

const cron = ref(props.cron || '')
const prompt = ref(props.prompt || '')
const valid = computed(() => !!cron.value.trim() && isValidCron(cron.value.trim()))
const desc = computed(() => (cron.value.trim() ? describeCron(cron.value.trim()) : ''))
const next = computed(() => {
  const nf = valid.value ? nextFire(cron.value.trim()) : null
  return nf ? new Date(nf).toLocaleString() : ''
})

function save() {
  if (valid.value) emit('set', { cron: cron.value.trim(), prompt: prompt.value.trim() })
}
</script>

<template>
  <EvDialog :title="`Schedule · ${member}`" width="30rem" @close="emit('cancel')">
    <label class="l">cron (5-field)</label>
    <input v-model="cron" placeholder="*/30 * * * *" :class="{ bad: cron && !valid }" />
    <p class="hint" :class="{ ok: valid, bad: cron && !valid }">
      <template v-if="!cron">e.g. <code>*/30 * * * *</code> · <code>0 9 * * 1-5</code></template>
      <template v-else-if="valid">{{ desc }}<span v-if="next"> · next: {{ next }}</span></template>
      <template v-else>invalid cron expression</template>
    </p>
    <label class="l">wake prompt (optional)</label>
    <input v-model="prompt" placeholder="injected as a system-reminder on wake" />
    <template #footer>
      <EvButton v-if="cron" variant="danger" @click="emit('clear')">Clear</EvButton>
      <EvButton @click="emit('cancel')">Cancel</EvButton>
      <EvButton variant="primary" :disabled="!valid" @click="save">Set</EvButton>
    </template>
  </EvDialog>
</template>

<style scoped>
.l {
  display: block;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  margin: var(--sp-2) 0 0.2rem;
}
input {
  width: 100%;
  font-family: var(--font-mono);
}
input.bad {
  border-color: var(--color-danger);
}
.hint {
  margin: 0.3rem 0 0;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.hint.ok {
  color: var(--status-completed);
}
.hint.bad {
  color: var(--color-danger);
}
code {
  background: var(--color-surface-2);
  padding: 0 0.25rem;
  border-radius: var(--r-sm);
}
</style>
