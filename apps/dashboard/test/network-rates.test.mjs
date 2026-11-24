import test from 'node:test';
import assert from 'node:assert/strict';
import { queryAgent } from '../public/agent-client.js';
import {
  candidateTableOptions,
  candidateForScope,
  formatShare,
  formatUsdPerHourMicro,
  formatUsdPerMillionMicro,
  relativeToCurrent,
  validateNetworkRatesResponse,
  renderCandidatesTable,
  renderTokenTrendFigure,
  renderTokenShareCards,
  renderTokenTrendModelChoices,
  tokenTrendAxis,
  clampTokenTrendRange,
  filterTokenTrendByRange,
  formatTokenTrendInput,
  parseTokenTrendInput,
  tokenTrendRangeFromInputs,
  tokenTrendRangeLabel,
  tokenTrendRangeMatches,
  tokenTrendRecentRange,
  tokenTrendSeriesEnd,
  tokenTrendWindow,
  validateNetworkRatesTokenTrendResponse,
  resolveNetworkScope,
  networkScopeToggleAvailable,
  tokenTrendFromPayload,
  tokenTrendModels,
  tokenTrendSeriesForMetric,
  tokenTrendSeriesForShare,
  tokenTrendShareBaselinePercent,
  tokenTrendShareSummary,
  NETWORK_METRIC_STORAGE_KEY,
  storedNetworkMetric,
  resolveNetworkMetric,
  networkMetricToggleAvailable,
  TOKEN_TREND_SHARE_WINDOW_MS,
  TOKEN_TREND_SHARE_MIN_SAMPLES,
  NETWORK_RATES_REFRESH_MS,
  formatRefreshCountdown,
  networkRefreshPlan,
} from '../public/network-rates.js';

const device = { url: 'https://mac.tail123.ts.net:8443', token: 'synthetic-test-token' };
const ratesBody = {
  enabled: true,
  auto_switch: false,
  auto_switch_since: null,
  window_samples: 6,
  samples: 4,
  updated_at: '2026-10-22T08:00:00Z',
  snapshot_at: '2026-10-22T08:00:00Z',
  current_model: 'Qwen3-32B',
  recommendation: null,
  candidates: [{ model: 'A', downloaded: true, avg_providers: 3, requests_per_hour: 1, tokens_per_hour: 1000, usd_per_hour_micro: '5000', price_source: 'official', insufficient: false }],
  switch: { next_eligible_at: null, blocked_reason: 'insufficient_samples', backoff_until: null },
  last_error: null,
};

test('hourly micro formatting uses two decimals or finer below one cent', () => {
  assert.equal(formatUsdPerHourMicro('142500'), '$0.14/h');
  assert.equal(formatUsdPerHourMicro('10000'), '$0.01/h');
  assert.equal(formatUsdPerHourMicro('8500'), '$0.0085/h');
  assert.equal(formatUsdPerHourMicro('500'), '$0.0005/h');
});

test('candidates escape server strings', () => {
  const escape = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
  const html = renderCandidatesTable([{ model: '<img onerror=1>', downloaded: false, avg_providers: 1, requests_per_hour: 1, tokens_per_hour: 1, usd_per_hour_micro: '1', price_source: 'fallback', insufficient: true }], escape);
  assert.match(html, /&lt;img onerror=1&gt;/);
  assert.doesNotMatch(html, /<img/);
});

