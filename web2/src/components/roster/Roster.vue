<script setup lang="ts">
// The team roster (LEFT region). Calm member cards + all the composition dialogs
// (add agent, schedule, skills, external-events guide) and the remove confirm.
// Emits 'select' so the workspace opens the member's live stream + inspector;
// everything else is handled here against the space store.
import { ref } from 'vue'
import { useRoute } from 'vue-router'
import { useSpaceStore } from '@/stores/space'
import { errMsg } from '@/lib/util'
import type { MemberInfo } from '@/types/wire'
import MemberCard from './MemberCard.vue'
import AddAgentDialog from '@/components/compose/AddAgentDialog.vue'
import ScheduleEditor from '@/components/compose/ScheduleEditor.vue'
import SkillsPanel from '@/components/compose/SkillsPanel.vue'
import EventSources from '@/components/compose/EventSources.vue'
import ConfirmDialog from '@/components/safety/ConfirmDialog.vue'
import EvPanel from '@/components/base/EvPanel.vue'

const emit = defineEmits<{ select: [name: string] }>()
const route = useRoute()
const space = useSpaceStore()

const showAdd = ref(false)
const schedFor = ref<MemberInfo | null>(null)
const skillsFor = ref('')
const showEvents = ref(false)
const removing = ref('')
const err = ref('')

async function cmd(verb: 'freeze' | 'unfreeze' | 'suspend' | 'resume', name: string) {
  try {
    await space.memberCmd(verb, name)
  } catch (e) {
    err.value = errMsg(e)
  }
}
async function onSetSchedule(d: { cron: string; prompt: string }) {
  const name = schedFor.value?.name
  schedFor.value = null
  if (!name) return
  try {
    await space.setSchedule(name, d.cron, d.prompt)
  } catch (e) {
    err.value = errMsg(e)
  }
}
async function onClearSchedule() {
  const name = schedFor.value?.name
  schedFor.value = null
  if (!name) return
  try {
    await space.clearSchedule(name)
  } catch (e) {
    err.value = errMsg(e)
  }
}
async function doRemove(deleteDir: boolean) {
  const name = removing.value
  removing.value = ''
  if (!name) return
  try {
    await space.removeMember(name, deleteDir)
  } catch (e) {
    err.value = errMsg(e)
  }
}
</script>

<template>
  <EvPanel class="rosterp">
    <template #header>
      <span class="title">Roster</span>
      <div class="hactions">
        <button class="hbtn" title="external events webhook" @click="showEvents = true">⚡</button>
        <button class="hbtn" @click="showAdd = true">+ add</button>
      </div>
    </template>

    <ul class="list">
      <MemberCard
        v-for="m in space.merged"
        :key="m.name"
        :member="m"
        :selected="route.query.m === m.name"
        :now="space.now"
        @select="emit('select', m.name)"
        @freeze="cmd('freeze', m.name)"
        @unfreeze="cmd('unfreeze', m.name)"
        @suspend="cmd('suspend', m.name)"
        @resume="cmd('resume', m.name)"
        @schedule="schedFor = m"
        @skills="skillsFor = m.name"
        @remove="removing = m.name"
      />
      <li v-if="!space.merged.length" class="dim">no members yet</li>
    </ul>
    <p v-if="err" class="err">{{ err }}</p>

    <AddAgentDialog v-if="showAdd" @created="showAdd = false" @cancel="showAdd = false" />
    <ScheduleEditor
      v-if="schedFor"
      :member="schedFor.name"
      :cron="schedFor.cron"
      :prompt="schedFor.schedulePrompt"
      @set="onSetSchedule"
      @clear="onClearSchedule"
      @cancel="schedFor = null"
    />
    <SkillsPanel v-if="skillsFor" :member="skillsFor" @close="skillsFor = ''" />
    <EventSources v-if="showEvents" @close="showEvents = false" />
    <ConfirmDialog
      v-if="removing"
      :title="`Remove ${removing}?`"
      :message="`${removing} stops running and the leader is asked to reassign its tasks. History is kept.`"
      confirm-label="Remove"
      :danger="true"
      checkbox-label="Also delete its on-disk definition (cannot be re-added without recreating)"
      @confirm="doRemove"
      @cancel="removing = ''"
    />
  </EvPanel>
</template>

<style scoped>
.rosterp {
  min-height: 0;
}
.hactions {
  display: flex;
  gap: var(--sp-1);
}
.hbtn {
  font-size: var(--fs-xs);
  padding: 0.1rem 0.45rem;
  background: transparent;
  border: 1px dashed var(--color-line);
  border-radius: var(--r-md);
  color: var(--color-text-muted);
  cursor: pointer;
}
.hbtn:hover {
  border-color: var(--color-accent);
  color: var(--color-text);
}
.list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: grid;
  gap: var(--sp-2);
}
.dim {
  color: var(--color-text-muted);
  font-size: var(--fs-sm);
}
.err {
  color: var(--color-danger);
  font-size: var(--fs-xs);
  margin-top: var(--sp-2);
}
</style>
