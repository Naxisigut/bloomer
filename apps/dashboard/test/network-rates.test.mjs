import test from 'node:test';
import assert from 'node:assert/strict';
import { queryAgent, submitNetworkRatesSettings } from '../public/agent-client.js';
import {
  blockedReasonLabel,
  formatUsdPerHourMicro,
  renderCandidatesTable,
  renderSwitchRecords,
  renderTokenTrendFigure,
  tokenTrendAxis,
  validateNetworkRatesSettings,
  validateNetworkRatesTokenTrendResponse,
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

test('settings payload only accepts auto_switch boolean', () => {
  assert.deepEqual(validateNetworkRatesSettings({ auto_switch: true }), { auto_switch: true });
  assert.throws(() => validateNetworkRatesSettings({ auto_switch: 'yes' }), { code: 'invalid_request' });
  assert.throws(() => validateNetworkRatesSettings({ auto_switch: true, force: true }), { code: 'invalid_request' });
});

test('blocked reasons map to Chinese labels', () => {
  assert.equal(blockedReasonLabel('warming_up'), '开启后观察期');
  assert.equal(blockedReasonLabel('below_threshold'), '优势不足 20%');
  assert.equal(blockedReasonLabel('custom_code'), 'custom_code');
});

test('switch records and candidates escape server strings', () => {
  const escape = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
  const html = renderCandidatesTable([{ model: '<img onerror=1>', downloaded: false, avg_providers: 1, requests_per_hour: 1, tokens_per_hour: 1, usd_per_hour_micro: '1', price_source: 'fallback', insufficient: true }], escape);
  assert.match(html, /&lt;img onerror=1&gt;/);
  assert.doesNotMatch(html, /<img/);
  const switches = renderSwitchRecords([{ id: 1, at: '2026-10-22T08:00:00Z', from_model: 'a&b', to_model: 'c', reason: 'better_model', status: 'failed', error: 'command_failed', job_id: 'j', summary: [{ model: 'x<script>', usd_per_hour_micro: '1000', avg_providers: 2 }] }], escape, () => '2026-10-22');
  assert.match(switches, /a&amp;b/);
  assert.match(switches, /x&lt;script&gt;/);
});

test('switch records show only the five most recent entries', () => {
  const items = Array.from({ length: 8 }, (_, index) => ({ id: index, at: '2026-10-22T08:00:00Z', from_model: `from-${index}`, to_model: 'to', reason: 'better_model', status: 'succeeded', error: null, job_id: 'j', summary: [] }));
  const html = renderSwitchRecords(items, String, () => '2026-10-22');
  assert.equal(html.match(/<details/g).length, 5);
  assert.match(html, /from-0/);
  assert.doesNotMatch(html, /from-5/);
});

test('token trend rejects old points payload and draws escaped multi-series curves', () => {
  assert.equal(validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at: '2026-10-23T01:00:00Z', tokens: 1200 }] }] }).series.length, 1);
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ points: [{ at: '2026-10-23T01:00:00Z', tokens: 1200 }] }));
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [], points: [] }));
  assert.throws(() => validateNetworkRatesTokenTrendResponse({ series: [{ model: 'a', points: [{ at: '2026-10-23T01:00:00Z', tokens: -1 }] }] }));
  const axis = tokenTrendAxis([1000, 1000]);
  assert.ok(axis.high > axis.low);
  const escape = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
  const figure = renderTokenTrendFigure([
    { model: '<img onerror=1>', points: [{ at: '2026-10-23T00:00:00Z', tokens: 1000 }, { at: '2026-10-23T01:00:00Z', tokens: 4000 }, { at: '2026-10-23T02:00:00Z', tokens: 2000 }] },
    { model: 'ok', points: [{ at: '2026-10-23T00:00:00Z', tokens: 800 }, { at: '2026-10-23T01:00:00Z', tokens: 1200 }, { at: '2026-10-23T02:00:00Z', tokens: 900 }] },
  ], { width: 640, height: 220, escape, insufficient: new Set(['ok']) });
  assert.equal(figure.markup.match(/class="network-token-line"/g).length, 2);
  assert.match(figure.markup, / C/);
  assert.match(figure.markup, /stroke-dasharray="6 4"/);
  assert.doesNotMatch(figure.markup, /network-token-area|linearGradient/);
  assert.match(figure.legend, /&lt;img onerror=1&gt;/);
  assert.match(figure.markup, /&lt;img onerror=1&gt;/);
  assert.doesNotMatch(figure.legend, /<img/);
  assert.doesNotMatch(figure.markup, /<img onerror/);
  const peak = figure.series[0].coords[1];
  assert.ok(peak.y < figure.series[0].coords[0].y && peak.y < figure.series[0].coords[2].y);
});

test('old agents return network_rates_unsupported for network rates routes', async () => {
  const failure = async (response, route = 'network-rates') => queryAgent(device, route, undefined, { fetcher: async () => response });
  for (const route of ['network-rates', 'network-rates-switches', 'network-rates-settings', 'network-rates-token-trend']) {
    await assert.rejects(failure(new Response('old', { status: 404 }), route), { code: 'network_rates_unsupported' });
  }
});

test('submitNetworkRatesSettings posts JSON auto_switch body', async () => {
  let sent;
  const fetcher = async (url, options) => {
    sent = { url: url.href, ...options };
    return Response.json({ ...ratesBody, auto_switch: true });
  };
  await submitNetworkRatesSettings(device, true, { fetcher });
  assert.equal(sent.url, device.url + '/v1/device/network-rates/settings');
  assert.equal(sent.method, 'POST');
  assert.deepEqual(JSON.parse(sent.body), { auto_switch: true });
});
