<script setup lang="ts">
import { ref } from 'vue'
import EvButton from '@/components/base/EvButton.vue'

defineProps<{ placeholder?: string }>()
const emit = defineEmits<{ send: [text: string] }>()
const draft = ref('')

function send() {
  const t = draft.value.trim()
  if (!t) return
  emit('send', t)
  draft.value = ''
}
// Enter sends; Shift+Enter newlines (.exact excludes the shift combo). Guard IME
// composition so committing a candidate with Enter doesn't fire a send (the RP fix).
function onEnter(e: KeyboardEvent) {
  if (e.isComposing) return
  e.preventDefault()
  send()
}
</script>

<template>
  <div class="composer">
    <textarea
      v-model="draft"
      rows="2"
      :placeholder="placeholder || 'Message…  (Enter to send, Shift+Enter for newline)'"
      @keydown.enter.exact="onEnter"
    />
    <EvButton variant="primary" @click="send">Send</EvButton>
  </div>
</template>

<style scoped>
.composer {
  display: flex;
  gap: var(--sp-2);
  margin-top: var(--sp-2);
}
textarea {
  flex: 1;
  resize: vertical;
  font-family: inherit;
}
</style>
