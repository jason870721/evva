<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import BlackboardPanel from '@/components/board/BlackboardPanel.vue'
import TeamBoard from '@/components/board/TeamBoard.vue'

const route = useRoute()
const router = useRouter()
function viewAll() {
  router.push({ name: 'completed', params: { spaceId: String(route.params.spaceId) }, query: route.query })
}
</script>

<template>
  <div class="bv">
    <BlackboardPanel />
    <TeamBoard @view-all="viewAll" />
  </div>
</template>

<style scoped>
.bv {
  height: 100%;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
</style>