test('candidate table shows short-window columns, provider counts, combined price and gain over current model', () => {
  const window = (usd, extra = {}) => ({ samples: 30, avg_providers: 5, busy_ratio: 0.5, requests_per_hour: 10, tokens_per_hour: 20000, usd_per_hour_micro: usd, trimmed_usd_per_hour_micro: '1000', ...extra });
  const rates = {
    ...ratesBody, current_model: 'cur', window_samples: 5, interval_minutes: 2, long_window_minutes: 60, switch_advantage_ratio: 0.2, chip_tiers: ['Pro'], memory_gb: 64,
    candidates: [
      { model: 'best', downloaded: true, avg_providers: 4, busy_ratio: 0.25, requests_per_hour: 10, tokens_per_hour: 30000, usd_per_hour_micro: '150000', trimmed_usd_per_hour_micro: null, price_source: 'official', effective_price_micro: '570000', input_ratio: 8.3, ratio_source: 'network', cohort: 'tier', insufficient: false, tier_avg_providers: 4, all_avg_providers: 9, hour: window('90000') },
      { model: 'cur', downloaded: true, avg_providers: 6, busy_ratio: 0.5, requests_per_hour: 10, tokens_per_hour: 30000, usd_per_hour_micro: '100000', trimmed_usd_per_hour_micro: '80000', price_source: 'official', effective_price_micro: '2550000', input_ratio: 1.2, ratio_source: 'own', cohort: 'all', insufficient: false, tier_avg_providers: 1, all_avg_providers: 6, hour: window('100000') },
      { model: 'cold', downloaded: false, avg_providers: 1, requests_per_hour: 0, tokens_per_hour: 0, usd_per_hour_micro: '0', price_source: 'fallback', insufficient: true },
    ],
  };
  assert.equal(validateNetworkRatesResponse(rates), rates);
  assert.equal(validateNetworkRatesResponse(ratesBody), ratesBody);
  assert.throws(() => validateNetworkRatesResponse({ ...rates, candidates: [{ ...rates.candidates[0], busy_ratio: 2 }] }));
  assert.throws(() => validateNetworkRatesResponse({ ...rates, candidates: [{ ...rates.candidates[0], cohort: 'x' }] }));
  const options = candidateTableOptions(rates);
  assert.equal(options.shortLabel, '近 10 分钟');
  assert.equal(options.longLabel, undefined);
  const html = renderCandidatesTable(rates.candidates, String, options);
  assert.match(html, /同档数\/全组数/);
  assert.match(html, />4\/9</);
  assert.match(html, />1\/6</);
  assert.match(html, /以上为近 10 分钟的数据。/);
  assert.doesNotMatch(html, /近 1 小时|colspan|rowspan|同档 Pro|全组（同档不足）/);
  assert.match(html, /\$0\.570\/M/);
  assert.match(html, /\$2\.55\/M/);
  assert.match(html, /全网输入比 8\.3/);
  assert.match(html, /本机输入比 1\.2/);
  assert.match(html, /network-rates-gain" title="相对当前模型">\+50%/);
  assert.match(html, /25%/);
  assert.match(html, /未下载/);
  assert.match(html, /兜底价/);
  assert.equal(formatShare(null), '—');
  assert.equal(formatUsdPerMillionMicro('220000'), '$0.220/M');
  assert.equal(relativeToCurrent('120000', '0'), null);
});

test('candidate table can show every model on all or tier, never mixed', () => {
  const window = (avg, tokens, usd, extra = {}) => ({ samples: 5, avg_providers: avg, busy_ratio: 0.4, requests_per_hour: 10, tokens_per_hour: tokens, usd_per_hour_micro: usd, trimmed_usd_per_hour_micro: '1000', ...extra });
  const candidates = [
    { model: 'best', downloaded: true, avg_providers: 3, requests_per_hour: 10, tokens_per_hour: 30000, usd_per_hour_micro: '150000', price_source: 'official', insufficient: false, tier_avg_providers: 3, all_avg_providers: 9, all: window(9, 18000, '90000'), tier: window(3, 30000, '150000') },
    { model: 'thin', downloaded: true, avg_providers: 6, requests_per_hour: 8, tokens_per_hour: 12000, usd_per_hour_micro: '60000', price_source: 'official', insufficient: false, tier_avg_providers: 1, all_avg_providers: 6, all: window(6, 12000, '60000'), tier: window(1, 48000, '240000') },
  ];
  const rates = { ...ratesBody, chip_tiers: ['Pro'], min_providers: 3, current_model: 'thin', candidates };
  assert.equal(resolveNetworkScope('tier', rates), 'tier');
  assert.equal(resolveNetworkScope('all', { ...rates, chip_tiers: [] }), 'all');
  assert.equal(networkScopeToggleAvailable(rates, { series: [], all: [], tier: [] }), true);
  assert.equal(networkScopeToggleAvailable({ ...rates, chip_tiers: [] }, { all: [], tier: [] }), false);
  const allHtml = renderCandidatesTable(candidates, String, candidateTableOptions(rates, 'all'));
  assert.match(allHtml, /18,000/);
  assert.match(allHtml, /12,000/);
  assert.match(allHtml, /（全组）/);
  assert.doesNotMatch(allHtml, /30,000|48,000/);
  const tierHtml = renderCandidatesTable(candidates, String, candidateTableOptions(rates, 'tier'));
  assert.match(tierHtml, /30,000/);
  assert.match(tierHtml, /48,000/);
  assert.match(tierHtml, /（同档 Pro）/);
  assert.doesNotMatch(tierHtml, /18,000|12,000/);
  assert.equal(candidateForScope(candidates[1], 'tier', 3).insufficient, true);
  assert.equal(candidateForScope(candidates[1], 'all', 3).insufficient, false);
  assert.doesNotMatch(tierHtml, /network-rates-muted/);
});

test('only undownloaded candidates mute the row, not insufficient samples', () => {
  const row = (model, extra = {}) => ({
    model, downloaded: true, avg_providers: 1, requests_per_hour: 0, tokens_per_hour: 0,
    usd_per_hour_micro: '0', price_source: 'official', insufficient: true, ...extra,
  });
  const thin = renderCandidatesTable([row('thin')], String);
  assert.doesNotMatch(thin, /network-rates-muted/);
  const cold = renderCandidatesTable([row('cold', { downloaded: false })], String);
  assert.match(cold, /<tr class="network-rates-muted">/);
});

test('token trend payload keeps all and tier series apart', () => {
  const point = (tokens) => ({ at: '2026-10-23T01:00:00Z', tokens });
  const payload = {
    series: [{ model: 'a', points: [point(1)] }],
    all: [{ model: 'a', points: [point(100)] }, { model: 'b', points: [point(200)] }],
    tier: [{ model: 'a', points: [point(3)] }],
  };
  assert.equal(validateNetworkRatesTokenTrendResponse(payload), payload);
  assert.deepEqual(tokenTrendFromPayload(payload, 'all').map(item => item.points[0].tokens), [100, 200]);
  assert.deepEqual(tokenTrendFromPayload(payload, 'tier').map(item => item.points[0].tokens), [3]);
  assert.deepEqual(tokenTrendModels(payload).sort(), ['a', 'a', 'a', 'b']);
  assert.equal(tokenTrendFromPayload({ series: payload.series }, 'tier')[0].points[0].tokens, 1);
});

test('token trend share joins all and tier totals at the same at', () => {
  assert.equal(TOKEN_TREND_SHARE_WINDOW_MS, 30 * 60_000);
  assert.equal(TOKEN_TREND_SHARE_MIN_SAMPLES, 3);
  const start = Date.parse('2026-10-23T01:00:00Z');
  const at = index => new Date(start + index * 2 * 60_000).toISOString();
  const totals = (values, extra = {}) => values.map((total, index) => ({ at: at(index), tokens: 1, total, ...extra }));
  const payload = {
    series: [{ model: 'a', points: [{ at: at(0), tokens: 1, total: 999 }] }],
    all: [
      { model: 'a', points: [...totals([100, 100, 100]), { at: at(5), tokens: 10, total: 50 }] },
      { model: 'b', points: totals([80, 80, 80]) },
    ],
    tier: [{
      model: 'a',
      points: [...totals([40, 40, 40]), { at: at(7), tokens: 5, total: 10 }, { at: at(8), tokens: 5 }],
    }],
  };
  assert.deepEqual(tokenTrendSeriesForShare(payload), [{ model: 'a', points: [{ at: at(2), tokens: 40 }] }]);
  assert.deepEqual(tokenTrendSeriesForShare({ series: [], all: [], tier: [] }), []);
  assert.deepEqual(tokenTrendSeriesForShare(payload.series), []);
  const third = tokenTrendSeriesForShare({
    all: [{ model: 'c', points: totals([3, 3, 3]) }],
    tier: [{ model: 'c', points: totals([1, 1, 1]) }],
  });
  assert.equal(third[0].points[0].tokens, 33);
  const over = tokenTrendSeriesForShare({
    all: [{ model: 'd', points: totals([10, 10, 10]) }],
    tier: [{ model: 'd', points: totals([15, 15, 15]) }],
  });
  assert.equal(over[0].points[0].tokens, 150);
  const figure = renderTokenTrendFigure(third, {
    width: 640, height: 220, escape: String, metricLabel: '各模型同档组总 token 占全组近 30 分钟滚动比例', periodLabel: '前 2 小时',
    formatTick: tick => `${tick}%`, formatValue: tokens => `${tokens}%`,
  });
  assert.match(figure.markup, /各模型同档组总 token 占全组近 30 分钟滚动比例/);
  assert.match(figure.markup, /33%/);
  assert.equal(figure.series[0].coords[0].tokens, 33);
});

test('token trend share rolls a 30-minute sum then divide', () => {
  const start = Date.parse('2026-10-23T01:00:00Z');
  const at = index => new Date(start + index * 2 * 60_000).toISOString();
  const pair = (tierTotals, allTotals) => ({
    all: [{ model: 'a', points: allTotals.map((total, index) => ({ at: at(index), tokens: 1, total })) }],
    tier: [{ model: 'a', points: tierTotals.map((total, index) => ({ at: at(index), tokens: 1, total })) }],
  });
  const rolled = tokenTrendSeriesForShare(pair([10, 10, 10], [10, 90, 100]));
  assert.deepEqual(rolled, [{ model: 'a', points: [{ at: at(2), tokens: 15 }] }]);
  assert.equal(tokenTrendSeriesForShare(pair([10, 10], [10, 90])).length, 0);
  assert.equal(tokenTrendSeriesForShare(pair([5, 5, 5], [0, 0, 0])).length, 0);
  const withOutside = tokenTrendSeriesForShare({
    all: [{ model: 'a', points: [
      { at: at(0), tokens: 1, total: 100000 },
      { at: new Date(start + 32 * 60_000).toISOString(), tokens: 1, total: 100 },
      { at: new Date(start + 42 * 60_000).toISOString(), tokens: 1, total: 100 },
      { at: new Date(start + 52 * 60_000).toISOString(), tokens: 1, total: 100 },
    ] }],
    tier: [{ model: 'a', points: [
      { at: at(0), tokens: 1, total: 100000 },
      { at: new Date(start + 32 * 60_000).toISOString(), tokens: 1, total: 10 },
      { at: new Date(start + 42 * 60_000).toISOString(), tokens: 1, total: 10 },
      { at: new Date(start + 52 * 60_000).toISOString(), tokens: 1, total: 10 },
    ] }],
  });
  assert.deepEqual(withOutside[0].points.at(-1), { at: new Date(start + 52 * 60_000).toISOString(), tokens: 10 });
  const lookback = tokenTrendSeriesForShare({
    all: [{ model: 'a', points: Array.from({ length: 16 }, (_, index) => ({ at: at(index), tokens: 1, total: 100 })) }],
    tier: [{ model: 'a', points: Array.from({ length: 16 }, (_, index) => ({ at: at(index), tokens: 1, total: index < 15 ? 0 : 100 })) }],
  });
  const boundary = lookback[0].points.find(point => point.at === at(15));
  assert.equal(boundary.tokens, 7);
  const recent = filterTokenTrendByRange(lookback, { start: Date.parse(at(15)), end: Date.parse(at(15)) });
  assert.equal(recent[0].points[0].tokens, 7);
});

test('token trend share baseline, summary lift and polyline', () => {
  const start = Date.parse('2026-10-23T01:00:00Z');
  const at = index => new Date(start + index * 2 * 60_000).toISOString();
  const points = totals => totals.map((total, index) => ({ at: at(index), tokens: 1, total }));
  assert.equal(tokenTrendShareBaselinePercent({ tier_avg_providers: 2.8, all_avg_providers: 10 }), 28);
  assert.equal(tokenTrendShareBaselinePercent({ tier_avg_providers: 4, all_avg_providers: 9 }), 44);
  assert.equal(tokenTrendShareBaselinePercent({ tier_avg_providers: 1, all_avg_providers: 0 }), null);
  assert.equal(tokenTrendShareBaselinePercent({ all_avg_providers: 10 }), null);
  assert.equal(tokenTrendShareBaselinePercent(null), null);
  const payload = {
    all: [
      { model: 'a', points: points([100, 100, 100, 100]) },
      { model: 'b', points: points([50, 50, 50]) },
      { model: 'c', points: points([0, 0, 0]) },
    ],
    tier: [
      { model: 'a', points: points([42, 42, 42, 10]) },
      { model: 'b', points: points([10, 10, 10]) },
      { model: 'c', points: points([1, 1, 1]) },
    ],
  };
  const candidates = [
    { model: 'a', tier_avg_providers: 2.8, all_avg_providers: 10 },
    { model: 'b', tier_avg_providers: 1, all_avg_providers: 0 },
  ];
  const summary = tokenTrendShareSummary(payload, {
    range: { start, end: Date.parse(at(2)) },
    models: ['b', 'a', 'c'],
    candidates,
  });
  assert.deepEqual(summary, [
    { model: 'b', share: 20, baseline: null, lift: null },
    { model: 'a', share: 42, baseline: 28, lift: 1.5 },
  ]);
  const polyline = renderTokenTrendFigure([
    { model: 'a', points: [{ at: at(0), tokens: 10 }, { at: at(1), tokens: 12 }, { at: at(2), tokens: 11 }], baseline: 80 },
  ], { width: 640, height: 220, escape: String, smooth: false });
  assert.match(polyline.markup, /class="network-token-baseline"/);
  assert.match(polyline.markup, / L/);
  assert.doesNotMatch(polyline.markup, / C/);
  assert.ok(polyline.markup.indexOf('network-token-baseline') < polyline.markup.indexOf('network-token-line'));
  const baselineY = Number(polyline.markup.match(/class="network-token-baseline"[^>]*y1="([^"]+)"/)[1]);
  assert.ok(baselineY >= 16 && baselineY <= 192);
  const curve = renderTokenTrendFigure([
    { model: 'a', points: [{ at: at(0), tokens: 10 }, { at: at(1), tokens: 12 }, { at: at(2), tokens: 11 }] },
  ], { width: 640, height: 220, escape: String });
  assert.match(curve.markup, / C/);
  assert.doesNotMatch(curve.markup, /network-token-baseline/);
  const aligned = renderTokenTrendFigure([
    { model: 'a', points: [{ at: at(0), tokens: 10 }] },
  ], { width: 640, height: 220, escape: String, xRange: { start: Date.parse(at(0)), end: Date.parse(at(2)) } });
  assert.ok(aligned.series[0].coords[0].x < 80);
  const cards = renderTokenShareCards(['a', 'b'], {
    escape: String,
    colorFor: () => '#5b8def',
    labelFor: model => model === 'a' ? 'A' : 'B',
    summaryFor: model => model === 'a' ? { share: 42, baseline: 28, lift: 1.5 } : null,
  });
  assert.match(cards, /data-share-chart="a"/);
  assert.match(cards, /data-share-chart="b"/);
  assert.match(cards, /A 同档\/全组占比/);
  assert.match(cards, /42% · 基线 28% · 1\.5×/);
  assert.doesNotMatch(cards, /data-share-chart="c"/);
});

test('token trend total is optional and plots cohort totals', () => {
  const at = '2026-10-23T01:00:00Z';
  const withoutTotal = { series: [{ model: 'a', points: [{ at, tokens: 36000 }] }] };
  assert.equal(validateNetworkRatesTokenTrendResponse(withoutTotal), withoutTotal);
  const withTotal = { series: [{ model: 'a', points: [{ at, tokens: 36000, total: 72000 }] }] };
  assert.equal(validateNetworkRatesTokenTrendResponse(withTotal), withTotal);
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at, tokens: 36000, total: -1 }] }] }));
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at, tokens: 36000, total: 1.5 }] }] }));
  const fixture = [{ model: 'a', points: [{ at, tokens: 36000, total: 72000 }, { at: '2026-10-23T01:05:00Z', tokens: 18000 }] }];
  assert.deepEqual(tokenTrendSeriesForMetric(fixture)[0].points.map(point => point.tokens), [36000, 18000]);
  assert.deepEqual(tokenTrendSeriesForMetric(fixture, 'total')[0].points, [{ at, tokens: 72000 }]);
  const figure = renderTokenTrendFigure(tokenTrendSeriesForMetric(fixture, 'total'), {
    width: 640, height: 220, escape: String, metricLabel: '各模型组总每小时输出 token', periodLabel: '前 2 小时',
  });
  assert.match(figure.markup, /各模型组总每小时输出 token/);
  assert.doesNotMatch(figure.markup, /每台每小时输出 token/);
  assert.equal(figure.series[0].coords[0].tokens, 72000);
});

