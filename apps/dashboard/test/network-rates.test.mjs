import test from 'node:test';
import assert from 'node:assert/strict';
import { queryAgent } from '../public/agent-client.js';
import {
  candidateTableOptions,
  formatShare,
  formatUsdPerHourMicro,
  formatUsdPerMillionMicro,
  relativeToCurrent,
  validateNetworkRatesResponse,
  renderCandidatesTable,
  renderTokenTrendFigure,
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
