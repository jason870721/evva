import { test } from 'node:test'
import assert from 'node:assert/strict'
import { isValidCron, describeCron, nextFire } from './cron.ts'

test('isValidCron accepts standard forms, rejects junk and wrong arity', () => {
  assert.ok(isValidCron('*/30 * * * *'))
  assert.ok(isValidCron('0 9 * * 1-5'))
  assert.ok(isValidCron('0 0 1 1 *'))
  assert.ok(!isValidCron('* * * *')) // 4 fields
  assert.ok(!isValidCron('bad'))
  assert.ok(!isValidCron('99 * * * *')) // minute out of range
})

test('describeCron renders common patterns', () => {
  assert.equal(describeCron('*/30 * * * *'), 'every 30 min')
  assert.equal(describeCron('0 * * * *'), 'hourly at :00')
  assert.equal(describeCron('0 9 * * *'), 'daily at 09:00')
  assert.equal(describeCron('30 8 * * 1'), 'weekly on Mon at 08:30')
  assert.equal(describeCron('nope'), 'invalid cron')
})

test('nextFire computes the next matching minute', () => {
  const from = new Date(2026, 0, 1, 10, 30, 0).getTime()
  assert.equal(nextFire('0 * * * *', from), new Date(2026, 0, 1, 11, 0, 0).getTime())
  assert.equal(nextFire('0 9 * * *', new Date(2026, 0, 1, 10, 0, 0).getTime()), new Date(2026, 0, 2, 9, 0, 0).getTime())
  assert.equal(nextFire('bad', from), null)
})
