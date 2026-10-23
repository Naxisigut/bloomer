import test from 'node:test';
import assert from 'node:assert/strict';
import { queryAgent, submitNetworkRatesSettings } from '../public/agent-client.js';
import {
  blockedReasonLabel,
  formatRecommendationTopbar,
  formatUsdPerHourMicro,
  renderCandidatesTable,
  renderSwitchRecords,
  validateNetworkRatesSettings,
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

test('topbar recommendation renders model and hourly rate or em dash when empty', () => {
  assert.equal(formatRecommendationTopbar({ rates: ratesBody, unsupported: false, error: null }), '—');
  assert.equal(formatRecommendationTopbar({ rates: null, unsupported: true, error: null }), '—');
  assert.equal(formatRecommendationTopbar({ rates: null, unsupported: false, error: '失败' }), '—');
  const withRec = { ...ratesBody, recommendation: { model: 'DeepSeek', usd_per_hour_micro: '142500', fallback: false } };
  assert.equal(formatRecommendationTopbar({ rates: withRec, unsupported: false, error: null }), 'DeepSeek · $0.14/h');
  const fallback = { ...withRec, recommendation: { model: 'gemma-4-26b-qat-4bit', usd_per_hour_micro: '8500', fallback: true } };
  assert.equal(formatRecommendationTopbar({ rates: fallback, unsupported: false, error: null }), 'gemma-4-26b-qat-4bit（兜底） · $0.0085/h');
  const prefixed = { ...withRec, recommendation: { model: 'EigenLabs/Qwen3.8-27B-4bit-mtpt', usd_per_hour_micro: '142500', fallback: false } };
  assert.equal(formatRecommendationTopbar({ rates: prefixed, unsupported: false, error: null }), 'Qwen3.8-27B-4bit-mtpt · $0.14/h');
});

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

test('old agents return network_rates_unsupported for network rates routes', async () => {
  const failure = async (response, route = 'network-rates') => queryAgent(device, route, undefined, { fetcher: async () => response });
  for (const route of ['network-rates', 'network-rates-switches', 'network-rates-settings']) {
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
