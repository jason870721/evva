<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useSpaceStore } from '@/stores/space'
import { errMsg } from '@/lib/util'
import type { SkillInfo } from '@/types/wire'
import EvDialog from '@/components/base/EvDialog.vue'
import EvButton from '@/components/base/EvButton.vue'
import ConfirmDialog from '@/components/safety/ConfirmDialog.vue'

// View/add/delete the space-shared skill library (RP-26): one copy EVERY member
// loads; a member's private skill with the same name shadows it. Adds and
// deletes hot-reload the whole team. The leader can also publish here via its
// skill_publish tool — this panel is the operator's final-arbiter surface.
const emit = defineEmits<{ close: [] }>()
const space = useSpaceStore()

const list = ref<SkillInfo[]>([])
const err = ref('')
const showAdd = ref(false)
const nName = ref('')
const nDesc = ref('')
const nBody = ref('')
const confirmDel = ref('')

async function load() {
  try {
    list.value = (await space.fetchSharedSkills()) || []
    err.value = ''
  } catch (e) {
    err.value = errMsg(e)
  }
}
async function add() {
  if (!nName.value.trim()) return
  try {
    await space.addSharedSkill({ name: nName.value.trim(), description: nDesc.value.trim(), body: nBody.value })
    nName.value = ''
    nDesc.value = ''
    nBody.value = ''
    showAdd.value = false
    await load()
  } catch (e) {
    err.value = errMsg(e)
  }
}
async function doDelete() {
  const skill = confirmDel.value
  confirmDel.value = ''
  if (!skill) return
  try {
    await space.deleteSharedSkill(skill)
    await load()
  } catch (e) {
    err.value = errMsg(e)
  }
}
onMounted(load)
</script>

<template>
  <EvDialog title="Shared skills · whole team" width="32rem" @close="emit('close')">
    <p class="disc">
      One copy <strong>every member loads</strong>; a member's private skill with the same name shadows it.
      Adding or deleting hot-reloads the whole team. The leader can also publish here with
      <code>skill_publish</code> — deleting is your final say over what it institutionalized.
    </p>

    <ul class="list">
      <li v-for="s in list" :key="s.name">
        <div class="info"><strong>{{ s.name }}</strong><span class="d">{{ s.description }}</span></div>
        <EvButton size="sm" variant="danger" @click="confirmDel = s.name">delete</EvButton>
      </li>
      <li v-if="!list.length" class="dim">no shared skills</li>
    </ul>

    <div v-if="showAdd" class="addform">
      <input v-model="nName" placeholder="skill name" />
      <input v-model="nDesc" placeholder="one-line description" />
      <textarea v-model="nBody" rows="4" placeholder="SKILL.md body (instructions the skill tool loads)" />
      <div class="frow">
        <EvButton size="sm" @click="showAdd = false">cancel</EvButton>
        <EvButton size="sm" variant="primary" :disabled="!nName.trim()" @click="add">add skill</EvButton>
      </div>
    </div>

    <p v-if="err" class="err">{{ err }}</p>

    <template #footer>
      <EvButton @click="emit('close')">Close</EvButton>
      <EvButton v-if="!showAdd" variant="primary" @click="showAdd = true">+ add shared skill</EvButton>
    </template>
  </EvDialog>

  <ConfirmDialog
    v-if="confirmDel"
    :title="`Delete shared skill &quot;${confirmDel}&quot;?`"
    :message="`Every member stops seeing it; the whole team's prompts reload on their next run.`"
    confirm-label="Delete"
    :danger="true"
    @confirm="doDelete"
    @cancel="confirmDel = ''"
  />
</template>

<style scoped>
.disc {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  margin-bottom: var(--sp-3);
}
.disc code {
  background: var(--color-surface-2);
  padding: 0 0.3rem;
  border-radius: var(--r-sm);
}
.list {
  list-style: none;
  margin: 0 0 var(--sp-3);
  padding: 0;
  display: grid;
  gap: var(--sp-2);
}
.list li {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--sp-2);
  border: 1px solid var(--color-line);
  border-radius: var(--r-md);
  padding: 0.4rem 0.5rem;
}
.info {
  display: grid;
}
.d {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.addform {
  display: grid;
  gap: var(--sp-2);
  border-top: 1px solid var(--color-line);
  padding-top: var(--sp-3);
}
.frow {
  display: flex;
  justify-content: flex-end;
  gap: var(--sp-2);
}
.err {
  color: var(--color-danger);
  font-size: var(--fs-sm);
}
.dim {
  color: var(--color-text-muted);
}
</style>
