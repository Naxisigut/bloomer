import test from 'node:test';
import assert from 'node:assert/strict';
import { reading, activityStatus, pressureStatus, activityReason, localInput, inputRange, DeviceHistoryPager } from '../public/device-state.js';
import { demoQuery } from '../lib/demo.mjs';
import { queryAgent } from '../lib/proxy.mjs';

const now = Date.parse('2026-10-19T10:00:00Z');
const response = overrides => ({ sample: { collected_at: new Date(now).toISOString(), is_idle: true, inference_active: false, request_work_pending: false, quality: { is_idle: 'ok', inference_active: 'ok', request_work_pending: 'ok' }, ...overrides }, meta: { data_stale: false } });

test('activity displays sampled device verdict and preserves missing/stale observations', () => {
  assert.equal(activityStatus(response({}), now).label, '空闲');
  assert.equal(activityStatus(response({is_idle:false,inference_active:true}), now).label, '处理中');
  assert.equal(activityStatus(response({is_idle:false,request_work_pending:true}), now).label, '处理中');
  assert.equal(activityStatus(response({is_idle:false}), now).label, '近期有收益');
  assert.equal(activityStatus(response({is_idle:null,activity_error:'earnings_stale'}), now).label, '未知');
  assert.equal(activityStatus(response({quality:{}}), now).label, '未知');
  assert.equal(activityStatus(response({collected_at:new Date(now-121000).toISOString()}),now).label,'状态已过期');
  assert.equal(activityStatus(response({collected_at:new Date(now+1000).toISOString()}),now).label,'状态已过期');
  assert.equal(activityStatus({sample:null},now).label,'等待状态采集');
  assert.equal(activityStatus({error:'old agent',code:'device_state_unsupported'},now).label,'状态未提供');
  assert.match(activityReason({activity_error:'no_device_earnings'}),/该设备/);
});

test('local pressure indicators distinguish severity and stale/unknown readings',()=>{
  for (const [key,raw,expected] of [['thermal_state','nominal',''],['thermal_state','fair','warning'],['thermal_state','serious','offline'],['thermal_state','critical','offline'],['memory_pressure','normal',''],['memory_pressure','warning','warning'],['memory_pressure','critical','offline']]) {
    const r=response({[key]:raw,quality:{[key]:'ok'}});
    assert.equal(pressureStatus(r,key,now).className,expected);
    assert.match(pressureStatus(r,key,now+121000).label,/过期/);
  }
  assert.equal(pressureStatus(response({}),'memory_pressure',now).label,'未知');
});

test('metric formatting preserves zero and false, respects quality and rejects invalid numeric values', () => {
  const sample = { cpu_percent: 0, provider_running: false, swap_used_bytes: 0, memory_available_bytes: 8 * 2 ** 30, dispatch_tier: 'A', quality: { cpu_percent: 'ok', provider_running: 'ok', swap_used_bytes: 'ok', memory_available_bytes: 'ok', dispatch_tier: 'unsupported' } };
  assert.equal(reading(sample, 'cpu_percent').text, '0.0%');
  assert.equal(reading(sample, 'provider_running').text, '未运行');
  assert.equal(reading({ ...sample, provider_running: 'false' }, 'provider_running').known, false);
  assert.equal(reading(sample, 'swap_used_bytes').text, '0 B');
  assert.equal(reading(sample, 'memory_available_bytes').text, '8.00 GiB');
  assert.equal(reading(sample, 'dispatch_tier').known, false);
  assert.equal(reading({ ...sample, cpu_percent: NaN }, 'cpu_percent').known, false);
  assert.equal(reading({ ...sample, cpu_percent: 101 }, 'cpu_percent').known, false);
  assert.equal(reading({ ...sample, provider_cpu_percent: 250, quality: { provider_cpu_percent: 'ok' } }, 'provider_cpu_percent').text, '250.0%');
  assert.equal(reading(null, 'cpu_percent').text, '未提供');
});

