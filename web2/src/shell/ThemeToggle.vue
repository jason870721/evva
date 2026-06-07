<script setup lang="ts">
import { useUiStore } from '../stores/ui'
import EvButton from '../components/base/EvButton.vue'
import EvIcon from '../components/base/EvIcon.vue'

const ui = useUiStore()
</script>

<template>
  <EvButton size="sm" :title="`theme: ${ui.theme} — click to switch`" @click="ui.toggleTheme()">
    <EvIcon name="refresh" :size="14" /> {{ ui.theme }}
  </EvButton>
</template>
