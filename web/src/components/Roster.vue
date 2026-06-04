<script setup>
import { ref } from 'vue'

defineProps({
  members: { type: Array, default: () => [] },
  selected: { type: String, default: '' },
})
const emit = defineEmits(['select', 'freeze', 'unfreeze', 'suspend', 'resume', 'add'])

const newMember = ref('')
function add() {
  const name = newMember.value.trim()
  if (name) {
    emit('add', name)
    newMember.value = ''
  }
}
</script>

<template>
  <div class="roster">
    <div class="rhead">Roster</div>
    <ul>
      <li
        v-for="m in members"
        :key="m.name"
        :class="{ sel: m.name === selected }"
        @click="emit('select', m.name)"
      >
        <div class="line1">
          <span class="name">{{ m.name }}</span>
          <span class="role" :class="m.role">{{ m.role }}</span>
        </div>
        <div class="line2">
          <span :class="['badge', m.membership]">{{ m.membership }}</span>
          <span :class="['badge', 'run-' + m.run]">{{ m.run }}</span>
          <span v-if="m.currentTask" class="task">#{{ m.currentTask }}</span>
        </div>
        <div class="ctl" @click.stop>
          <button v-if="m.membership === 'active'" @click="emit('freeze', m.name)">freeze</button>
          <button v-else @click="emit('unfreeze', m.name)">unfreeze</button>
          <button v-if="m.run === 'busy'" @click="emit('suspend', m.name)">suspend</button>
          <button v-else-if="m.run === 'suspended'" @click="emit('resume', m.name)">resume</button>
        </div>
      </li>
    </ul>

    <div class="add">
      <input v-model="newMember" placeholder="agents/sub/<name>" @keyup.enter="add" />
      <button @click="add">add</button>
    </div>
  </div>
</template>

<style scoped>
.roster {
  display: flex;
  flex-direction: column;
  height: 100%;
}
.rhead {
  font-weight: 600;
  font-size: 0.85rem;
  padding: 0 0.2rem 0.5rem;
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
}
.role {
  font-size: 0.65rem;
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
  font-size: 0.65rem;
  padding: 0.05rem 0.35rem;
  border-radius: 10px;
  border: 1px solid var(--line);
  color: var(--dim);
}
.badge.active { color: #22c55e; border-color: #22c55e55; }
.badge.frozen { color: #60a5fa; border-color: #60a5fa55; }
.badge.run-busy { color: #f59e0b; border-color: #f59e0b55; }
.badge.run-suspended { color: #ef4444; border-color: #ef444455; }
.task {
  font-family: var(--mono);
  font-size: 0.65rem;
  color: var(--dim);
}
.ctl {
  display: flex;
  gap: 0.3rem;
  margin-top: 0.45rem;
}
.ctl button {
  font-size: 0.68rem;
  padding: 0.1rem 0.4rem;
}
.add {
  display: flex;
  gap: 0.3rem;
  margin-top: 0.5rem;
}
.add input {
  flex: 1;
  min-width: 0;
}
</style>
