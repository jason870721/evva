<script setup lang="ts">
// Picks the gate surface by preference (modal vs tray, FE-6) and bridges replies
// to the gate store. Modal shows approvals before questions (one at a time);
// hydrate-on-connect lives in the connection store.
import { computed } from 'vue'
import { useUiStore } from '@/stores/ui'
import { useGateStore, type PermissionReply, type QuestionReply } from '@/stores/gate'
import ApprovalModal from './ApprovalModal.vue'
import ApprovalTray from './ApprovalTray.vue'

const ui = useUiStore()
const gate = useGateStore()
const headReqId = computed(() => gate.headApproval?.requestId || gate.headQuestion?.requestId || '')
const headError = computed(() => gate.errors[headReqId.value])

function onPerm(d: PermissionReply) {
  gate.respondPermission(d)
}
function onQ(d: QuestionReply) {
  gate.respondQuestion(d)
}
</script>

<template>
  <template v-if="gate.pendingCount">
    <ApprovalModal
      v-if="ui.gateMode === 'modal'"
      :approval="gate.headApproval"
      :question="gate.headApproval ? null : gate.headQuestion"
      :pending-count="gate.pendingCount"
      :error="headError"
      @permission="onPerm"
      @question="onQ"
    />
    <ApprovalTray
      v-else
      :approvals="gate.approvals"
      :questions="gate.questions"
      :errors="gate.errors"
      @permission="onPerm"
      @question="onQ"
    />
  </template>
</template>
