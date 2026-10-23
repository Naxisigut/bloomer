import test from 'node:test';
import assert from 'node:assert/strict';
import { buildAnalysisParams, deriveAnalysisView, analysisPeriodRange } from '../public/analysis-data.js';
import { trendOption, cumulativeOption, weekdayHourHeatmap, modelHourHeatmap, modelCompareOption, modelShareOption } from '../public/analysis-charts.js';
import { demoQuery } from '../public/demo.js';

test('analysis chart options include expected series and axes', () => {
  const clock = new Date('2026-10-22T08:00:00Z');
  const range = analysisPeriodRange('30', 'Asia/Shanghai', clock);
  const body = demoQuery('analysis', buildAnalysisParams({ range, granularity: 'day' }), clock);
  const derived = deriveAnalysisView(body, { now: clock });
  const trend = trendOption({ filledBuckets: derived.filledBuckets, modelOrder: derived.modelOrder, mode: 'bar' });
  assert.equal(trend.series[0].type, 'bar');
  assert.ok(trend.dataZoom.length >= 1);
  const cumulative = cumulativeOption({ cumulative: derived.cumulative, modelOrder: derived.modelOrder });
  assert.ok(cumulative.series.some(item => item.name === '合计'));
  const heatmap = weekdayHourHeatmap(body.weekday_hour);
  assert.equal(heatmap.series[0].type, 'heatmap');
});

test('model charts show short labels and keep colliding ids', () => {
  const models = ['EigenLabs/Qwen3.8-27B-4bit-mtpt', 'Qwen3.5-9B', 'OrgA/same', 'OrgB/same'];
  const rows = models.map((model, index) => ({ model, amount_micro_usd: String((index + 1) * 1_000_000), record_count: index + 1, active_buckets: 1 }));
  const hourOfDay = models.map(model => ({ hour: 0, model, amount_micro_usd: '1000000', record_count: 1 }));
  const heatmap = modelHourHeatmap(hourOfDay, models);
  assert.deepEqual(heatmap.yAxis.data, ['Qwen3.8-27B-4bit-mtpt', 'Qwen3.5-9B', 'OrgA/same', 'OrgB/same']);
  assert.ok(heatmap.grid.left >= 120);
  assert.equal(heatmap.yAxis.axisLabel.overflow, 'truncate');
  const compare = modelCompareOption(rows, 1);
  assert.deepEqual(compare.xAxis.data, ['Qwen3.8-27B-4bit-mtpt', 'Qwen3.5-9B', 'OrgA/same', 'OrgB/same']);
  const share = modelShareOption(rows);
  assert.deepEqual(share.series[0].data.map(item => item.name), ['Qwen3.8-27B-4bit-mtpt', 'Qwen3.5-9B', 'OrgA/same', 'OrgB/same']);
});
