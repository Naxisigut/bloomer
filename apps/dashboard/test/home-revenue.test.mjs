import test from 'node:test';
import assert from 'node:assert/strict';
import { hourlyPoints, smoothLinePath } from '../public/home-revenue.js';
import { timeRange, sumMoney } from '../public/data.js';

test('today hourly lines use China dates and exclude yesterday and future buckets', () => {
  const range = timeRange('1', 'Asia/Shanghai', new Date('2026-10-21T04:30:00Z'));
  const items = [
    { from: '2026-10-20T15:00:00Z', amount_micro_usd: '99' },
    { from: '2026-10-20T16:00:00Z', amount_micro_usd: '9007199254740993' },
    { from: '2026-10-21T04:00:00Z', amount_micro_usd: '-1' },
    { from: '2026-10-21T05:00:00Z', amount_micro_usd: '99' },
    { from: '2026-10-21T16:00:00Z', amount_micro_usd: '99' },
  ];
  const points = hourlyPoints(items, range);
  assert.equal(points.length, 13); // Midnight through the current 12:00 hour, inclusive.
  assert.equal(points[0].from, '2026-10-20T16:00:00Z');
  assert.equal(points.at(-1).from, '2026-10-21T04:00:00Z');
  assert.ok(points.slice(1, -1).every(point => point.amount_micro_usd === '0'));
  assert.equal(sumMoney(points), 9007199254740992n);
});

test('successful hourly queries fill missing and zero hours without changing totals', () => {
  const range = { from: '2026-10-20T16:00:00Z', to: '2026-10-21T16:00:00Z' };
  const items = [19, 16, 21, 17].map(hour => ({ from: `2026-10-20T${hour}:00:00Z`, amount_micro_usd: '1' }));
  items.push({ from: '2026-10-20T18:00:00Z', amount_micro_usd: '0', record_count: 2 });
  const points = hourlyPoints(items, range);
  assert.equal(points.length, 24);
  assert.equal(sumMoney(points), 4n);
  assert.equal(points[2].amount_micro_usd, '0');
  assert.equal(points[2].record_count, 2); // A real zero-net bucket keeps its metadata.
  assert.equal(points[4].amount_micro_usd, '0');
  assert.equal(points[4].record_count, 0);
  assert.ok(points.every((point, index) => point.instant === Date.parse(range.from) + index * 3_600_000));
});

test('empty successful queries still show zero points through the current hour', () => {
  const range = timeRange('1', 'Asia/Shanghai', new Date('2026-10-20T19:00:00Z'));
  const points = hourlyPoints([], range);
  assert.equal(points.length, 4);
  assert.equal(points.at(-1).instant, Date.parse('2026-10-20T19:00:00Z'));
  assert.ok(points.every(point => point.amount_micro_usd === '0'));
  assert.deepEqual(hourlyPoints([], { from: range.from, to: range.from }), []);
});

test('smooth curves pass through hourly observations without overshooting between zeroes and peaks', () => {
  const points = [0, 0, 4, 0, 0, 9, 10, 0].map((y, index) => ({ x: index * 40, y }));
  const path = smoothLinePath(points);
  assert.ok(path.startsWith('M0,0'));
  const curves = [...path.matchAll(/C([^C]+)/g)].map(match => match[1].trim().split(/[ ,]+/).map(Number));
  assert.equal(curves.length, points.length - 1);
  curves.forEach((curve, index) => {
    const [x1, y1, x2, y2, x, y] = curve, before = points[index], after = points[index + 1];
    assert.deepEqual({ x, y }, after);
    assert.ok(x1 > before.x && x2 < after.x);
    for (let step = 0; step <= 20; step++) {
      const t = step / 20;
      const value = (1 - t) ** 3 * before.y + 3 * (1 - t) ** 2 * t * y1 + 3 * (1 - t) * t ** 2 * y2 + t ** 3 * after.y;
      assert.ok(value >= Math.min(before.y, after.y) - 1e-9 && value <= Math.max(before.y, after.y) + 1e-9);
    }
  });
  assert.equal(smoothLinePath([]), '');
  assert.equal(smoothLinePath([{ x: 1, y: 0 }]), 'M1,0');
});
