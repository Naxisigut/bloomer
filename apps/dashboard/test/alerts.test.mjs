import test from 'node:test';
import assert from 'node:assert/strict';
import { queryAgent, createAgentClient } from '../public/agent-client.js';
import { alertRecordHTML } from '../public/alert-ui.js';
import { demoQuery } from '../public/demo.js';

const device = { url: 'http://127.0.0.1:17890', token: 'dashboard-token' };
const escape = value => String(value).replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
test('alert history and empty-body test POST use fixed authenticated routes', async () => {
  const record = demoQuery('alerts').items[0];
  const calls = [];
  const fetcher = async (url, options) => { calls.push([url, options]); return new Response(JSON.stringify(url.pathname.endsWith('/test') ? { type: 'test', delivery_status: 'pending' } : { items: [record] }), { status: url.pathname.endsWith('/test') ? 202 : 200 }); };
  assert.deepEqual((await queryAgent(device, 'alerts', undefined, { fetcher })).items, [record]);
  await queryAgent(device, 'alert-test', undefined, { fetcher });
  assert.equal(calls[0][0].pathname, '/v1/alerts'); assert.equal(calls[0][1].method, 'GET');
  assert.equal(calls[1][0].pathname, '/v1/alerts/test'); assert.equal(calls[1][1].method, 'POST'); assert.equal(calls[1][1].body, undefined);
  for (const [, options] of calls) assert.equal(options.headers.Authorization, 'Bearer dashboard-token');
  await assert.rejects(queryAgent(device, 'alerts', new URLSearchParams({ limit: '500' }), { fetcher }), { code: 'invalid_request' });
  await assert.rejects(queryAgent(device, 'alerts', undefined, { fetcher: async () => new Response('{}', { status: 404 }) }), { code: 'alerts_unsupported' });
  await assert.rejects(queryAgent(device, 'alert-test', undefined, { fetcher: async () => new Response('{"code":"alerts_disabled"}', { status: 503 }) }), { code: 'alerts_disabled' });
  await assert.rejects(queryAgent(device, 'alerts', undefined, { fetcher: async () => new Response('{"items":[{}]}') }), { code: 'invalid_response' });
});
test('alert rows escape device text, show retry results and demo never sends a push', async () => {
  const record = { ...demoQuery('alerts').items[0], device_name: '<script>bad</script>', body: '<img src=x onerror=bad>', attempts: 2, next_attempt_at: new Date().toISOString(), delivery_status: 'retrying' };
  const html = alertRecordHTML(record, escape, value => value);
  assert.ok(!html.includes('<script>')); assert.ok(!html.includes('<img'));
  assert.ok(html.includes('已恢复')); assert.ok(html.includes('等待重试')); assert.ok(html.includes('已尝试 2 次'));
  const client = createAgentClient({ selected: 'demo' });
  assert.equal((await client.query('alerts')).items.length, 1);
  await assert.rejects(client.query('alert-test'), { code: 'demo_read_only' });
});
