import test from 'node:test';
import assert from 'node:assert/strict';
import { clockOffsetFromHealth, correctedNow, enrichHealthTiming } from '../public/clock.js';

test('clock offset uses request midpoint and missing server_time stays zero', () => {
  assert.equal(clockOffsetFromHealth(undefined, 1000), 0);
  assert.equal(clockOffsetFromHealth('invalid', 1000), 0);
  const server = '2026-10-22T02:00:00.000Z';
  assert.equal(clockOffsetFromHealth(server, 1000), Date.parse(server) - 1000);
  const enriched = enrichHealthTiming({ version: '1', collection_state: 'idle', server_time: server }, 900, 1100);
  assert.equal(enriched.clockOffset, Date.parse(server) - 1000);
  assert.equal(enrichHealthTiming({ error: 'x' }, 0, 1).clockOffset, undefined);
});

test('corrected now shifts browser clock by stored offset', () => {
  const base = Date.now();
  const shifted = correctedNow(5000);
  assert.ok(shifted.getTime() >= base + 4990 && shifted.getTime() <= base + 5010);
});
