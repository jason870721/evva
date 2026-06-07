import { test } from 'node:test'
import assert from 'node:assert/strict'
import { buildTimeline } from './timeline.ts'

test('buildTimeline merges messages + task lifecycle, newest first', () => {
  const msgs = [{ id: 'x', sender: 'lead', recipient: 'qa', body: 'go', createdAt: 100 }]
  const tasks = [
    { id: 1, title: 't', spec: '', status: 'running', assignee: 'qa', createdBy: 'lead', createdAt: 50, updatedAt: 150 },
  ]
  const tl = buildTimeline(msgs, tasks)
  assert.equal(tl.length, 3) // message + task-created + task-transition
  assert.equal(tl[0].time, 150) // newest first
  const kinds = new Set(tl.map((i) => i.kind))
  assert.ok(kinds.has('message'))
  assert.ok(kinds.has('task'))
})

test('buildTimeline omits a no-op transition (updatedAt === createdAt)', () => {
  const tasks = [
    { id: 2, title: 'x', spec: '', status: 'pending', assignee: '', createdBy: 'lead', createdAt: 10, updatedAt: 10 },
  ]
  const tl = buildTimeline([], tasks)
  assert.equal(tl.length, 1) // only "created", no transition
})
