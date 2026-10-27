import test from 'node:test';
import assert from 'node:assert/strict';
import { MODEL_COLORS, REWARD_COLOR, HOME_MODEL_COLORS, TOTAL_COLOR, MAX_MODELS } from '../public/palette.js';
import { trendOption, modelShareOption, modelCompareOption, legendLayout, legendExtraHeight } from '../public/analysis-charts.js';
import { modelShareRows } from '../public/analysis-data.js';

// Perceptual distance good enough to reject an accidental near-duplicate.
function lab(hex) {
  const n = parseInt(hex.slice(1), 16);
  const f = c => (c > 0.04045 ? ((c + 0.055) / 1.055) ** 2.4 : c / 12.92);
  const r = f(((n >> 16) & 255) / 255), g = f(((n >> 8) & 255) / 255), b = f((n & 255) / 255);
  const t = c => (c > 0.008856 ? Math.cbrt(c) : 7.787 * c + 16 / 116);
  const x = t((r * 0.4124 + g * 0.3576 + b * 0.1805) / 0.95047);
  const y = t(r * 0.2126 + g * 0.7152 + b * 0.0722);
  const z = t((r * 0.0193 + g * 0.1192 + b * 0.9505) / 1.08883);
  return [116 * y - 16, 500 * (x - y), 200 * (y - z)];
}
const deltaE = (a, b) => {
  const A = lab(a), B = lab(b);
  return Math.hypot(A[0] - B[0], A[1] - B[1], A[2] - B[2]);
};

test('palette offers one distinct colour per selectable model', () => {
  assert.equal(MAX_MODELS, MODEL_COLORS.length);
  assert.equal(new Set(MODEL_COLORS).size, MODEL_COLORS.length);
  // The cap must cover a machine with more than the old eight models installed.
  assert.ok(MAX_MODELS >= 12);
  for (const color of MODEL_COLORS) assert.match(color, /^#[0-9a-f]{6}$/);
});

test('every palette colour is perceptually distinct, including the reward grey', () => {
  const all = [...MODEL_COLORS, REWARD_COLOR];
  for (let i = 0; i < all.length; i++) {
    for (let j = i + 1; j < all.length; j++) {
      const distance = deltaE(all[i], all[j]);
      assert.ok(distance >= 20, `${all[i]} and ${all[j]} are too close (ΔE ${distance.toFixed(1)})`);
    }
  }
});

test('home chart reuses the palette without repeating the total colour', () => {
  assert.equal(TOTAL_COLOR, MODEL_COLORS[0]);
  assert.equal(HOME_MODEL_COLORS.includes(TOTAL_COLOR), false);
  assert.equal(new Set(HOME_MODEL_COLORS).size, HOME_MODEL_COLORS.length);
  assert.ok(HOME_MODEL_COLORS.length >= 11);
});

test('twelve models plus a reward each get a unique colour', () => {
  const models = MODEL_COLORS.map((_, index) => `model-${index}`);
  const order = ['base_reward', ...models];
  const filledBuckets = [{
    from: '2026-10-21T16:00:00.000Z', to: '2026-10-22T16:00:00.000Z',
    items: order.map(model => ({ model, amount_micro_usd: '1000000', record_count: 1 })),
    total_micro_usd: String(order.length * 1_000_000), record_count: order.length,
    task_micro_usd: String(models.length * 1_000_000), task_record_count: models.length,
  }];
  const option = trendOption({ filledBuckets, modelOrder: order, mode: 'bar' });
  const drawn = option.series.filter(series => series.name !== '合计');
  const colors = drawn.map(series => series.itemStyle.color);
  assert.equal(colors.length, MAX_MODELS + 1);
  assert.equal(new Set(colors).size, colors.length, 'a model reused another model colour');
  assert.deepEqual(drawn.filter(series => series.itemStyle.color === REWARD_COLOR).map(series => series.name), ['基础奖励']);
});

test('crowded model charts switch to a compact layout instead of overflowing', () => {
  const shares = modelShareRows(MODEL_COLORS.map((_, index) => ({
    model: `model-${index}`, amount_micro_usd: String((index + 1) * 1_000_000), record_count: index + 1,
  })));
  const legend = modelShareOption(shares).legend;
  assert.equal(legend.type, 'scroll');
  assert.ok(legend.itemGap < 16);
  assert.ok(legend.textStyle.fontSize < 12);

  const sparse = modelShareOption(modelShareRows([{ model: 'a', amount_micro_usd: '1', record_count: 1 }])).legend;
  assert.equal(sparse.itemGap, 16);
  assert.equal(sparse.textStyle.fontSize, 12);

  const compare = modelCompareOption(MODEL_COLORS.map((model, index) => ({
    model, amount_micro_usd: '1000000', record_count: 1, active_buckets: 1,
  })), 1);
  assert.equal(compare.xAxis.axisLabel.rotate, 40);
  assert.ok(compare.grid.bottom > 40);

  const few = modelCompareOption([{ model: 'a', amount_micro_usd: '1000000', record_count: 1, active_buckets: 1 }], 1);
  assert.equal(few.xAxis.axisLabel.rotate, undefined);
  assert.equal(few.grid.bottom, 40);
});
test('series legends wrap instead of paginating until they would take three rows', () => {
  // In Node there is no canvas, so widths fall back to a fixed per-character estimate.
  const short = ['a', 'b', 'c'];
  const long = Array.from({ length: 12 }, (_, index) => `Long-Model-Name-${index}`);
  assert.equal(legendLayout(short, 900).rows, 1);
  assert.ok(legendLayout(long, 500).rows > 1);
  assert.ok(legendLayout(long, 500).height > legendLayout(long, 2400).height);
  assert.equal(legendExtraHeight(short, 900), 0);

  const bucket = { from: '2026-10-21T16:00:00.000Z', to: '2026-10-22T16:00:00.000Z', items: [], total_micro_usd: '0', record_count: 0, task_micro_usd: '0', task_record_count: 0 };
  const models = Array.from({ length: 9 }, (_, index) => `Long-Model-Name-${index}`);
  const wrapped = trendOption({ filledBuckets: [bucket], modelOrder: models, mode: 'bar', chartWidth: 600 });
  const single = trendOption({ filledBuckets: [bucket], modelOrder: models.slice(0, 2), mode: 'bar', chartWidth: 600 });
  // Nine long names cannot fit on one line, so the grid yields rows to the legend.
  assert.ok(Number(wrapped.grid.bottom) > Number(single.grid.bottom));
  // A wide canvas needs fewer rows than a narrow one for the same models.
  const wide = trendOption({ filledBuckets: [bucket], modelOrder: models, mode: 'bar', chartWidth: 1600 });
  assert.ok(Number(wide.grid.bottom) <= Number(wrapped.grid.bottom));
});
