import test from 'node:test';
import assert from 'node:assert/strict';
import { reading, activityStatus, pressureStatus, activityReason } from '../public/device-state.js';
import { demoQuery } from '../public/demo.js';
import { queryAgent } from '../public/agent-client.js';

const now = Date.parse('2026-10-19T10:00:00Z');
const response = overrides => ({ sample: { collected_at: new Date(now).toISOString(), is_idle: true, inference_active: false, request_work_pending: false, quality: { is_idle: 'ok', inference_active: 'ok', request_work_pending: 'ok' }, ...overrides }, meta: { data_stale: false } });

test('activity displays local task flags and ignores earnings idle verdict', () => {
  assert.equal(activityStatus(response({}), now).label, '无任务');
  assert.equal(activityStatus(response({is_idle:false,inference_active:true,request_work_pending:true}), now).label, '处理中');
  assert.equal(activityStatus(response({is_idle:false,request_work_pending:true}), now).label, '排队');
  assert.equal(activityStatus(response({is_idle:false,activity_error:'earnings_stale'}), now).label, '无任务');
  assert.equal(activityStatus(response({is_idle:null,inference_active:false,request_work_pending:false,activity_error:'earnings_stale',quality:{is_idle:'unavailable',inference_active:'ok',request_work_pending:'ok'}}), now).label, '无任务');
  assert.equal(activityStatus(response({inference_active:true,quality:{inference_active:'ok'}}), now).label, '处理中');
  assert.equal(activityStatus(response({quality:{inference_active:'ok',request_work_pending:'stale'}}), now).label, '未知');
  assert.equal(activityStatus(response({quality:{}}), now).label, '未知');
  assert.equal(activityStatus(response({provider_running:false,quality:{provider_running:'ok',is_idle:'unavailable'}}), now).label, '未运行');
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

test('demo latest state omits legacy routing fields', () => {
  const clock = new Date(now);
  const latest = demoQuery('device-state', new URLSearchParams(), clock);
  assert.equal(latest.sample.dispatch_tier, null);
  assert.equal(latest.sample.quality.dispatch_tier, 'unsupported');
  assert.equal(reading(latest.sample, 'inference_active').known, true);
  assert.equal(Object.hasOwn(latest.sample, 'routing_state'), false);
  assert.equal(latest.meta.sample_interval_seconds, 60);
});

test('browser state client rejects unknown query params and handles older non-JSON agents', async () => {
  await assert.rejects(queryAgent({ url: 'http://127.0.0.1', token: 'private' }, 'device-state', new URLSearchParams({ limit: '1' }), { fetcher: async () => new Response('{}', { status: 200 }) }), { code: 'invalid_request' });
  await assert.rejects(queryAgent({ url: 'http://127.0.0.1', token: 'private' }, 'device-state', new URLSearchParams(), { fetcher: async () => new Response('old server', { status: 404 }) }), { code: 'device_state_unsupported' });
});