test('token trend requests fields are optional and map like total', () => {
  const at = '2026-10-23T01:00:00Z';
  const omitted = { series: [{ model: 'a', points: [{ at, tokens: 36000 }] }] };
  assert.equal(validateNetworkRatesTokenTrendResponse(omitted), omitted);
  const zero = { series: [{ model: 'a', points: [{ at, tokens: 36000, requests: 0, requests_total: 0 }] }] };
  assert.equal(validateNetworkRatesTokenTrendResponse(zero), zero);
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at, tokens: 36000, requests: -1 }] }] }));
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at, tokens: 36000, requests: 1.5 }] }] }));
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at, tokens: 36000, requests_total: -1 }] }] }));
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at, tokens: 36000, requests_total: 1.5 }] }] }));
  const fixture = [{ model: 'a', points: [
    { at, tokens: 36000, total: 72000, requests: 4, requests_total: 8 },
    { at: '2026-10-23T01:05:00Z', tokens: 18000, total: 36000 },
  ] }];
  assert.deepEqual(tokenTrendSeriesForMetric(fixture, 'requests_total')[0].points, [{ at, tokens: 8 }]);
  assert.deepEqual(tokenTrendSeriesForMetric(fixture)[0].points.map(point => point.tokens), [36000, 18000]);
});

