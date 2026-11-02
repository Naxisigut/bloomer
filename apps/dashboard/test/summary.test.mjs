import test from 'node:test';
import assert from 'node:assert/strict';
import { createSummaryLoader } from '../public/summary.js';
import { queryAgent } from '../public/agent-client.js';
import { demoQuery } from '../public/demo.js';
import { AppError } from '../public/errors.js';

const now = new Date('2026-10-16T00:00:00Z');
const overview = { account_id: 'a', official: { available_balance_micro_usd: '7000' }, local_last_365_days: { record_count: 3, amount_micro_usd: '30' }, meta: { data_stale: false } };
const summary = { ...overview, last_hour: { from: '', to: '', record_count: 1, amount_micro_usd: '5' }, last_24_hours: { from: '', to: '', record_count: 2, amount_micro_usd: '12' } };

test('summary loader issues one request when the Agent supports it', async () => {
  const calls = [];
  const load = createSummaryLoader({ query: async (route, params, device) => { calls.push([route, device]); return summary; } });
  assert.deepEqual(await load('mac', now), summary);
  assert.deepEqual(calls, [['summary', 'mac']]);
});

test('summary loader falls back to overview and stats for older Agents and remembers it', async () => {
  const calls = [];
  const query = async (route, params) => {
    calls.push(route);
    if (route === 'summary') throw new AppError(404, 'summary_unsupported', 'old');
    if (route === 'overview') return overview;
    const hours = (Date.parse(params.to) - Date.parse(params.from)) / 3_600_000;
    return { items: hours === 1 ? [{ amount_micro_usd: '5', record_count: 1 }] : [{ amount_micro_usd: '7', record_count: 1 }, { amount_micro_usd: '-2', record_count: 2 }] };
  };
  const load = createSummaryLoader({ query });
  const result = await load('old', now);
  assert.deepEqual(result.last_hour, { from: '2026-10-15T23:00:00.000Z', to: now.toISOString(), record_count: 1, amount_micro_usd: '5' });
  assert.deepEqual(result.last_24_hours, { from: '2026-10-15T00:00:00.000Z', to: now.toISOString(), record_count: 3, amount_micro_usd: '5' });
  assert.equal(result.official.available_balance_micro_usd, '7000');
  await load('old', now);
  assert.deepEqual(calls, ['summary', 'overview', 'stats', 'stats', 'overview', 'stats', 'stats']);
});

test('summary loader surfaces other failures without falling back', async () => {
  const calls = [];
  const load = createSummaryLoader({ query: async route => { calls.push(route); throw new AppError(503, 'data_unavailable', 'none'); } });
  await assert.rejects(load('mac', now), { code: 'data_unavailable' });
  assert.deepEqual(calls, ['summary']);
});

test('summary route maps old Agents and rejects malformed totals', async () => {
  const device = { url: 'https://mac.tail123.ts.net:8443', token: 'synthetic-test-token' };
  const call = response => queryAgent(device, 'summary', undefined, { fetcher: async () => response });
  await assert.rejects(call(new Response('old', { status: 404 })), { code: 'summary_unsupported' });
  await assert.rejects(call(Response.json({ ...summary, last_hour: { record_count: 1, amount_micro_usd: 5 } })), { code: 'invalid_response' });
  await assert.rejects(call(Response.json({ ...summary, meta: undefined })), { code: 'invalid_response' });
  assert.deepEqual(await call(Response.json(summary)), summary);
});

test('demo summary matches the rolling stats it replaces', () => {
  const body = demoQuery('summary', undefined, now);
  for (const [key, hours] of [['last_hour', 1], ['last_24_hours', 24]]) {
    const range = { from: new Date(+now - hours * 3_600_000).toISOString(), to: now.toISOString() };
    const stats = demoQuery('stats', new URLSearchParams({ ...range, group_by: 'day', timezone: 'Asia/Shanghai' }), now);
    assert.equal(body[key].amount_micro_usd, stats.items.reduce((sum, item) => sum + BigInt(item.amount_micro_usd), 0n).toString());
    assert.equal(body[key].record_count, stats.items.reduce((sum, item) => sum + item.record_count, 0));
  }
  assert.equal(body.official.available_balance_micro_usd, demoQuery('overview', undefined, now).official.available_balance_micro_usd);
});
