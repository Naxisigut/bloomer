import test from 'node:test';
import assert from 'node:assert/strict';
import { queryAgent } from '../public/agent-client.js';
import { buildAnalysisParams, analysisPeriodRange } from '../public/analysis-data.js';
import { MAX_MODELS } from '../public/palette.js';

const device = { url: 'https://mac.tail123.ts.net:8443', token: 'synthetic-test-token' };

test('analysis route allows repeated model params and maps unsupported agents', async () => {
  let url;
  const fetcher = async (target, options) => {
    url = target.href;
    return Response.json({
      timezone: 'Asia/Shanghai', granularity: 'day', from: '2026-10-01T00:00:00.000Z', to: '2026-10-08T00:00:00.000Z',
      totals: { amount_micro_usd: '1', record_count: 1 },
      models: [{ model: 'm', amount_micro_usd: '1', record_count: 1, active_buckets: 1 }],
      series: [{ from: '2026-10-01T00:00:00.000Z', to: '2026-10-02T00:00:00.000Z', items: [{ model: 'm', amount_micro_usd: '1', record_count: 1 }] }],
      hour_of_day: [{ hour: 10, model: 'm', amount_micro_usd: '1', record_count: 1 }],
      weekday_hour: [{ weekday: 1, hour: 10, amount_micro_usd: '1', record_count: 1 }],
    });
  };
  const range = analysisPeriodRange('7', 'Asia/Shanghai', new Date('2026-10-22T08:00:00Z'));
  const params = buildAnalysisParams({ range, granularity: 'day', models: ['a', 'b'] });
  await queryAgent(device, 'analysis', params, { fetcher });
  assert.match(url, /model=a&model=b/);
  await assert.rejects(queryAgent(device, 'analysis', params, { fetcher: async () => new Response('old', { status: 404 }) }), { code: 'analysis_unsupported' });
  await assert.rejects(queryAgent(device, 'analysis', params, { fetcher: async () => Response.json({ code: 'device_identity_unavailable' }, { status: 503 }) }), { code: 'device_identity_unavailable' });
});

test('model filter limit follows the palette size', async () => {
  const range = analysisPeriodRange('7', 'Asia/Shanghai', new Date('2026-10-22T08:00:00Z'));
  const fetcher = async () => Response.json({
    timezone: 'Asia/Shanghai', granularity: 'day', from: '2026-10-01T00:00:00.000Z', to: '2026-10-08T00:00:00.000Z',
    totals: { amount_micro_usd: '1', record_count: 1 }, models: [],
    series: [], hour_of_day: [], weekday_hour: [],
  });
  const full = Array.from({ length: MAX_MODELS }, (_, index) => `m${index}`);
  await queryAgent(device, 'analysis', buildAnalysisParams({ range, granularity: 'day', models: full }), { fetcher });
  const tooMany = new URLSearchParams({ from: range.from, to: range.to, timezone: 'Asia/Shanghai', granularity: 'day' });
  for (const model of [...full, 'extra']) tooMany.append('model', model);
  await assert.rejects(queryAgent(device, 'analysis', tooMany, { fetcher }), { code: 'invalid_request' });
  // buildAnalysisParams must not silently drop models while the palette can still draw them.
  assert.equal(buildAnalysisParams({ range, granularity: 'day', models: full }).getAll('model').length, MAX_MODELS);
});
