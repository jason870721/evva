<script setup>
defineProps({
  spaces: { type: Array, default: () => [] },
  error: { type: String, default: '' },
})
const emit = defineEmits(['enter', 'refresh', 'reset-token'])

// A stopped space has no live agents to stream, so it can't be opened — start it
// first with `evva swarm run <name>`. Only running spaces are enterable.
function enter(s) {
  if (s.status === 'stopped') return
  emit('enter', s)
}
</script>

<template>
  <section class="picker">
    <header>
      <h1>evva · swarm</h1>
      <div class="actions">
        <button @click="emit('refresh')">↻ refresh</button>
        <button class="ghost" @click="emit('reset-token')">change token</button>
      </div>
    </header>

    <p v-if="error" class="err">{{ error }}</p>

    <p v-if="!spaces.length" class="dim">
      No swarms registered. Start one with <code>evva swarm .</code> in a
      directory that has an <code>evva-swarm.yml</code>.
    </p>

    <ul v-else class="spaces">
      <li
        v-for="s in spaces"
        :key="s.id"
        :class="{ stopped: s.status === 'stopped' }"
        @click="enter(s)"
      >
        <div class="name">
          {{ s.name || s.id }}
          <span class="status" :class="s.status || 'running'">{{ s.status || 'running' }}</span>
        </div>
        <div class="meta">
          <span>{{ s.members }} member{{ s.members === 1 ? '' : 's' }}</span>
          <span class="path">{{ s.workdir }}</span>
        </div>
        <div class="id">{{ s.id }}</div>
        <div v-if="s.status === 'stopped'" class="hint">
          stopped — run <code>evva swarm run {{ s.name || s.id }}</code> to start
        </div>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.picker {
  max-width: 48rem;
  margin: 3rem auto;
  padding: 0 1.25rem;
}
header {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}
h1 {
  font-size: 1.4rem;
}
.actions {
  display: flex;
  gap: 0.5rem;
}
.spaces {
  list-style: none;
  padding: 0;
  margin-top: 1.5rem;
  display: grid;
  gap: 0.75rem;
}
.spaces li {
  border: 1px solid var(--line);
  border-radius: 8px;
  padding: 0.9rem 1.1rem;
  cursor: pointer;
  background: var(--panel);
}
.spaces li:hover {
  border-color: var(--accent);
}
.name {
  font-weight: 600;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.status {
  font-size: 0.62rem;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  font-weight: 600;
  padding: 0.05rem 0.4rem;
  border-radius: 10px;
  border: 1px solid;
}
.status.running {
  color: #22c55e;
  border-color: #22c55e55;
}
.status.stopped {
  color: var(--dim);
  border-color: var(--line);
}
/* A stopped space can't be opened (no live agents) — show it dimmed + not-allowed. */
.spaces li.stopped {
  opacity: 0.6;
  cursor: not-allowed;
}
.spaces li.stopped:hover {
  border-color: var(--line);
}
.hint {
  margin-top: 0.4rem;
  font-size: 0.72rem;
  color: var(--dim);
}
.meta {
  display: flex;
  gap: 1rem;
  color: var(--dim);
  font-size: 0.85rem;
  margin-top: 0.2rem;
}
.path {
  font-family: var(--mono);
}
.id {
  font-family: var(--mono);
  font-size: 0.7rem;
  color: var(--dim);
  margin-top: 0.35rem;
}
.err {
  color: var(--danger);
}
.dim {
  color: var(--dim);
}
</style>