test('token trend share can use requests_total instead of total', () => {
  const start = Date.parse('2026-10-23T01:00:00Z');
  const at = index => new Date(start + index * 2 * 60_000).toISOString();
  const points = (totals, requestTotals) => totals.map((total, index) => ({
    at: at(index), tokens: 1, total, requests: 1, requests_total: requestTotals[index],
  }));
  const payload = {
    all: [{ model: 'a', points: points([100, 100, 100], [10, 10, 10]) }],
    tier: [{ model: 'a', points: points([40, 40, 40], [8, 8, 8]) }],
  };
  assert.deepEqual(tokenTrendSeriesForShare(payload), [{ model: 'a', points: [{ at: at(2), tokens: 40 }] }]);
  assert.deepEqual(tokenTrendSeriesForShare(payload, 'tokens'), [{ model: 'a', points: [{ at: at(2), tokens: 40 }] }]);
  assert.deepEqual(tokenTrendSeriesForShare(payload, 'requests'), [{ model: 'a', points: [{ at: at(2), tokens: 80 }] }]);
  assert.deepEqual(tokenTrendSeriesForShare(payload, 'requests_total'), [{ model: 'a', points: [{ at: at(2), tokens: 80 }] }]);
  assert.equal(tokenTrendShareSummary(payload, { models: ['a'] })[0].share, 40);
  assert.equal(tokenTrendShareSummary(payload, { models: ['a'], metric: 'requests' })[0].share, 80);
  const missingRequests = {
    all: [{ model: 'a', points: points([100, 100, 100], [10, 10, 10]).map(({ requests_total, ...point }) => point) }],
    tier: [{ model: 'a', points: points([40, 40, 40], [8, 8, 8]).map(({ requests_total, ...point }) => point) }],
  };
  assert.deepEqual(tokenTrendSeriesForShare(missingRequests, 'requests'), []);
});

