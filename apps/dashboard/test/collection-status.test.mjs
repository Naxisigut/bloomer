import test from 'node:test';
import assert from 'node:assert/strict';
import { collectionStatus, collectionStatusItems } from '../public/collection-status.js';

const now = Date.parse('2026-10-23T08:00:00Z');
const fresh = new Date(now - 30_000).toISOString();
const health = { collection_state: 'idle', data_stale: false, last_successful_sync_at: fresh };
const deviceState = { sample: { collected_at: fresh }, meta: { data_stale: false, collection_enabled: true } };
const networkRates = { enabled: true, updated_at: fresh, last_error: null, interval_minutes: 5 };

test('collection is normal only when every enabled collector is fresh', () => {
  assert.equal(collectionStatus({ health, deviceState, networkRates, now }).label, '采集正常');
  assert.equal(collectionStatus({ health: { ...health, collection_state: 'rate_limited' }, deviceState, networkRates, now }).label, '采集正常');
  assert.equal(collectionStatus({ health: { ...health, collection_state: 'disabled' }, deviceState, networkRates, now }).label, '采集正常');
  assert.equal(collectionStatus({ health, deviceState: { ...deviceState, meta: { ...deviceState.meta, collection_enabled: false } }, networkRates, now }).label, '采集正常');
  assert.equal(collectionStatus({ health, deviceState, networkRates: { ...networkRates, enabled: false }, now }).label, '采集正常');
});

test('a disabled collector is left out and an interrupted one fails the summary', () => {
  const closed = collectionStatus({
    health: { collection_state: 'disabled' },
    deviceState: { sample: null, meta: { data_stale: true, collection_enabled: false } },
    networkRates: { enabled: false },
    now,
  });
  assert.equal(closed.label, '采集已关闭');
  const stale = collectionStatus({ health: { ...health, data_stale: true }, deviceState, networkRates, now });
  assert.equal(stale.label, '采集异常');
  assert.match(stale.detail, /收益采集已中断/);
  const rates = collectionStatus({ health, deviceState, networkRates: { ...networkRates, last_error: 'stats_unavailable' }, now });
  assert.equal(rates.label, '采集异常');
  assert.match(rates.detail, /全网数据最近一次采集失败/);
  const old = collectionStatus({ health, deviceState, networkRates: { ...networkRates, updated_at: new Date(now - 11 * 60_000).toISOString() }, now });
  assert.equal(old.label, '采集异常');
  assert.match(old.detail, /全网数据采集已中断/);
});

test('missing inputs stay in progress', () => {
  assert.equal(collectionStatus({ health, deviceState, now }).label, '读取中');
});

test('collection status items expose three agent collectors', () => {
  const items = collectionStatusItems({ health, deviceState, networkRates, now });
  assert.deepEqual(items.map(item => item.label), ['收益采集', '运行状态采集', '全网数据采集']);
  assert.ok(items.every(item => item.text === '正常'));
  const closed = collectionStatusItems({
    health: { collection_state: 'disabled' },
    deviceState: { meta: { collection_enabled: false } },
    networkRates: { enabled: false },
    now,
  });
  assert.ok(closed.every(item => item.text === '已关闭'));
});
