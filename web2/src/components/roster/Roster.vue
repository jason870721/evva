<script setup lang="ts">
// The team roster (LEFT region). Calm member cards + all the composition dialogs
// (add agent, schedule, skills, external-events guide) and the remove confirm.
// Emits 'select' so the workspace opens the member's live stream + inspector;
// everything else is handled here against the space store.
import { ref, computed } from 'vue'
import { useRoute } from 'vue-router'
import { useSpaceStore, type BulkResult } from '@/stores/space'
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
const clearing = ref('')
// bypass means fully autonomous — it alone goes through a confirm.
const bypassing = ref('')
const err = ref('')

// ── Multi-select / bulk ops ────────────────────────────────────────────────
// Selection is ephemeral (never in the URL — that's reserved for single-member
// inspect via ?m=). Entering select mode turns cards into checkbox rows and
// surfaces the bulk bar; leaving it clears the selection.
type LifeVerb = 'suspend' | 'resume' | 'freeze' | 'unfreeze'
// Members past this many → the destructive confirm (clear / full) demands a
// type-to-confirm phrase rather than a plain OK.
const TYPE_CONFIRM_THRESHOLD = 4

const selectMode = ref(false)
const sel = ref<Set<string>>(new Set())
const bulkBusy = ref(false)
const summary = ref('')
const confirm = ref<{ kind: 'clear' | 'full'; names: string[] } | null>(null)

const selected = computed(() => space.merged.filter((m) => sel.value.has(m.name)))
// clear / compact refuse a member with a run in flight (run === 'busy'); a
// suspended member has no run in flight, so it is eligible.
const compactable = computed(() => selected.value.filter((m) => m.run !== 'busy').map((m) => m.name))
const busyCount = computed(() => selected.value.filter((m) => m.run === 'busy').length)
const suspendable = computed(() => selected.value.filter((m) => m.run === 'busy').map((m) => m.name))
const resumable = computed(() => selected.value.filter((m) => m.run === 'suspended').map((m) => m.name))
const freezable = computed(() => selected.value.filter((m) => m.membership === 'active').map((m) => m.name))
const unfreezable = computed(() => selected.value.filter((m) => m.membership === 'frozen').map((m) => m.name))

const confirmNeedsType = computed(() => (confirm.value?.names.length ?? 0) >= TYPE_CONFIRM_THRESHOLD)

function toggleSelect() {
  if (selectMode.value) exitSelect()
  else selectMode.value = true
}
function exitSelect() {
  selectMode.value = false
  sel.value = new Set()
  summary.value = ''
}
function toggle(name: string) {
  const s = new Set(sel.value)
  if (s.has(name)) s.delete(name)
  else s.add(name)
  sel.value = s
}
function selectIdle() {
  sel.value = new Set(space.merged.filter((m) => m.run !== 'busy').map((m) => m.name))
}
function selectNone() {
  sel.value = new Set()
}

function formatSummary(verb: string, r: BulkResult): string {
  const parts = [`${verb} ${r.ok.length}`]
  if (r.failed.length) parts.push(`skipped ${r.failed.length} (${r.failed[0].error})`)
  return parts.join(' · ')
}
async function run(verb: string, names: string[], fn: () => Promise<BulkResult>) {
  if (!names.length) return
  bulkBusy.value = true
  summary.value = ''
  try {
    summary.value = formatSummary(verb, await fn())
  } catch (e) {
    summary.value = errMsg(e)
  } finally {
    bulkBusy.value = false
  }
}
function bulkMicro() {
  const names = compactable.value
  void run('compacted', names, () => space.bulkCompact(names, 'micro'))
}
function bulkLife(verb: LifeVerb, names: string[]) {
  const past = { suspend: 'suspended', resume: 'resumed', freeze: 'froze', unfreeze: 'unfroze' }[verb]
  void run(past, names, () => space.bulkCmd(verb, names))
}
async function onBulkConfirm() {
  const c = confirm.value
  confirm.value = null
  if (!c) return
  if (c.kind === 'clear') await run('cleared', c.names, () => space.bulkClear(c.names))
  else await run('compacted', c.names, () => space.bulkCompact(c.names, 'full'))
}

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
async function doClear() {
  const name = clearing.value
  clearing.value = ''
  if (!name) return
  try {
    await space.clearMember(name)
  } catch (e) {
    err.value = errMsg(e) // 409 busy lands here: "suspend it or wait"
  }
}
async function applyPermMode(name: string, mode: string) {
  try {
    await space.setPermissionMode(name, mode)
  } catch (e) {
    err.value = errMsg(e)
  }
}
function onPermMode(name: string, mode: string) {
  if (mode === 'bypass') {
    bypassing.value = name
    return
  }
  void applyPermMode(name, mode)
}
async function doBypass() {
  const name = bypassing.value
  bypassing.value = ''
  if (!name) return
  await applyPermMode(name, 'bypass')
}
</script>