test('network metric preference falls back when request fields are missing', () => {
  const at = '2026-10-23T01:00:00Z';
  const without = { series: [{ model: 'a', points: [{ at, tokens: 1, total: 2 }] }] };
  const withRequests = { series: [{ model: 'a', points: [{ at, tokens: 1, requests: 0 }] }] };
  const withTotal = { series: [], all: [{ model: 'a', points: [{ at, tokens: 1, requests_total: 3 }] }], tier: [] };
  assert.equal(networkMetricToggleAvailable(without), false);
  assert.equal(networkMetricToggleAvailable({ series: [], all: [], tier: [] }), false);
  assert.equal(networkMetricToggleAvailable(withRequests), true);
  assert.equal(networkMetricToggleAvailable(withTotal), true);
  assert.equal(resolveNetworkMetric('requests', without), 'tokens');
  assert.equal(resolveNetworkMetric('requests', withRequests), 'requests');
  assert.equal(resolveNetworkMetric('tokens', withRequests), 'tokens');
  assert.equal(resolveNetworkMetric('bogus', withRequests), 'tokens');
  const original = globalThis.localStorage;
  const store = new Map();
  globalThis.localStorage = {
    getItem(key) { return store.has(key) ? store.get(key) : null; },
    setItem(key, value) { store.set(key, String(value)); },
  };
  try {
    assert.equal(storedNetworkMetric(), 'tokens');
    localStorage.setItem(NETWORK_METRIC_STORAGE_KEY, 'requests');
    assert.equal(storedNetworkMetric(), 'requests');
    localStorage.setItem(NETWORK_METRIC_STORAGE_KEY, 'bogus');
    assert.equal(storedNetworkMetric(), 'tokens');
  } finally {
    globalThis.localStorage = original;
  }
});

