import test from 'node:test';
import assert from 'node:assert/strict';
import { DASHBOARD_TIMEZONE, dateKey, money, sumMoney, sumCount, timeRange, rollingRange, trendGroups, zonedMidnight, csvRows } from '../public/data.js';
import { demoQuery } from '../public/demo.js';

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

test('single-day CSV uses the selected China calendar date across UTC midnight', () => {
  const custom = { from: '2026-10-21', to: '2026-10-21' };
  const range = timeRange('custom', DASHBOARD_TIMEZONE, new Date('2026-10-22T04:00:00Z'), custom);
  assert.equal(range.from, '2026-10-20T16:00:00.000Z');
  assert.equal(range.to, '2026-10-21T16:00:00.000Z');
  assert.equal(dateKey(range.from, DASHBOARD_TIMEZONE), custom.from);
  const times = ['2026-10-20T16:00:00Z', '2026-10-20T16:30:00.123456Z', '2026-10-21T15:59:59.999999Z'];
  const items = times.map((created_at, index) => ({ record_id: String(index), created_at, model: 'test', amount_micro_usd: '1', prompt_tokens: 1, completion_tokens: 2, provider_id: 'provider', job_id: 'job' }));
  const lines = csvRows(items).split('\r\n');
  assert.ok(lines[0].includes('时间 (北京时间 UTC+8)'));
  for (const [index, time] of ['2026-10-21T00:00:00+08:00', '2026-10-21T00:30:00.123456+08:00', '2026-10-21T23:59:59.999999+08:00'].entries()) {
    assert.ok(lines[index + 1].includes(`"${time}"`));
    assert.equal(Date.parse(time), Date.parse(times[index]));
  }
  const today = timeRange('custom', DASHBOARD_TIMEZONE, new Date('2026-10-21T04:00:00Z'), custom);
  assert.equal(today.from, range.from);
  assert.equal(today.to, '2026-10-21T04:00:00.000Z');
  // Explicit source offsets are normalized to the same dashboard timezone.
  assert.ok(csvRows([{ ...items[1], created_at: '2026-10-20T17:30:00.123456+01:00' }]).includes('2026-10-21T00:30:00.123456+08:00'));
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

test('browser demo filters, paginates and refuses all control routes', () => {
  const range = new URLSearchParams({ ...rollingRange(30 * 24), limit: 10 });
  const first = demoQuery('earnings', range);
  assert.equal(first.items.length, 10); assert.ok(first.next_cursor);
  const second = demoQuery('earnings', new URLSearchParams({ ...Object.fromEntries(range), cursor: first.next_cursor }));
  assert.ok(second.items.every(item => !first.items.some(other => other.record_id === item.record_id)));
  const model = first.items[0].model;
  assert.ok(demoQuery('earnings', new URLSearchParams({ ...Object.fromEntries(range), model })).items.every(item => item.model === model));
  for (const route of ['control', 'control-models', 'control-jobs']) assert.throws(() => demoQuery(route), { code: 'demo_read_only' });
});
