import test from 'node:test';
import assert from 'node:assert/strict';
import http from 'node:http';
import { mkdtemp, readFile, stat, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createDashboard } from '../server.mjs';
import { DeviceStore, validateDevice } from '../lib/config.mjs';
import { queryAgent } from '../lib/proxy.mjs';
import { money, sumMoney, sumCount, timeRange, rollingRange, trendGroups, zonedMidnight, csvRows } from '../public/data.js';
import { demoQuery } from '../lib/demo.mjs';

async function listen(server, t) {
  await new Promise((resolve, reject) => { server.once('error', reject); server.listen(0, '127.0.0.1', resolve); });
  t.after(() => new Promise(resolve => { server.close(resolve); server.closeAllConnections(); }));
  return `http://127.0.0.1:${server.address().port}`;
}
async function storeFor(t) {
  const dataDir = await mkdtemp(path.join(os.tmpdir(), 'bloomer-dashboard-test-'));
  t.after(() => rm(dataDir, { recursive: true, force: true }));
  return new DeviceStore({ dataDir, agentDir: null });
}

test('money preserves micro-USD precision including int64 values and rounding', () => {
  assert.equal(money('9223372036854775807', 6), '$9,223,372,036,854.775807');
  assert.equal(money('-10001', 6), '−$0.010001');
  assert.equal(money('1999999', 2), '$2.00');
  assert.equal(money('1', 6), '$0.000001');
  assert.equal(sumMoney([{ amount_micro_usd: '9007199254740993' }, { amount_micro_usd: '1' }]), 9007199254740994n);
  assert.equal(money(null), '—');
});

test('calendar ranges honor timezone and daylight saving changes', () => {
  assert.equal(zonedMidnight('2026-10-18', 'Asia/Shanghai').toISOString(), '2026-10-17T16:00:00.000Z');
  const range = timeRange('custom', 'America/New_York', new Date('2026-03-10T12:00:00Z'), { from: '2026-03-08', to: '2026-03-08' });
  assert.equal(range.from, '2026-03-08T05:00:00.000Z');
  assert.equal(range.to, '2026-03-09T04:00:00.000Z');
  assert.equal((Date.parse(range.to) - Date.parse(range.from)) / 3600000, 23);
  assert.throws(() => timeRange('custom', 'UTC', new Date('2026-01-01T12:00Z'), { from: '2026-01-02', to: '2026-01-04' }));
});

test('CSV escapes formulas and commas while retaining exact micro amounts', () => {
  const csv = csvRows([{ record_id: '=HYPERLINK("bad")', created_at: '2026-01-01T00:00:00Z', model: 'Qwen,"test"', amount_micro_usd: '9007199254740993', prompt_tokens: 1, completion_tokens: 2, provider_id: '@bad', job_id: 'safe' }]);
  assert.ok(csv.startsWith('\uFEFF'));
  assert.ok(csv.includes("'=HYPERLINK"));
  assert.ok(csv.includes('Qwen,""test""'));
  assert.ok(csv.includes('9007199254.740993'));
  assert.ok(csv.includes("'@bad"));
});

test('rolling earnings windows keep exact elapsed hours across midnight and DST', () => {
  const now = new Date('2026-03-09T04:30:45.123Z'); // 00:30 in New York after the spring transition.
  const hour = rollingRange(1, now);
  const day = rollingRange(24, now);
  assert.equal(hour.from, '2026-03-09T03:30:45.123Z');
  assert.equal(day.from, '2026-03-08T04:30:45.123Z');
  assert.equal(day.to, hour.to);
  assert.equal(Date.parse(day.to) - Date.parse(day.from), 24 * 3_600_000);
  const calendar = timeRange('1', 'America/New_York', now);
  assert.notEqual(day.from, calendar.from);
  assert.equal(now.toISOString(), '2026-03-09T04:30:45.123Z');
});

test('trend grouping follows selected calendar span, including custom dates and DST', () => {
  const now = new Date('2026-11-04T12:00:00Z');
  for (const [days, groups] of [['1', ['hour']], ['7', ['day']], ['30', ['day']], ['31', ['day', 'month']], ['365', ['day', 'month']]]) {
    assert.deepEqual(trendGroups(timeRange(days, 'Asia/Shanghai', now), 'Asia/Shanghai'), groups);
  }
  const custom = (from, to) => timeRange('custom', 'America/New_York', now, { from, to });
  // Both short and long DST days stay hourly; crossing midnight uses daily buckets.
  for (const date of ['2026-03-08', '2026-11-01']) assert.deepEqual(trendGroups(custom(date, date), 'America/New_York'), ['hour']);
  assert.deepEqual(trendGroups(custom('2026-11-01', '2026-11-02'), 'America/New_York'), ['day']);
  assert.deepEqual(trendGroups(custom('2026-10-02', '2026-10-31'), 'America/New_York'), ['day']);
  assert.deepEqual(trendGroups(custom('2026-10-01', '2026-10-31'), 'America/New_York'), ['day', 'month']);
});