<template>
  <EvPanel class="rosterp">
    <template #header>
      <span class="title">Roster</span>
      <div class="hactions">
        <button class="hbtn" :class="{ on: selectMode }" title="select multiple members" @click="toggleSelect">
          {{ selectMode ? '✗ done' : '✓ select' }}
        </button>
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
        :select-mode="selectMode"
        :checked="sel.has(m.name)"
        :busy="space.memberBusy(m.name)"
        @toggle="toggle(m.name)"
        @select="emit('select', m.name)"
        @freeze="cmd('freeze', m.name)"
        @unfreeze="cmd('unfreeze', m.name)"
        @suspend="cmd('suspend', m.name)"
        @resume="cmd('resume', m.name)"
        @schedule="schedFor = m"
        @skills="skillsFor = m.name"
        @clear="clearing = m.name"
        @remove="removing = m.name"
        @perm-mode="(mode: string) => onPermMode(m.name, mode)"
      />
      <li v-if="!space.merged.length" class="dim">no members yet</li>
    </ul>
    <p v-if="err" class="err">{{ err }}</p>

    <div v-if="selectMode && sel.size" class="bulkbar">
      <div class="brow meta">
        <span>{{ sel.size }} sel · {{ compactable.length }} idle · {{ busyCount }} busy</span>
        <span class="spacer" />
        <button class="chip" @click="selectIdle">idle</button>
        <button class="chip" @click="selectNone">none</button>
      </div>
      <div class="brow">
        <span class="glabel">data</span>
        <button class="b risky" :disabled="bulkBusy || !compactable.length" @click="confirm = { kind: 'clear', names: compactable }">
          🧹 clear ({{ compactable.length }})
        </button>
        <button class="b" :disabled="bulkBusy || !compactable.length" title="elide old tool results — free, instant" @click="bulkMicro">
          🗜 micro ({{ compactable.length }})
        </button>
        <button class="b risky" :disabled="bulkBusy || !compactable.length" title="summarize transcript — one LLM call each, lossy" @click="confirm = { kind: 'full', names: compactable }">
          full… ({{ compactable.length }})
        </button>
      </div>
      <div class="brow">
        <span class="glabel">life</span>
        <button class="b" :disabled="bulkBusy || !suspendable.length" title="suspend running" @click="bulkLife('suspend', suspendable)">
          ⏸ suspend ({{ suspendable.length }})
        </button>
        <button class="b" :disabled="bulkBusy || !resumable.length" title="resume suspended" @click="bulkLife('resume', resumable)">
          ▶ resume ({{ resumable.length }})
        </button>
        <button class="b" :disabled="bulkBusy || !freezable.length" title="freeze active" @click="bulkLife('freeze', freezable)">
          ❄ freeze ({{ freezable.length }})
        </button>
        <button class="b" :disabled="bulkBusy || !unfreezable.length" title="unfreeze frozen" @click="bulkLife('unfreeze', unfreezable)">
          ▶❄ unfreeze ({{ unfreezable.length }})
        </button>
      </div>
      <p v-if="summary" class="bsum">{{ summary }}</p>
    </div>

    <ConfirmDialog
      v-if="confirm"
      :title="confirm.kind === 'clear' ? `Clear ${confirm.names.length} session(s)?` : `Full-compact ${confirm.names.length} member(s)?`"
      :message="
        confirm.kind === 'clear'
          ? `Wipes the conversation of ${confirm.names.join(', ')} — each starts over with a blank context (schedule, skills, memory kept). Any member that goes busy is skipped.`
          : `Summarizes the transcript of ${confirm.names.join(', ')} into a brief — one LLM call each, lossy. Any member that goes busy is skipped.`
      "
      :confirm-label="confirm.kind === 'clear' ? 'Clear sessions' : 'Replace transcripts'"
      :danger="true"
      :require-type="confirmNeedsType ? (confirm.kind === 'clear' ? 'clear' : 'compact') : undefined"
      @confirm="onBulkConfirm"
      @cancel="confirm = null"
    />

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
    <ConfirmDialog
      v-if="clearing"
      :title="`Clear ${clearing}'s session?`"
      :message="`${clearing} starts over with a blank context — its conversation history is wiped (a busy member refuses; suspend it first). Schedule, skills, and memory files are kept.`"
      confirm-label="Clear session"
      :danger="true"
      @confirm="doClear"
      @cancel="clearing = ''"
    />
    <ConfirmDialog
      v-if="bypassing"
      :title="`Set ${bypassing} to bypass?`"
      :message="`${bypassing} runs fully autonomous — every tool call executes without approval (deny rules still bind). Applies immediately, mid-run included, and survives restarts until the swarm is freshly re-registered.`"
      confirm-label="Set bypass"
      :danger="true"
      @confirm="doBypass"
      @cancel="bypassing = ''"
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
.hbtn.on {
  border-style: solid;
  border-color: var(--color-accent);
  color: var(--color-text);
}
.bulkbar {
  position: sticky;
  bottom: 0;
  margin-top: var(--sp-2);
  padding: var(--sp-2);
  display: grid;
  gap: 0.3rem;
  background: var(--color-surface);
  border: 1px solid var(--color-accent);
  border-radius: var(--r-md);
}
.brow {
  display: flex;
  align-items: center;
  gap: 0.25rem;
  flex-wrap: wrap;
}
.brow.meta {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.spacer {
  flex: 1;
}
.glabel {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  width: 2.2rem;
  flex-shrink: 0;
}
.bulkbar .b,
.bulkbar .chip {
  background: var(--color-bg);
  border: 1px solid var(--color-line);
  border-radius: var(--r-sm);
  color: var(--color-text);
  cursor: pointer;
  font-size: var(--fs-xs);
  padding: 0.15rem 0.4rem;
  white-space: nowrap;
}
.bulkbar .b:hover:not(:disabled),
.bulkbar .chip:hover {
  border-color: var(--color-accent);
}
.bulkbar .b:disabled {
  opacity: 0.45;
  cursor: default;
}
.bulkbar .b.risky:not(:disabled) {
  color: var(--color-danger);
  border-color: color-mix(in srgb, var(--color-danger) 45%, transparent);
}
.bsum {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  margin: 0.1rem 0 0;
}
</style>
