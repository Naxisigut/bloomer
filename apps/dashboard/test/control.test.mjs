import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { validateControl, submitControl, queryAgent } from '../lib/proxy.mjs';
import { createDashboard } from '../server.mjs';
import { DeviceStore } from '../lib/config.mjs';

const request = { request_id: '0123456789abcdef', action: 'switch', models: ['org/model-a', 'model-b'] };
test('control validation refuses shell/options/paths and unknown or conflicting inputs', () => {
  assert.deepEqual(validateControl(request), request);
  assert.deepEqual(validateControl({ request_id: request.request_id, action: 'restart' }), { request_id: request.request_id, action: 'restart' });
  for (const value of [null, [], { ...request, force: true }, { ...request, request_id: 'short' }, { ...request, action: 'exec' }, { ...request, models: [] }, { ...request, models: ['--force'] }, { ...request, models: ['a/../private'] }, { ...request, models: ['x;shutdown'] }, { ...request, models: ['a', 'a'] }, { ...request, action: 'download' }, { ...request, action: 'stop' }]) assert.throws(() => validateControl(value), { code: 'invalid_request' });
});

test('control proxy sends POST to a fixed path, keeps tokens server-side, and handles old agents', async () => {
  const device = { url: 'https://mac.tail123.ts.net', token: 'private-device-token' };
  let sent;
  const result = await submitControl(device, request, async (url, options) => {
    sent = { url: url.href, ...options };
    return new Response(JSON.stringify({ id: 'job-1', status: 'running' }), { status: 202 });
  });
  assert.equal(result.status, 'running'); assert.equal(sent.url, device.url + '/v1/device/control/jobs');
  assert.equal(sent.method, 'POST'); assert.equal(sent.redirect, 'error'); assert.equal(sent.headers.Origin, undefined);
  assert.equal(sent.headers.Authorization, 'Bearer private-device-token'); assert.deepEqual(JSON.parse(sent.body), request);
  for (const route of ['control', 'control-models', 'control-jobs']) await assert.rejects(queryAgent(device, route, new URLSearchParams(), async () => new Response('old agent', { status: 404 })), { code: 'control_unsupported' });
  await assert.rejects(submitControl(device, request, async () => new Response(JSON.stringify({ code: 'control_busy', message: 'private output' }), { status: 409 })), error => error.code === 'control_busy' && !error.message.includes('private'));
  await assert.rejects(queryAgent(device, 'control', new URLSearchParams({ force: 'true' })), { code: 'invalid_request' });
});

async function listen(server, t) {
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  return `http://127.0.0.1:${server.address().port}`;
}
test('dashboard control end-to-end uses saved device, rejects demo and foreign origins', async t => {
  const sent = [];
  const agent = http.createServer(async (req, res) => {
    assert.equal(req.headers.authorization, 'Bearer synthetic-test-token');
    const chunks = []; for await (const chunk of req) chunks.push(chunk);
    sent.push({ method: req.method, path: req.url, body: Buffer.concat(chunks).toString() });
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(req.method === 'POST' ? { id: 'job-1', status: 'running', ...JSON.parse(sent.at(-1).body) } : { enabled: true, available: true, jobs: [] }));
  });
  const agentURL = await listen(agent, t);
  const dataDir = await mkdtemp(path.join(os.tmpdir(), 'bloomer-control-test-'));
  t.after(() => rm(dataDir, { recursive: true, force: true }));
  const store = new DeviceStore({ dataDir, agentDir: null });
  const server = await createDashboard({ store }); const url = await listen(server, t);
  const device = await store.add({ name: '合成设备', url: agentURL, token: 'synthetic-test-token' });
  const endpoint = `${url}/api/devices/${device.id}/control-jobs`;
  const options = { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: url }, body: JSON.stringify(request) };
  const response = await fetch(endpoint, options); assert.equal(response.status, 202); assert.equal((await response.json()).id, 'job-1');
  assert.deepEqual(sent.at(-1), { method: 'POST', path: '/v1/device/control/jobs', body: JSON.stringify(request) });
  const before = sent.length;
  assert.equal((await fetch(endpoint, { ...options, headers: { ...options.headers, Origin: 'https://evil.example' } })).status, 403);
  assert.equal((await fetch(endpoint + '?force=true', options)).status, 400);
  assert.equal((await fetch(endpoint, { ...options, body: JSON.stringify({ ...request, force: true }) })).status, 400);
  assert.equal((await fetch(`${url}/api/devices/demo/control-jobs`, options)).status, 403);
  assert.equal(sent.length, before);
  assert.equal((await fetch(`${url}/api/devices/${device.id}/control`)).status, 200);
  assert.equal((await fetch(`${url}/control-ui.js`)).status, 200);
  const html = await (await fetch(url)).text(); assert.ok(html.includes('control-confirm-dialog')); assert.ok(html.includes('control-local-models'));
});
