import test from 'node:test';
import assert from 'node:assert/strict';
import { DeviceStore, validateDevice, CONNECTIONS_KEY } from '../public/connections.js';
import { createAgentClient } from '../public/agent-client.js';

function memoryStorage() {
  const values = new Map();
  return { getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) };
}
const input = { name: '家中 Mac', url: 'https://mac.tail123.ts.net:8443', token: 'synthetic-access-token' };
const healthy = () => Response.json({ version: 'test', collection_state: 'idle' });

test('addresses accept loopback or Tailscale HTTPS and reject paths/credentials', () => {
  assert.equal(validateDevice(input).url, input.url);
  assert.equal(validateDevice({ ...input, url: 'http://127.0.0.1:17890' }).url, 'http://127.0.0.1:17890');
  for (const url of ['http://mac.tail123.ts.net', 'https://example.com', 'http://127.0.0.1.evil.com', 'https://user:pass@mac.tail123.ts.net', 'http://127.0.0.1/v1', 'http://127.0.0.1?token=secret', 'file:///etc/passwd']) assert.throws(() => validateDevice({ ...input, url }), { code: 'invalid_url' });
  for (const token of ['', 'bad token', 'bad\ntoken']) assert.throws(() => validateDevice({ ...input, token }), { code: 'invalid_token' });
});

test('health is tested before save; reload restores connections, tokens and selected device', async () => {
  const storage = memoryStorage(), store = new DeviceStore({ storage }); store.load();
  let requests = 0;
  const client = createAgentClient(store, { fetcher: async (url, options) => {
    requests++; assert.equal(url.href, input.url + '/v1/health'); assert.equal(options.headers.Authorization, 'Bearer ' + input.token); return healthy();
  } });
  await client.connect(input); assert.equal(requests, 1); assert.equal(storage.getItem(CONNECTIONS_KEY), null);
  const { device } = await client.connect(input, true); assert.equal(requests, 2); assert.equal(device.token, undefined);
  const reloaded = new DeviceStore({ storage }); reloaded.load();
  assert.equal(reloaded.selected, device.id); assert.equal(reloaded.get(device.id).token, input.token);
  reloaded.select('demo'); const again = new DeviceStore({ storage }); again.load(); assert.equal(again.selected, 'demo');
  const other = new DeviceStore({ storage: memoryStorage() }); other.load(); assert.deepEqual(other.list(), []);
  reloaded.remove(device.id); assert.deepEqual(reloaded.list(), []); assert.ok(!storage.getItem(CONNECTIONS_KEY).includes(input.token));
  assert.equal(requests, 2, 'selection/removal must not contact Agent');
});

test('failed connection and malformed health never persist a token', async () => {
  for (const fetcher of [async () => Response.json({ code: 'unauthorized' }, { status: 401 }), async () => Response.json({ wrong: 'service' }), async () => { throw new TypeError('private-token'); }]) {
    const storage = memoryStorage(), store = new DeviceStore({ storage }); store.load();
    await assert.rejects(createAgentClient(store, { fetcher }).connect(input, true)); assert.equal(storage.getItem(CONNECTIONS_KEY), null);
  }
});

test('addMany rejects duplicate device URLs', () => {
  const storage = memoryStorage(), store = new DeviceStore({ storage }); store.load();
  store.add(input);
  assert.throws(() => store.add({ ...input, name: '第二台' }), { code: 'device_exists' });
  assert.equal(store.list().length, 1);
});

test('storage errors do not claim a save or overwrite corrupted records', () => {
  const storage = memoryStorage(); storage.setItem(CONNECTIONS_KEY, '{malformed');
  const corrupt = new DeviceStore({ storage }); assert.throws(() => corrupt.load(), { code: 'invalid_connections' });
  assert.throws(() => corrupt.add(input), { code: 'storage_unavailable' }); assert.equal(storage.getItem(CONNECTIONS_KEY), '{malformed');
  const store = new DeviceStore({ storage: { getItem: () => null, setItem: () => { throw new Error('quota'); } } }); store.load();
  assert.throws(() => store.add(input), { code: 'storage_unavailable' }); assert.equal(store.list().length, 0);
});
