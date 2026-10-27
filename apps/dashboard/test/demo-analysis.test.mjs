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

test('demo data includes reward rows so the page exercises the reward path', () => {
  const clock = new Date('2026-10-22T08:00:00Z');
  const range = analysisPeriodRange('30', 'Asia/Shanghai', clock);
  const all = demoQuery('analysis', buildAnalysisParams({ range, granularity: 'day' }), clock);
  const reward = all.models.find(item => item.model === 'base_reward');
  assert.ok(reward, 'demo must include a base_reward row');
  assert.ok(BigInt(reward.amount_micro_usd) > 0n);

  // Selecting a real model excludes the reward row, exactly like the Agent's model filter.
  const model = all.models.find(item => item.model !== 'base_reward').model;
  const filtered = demoQuery('analysis', buildAnalysisParams({ range, granularity: 'day', models: [model] }), clock);
  assert.equal(filtered.totals.amount_micro_usd, all.models.find(item => item.model === model).amount_micro_usd);
  assert.equal(BigInt(all.totals.amount_micro_usd), BigInt(filtered.totals.amount_micro_usd) + BigInt(reward.amount_micro_usd) + all.models.filter(item => item.model !== model && item.model !== 'base_reward').reduce((sum, item) => sum + BigInt(item.amount_micro_usd), 0n));

  const stats = demoQuery('stats', new URLSearchParams({ ...range, group_by: 'model' }), clock);
  assert.ok(stats.items.some(item => item.key === 'base_reward'));
});
