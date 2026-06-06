<script setup>
import { ref } from 'vue'
import { agentColor } from '../colors.js'
import { displayPhase, phaseClass, elapsed } from '../events.js'

defineProps({
  members: { type: Array, default: () => [] },
  selected: { type: String, default: '' },
  now: { type: Number, default: 0 }, // ticking clock for live elapsed times
})
const emit = defineEmits([
  'select',
  'freeze',
  'unfreeze',
  'suspend',
  'resume',
  'open-form', // open the add-agent form (RP-8)
  'remove', // retire a worker (RP-8)
  'set-schedule', // { name, cron, prompt } (RP-8)
  'clear-schedule', // name (RP-8)
  'open-skills', // open the view/add/delete-skills dialog for a member (RP-10)
])

// Per-member inline schedule editor: which member's editor is open + its fields.
const editing = ref('')
const cron = ref('')
const prompt = ref('')
function openSchedule(m) {
  if (editing.value === m.name) {
    editing.value = ''
    return
  }
  editing.value = m.name
  cron.value = m.cron || ''
  prompt.value = m.schedulePrompt || ''
}
function saveSchedule(name) {
  const c = cron.value.trim()
  if (!c) return
  emit('set-schedule', { name, cron: c, prompt: prompt.value.trim() })
  editing.value = ''
}
function clearSchedule(name) {
  emit('clear-schedule', name)
  editing.value = ''
}
</script>

