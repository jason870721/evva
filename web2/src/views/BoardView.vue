<script setup lang="ts">
import { useRoute, useRouter } from 'vue-router'
import TeamBoard from '@/components/board/TeamBoard.vue'

const route = useRoute()
const router = useRouter()
function viewAll() {
  router.push({ name: 'completed', params: { spaceId: String(route.params.spaceId) }, query: route.query })
}
</script>

<template>
  <TeamBoard @view-all="viewAll" />
</template>