test('token trend range filter slices within the latest 24h payload', () => {
  const series = [{
    model: 'a',
    points: [
      { at: '2026-10-23T00:00:00.000Z', tokens: 1 },
      { at: '2026-10-23T06:00:00.000Z', tokens: 2 },
      { at: '2026-10-23T06:00:30.000Z', tokens: 4 },
      { at: '2026-10-23T12:00:00.000Z', tokens: 3 },
    ],
  }];
  const end = Date.parse('2026-10-23T12:00:00.000Z');
  assert.equal(tokenTrendSeriesEnd(series), end);
  const window = tokenTrendWindow(series);
  assert.deepEqual(window, { start: end - 24 * 3_600_000, end });
  assert.equal(formatTokenTrendInput(end), '2026-10-23T20:00');
  assert.equal(parseTokenTrendInput('2026-10-23T20:00'), end);
  assert.equal(tokenTrendRangeLabel(null, window), '前 2 小时');
  assert.equal(tokenTrendRangeLabel(window, window), '近 24 小时');
  const outside = clampTokenTrendRange({ start: window.start - 3_600_000, end: window.end + 3_600_000 }, window);
  assert.deepEqual(outside, window);
  const swapped = clampTokenTrendRange({ start: end, end: end - 6 * 3_600_000 }, window);
  assert.equal(swapped.start, end - 6 * 3_600_000);
  assert.equal(swapped.end, end);
  const six = filterTokenTrendByRange(series, { start: end - 6 * 3_600_000, end });
  assert.deepEqual(six[0].points.map(point => point.tokens), [2, 4, 3]);
  assert.equal(filterTokenTrendByRange(series, window)[0].points.length, 4);
  const minute = tokenTrendRangeFromInputs('2026-10-23T14:00', '2026-10-23T14:00', window);
  assert.equal(minute.start, Date.parse('2026-10-23T06:00:00.000Z'));
  assert.equal(minute.end, Date.parse('2026-10-23T06:00:59.999Z'));
  assert.deepEqual(filterTokenTrendByRange(series, minute)[0].points.map(point => point.tokens), [2, 4]);
  const full = tokenTrendRangeFromInputs(formatTokenTrendInput(window.start), formatTokenTrendInput(window.end), window);
  assert.deepEqual(full, window);
  assert.equal(tokenTrendRangeLabel(full, window), '近 24 小时');
  const partial = tokenTrendRangeFromInputs('2026-10-23T14:00', '2026-10-23T18:00', window);
  assert.match(tokenTrendRangeLabel(partial, window), /14:00/);
  assert.match(tokenTrendRangeLabel(partial, window), /18:00/);
  const recent = tokenTrendRecentRange(window);
  assert.equal(recent.end, window.end);
  assert.equal(recent.start, window.end - 2 * 3_600_000);
  assert.equal(tokenTrendRangeLabel(recent, window), '前 2 小时');
  assert.equal(tokenTrendRangeMatches(recent, tokenTrendRecentRange(window)), true);
  const shortWindow = { start: end - 3_600_000, end };
  assert.deepEqual(tokenTrendRecentRange(shortWindow), shortWindow);
});

