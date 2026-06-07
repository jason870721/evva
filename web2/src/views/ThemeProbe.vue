<script setup lang="ts">
// FE-1 theme probe (now at /probe) — a living reference for the token system and
// base atoms. Proves the foundation end-to-end: tokens render, the theme switch
// swaps the whole palette with no FOUC, atoms read only tokens, and the ported
// pure-logic layer (colors/events) runs in-bundle.
import { ref } from 'vue'
import { useUiStore, THEMES, type ThemeName } from '../stores/ui'
import EvButton from '../components/base/EvButton.vue'
import EvBadge from '../components/base/EvBadge.vue'
import EvPill from '../components/base/EvPill.vue'
import EvPanel from '../components/base/EvPanel.vue'
import EvSpinner from '../components/base/EvSpinner.vue'
import EvIcon from '../components/base/EvIcon.vue'
import EvDialog from '../components/base/EvDialog.vue'
import { agentColor } from '../lib/colors'
import { displayPhase, phaseClass } from '../lib/events'

const ui = useUiStore()
const dialogOpen = ref(false)

const SWATCHES: [string, string][] = [
  ['--color-bg', 'bg'],
  ['--color-surface', 'surface'],
  ['--color-text', 'text'],
  ['--color-text-muted', 'muted'],
  ['--color-accent', 'accent'],
  ['--color-accent-2', 'accent-2'],
  ['--phase-thinking', 'thinking'],
  ['--phase-executing', 'executing'],
  ['--phase-waiting', 'waiting'],
  ['--color-tool-result', 'tool-result'],
  ['--status-completed', 'success'],
  ['--color-danger', 'danger'],
  ['--status-suspended', 'paused'],
  ['--color-info', 'info'],
  ['--color-flourish', 'flourish'],
]

const PHASES: { tone: string; label: string; glyph?: string }[] = [
  { tone: 'thinking', label: 'thinking' },
  { tone: 'executing', label: 'executing:bash' },
  { tone: 'waiting', label: 'waiting-approval' },
  { tone: 'error', label: 'error' },
  { tone: 'idle', label: 'ready' },
  { tone: 'suspended', label: 'suspended' },
]
const STATUSES = ['pending', 'running', 'suspended', 'verifying', 'completed']
const ICONS = ['play', 'pause', 'square', 'clock', 'shield', 'bolt', 'dots', 'warning', 'snowflake', 'refresh', 'check', 'close']
const AGENTS = ['lead', 'qa', 'frontend', 'backend-a', 'designer', 'pm']

const sample = { run: 'busy', phase: 'executing', tool: 'bash' }
const sampleLabel = `${displayPhase(sample)}  ·  class=${phaseClass(sample)}`

function pick(t: ThemeName) {
  ui.setTheme(t)
}
</script>