test('monthly demo stats use local calendar boundaries and preserve filtered totals', () => {
  const clock = new Date();
  // The last 70 days include partial months without depending on a fixed test date.
  const range = { ...rollingRange(70 * 24, clock), timezone: 'America/New_York' };
  const all = demoQuery('stats', new URLSearchParams({ ...range, group_by: 'day' }), clock);
  assert.ok(all.items.length > 31);
  const model = demoQuery('stats', new URLSearchParams({ ...range, group_by: 'model' }), clock).items[0].key;
  const days = demoQuery('stats', new URLSearchParams({ ...range, model, group_by: 'day' }), clock);
  const months = demoQuery('stats', new URLSearchParams({ ...range, model, group_by: 'month' }), clock);
  assert.ok(months.items.length >= 3);
  assert.ok(months.items.length < days.items.length);
  assert.equal(sumMoney(months.items), sumMoney(days.items));
  assert.equal(sumCount(months.items), sumCount(days.items));
  for (const month of months.items) {
    assert.match(month.key, /^\d{4}-\d{2}$/);
    const next = new Date(`${month.key}-01T12:00:00Z`);
    next.setUTCMonth(next.getUTCMonth() + 1);
    assert.equal(month.from, zonedMidnight(month.key + '-01', range.timezone).toISOString());
    assert.equal(month.to, zonedMidnight(next.toISOString().slice(0, 10), range.timezone).toISOString());
    const selected = days.items.filter(day => day.key.startsWith(month.key));
    assert.equal(BigInt(month.amount_micro_usd), sumMoney(selected));
    assert.equal(month.record_count, sumCount(selected));
  }
});

test('connection validation only accepts loopback or private Tailscale HTTPS origins', () => {
  assert.equal(validateDevice({ name: '设备', url: 'https://mac.tail123.ts.net:8443', token: 'secret' }).url, 'https://mac.tail123.ts.net:8443');
  for (const url of ['http://mac.tail123.ts.net', 'https://example.com', 'http://127.0.0.1.evil.com', 'https://user:pass@mac.tail123.ts.net', 'http://127.0.0.1/v1', 'http://127.0.0.1?token=secret', 'file:///etc/passwd']) {
    assert.throws(() => validateDevice({ name: '设备', url, token: 'secret' }), { code: 'invalid_url' });
  }
});

test('concurrent device writes persist atomically with private file permissions', async t => {
  const store = await storeFor(t); await store.load();
  await Promise.all(Array.from({ length: 4 }, (_, index) => store.add({ name: `Mac ${index}`, url: `http://127.0.0.1:${18000 + index}`, token: `secret-${index}` })));
  const saved = JSON.parse(await readFile(store.file, 'utf8'));
  assert.equal(saved.devices.length, 4);
  assert.equal((await stat(store.file)).mode & 0o777, 0o600);
  assert.equal((await stat(path.dirname(store.file))).mode & 0o777, 0o700);
  const reload = new DeviceStore({ dataDir: path.dirname(store.file), agentDir: null }); await reload.load();
  assert.equal(reload.list().length, 4);
  await store.remove(store.list()[0].id);
  assert.equal(store.list().length, 3);
});