test('token trend rejects old points payload and draws escaped multi-series curves', () => {
  assert.equal(validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at: '2026-10-23T01:00:00Z', tokens: 1200 }] }] }).series.length, 1);
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ points: [{ at: '2026-10-23T01:00:00Z', tokens: 1200 }] }));
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [], points: [] }));
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at: '2026-10-23T01:00:00Z', tokens: -1 }] }] }));
  assert.equal(validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at: '2026-10-23T01:00:00Z', tokens: 1200, total: 0 }] }] }).series[0].points[0].total, 0);
  const axis = tokenTrendAxis([1000, 1000]);
  assert.ok(axis.high > axis.low);
  const escape = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
  const models = ['<img onerror=1>', 'ok'];
  const choices = renderTokenTrendModelChoices(models, new Set(models), { escape, insufficient: new Set(['ok']), colorFor: model => ({ '<img onerror=1>': '#5b8def', ok: '#f2994a' }[model]) });
  assert.match(choices, /data-network-token-model/);
  assert.match(choices, /&lt;img onerror=1&gt;/);
  assert.match(choices, /is-insufficient/);
  assert.doesNotMatch(choices, /<img/);
  const figure = renderTokenTrendFigure([
    { model: '<img onerror=1>', points: [{ at: '2026-10-23T00:00:00Z', tokens: 1000 }, { at: '2026-10-23T01:00:00Z', tokens: 4000 }, { at: '2026-10-23T02:00:00Z', tokens: 2000 }] },
    { model: 'ok', points: [{ at: '2026-10-23T00:00:00Z', tokens: 800 }, { at: '2026-10-23T01:00:00Z', tokens: 1200 }, { at: '2026-10-23T02:00:00Z', tokens: 900 }] },
  ], { width: 640, height: 220, escape, insufficient: new Set(['ok']), colorFor: model => ({ '<img onerror=1>': '#5b8def', ok: '#f2994a' }[model]) });
  assert.equal(figure.markup.match(/class="network-token-line"/g).length, 2);
  assert.match(figure.markup, / C/);
  assert.match(figure.markup, /stroke-dasharray="6 4"/);
  assert.doesNotMatch(figure.markup, /network-token-area|linearGradient/);
  assert.match(figure.markup, /&lt;img onerror=1&gt;/);
  assert.doesNotMatch(figure.markup, /<img onerror/);
  const peak = figure.series[0].coords[1];
  assert.ok(peak.y < figure.series[0].coords[0].y && peak.y < figure.series[0].coords[2].y);
});

test('network refresh countdown is two minutes and joins the same device', () => {
  assert.equal(NETWORK_RATES_REFRESH_MS, 120_000);
  assert.equal(formatRefreshCountdown(120_000), '2:00');
  assert.equal(formatRefreshCountdown(61_000), '1:01');
  assert.equal(formatRefreshCountdown(0), '0:00');
  assert.equal(formatRefreshCountdown(-500), '0:00');
  assert.equal(networkRefreshPlan('device-a', 'device-a'), 'join');
  assert.equal(networkRefreshPlan('device-a', 'device-b'), 'start');
  assert.equal(networkRefreshPlan(null, 'device-a'), 'start');
});

test('old agents return network_rates_unsupported for network rates routes', async () => {
  const failure = async (response, route = 'network-rates') => queryAgent(device, route, undefined, { fetcher: async () => response });
  for (const route of ['network-rates', 'network-rates-token-trend']) {
    await assert.rejects(failure(new Response('old', { status: 404 }), route), { code: 'network_rates_unsupported' });
  }
});
