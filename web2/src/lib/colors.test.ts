import { test } from 'node:test'
import assert from 'node:assert/strict'
import { agentColor, contextColor } from './colors.ts'

test('agentColor is stable and case/space-insensitive', () => {
  const a = agentColor('builder')
  assert.equal(a, agentColor('builder'))
  assert.equal(a, agentColor('  Builder '))
  assert.match(a, /^#[0-9a-f]{6}$/i)
})

test('fixed pseudo-recipients get their reserved colours', () => {
  assert.equal(agentColor('user'), '#e6edf3')
  assert.equal(agentColor('all'), '#8a929c')
})

test('distinct members usually get distinct colours', () => {
  const names = ['lead', 'builder', 'reviewer']
  const colors = new Set(names.map(agentColor))
  assert.equal(colors.size, names.length)
})

test('empty/unknown name falls back to neutral, never throws', () => {
  assert.equal(agentColor(''), '#8a929c')
  assert.equal(agentColor(null), '#8a929c')
  assert.equal(agentColor(undefined), '#8a929c')
})

test('contextColor hits the TUI band anchors (green→yellow→red)', () => {
  assert.equal(contextColor(0), '#39ff14') // green
  assert.equal(contextColor(20), '#39ff14') // still green at the band edge
  assert.equal(contextColor(50), '#fafc4e') // yellow plateau
  assert.equal(contextColor(100), '#ff003c') // red
})

test('contextColor interpolates through the transition bands', () => {
  const mid = contextColor(30) // halfway green→yellow
  assert.match(mid, /^#[0-9a-f]{6}$/i)
  assert.notEqual(mid, '#39ff14')
  assert.notEqual(mid, '#fafc4e')
  const hot = contextColor(70) // halfway yellow→red
  assert.match(hot, /^#[0-9a-f]{6}$/i)
  assert.notEqual(hot, '#fafc4e')
  assert.notEqual(hot, '#ff003c')
})

test('contextColor clamps out-of-range input, never throws', () => {
  assert.equal(contextColor(-50), '#39ff14')
  assert.equal(contextColor(999), '#ff003c')
  assert.equal(contextColor(NaN), '#39ff14')
})
