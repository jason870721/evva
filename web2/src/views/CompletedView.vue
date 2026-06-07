<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useLedgerStore } from '@/stores/ledger'
import { useSpaceStore } from '@/stores/space'
import type { TaskInfo } from '@/types/wire'
import TaskCard from '@/components/board/TaskCard.vue'
import EvButton from '@/components/base/EvButton.vue'
import EvPanel from '@/components/base/EvPanel.vue'

// Paged completed history (RP-6): pulls a page at a time via ledger.page().
const ledger = useLedgerStore()
const space = useSpaceStore()
const route = useRoute()
const router = useRouter()
const items = ref<TaskInfo[]>([])
const total = ref(0)
const offset = ref(0)
const loading = ref(false)
const LIMIT = 20

async function loadMore() {
  loading.value = true
  try {
    const p = await ledger.page('completed', LIMIT, offset.value)
    items.value = [...items.value, ...(p.tasks || [])]
    total.value = p.total || 0
    offset.value += (p.tasks || []).length
  } finally {
    loading.value = false
  }
}
function openTask(id: number) {
  router.push({ query: { ...route.query, t: String(id), m: undefined } })
}
onMounted(loadMore)
</script>

<template>
  <EvPanel :title="`Completed · ${total}`" class="fill">
    <div class="list">
      <TaskCard v-for="t in items" :key="t.id" :task="t" :now="space.now" @open="openTask" />
      <div v-if="!items.length && !loading" class="dim">no completed tasks</div>
    </div>
    <EvButton v-if="items.length < total" size="sm" :loading="loading" @click="loadMore">
      load more ({{ items.length }} / {{ total }})
    </EvButton>
  </EvPanel>
</template>

<style scoped>
.fill {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: grid;
  gap: var(--sp-2);
  align-content: start;
  margin-bottom: var(--sp-2);
}
.dim {
  color: var(--color-text-muted);
}
</style>