test('query inputs use explicit timezones and reject invalid dates and backwards intervals', () => {
  assert.equal(localInput(now, 'Asia/Shanghai'), '2026-10-19T18:00');
  assert.deepEqual(inputRange('2026-10-19T17:00', '2026-10-19T18:00', 'Asia/Shanghai'), { from: '2026-10-19T09:00:00.000Z', to: '2026-10-19T10:00:00.000Z' });
  assert.equal(inputRange('2026-10-19T09:00', '2026-10-19T10:00', 'UTC').to, '2026-10-19T10:00:00.000Z');
  assert.throws(() => inputRange('2026-02-30T09:00', '2026-03-01T10:00', 'UTC'));
  assert.throws(() => inputRange('2026-10-19T10:00', '2026-10-19T09:00', 'UTC'));
});

test('state history freezes bounds, caches previous pages, and never repeats IDs', async () => {
  const clock = new Date(now), range = { from: new Date(now - 86400000).toISOString(), to: clock.toISOString() };
  const calls = [];
  const pager = new DeviceHistoryPager(async params => { calls.push(params); return demoQuery('device-history', new URLSearchParams(params), clock); }, range, 50);
  const first = await pager.page(0), second = await pager.page(1);
  assert.equal(first.items.length, 50); assert.equal(second.items.length, 50);
  assert.ok(second.items.every(item => item.id < first.items.at(-1).id));
  assert.equal(calls[1].before_id, first.next_before_id);
  assert.equal(calls[1].to, range.to);
  assert.equal(await pager.page(0), first); assert.equal(calls.length, 2);
  const old = demoQuery('device-history', new URLSearchParams({ from: new Date(now - 30 * 86400000).toISOString(), to: range.to, limit: '1' }), clock);
  assert.equal(old.meta.range_truncated, true);
  const latest = demoQuery('device-state', new URLSearchParams(), clock);
  assert.equal(latest.sample.dispatch_tier, null); assert.equal(latest.sample.quality.dispatch_tier, 'unsupported');
  assert.equal(reading(latest.sample, 'inference_active').known, true);
  const states = new Set([...first.items, ...second.items].map(item => item.is_idle));
  assert.ok(states.has(true)); assert.ok(states.has(false)); assert.ok(states.has(null));
  assert.equal(Object.hasOwn(latest.sample,'routing_state'),false);
});

test('failed or aborted state history pages remain retryable and invalid cursors fail', async () => {
  const range = { from: new Date(now - 86400000).toISOString(), to: new Date(now).toISOString() };
  let fail = true;
  const pager = new DeviceHistoryPager(async () => { if (fail) { fail = false; throw new Error('offline'); } return { items: [], next_before_id: null }; }, range, 50);
  await assert.rejects(pager.page(0), /offline/); assert.equal(pager.pages.length, 0);
  assert.equal((await pager.page(0)).items.length, 0);
  const controller = new AbortController();
  const cancelled = new DeviceHistoryPager(async () => { controller.abort(); return { items: [], next_before_id: null }; }, range, 50);
  await assert.rejects(cancelled.page(0, controller.signal), { name: 'AbortError' }); assert.equal(cancelled.pages.length, 0);
  const invalid = new DeviceHistoryPager(async () => ({ items: [{ id: 10, collected_at: new Date(now).toISOString() }], next_before_id: 11 }), range, 50);
  await assert.rejects(invalid.page(0), /翻页位置/);
});

test('state proxy forwards only whitelisted history params and handles older non-JSON agents', async () => {
  let requested;
  const fetcher = async (url, options) => { requested = { url, options }; return new Response(JSON.stringify({ items: [], next_before_id: null }), { status: 200 }); };
  await queryAgent({ url: 'http://127.0.0.1:17890', token: 'private' }, 'device-history', new URLSearchParams({ from: '2026-10-19T00:00:00Z', to: '2026-10-19T01:00:00Z', before_id: '42', limit: '25' }), fetcher);
  assert.equal(requested.url.pathname, '/v1/device/state/history');
  assert.equal(requested.url.searchParams.get('before_id'), '42');
  assert.equal(requested.options.headers.Authorization, 'Bearer private');
  for (const params of [new URLSearchParams({ model: 'no' }), new URLSearchParams('limit=1&limit=2'), new URLSearchParams({ url: 'https://example.com' })]) await assert.rejects(queryAgent({ url: 'http://127.0.0.1', token: 'private' }, 'device-history', params, fetcher), { code: 'invalid_request' });
  await assert.rejects(queryAgent({ url: 'http://127.0.0.1', token: 'private' }, 'device-state', new URLSearchParams(), async () => new Response('old server', { status: 404 })), { code: 'device_state_unsupported' });
});