test('proxy rejects redirects, excludes browser Origin and sanitizes upstream failures', async t => {
  let token, origin, query;
  const upstream = http.createServer((req, res) => {
    token = req.headers.authorization; origin = req.headers.origin; query = req.url;
    if (req.url === '/v1/health') { res.writeHead(401, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ code: 'unauthorized', message: 'raw secret should not escape' })); }
    else if (req.url.startsWith('/v1/earnings?')) { res.writeHead(200, { 'Content-Type': 'application/json' }); res.end(JSON.stringify({ items: [], next_cursor: null })); }
    else { res.writeHead(302, { Location: '/v1/earnings' }); res.end(); }
  });
  const url = await listen(upstream, t);
  const device = { url, token: 'private-token' };
  await assert.rejects(queryAgent(device, 'health'), error => error.code === 'unauthorized' && !error.message.includes('raw secret'));
  assert.equal(token, 'Bearer private-token'); assert.equal(origin, undefined);
  await queryAgent(device, 'earnings', new URLSearchParams({ model: 'a+b / c', limit: 10 }));
  assert.ok(query.includes('model=a%2Bb+%2F+c'));
  await assert.rejects(queryAgent(device, 'overview'), { code: 'device_unreachable' });
  await assert.rejects(queryAgent(device, 'earnings', new URLSearchParams({ url: 'https://evil.com' })), { code: 'invalid_request' });
});

test('dashboard serves real proxy data, redacts saved tokens, and blocks foreign origins/hosts', async t => {
  const upstream = http.createServer((req, res) => {
    assert.equal(req.headers.authorization, 'Bearer local-test-token');
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify(req.url === '/v1/health' ? { version: 'test', data_stale: true, collection_state: 'idle' } : { account_id: 'test-account', official: { total_micro_usd: '1' } }));
  });
  const upstreamURL = await listen(upstream, t);
  const store = await storeFor(t);
  const server = await createDashboard({ store }); const url = await listen(server, t);
  const page = await fetch(url); assert.equal(page.status, 200); assert.match(await page.text(), /收益分析/);
  const paginationModule = await fetch(`${url}/pagination.js`);
  assert.equal(paginationModule.status, 200); assert.match(await paginationModule.text(), /class CursorPager/);
  const create = await fetch(`${url}/api/devices`, { method: 'POST', headers: { 'Content-Type': 'application/json', Origin: url }, body: JSON.stringify({ name: '测试设备', url: upstreamURL, token: 'local-test-token' }) });
  assert.equal(create.status, 201); const device = await create.json(); assert.equal(device.token, undefined);
  const listed = await (await fetch(`${url}/api/devices`)).text(); assert.ok(!listed.includes('local-test-token'));
  const overview = await (await fetch(`${url}/api/devices/${device.id}/overview`)).json(); assert.equal(overview.account_id, 'test-account');
  const foreign = await fetch(`${url}/api/devices`, { headers: { Origin: 'https://evil.example' } }); assert.equal(foreign.status, 403);
  const crossSite = await fetch(`${url}/api/devices`, { headers: { 'Sec-Fetch-Site': 'cross-site' } }); assert.equal(crossSite.status, 403);
  const invalidHost = await new Promise((resolve, reject) => {
    const request = http.get(`${url}/api/devices`, { headers: { Host: `evil.example:${server.address().port}` } }, response => { response.resume(); resolve(response.statusCode); });
    request.once('error', reject);
  });
  assert.equal(invalidHost, 403);
  const duplicate = await fetch(`${url}/api/devices`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name: '重复', url: upstreamURL, token: 'local-test-token' }) }); assert.equal(duplicate.status, 409);
  assert.equal((await fetch(`${url}/api/devices/${device.id}`, { method: 'DELETE' })).status, 200);
});

test('demo endpoints use the actual contract, filter models, and paginate without overlap', async t => {
  const server = await createDashboard({ store: await storeFor(t) }); const url = await listen(server, t);
  const range = { from: new Date(Date.now() - 30 * 86400000).toISOString(), to: new Date().toISOString(), limit: 10 };
  const endpoint = `${url}/api/devices/demo/earnings?`;
  const first = await (await fetch(endpoint + new URLSearchParams(range))).json(); assert.equal(first.items.length, 10); assert.ok(first.next_cursor);
  const second = await (await fetch(endpoint + new URLSearchParams({ ...range, cursor: first.next_cursor }))).json();
  const ids = new Set(first.items.map(item => item.record_id)); assert.ok(second.items.every(item => !ids.has(item.record_id)));
  const filtered = await (await fetch(endpoint + new URLSearchParams({ ...range, model: first.items[0].model }))).json(); assert.ok(filtered.items.every(item => item.model === first.items[0].model));
  const stats = await (await fetch(`${url}/api/devices/demo/stats?` + new URLSearchParams({ ...range, group_by: 'model', timezone: 'Asia/Shanghai' }))).json();
  assert.equal(stats.group_by, 'model'); assert.ok(stats.items.every(item => /^\d+$/.test(item.amount_micro_usd))); assert.equal(stats.meta.coverage.status, 'partial');
});
