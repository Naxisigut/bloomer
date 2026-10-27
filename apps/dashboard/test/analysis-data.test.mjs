import test from 'node:test';
import assert from 'node:assert/strict';
import {
  analysisPeriodRange, allowedGranularities, defaultGranularity, buildAnalysisParams, fillSeriesBuckets, deriveAnalysisView, deriveMetrics, modelShareRows, hourOfDayDailyAverage, validateAnalysisShape,
} from '../public/analysis-data.js';
import { demoQuery } from '../public/demo.js';

test('analysis period and granularity rules follow design defaults', () => {
  const now = new Date('2026-10-22T04:00:00Z');
  const week = analysisPeriodRange('7', 'Asia/Shanghai', now);
  assert.equal(defaultGranularity(week), 'day');
  assert.deepEqual(allowedGranularities(week), ['hour', 'day', 'month']);
  const twoDay = analysisPeriodRange('today', 'Asia/Shanghai', now);
  assert.equal(defaultGranularity(twoDay), 'hour');
  const year = analysisPeriodRange('365', 'Asia/Shanghai', now);
  assert.equal(defaultGranularity(year), 'month');
  const params = buildAnalysisParams({ range: week, granularity: 'day', models: ['a', 'b'] });
  assert.equal(params.get('granularity'), 'day');
  assert.deepEqual(params.getAll('model'), ['a', 'b']);
});

test('fillSeriesBuckets pads past empty buckets and derive metrics', () => {
  const clock = new Date('2026-10-22T12:00:00Z');
  const range = analysisPeriodRange('7', 'Asia/Shanghai', clock);
  const body = demoQuery('analysis', buildAnalysisParams({ range, granularity: 'day' }), clock);
  assert.ok(validateAnalysisShape(body));
  const filled = fillSeriesBuckets(body, clock);
  assert.ok(filled.length >= 1);
  const derived = deriveAnalysisView(body, { now: clock });
  assert.ok(derived.metrics.recordCount >= 0);
  assert.ok(derived.filledBuckets.length >= filled.length - 1);
});

test('reward rows stay in totals but leave task counts and averages', () => {
  const response = {
    timezone: 'Asia/Shanghai', granularity: 'day',
    from: '2026-10-20T16:00:00.000Z', to: '2026-10-22T16:00:00.000Z',
    totals: { amount_micro_usd: '1100', record_count: 4 },
    models: [
      { model: 'Qwen3-32B', amount_micro_usd: '1000', record_count: 3, active_buckets: 2 },
      { model: 'base_reward', amount_micro_usd: '100', record_count: 1, active_buckets: 1 },
    ],
    series: [
      { from: '2026-10-20T16:00:00.000Z', to: '2026-10-21T16:00:00.000Z', items: [
        { model: 'Qwen3-32B', amount_micro_usd: '600', record_count: 2 },
        { model: 'base_reward', amount_micro_usd: '100', record_count: 1 },
      ] },
      { from: '2026-10-21T16:00:00.000Z', to: '2026-10-22T16:00:00.000Z', items: [
        { model: 'Qwen3-32B', amount_micro_usd: '400', record_count: 1 },
      ] },
    ],
    hour_of_day: [
      { hour: 4, model: 'base_reward', amount_micro_usd: '100', record_count: 1 },
      { hour: 10, model: 'Qwen3-32B', amount_micro_usd: '1000', record_count: 3 },
    ],
    weekday_hour: [{ weekday: 3, hour: 10, amount_micro_usd: '1100', record_count: 4 }],
  };
  const clock = new Date('2026-10-22T16:00:00.000Z');
  const filled = fillSeriesBuckets(response, clock);
  assert.ok(filled.every(bucket => bucket.total_micro_usd === '0' || BigInt(bucket.task_micro_usd) <= BigInt(bucket.total_micro_usd)));
  assert.deepEqual(filled.map(bucket => bucket.task_record_count), [2, 1]);

  const metrics = deriveMetrics(response, filled, clock);
  assert.equal(metrics.totalAmount, '1100');
  assert.equal(metrics.rewardAmount, '100');
  assert.equal(metrics.taskAmount, '1000');
  assert.equal(metrics.recordCount, 3);
  assert.equal(metrics.rewardRecordCount, 1);
  assert.equal(metrics.avgPerRecord, '333');
  // The reward hour is excluded from the peak so a lump sum cannot fake a busy hour.
  assert.equal(metrics.peakHour.hour, 10);

  const shares = modelShareRows(response.models);
  const reward = shares.find(row => row.model === 'base_reward');
  assert.equal(reward.isReward, true);
  assert.equal(reward.avgPerRecord, null);
  assert.equal(reward.share, 9.09);
  assert.equal(shares.find(row => row.model === 'Qwen3-32B').avgPerRecord, '333');

  const hourly = hourOfDayDailyAverage(response.hour_of_day, metrics.spanDays, null);
  assert.deepEqual(hourly.map(row => row.model), ['Qwen3-32B']);

  const view = deriveAnalysisView(response, { now: clock });
  assert.deepEqual(view.modelOrder, ['Qwen3-32B', 'base_reward']);
});
