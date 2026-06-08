<script setup lang="ts">
import { computed } from 'vue'
import { useConnectionStore } from '@/stores/connection'
import EvDialog from '@/components/base/EvDialog.vue'

// Webhook wiring guide (RP-9): how an external app pushes a signal to the leader.
// Read-only — received events arrive as leader messages and show in the Timeline.
const emit = defineEmits<{ close: [] }>()
const conn = useConnectionStore()
const url = computed(() => `${location.origin}/api/swarm/${conn.spaceId}/event`)
const curl = computed(
  () =>
    `curl -X POST ${url.value} \\\n  -H 'Content-Type: application/json' \\\n  -d '{"title":"price alert","body":"BTC > 70k","source":"trader-engine"}'`,
)
</script>

<template>
  <EvDialog title="External events · webhook" width="34rem" @close="emit('close')">
    <p class="p">Push an external signal to the leader (RP-9). In test mode the endpoint is loopback-only, no token.</p>
    <label class="l">endpoint</label>
    <pre class="code">{{ url }}</pre>
    <label class="l">example</label>
    <pre class="code">{{ curl }}</pre>
    <p class="note">Received events arrive as leader messages — watch them in the Timeline.</p>
  </EvDialog>
</template>

<style scoped>
.p {
  font-size: var(--fs-sm);
}
.l {
  display: block;
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  margin: var(--sp-3) 0 0.2rem;
}
.code {
  margin: 0;
  background: var(--color-bg);
  border: 1px solid var(--color-line);
  border-radius: var(--r-sm);
  padding: 0.5rem;
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  word-break: break-all;
}
.note {
  margin-top: var(--sp-3);
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
}
</style>
