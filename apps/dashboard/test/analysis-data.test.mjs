import test from 'node:test';
import assert from 'node:assert/strict';
import {
  analysisPeriodRange, allowedGranularities, defaultGranularity, buildAnalysisParams, fillSeriesBuckets, deriveAnalysisView, validateAnalysisShape,
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
