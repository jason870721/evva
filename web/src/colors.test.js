import { test } from 'node:test'
import assert from 'node:assert/strict'
import { agentColor } from './colors.js'

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
