import test from 'node:test';
import assert from 'node:assert/strict';
import { demoQuery } from '../public/demo.js';
import { buildAnalysisParams, analysisPeriodRange, validateAnalysisShape } from '../public/analysis-data.js';

test('demo analysis response matches contract and models ignore filter', () => {
  const clock = new Date('2026-10-22T08:00:00Z');
  const range = analysisPeriodRange('30', 'Asia/Shanghai', clock);
  const all = demoQuery('analysis', buildAnalysisParams({ range, granularity: 'day' }), clock);
  assert.ok(validateAnalysisShape(all));
  assert.ok(all.models.length >= 2);
  const filtered = demoQuery('analysis', buildAnalysisParams({ range, granularity: 'day', models: [all.models[0].model] }), clock);
  assert.ok(filtered.totals.record_count <= all.totals.record_count);
  assert.deepEqual(filtered.models.map(item => item.model), all.models.map(item => item.model));
  assert.ok(filtered.hour_of_day.every(item => item.model === all.models[0].model));
});
