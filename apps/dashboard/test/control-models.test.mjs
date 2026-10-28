import test from 'node:test';
import assert from 'node:assert/strict';
import { AppError } from '../public/errors.js';
import { createControlModelsStore } from '../public/control-models.js';

const body = { local: [{ id: 'a' }], catalog: [{ id: 'b' }] };

test('model discovery is cached per device and reused without another request', async () => {
  const calls = [];
  const store = createControlModelsStore({ query: async (route, params, id) => { calls.push([route, id]); return body; } });
  assert.equal(store.get('one'), null);
  const first = await store.load('one');
  assert.deepEqual(first.local, body.local);
  assert.equal(first.error, '');
  assert.equal(store.get('one'), first);
  assert.deepEqual(calls, [['control-models', 'one']]);
});

test('concurrent loads share one request and a later load refetches', async () => {
  let resolve;
  const calls = [];
  const store = createControlModelsStore({ query: (route, params, id) => { calls.push(id); return new Promise(done => { resolve = done; }); } });
  const a = store.load('one'), b = store.load('one');
  assert.equal(a, b);
  resolve(body);
  await a;
  assert.deepEqual(calls, ['one']);
  store.load('one');
  assert.deepEqual(calls, ['one', 'one']);
});

test('a cached snapshot is reused, failures included, until a load is requested', async () => {
  let fail = true;
  const calls = [];
  const store = createControlModelsStore({ query: async (route, params, id) => { calls.push(id); if (fail) throw new AppError(504, 'request_timeout', '超时'); return body; } });
  const failed = await store.load('one');
  assert.equal(failed.error, '超时');
  assert.deepEqual(failed.local, []);
  // The stored snapshot is what an automatic entry reads; it triggers no request.
  assert.equal(store.get('one').error, '超时');
  assert.equal(calls.length, 1);
  fail = false;
  const fresh = await store.load('one');
  assert.equal(fresh.error, '');
  assert.deepEqual(fresh.local, body.local);
  assert.equal(store.get('one'), fresh);
  store.invalidate('one');
  assert.equal(store.get('one'), null);
});

test('demo devices and unknown ids never request a device', async () => {
  let calls = 0;
  const store = createControlModelsStore({ query: async () => { calls++; return body; } });
  assert.equal(await store.load('demo'), null);
  assert.equal(await store.load(undefined), null);
  assert.equal(calls, 0);
});
