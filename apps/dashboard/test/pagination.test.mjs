import test from 'node:test';
import assert from 'node:assert/strict';
import { CursorPager } from '../public/pagination.js';

function fixture(count, beforeRead = () => {}) {
  const calls = [];
  const items = Array.from({ length: count }, (_, index) => ({ record_id: String(index) }));
  const pager = new CursorPager(async request => {
    const start = request.cursor ? Number(request.cursor.slice(7)) : 0;
    calls.push({ start, limit: request.limit });
    await beforeRead(request, start);
    const page = items.slice(start, start + request.limit);
    const next = start + page.length;
    return { items: page, next_cursor: next < items.length ? `cursor:${next}` : null, meta: {} };
  });
  return { pager, calls };
}

test('page jumps batch cursor traversal, cache boundaries, and honor edited page sizes', async () => {
  const { pager, calls } = fixture(1200);
  await pager.page(0, 10);
  const distant = await pager.page(99, 10);
  assert.deepEqual(distant.items.map(item => item.record_id), Array.from({ length: 10 }, (_, index) => String(990 + index)));
  assert.equal(calls.length, 4); // First page, two batches to locate the target, then its rows.
  assert.ok(calls.every(call => call.limit <= 500));
  const previousCalls = calls.length;
  assert.equal((await pager.page(1, 10)).items[0].record_id, '10');
  assert.equal(calls.length, previousCalls + 1);
  const resized = await pager.page(8, 25);
  assert.equal(resized.index, 8);
  assert.equal(resized.items[0].record_id, '200');
  assert.equal(resized.items.at(-1).record_id, '224');
  await assert.rejects(pager.page(0, 501));
  await assert.rejects(pager.page(-1, 10));
});

test('out-of-range jumps resolve to the partial last page and handle empty results', async () => {
  const { pager } = fixture(23);
  const result = await pager.page(50, 10);
  assert.equal(result.index, 2);
  assert.deepEqual(result.items.map(item => item.record_id), ['20', '21', '22']);
  assert.equal(result.next_cursor, null);
  const empty = await fixture(0).pager.page(100, 25);
  assert.equal(empty.index, 0);
  assert.deepEqual(empty.items, []);
});

test('cancelled and failed page jumps remain retryable', async () => {
  const controller = new AbortController();
  let began;
  const started = new Promise(resolve => { began = resolve; });
  let block = true;
  const { pager } = fixture(1200, async ({ signal }) => {
    if (!block) return;
    began();
    await new Promise((resolve, reject) => signal.addEventListener('abort', () => { block = false; reject(signal.reason); }, { once: true }));
  });
  const cancelled = pager.page(99, 10, { signal: controller.signal });
  await started;
  controller.abort();
  await assert.rejects(cancelled, { name: 'AbortError' });
  assert.equal((await pager.page(0, 25)).items.length, 25);

  let fail = true;
  const retry = fixture(1200, (request, start) => {
    if (start >= 500 && fail) { fail = false; throw new Error('network failure'); }
  });
  await assert.rejects(retry.pager.page(90, 10), /network failure/);
  assert.equal((await retry.pager.page(90, 10)).items[0].record_id, '900');
});

test('non-advancing cursors fail instead of making unbounded requests', async () => {
  let calls = 0;
  const pager = new CursorPager(async () => {
    calls++;
    return { items: [{ record_id: 'same-record' }], next_cursor: 'same-cursor' };
  });
  await assert.rejects(pager.page(99, 10), /分页数据无效/);
  assert.equal(calls, 2);
});