<template>
  <div class="probe">
    <header class="top">
      <div class="brand">
        <span class="logo">evva</span><span class="sep">·</span><span>swarm</span>
        <span class="tag">FE v2 · foundations</span>
      </div>
      <div class="themectl">
        <span class="now">theme: <code>{{ ui.theme }}</code></span>
        <EvButton
          v-for="t in THEMES"
          :key="t"
          size="sm"
          :variant="ui.theme === t ? 'primary' : 'ghost'"
          @click="pick(t)"
        >
          {{ t }}
        </EvButton>
        <EvButton size="sm" @click="ui.toggleTheme()">
          <EvIcon name="refresh" :size="14" /> toggle
        </EvButton>
        <RouterLink to="/"><EvButton size="sm">← app</EvButton></RouterLink>
      </div>
    </header>

    <div class="grid">
      <EvPanel title="Design tokens (NEON TOKYO)">
        <div class="swatches">
          <div v-for="[token, label] in SWATCHES" :key="token" class="swatch">
            <span class="chip" :style="{ background: `var(${token})` }" />
            <span class="sl">{{ label }}</span>
          </div>
        </div>
        <p class="hint">切換主題只換 primitive 對照表，這些語意色自動跟著走。</p>
      </EvPanel>

      <EvPanel title="Phase pills (對齊 TUI)">
        <div class="row">
          <EvPill v-for="p in PHASES" :key="p.label" :tone="p.tone" :label="p.label" :glyph="p.glyph" />
        </div>
        <div class="sub">task status</div>
        <div class="row">
          <EvPill v-for="s in STATUSES" :key="s" :tone="s" :label="s" />
        </div>
      </EvPanel>

      <EvPanel title="Buttons">
        <div class="row">
          <EvButton variant="primary">Primary</EvButton>
          <EvButton>Ghost</EvButton>
          <EvButton variant="danger">Danger</EvButton>
          <EvButton :loading="true">Loading</EvButton>
          <EvButton size="sm">small</EvButton>
          <EvButton @click="dialogOpen = true">Open dialog</EvButton>
        </div>
      </EvPanel>

      <EvPanel title="Badges & icons">
        <div class="row">
          <EvBadge tone="accent">accent</EvBadge>
          <EvBadge tone="success">active</EvBadge>
          <EvBadge tone="frozen">frozen</EvBadge>
          <EvBadge tone="warning">paused</EvBadge>
          <EvBadge tone="danger">error</EvBadge>
          <EvBadge tone="info">info</EvBadge>
        </div>
        <div class="row icons">
          <EvIcon v-for="n in ICONS" :key="n" :name="n" :size="20" />
        </div>
      </EvPanel>

      <EvPanel title="Per-agent colour legend">
        <div class="row">
          <span v-for="a in AGENTS" :key="a" class="agent">
            <span class="dot" :style="{ background: agentColor(a) }" />{{ a }}
          </span>
        </div>
      </EvPanel>

      <EvPanel title="Ported lib smoke (colors.ts / events.ts)">
        <div class="row mid">
          <EvSpinner :size="18" />
          <code>{{ sampleLabel }}</code>
        </div>
        <p class="hint">這行由 events.ts 的 displayPhase/phaseClass 在 bundle 內計算；測試見 <code>npm test</code>。</p>
      </EvPanel>
    </div>

    <EvDialog v-if="dialogOpen" title="EvDialog" width="26rem" @close="dialogOpen = false">
      <p>Esc 或點背景關閉。FE-6 的審批/確認、FE-7 的表單都會建在這個基座上。</p>
      <template #footer>
        <EvButton @click="dialogOpen = false">Cancel</EvButton>
        <EvButton variant="primary" @click="dialogOpen = false">OK</EvButton>
      </template>
    </EvDialog>
  </div>
</template>

<style scoped>
.probe {
  max-width: 72rem;
  margin: 0 auto;
  padding: var(--sp-5) var(--sp-4) var(--sp-6);
}
.top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: var(--sp-3);
  margin-bottom: var(--sp-5);
}
.brand {
  display: flex;
  align-items: baseline;
  gap: 0.4rem;
  font-size: var(--fs-xl);
  font-weight: 700;
}
.logo {
  color: var(--color-accent);
}
.sep {
  color: var(--color-accent-2);
}
.tag {
  font-size: var(--fs-xs);
  font-weight: 500;
  color: var(--color-text-muted);
  border: 1px solid var(--color-line);
  border-radius: var(--r-pill);
  padding: 0.1rem 0.5rem;
  margin-left: 0.4rem;
}
.themectl {
  display: flex;
  align-items: center;
  gap: var(--sp-2);
}
.now {
  font-size: var(--fs-sm);
  color: var(--color-text-muted);
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(20rem, 1fr));
  gap: var(--sp-3);
}
.swatches {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(5rem, 1fr));
  gap: var(--sp-2);
}
.swatch {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.chip {
  height: 2.2rem;
  border-radius: var(--r-md);
  border: 1px solid var(--color-line);
}
.sl {
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
}
.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--sp-2);
}
.row.mid {
  align-items: center;
}
.row.icons {
  margin-top: var(--sp-3);
  color: var(--color-accent);
}
.sub {
  margin: var(--sp-3) 0 var(--sp-1);
  font-size: var(--fs-xs);
  color: var(--color-text-muted);
}
.agent {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  font-size: var(--fs-sm);
}
.dot {
  width: 0.7rem;
  height: 0.7rem;
  border-radius: 50%;
}
.hint {
  margin-top: var(--sp-3);
  font-size: var(--fs-xs);
  color: var(--color-text-faint);
}
code {
  background: var(--color-surface-2);
  padding: 0.05rem 0.3rem;
  border-radius: var(--r-sm);
}
</style>
