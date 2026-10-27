import test from 'node:test';
import assert from 'node:assert/strict';
import { buildAnalysisParams, deriveAnalysisView, analysisPeriodRange } from '../public/analysis-data.js';
import { trendOption, cumulativeOption, weekdayHourHeatmap, modelHourHeatmap, modelCompareOption, modelShareOption, taskAvgOption } from '../public/analysis-charts.js';
import { modelShareRows } from '../public/analysis-data.js';

import { demoQuery } from '../public/demo.js';

test('analysis chart options include expected series and axes', () => {
  const clock = new Date('2026-10-22T08:00:00Z');
  const range = analysisPeriodRange('30', 'Asia/Shanghai', clock);
  const body = demoQuery('analysis', buildAnalysisParams({ range, granularity: 'day' }), clock);
  const derived = deriveAnalysisView(body, { now: clock });
  const trend = trendOption({ filledBuckets: derived.filledBuckets, modelOrder: derived.modelOrder, mode: 'bar' });
  assert.equal(trend.series[0].type, 'bar');
  assert.ok(trend.dataZoom.length >= 1);
  assert.equal(trend.xAxis.type, 'category');
  assert.ok(trend.xAxis.data.length > 0);
  assert.ok(trend.xAxis.data.every(label => typeof label === 'string' && !label.includes('T')));
  const totalSeries = trend.series.find(item => item.name === '合计');
  assert.equal(totalSeries.label.show, true);
  assert.equal(totalSeries.label.position, 'top');
  assert.match(totalSeries.label.formatter({ dataIndex: 0 }), /^\$/);
  const hover = trend.tooltip.formatter(trend.series.filter(item => item.name !== '合计').map(item => ({
    axisValue: trend.xAxis.data[0],
    marker: '',
    seriesName: item.name,
    value: item.data[0],
  })));
  assert.equal(hover.includes('合计'), false);
  assert.match(hover, new RegExp(trend.series[0].name));
  const short = trendOption({ filledBuckets: derived.filledBuckets.slice(0, 3), modelOrder: derived.modelOrder, mode: 'line' });
  assert.equal(short.xAxis.type, 'category');
  assert.equal(short.dataZoom.length, 1);
  assert.equal(short.dataZoom[0].type, 'inside');
  assert.match(String(short.grid.right), /%$/);
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

test('reward payouts are labelled, excluded from task charts and never counted as a model', () => {
  const buckets = [{
    from: '2026-10-21T16:00:00.000Z', to: '2026-10-22T16:00:00.000Z',
    items: [], total_micro_usd: '1600', record_count: 5, task_micro_usd: '1000', task_record_count: 4,
  }];
  const taskAvg = taskAvgOption(buckets);
  assert.equal(taskAvg.series[0].data[0], 4);
  assert.equal(taskAvg.series[1].data[0], 0.00025);

  const rows = [
    { model: 'Qwen3-32B', amount_micro_usd: '1000', record_count: 4, active_buckets: 1 },
    { model: 'base_reward', amount_micro_usd: '600', record_count: 1, active_buckets: 1 },
  ];
  const shares = modelShareRows(rows);
  const share = modelShareOption(shares);
  assert.deepEqual(share.series[0].data.map(item => item.name), ['Qwen3-32B', '基础奖励']);
  const rewardSlice = share.series[0].data.at(-1);
  assert.equal(rewardSlice.itemStyle.color, '#94a3b8');

  const heatmap = modelHourHeatmap([
    { hour: 4, model: 'Qwen3-32B', amount_micro_usd: '10', record_count: 1 },
  ], ['Qwen3-32B']);
  assert.deepEqual(heatmap.yAxis.data, ['Qwen3-32B']);
});

test('eight selected models keep distinct colours even with a reward in the series order', () => {
  const models = Array.from({ length: 8 }, (_, index) => `model-${index}`);
  for (const rewardAt of [0, 3, 8]) {
    const order = [...models];
    order.splice(rewardAt, 0, 'base_reward');
    const filledBuckets = [{ from: '2026-10-21T16:00:00.000Z', to: '2026-10-22T16:00:00.000Z', items: [...order.map(model => ({ model, amount_micro_usd: '1000000', record_count: 1 }))], total_micro_usd: '9000000', record_count: order.length, task_micro_usd: '8000000', task_record_count: 8 }];
    const option = trendOption({ filledBuckets, modelOrder: order, mode: 'bar' });
    const modelColors = option.series.filter(series => series.name !== '合计').map(series => series.itemStyle.color);
    assert.equal(new Set(modelColors).size, modelColors.length, `duplicate colour when reward sits at ${rewardAt}`);
    assert.equal(modelColors.filter(color => color === '#94a3b8').length, 1);
  }
});