<template>
  <div class="roster">
    <div class="rhead">
      <span>Roster</span>
      <button class="addbtn" @click="emit('open-form')" title="Add a new agent">+ add agent</button>
    </div>
    <ul>
      <li
        v-for="m in members"
        :key="m.name"
        :class="{ sel: m.name === selected }"
        @click="emit('select', m.name)"
      >
        <div class="line1">
          <span class="name">
            <span class="dot" :style="{ background: agentColor(m.name) }"></span>{{ m.name }}
          </span>
          <span class="role" :class="m.role">{{ m.role }}</span>
        </div>
        <div class="line2">
          <!-- "active" is the norm — only flag the exception (frozen) to cut noise. -->
          <span v-if="m.membership !== 'active'" :class="['badge', m.membership]">{{ m.membership }}</span>
          <span :class="['badge', 'phase-' + phaseClass(m)]" :title="displayPhase(m)">{{ displayPhase(m) }}</span>
          <span v-if="phaseClass(m) !== 'idle' && m.phaseSince" class="since">{{ elapsed(m.phaseSince, now) }}</span>
          <span v-if="m.currentTask" class="task">#{{ m.currentTask }}</span>
        </div>
        <!-- The crontab, pinned to the card so it's always visible (RP-7/RP-8). -->
        <div v-if="m.cron" class="sched" :title="m.schedulePrompt">
          ⏰ {{ m.cron }}<span v-if="m.schedulePrompt"> · {{ m.schedulePrompt }}</span>
        </div>
        <div class="ctl" @click.stop>
          <button v-if="m.membership === 'active'" @click="emit('freeze', m.name)">freeze</button>
          <button v-else @click="emit('unfreeze', m.name)">unfreeze</button>
          <button v-if="m.run === 'busy'" @click="emit('suspend', m.name)">suspend</button>
          <button v-else-if="m.run === 'suspended'" @click="emit('resume', m.name)">resume</button>
          <button @click="openSchedule(m)">schedule</button>
          <button @click="emit('open-skills', m.name)">skills</button>
          <!-- The leader is unique — never removable (RP-8 §3.E). -->
          <button v-if="m.role !== 'leader'" class="rm" @click="emit('remove', m.name)">remove</button>
        </div>
        <!-- Inline schedule editor: the operator may schedule ANY member, incl. the leader. -->
        <div v-if="editing === m.name" class="schededit" @click.stop>
          <input v-model="cron" placeholder='cron e.g. "*/30 * * * *"' @keyup.enter="saveSchedule(m.name)" />
          <input v-model="prompt" placeholder="wake prompt (optional)" @keyup.enter="saveSchedule(m.name)" />
          <div class="srow">
            <button @click="saveSchedule(m.name)">set</button>
            <button v-if="m.cron" class="rm" @click="clearSchedule(m.name)">clear</button>
          </div>
        </div>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.roster {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.rhead {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-weight: 600;
  font-size: 0.85rem;
  padding: 0 0.2rem 0.5rem;
}
.addbtn {
  font-size: var(--fs-xs);
  padding: 0.15rem 0.5rem;
  background: transparent;
  border: 1px dashed var(--line);
  border-radius: 6px;
  color: var(--dim);
  cursor: pointer;
}
.addbtn:hover {
  border-color: var(--accent);
  color: #e6edf3;
}
ul {
  list-style: none;
  margin: 0;
  padding: 0;
  overflow: auto;
  flex: 1;
  display: grid;
  gap: 0.4rem;
}
li {
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.5rem 0.55rem;
  cursor: pointer;
  background: var(--panel);
}
li.sel {
  border-color: var(--accent);
}
.line1 {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}
.name {
  font-weight: 600;
  font-size: 0.85rem;
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
}
.dot {
  width: 0.55rem;
  height: 0.55rem;
  border-radius: 50%;
  flex: none;
}
.role {
  font-size: var(--fs-xs);
  text-transform: uppercase;
  color: var(--dim);
}
.role.leader {
  color: var(--accent);
}
.line2 {
  display: flex;
  gap: 0.35rem;
  margin-top: 0.35rem;
  align-items: center;
}
.badge {
  font-size: var(--fs-xs);
  padding: 0.05rem 0.35rem;
  border-radius: 10px;
  border: 1px solid var(--line);
  color: var(--dim);
}
.badge.active { color: #22c55e; border-color: #22c55e55; }
.badge.frozen { color: #60a5fa; border-color: #60a5fa55; }
/* "thinking" — the LLM is generating (running/thinking/texting collapsed). Sky
   blue makes "the model is working" pop, distinct from the amber busy below
   (which now only shows for non-LLM busy states) and executing a tool. */
.badge.phase-thinking { color: #38bdf8; border-color: #38bdf855; }
.badge.phase-busy { color: #f59e0b; border-color: #f59e0b55; }
.badge.phase-suspended { color: #ef4444; border-color: #ef444455; }
/* waiting-approval / waiting-input demands operator action — make it loud. */
.badge.phase-waiting { color: #a855f7; border-color: #a855f7; font-weight: 600; }
.badge.phase-error { color: #ef4444; border-color: #ef444455; }
.badge.phase-idle { color: var(--dim); }
.since {
  font-family: var(--mono);
  font-size: var(--fs-xs);
  color: var(--dim);
}
.task {
  font-family: var(--mono);
  font-size: var(--fs-xs);
  color: var(--dim);
}
.sched {
  margin-top: 0.35rem;
  font-size: var(--fs-xs);
  color: var(--dim);
  font-family: var(--mono);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
/* Controls are revealed on hover / when selected, so the resting roster is calm. */
.ctl {
  display: none;
  gap: 0.3rem;
  margin-top: 0.45rem;
  flex-wrap: wrap;
}
li:hover .ctl,
li.sel .ctl {
  display: flex;
}
.ctl button {
  font-size: var(--fs-xs);
  padding: 0.1rem 0.4rem;
}
.ctl .rm {
  margin-left: auto;
  color: var(--danger);
  border-color: var(--danger);
}
.schededit {
  margin-top: 0.45rem;
  display: grid;
  gap: 0.3rem;
}
.schededit input {
  width: 100%;
  min-width: 0;
  font-size: var(--fs-xs);
  font-family: var(--mono);
}
.srow {
  display: flex;
  gap: 0.3rem;
}
.srow button {
  font-size: var(--fs-xs);
  padding: 0.1rem 0.5rem;
}
.srow .rm {
  color: var(--danger);
  border-color: var(--danger);
}
</style>
