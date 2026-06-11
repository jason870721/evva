import { test } from 'node:test'
import assert from 'node:assert/strict'
import { splitProposals, countOpen } from './proposals.ts'
import type { ProposalInfo } from '../types/wire.ts'

function p(id: number, status: ProposalInfo['status'], createdAt: number, decidedAt?: number): ProposalInfo {
  return { id, proposer: 'qa', title: `p${id}`, status, createdAt, decidedAt }
}

test('splitProposals keeps the open queue oldest-first, sorts decided newest-first', () => {
  const { open, decided } = splitProposals([
    p(1, 'open', 100),
    p(2, 'accepted', 200, 500),
    p(3, 'open', 300),
    p(4, 'declined', 400, 900),
  ])
  assert.deepEqual(
    open.map((x) => x.id),
    [1, 3],
  )
  assert.deepEqual(
    decided.map((x) => x.id),
    [4, 2],
  )
})

test('splitProposals falls back to createdAt when decidedAt is absent', () => {
  const { decided } = splitProposals([p(1, 'accepted', 100), p(2, 'declined', 300)])
  assert.deepEqual(
    decided.map((x) => x.id),
    [2, 1],
  )
})

test('splitProposals and countOpen tolerate empty input', () => {
  assert.deepEqual(splitProposals(null), { open: [], decided: [] })
  assert.deepEqual(splitProposals(undefined).open, [])
  assert.equal(countOpen([]), 0)
  assert.equal(countOpen(null), 0)
})

test('countOpen counts only open proposals', () => {
  assert.equal(countOpen([p(1, 'open', 1), p(2, 'accepted', 2, 3), p(3, 'open', 3)]), 2)
})
