<script setup lang="ts">
// Contextual right rail driven by the URL query: ?m=<member> → MemberInspector
// (FE-4: Live/History/Mailbox); ?t=<taskId> → TaskInspector (FE-5: detail +
// related messages). Closing strips the query, leaving the center view untouched
// (focused vs selected separation, RP-4 H7).
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import EvPanel from '../components/base/EvPanel.vue'
import EvButton from '../components/base/EvButton.vue'
import MemberInspector from '../components/inspector/MemberInspector.vue'
import TaskInspector from '../components/inspector/TaskInspector.vue'

const route = useRoute()
const router = useRouter()
const member = computed(() => (route.query.m ? String(route.query.m) : ''))
const taskId = computed(() => (route.query.t ? String(route.query.t) : ''))

function close() {
  const q = { ...route.query }
  delete q.m
  delete q.t
  router.replace({ query: q })
}
</script>

<template>
  <EvPanel class="ins">
    <template #header>
      <span class="title">{{ member ? `member · ${member}` : taskId ? `task · #${taskId}` : 'inspector' }}</span>
      <EvButton size="sm" @click="close">close</EvButton>
    </template>
    <MemberInspector v-if="member" :member="member" />
    <TaskInspector v-else-if="taskId" :task-id="Number(taskId)" />
  </EvPanel>
</template>

<style scoped>
.ins {
  height: 100%;
  display: flex;
  flex-direction: column;
}
/* Same fix as TimelineView: EvPanel's .body is a plain padded div — flex it
   into the height chain so the inspectors' scroll panes (mailbox, history)
   can actually overflow + scroll instead of being clipped. */
.ins :deep(.body) {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
</style>
