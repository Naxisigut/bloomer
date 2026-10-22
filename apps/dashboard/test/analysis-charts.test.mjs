import test from 'node:test';
import assert from 'node:assert/strict';
import { buildAnalysisParams, deriveAnalysisView, analysisPeriodRange } from '../public/analysis-data.js';
import { trendOption, cumulativeOption, weekdayHourHeatmap } from '../public/analysis-charts.js';
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
