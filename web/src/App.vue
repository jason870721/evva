<script setup>
import { ref, onMounted } from 'vue'
import { createApi } from './api.js'
import SpacePicker from './components/SpacePicker.vue'
import SpaceView from './components/SpaceView.vue'

const TOKEN_KEY = 'evva-swarm-token'

const token = ref(localStorage.getItem(TOKEN_KEY) || '')
const tokenDraft = ref('')
const spaces = ref([])
const active = ref(null) // the SpaceInfo we entered, or null
const error = ref('')

const api = createApi(() => token.value)

function saveToken() {
  const t = tokenDraft.value.trim()
  if (!t) return
  token.value = t
  localStorage.setItem(TOKEN_KEY, t)
  loadSpaces()
}
function resetToken() {
  token.value = ''
  localStorage.removeItem(TOKEN_KEY)
  spaces.value = []
  active.value = null
}

async function loadSpaces() {
  if (!token.value) return
  try {
    spaces.value = (await api.spaces()) || []
    error.value = ''
  } catch (e) {
    error.value = String(e.message || e)
  }
}

function leave() {
  active.value = null
  loadSpaces()
}

onMounted(loadSpaces)
</script>

<template>
  <!-- Token gate: the service prints the session token on `evva service start`. -->
  <div v-if="!token" class="gate">
    <h1>evva · swarm</h1>
    <p class="dim">Enter the session token. Default while in development: <code>root</code>.</p>
    <div class="row">
      <input
        v-model="tokenDraft"
        type="password"
        placeholder="session token (default: root)"
        @keyup.enter="saveToken"
      />
      <button class="primary" @click="saveToken">Connect</button>
    </div>
  </div>

  <SpaceView
    v-else-if="active"
    :api="api"
    :token="token"
    :space="active"
    @leave="leave"
  />

  <SpacePicker
    v-else
    :spaces="spaces"
    :error="error"
    @enter="(s) => (active = s)"
    @refresh="loadSpaces"
    @reset-token="resetToken"
  />
</template>

<style>
:root {
  --bg: #0e1116;
  --panel: #161b22;
  --line: #2a313c;
  --dim: #8a929c;
  --accent: #3b82f6;
  --danger: #ef4444;
  --mono: ui-monospace, SFMono-Regular, Menlo, monospace;
  /* Type scale (RP-4 UX-4). One small set of steps, floored at ~11.5px so the
     dense meta text is still legible — replaces the scattered 0.62–0.68rem
     (≈10px) values. Components reference these instead of magic numbers. */
  --fs-xs: 0.72rem;
  --fs-sm: 0.8rem;
  --fs-md: 0.9rem;
  --fs-lg: 1.05rem;
}
* {
  box-sizing: border-box;
}
body {
  margin: 0;
  background: var(--bg);
  color: #e6edf3;
  font-family: system-ui, -apple-system, Segoe UI, Roboto, sans-serif;
  font-size: var(--fs-md);
}
/* Visible keyboard focus everywhere (a11y) — the browser default is often
   invisible on a dark theme. */
:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: 1px;
}
button {
  background: var(--panel);
  color: inherit;
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.3rem 0.6rem;
  cursor: pointer;
  font-size: 0.8rem;
}
button:hover {
  border-color: var(--accent);
}
button.ghost {
  background: transparent;
}
button.primary {
  background: var(--accent);
  border-color: var(--accent);
  color: #fff;
}
button.danger {
  color: var(--danger);
  border-color: var(--danger);
}
input,
textarea {
  background: var(--bg);
  color: inherit;
  border: 1px solid var(--line);
  border-radius: 6px;
  padding: 0.35rem 0.5rem;
  font-size: 0.85rem;
}
input:focus,
textarea:focus {
  outline: none;
  border-color: var(--accent);
}
code {
  font-family: var(--mono);
  background: var(--panel);
  padding: 0.05rem 0.3rem;
  border-radius: 4px;
}
</style>

<style scoped>
.gate {
  max-width: 30rem;
  margin: 6rem auto;
  padding: 0 1.25rem;
}
.dim {
  color: var(--dim);
}
.row {
  display: flex;
  gap: 0.5rem;
  margin-top: 1rem;
}
.row input {
  flex: 1;
}
</style>
