import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { queryAgent, submitControl, createAgentClient } from '../public/agent-client.js';
const device = { url: 'https://mac.tail123.ts.net:8443', token: 'synthetic-test-token' };
const operation = { request_id: '0123456789abcdef', action: 'switch', models: ['org/model-a', 'model-b'] };

test('browser sends direct Bearer GET/POST to fixed routes with allowed parameters', async () => {
  let sent;
  const fetcher = async (url, options) => { sent = { url: url.href, ...options }; return Response.json(options.method === 'POST' ? { id: 'job-1', status: 'running' } : { items: [], next_cursor: null }); };
  await queryAgent(device, 'earnings', new URLSearchParams({ model: 'a+b / c', limit: 10 }), { fetcher });
  assert.equal(sent.url, device.url + '/v1/earnings?model=a%2Bb+%2F+c&limit=10'); assert.equal(sent.method, 'GET');
  assert.equal(sent.mode, 'cors'); assert.equal(sent.credentials, 'omit'); assert.equal(sent.redirect, 'error');
  assert.equal(sent.headers.Authorization, 'Bearer ' + device.token); assert.equal(sent.headers.Origin, undefined);
  await submitControl(device, operation, { fetcher });
  assert.equal(sent.url, device.url + '/v1/device/control/jobs'); assert.equal(sent.method, 'POST');
  assert.equal(sent.headers['Content-Type'], 'application/json'); assert.deepEqual(JSON.parse(sent.body), operation);
  for (const params of [new URLSearchParams({ url: 'https://evil.com' }), new URLSearchParams('limit=1&limit=2')]) await assert.rejects(queryAgent(device, 'earnings', params, { fetcher }), { code: 'invalid_request' });
});

test('Chinese errors cover 401, old agents, network and malformed or oversized responses', async () => {
  const failure = async (response, route = 'health') => queryAgent(device, route, undefined, { fetcher: async () => response });
  await assert.rejects(failure(Response.json({ message: device.token }, { status: 401 })), error => error.code === 'unauthorized' && !error.message.includes(device.token));
  await assert.rejects(failure(Response.json({ code: 'control_busy', message: device.token }, { status: 409 }), 'control'), { code: 'control_busy' });
  for (const route of ['control', 'control-models', 'control-jobs']) await assert.rejects(failure(new Response('old', { status: 404 }), route), { code: 'control_unsupported' });
  await assert.rejects(failure(new Response('old', { status: 501 }), 'device-state'), { code: 'device_state_unsupported' });
  for (const response of [new Response('not JSON'), Response.json(null), Response.json([]), Response.json({}), new Response('{}', { headers: { 'content-length': '8000001' } }), new Response(' '.repeat(8_000_001))]) await assert.rejects(failure(response), { code: 'invalid_response' });
  for (const [route, body] of [['earnings', { items: [{}] }], ['stats', { items: [{ key: 'm', amount_micro_usd: '1', record_count: -1 }] }], ['stats', { items: [{ key: 'm', amount_micro_usd: 9007199254740992, record_count: 1 }] }], ['control-models', { local: null, catalog: [] }]]) await assert.rejects(failure(Response.json(body), route), { code: 'invalid_response' });
  await assert.rejects(queryAgent(device, 'health', undefined, { fetcher: async () => { throw new TypeError(device.token); } }), error => error.code === 'device_unreachable' && /无法区分网络、证书或 CORS/.test(error.message) && !error.message.includes(device.token));
});

test('caller cancellation is preserved and timeout covers request and body reads', async () => {
  const fetcher = async (_url, { signal }) => new Promise((_resolve, reject) => signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true }));
  const controller = new AbortController();
  const promise = queryAgent(device, 'health', undefined, { fetcher, signal: controller.signal }); controller.abort();
  await assert.rejects(promise, { name: 'AbortError' });
  await assert.rejects(queryAgent(device, 'health', undefined, { fetcher, timeoutMs: 5 }), { code: 'request_timeout' });
  const slowBody = async (_url, { signal }) => new Response(new ReadableStream({ start(controller) { signal.addEventListener('abort', () => controller.error(new DOMException('aborted', 'AbortError'))); } }));
  await assert.rejects(queryAgent(device, 'health', undefined, { fetcher: slowBody, timeoutMs: 5 }), { code: 'request_timeout' });
});

test('loopback mock accepts once; lost POST response is reconciled with original request ID', async t => {
  const jobs = []; let disconnect = true;
  const mock = http.createServer(async (req, res) => {
    assert.equal(req.headers.authorization, 'Bearer ' + device.token); res.setHeader('Content-Type', 'application/json');
    if (req.method === 'POST') {
      const chunks = []; for await (const chunk of req) chunks.push(chunk);
      const body = JSON.parse(Buffer.concat(chunks)); let job = jobs.find(job => job.request_id === body.request_id);
      if (!job) { job = { ...body, id: 'job-1', status: 'succeeded' }; jobs.push(job); }
      if (disconnect) { disconnect = false; res.destroy(); return; }
      res.writeHead(202); res.end(JSON.stringify(job));
    } else res.end(JSON.stringify({ enabled: true, available: true, jobs }));
  });
  await new Promise(resolve => mock.listen(0, '127.0.0.1', resolve));
  t.after(() => new Promise(resolve => { mock.close(resolve); mock.closeAllConnections(); }));
  const local = { ...device, url: `http://127.0.0.1:${mock.address().port}` };
  await assert.rejects(submitControl(local, operation), { code: 'device_unreachable' });
  assert.equal((await queryAgent(local, 'control')).jobs[0].request_id, operation.request_id);
  assert.equal((await submitControl(local, operation)).id, 'job-1'); assert.equal(jobs.length, 1);
  const client = createAgentClient({ selected: 'demo', get() { throw new Error('must not access store'); } });
  assert.equal((await client.query('health')).version, '1.0.10');
  await assert.rejects(client.submit('demo', operation), { code: 'demo_read_only' });
});
