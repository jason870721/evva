<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useSpaceStore } from '@/stores/space'
import { isValidCron } from '@/lib/cron'
import { errMsg } from '@/lib/util'
import EvDialog from '@/components/base/EvDialog.vue'
import EvButton from '@/components/base/EvButton.vue'
import ToolPicker from './ToolPicker.vue'

// Author a new worker (RP-8): identity / persona / tools / optional schedule.
// Collaboration tools are role-injected server-side. The leader is unique and is
// never created here.
const emit = defineEmits<{ created: []; cancel: [] }>()
const space = useSpaceStore()

const name = ref('')
const whenToUse = ref('')
const systemPrompt = ref('')
const model = ref('')
const effort = ref('')
const active = ref<string[]>([])
const deferred = ref<string[]>([])
const cron = ref('')
const prompt = ref('')
const tools = ref<string[]>([])
const models = ref<string[]>([])
const err = ref('')
const busy = ref(false)

const EFFORTS = ['low', 'medium', 'high', 'ultra'] as const

const cronOk = computed(() => !cron.value.trim() || isValidCron(cron.value.trim()))
const canCreate = computed(() => !!name.value.trim() && cronOk.value)

onMounted(async () => {
  try {
    ;[tools.value, models.value] = await Promise.all([
      space.fetchTools().then((t) => t || []),
      space.fetchModels().then((m) => m || []),
    ])
  } catch (e) {
    err.value = errMsg(e)
  }
})

async function create() {
  if (!canCreate.value) return
  busy.value = true
  err.value = ''
  try {
    await space.createMember({
      name: name.value.trim(),
      systemPrompt: systemPrompt.value,
      whenToUse: whenToUse.value.trim(),
      model: model.value,
      effort: effort.value,
      active: active.value,
      deferred: deferred.value,
      cron: cron.value.trim(),
      prompt: prompt.value.trim(),
    })
    emit('created')
  } catch (e) {
    err.value = errMsg(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <EvDialog title="Add agent" width="36rem" @close="emit('cancel')">
    <section class="sec">
      <h4>Identity</h4>
      <input v-model="name" placeholder="name (e.g. analyst)" />
      <input v-model="whenToUse" placeholder="when to use — the leader reads this to delegate" />
    </section>
    <section class="sec">
      <h4>Persona</h4>
      <textarea v-model="systemPrompt" rows="4" placeholder="system prompt" />
    </section>
    <section class="sec">
      <h4>Model &amp; effort <span class="opt">(optional — fixed after creation)</span></h4>
      <div class="pair">
        <select v-model="model">
          <option value="">model: default</option>
          <option v-for="m in models" :key="m" :value="m">{{ m }}</option>
        </select>
        <select v-model="effort">
          <option value="">effort: default</option>
          <option v-for="e in EFFORTS" :key="e" :value="e">{{ e }}</option>
        </select>
      </div>
    </section>
    <section class="sec">
      <h4>Tools</h4>
      <ToolPicker :tools="tools" v-model:active="active" v-model:deferred="deferred" />
    </section>
    <section class="sec">
      <h4>Schedule <span class="opt">(optional)</span></h4>
      <input v-model="cron" placeholder="cron e.g. */30 * * * *" :class="{ bad: cron && !cronOk }" />
      <input v-model="prompt" placeholder="wake prompt (optional)" />
    </section>
    <p v-if="err" class="err">{{ err }}</p>
    <template #footer>
      <EvButton @click="emit('cancel')">Cancel</EvButton>
      <EvButton variant="primary" :disabled="!canCreate" :loading="busy" @click="create">Create</EvButton>
    </template>
  </EvDialog>
</template>

<style scoped>
.sec {
  margin-bottom: var(--sp-3);
  display: grid;
  gap: var(--sp-2);
}
h4 {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  color: var(--color-text-muted);
  margin: 0;
}
.opt {
  text-transform: none;
  color: var(--color-text-faint);
}
input,
textarea {
  width: 100%;
}
.pair {
  display: flex;
  gap: var(--sp-2);
}
.pair select {
  flex: 1;
  min-width: 0;
}
input.bad {
  border-color: var(--color-danger);
}
.err {
  color: var(--color-danger);
  font-size: var(--fs-sm);
}
</style>
