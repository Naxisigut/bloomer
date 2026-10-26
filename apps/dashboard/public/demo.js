import { dateKey, zonedMidnight, shiftDate, sumMoney } from './data.js';
import { AppError } from './errors.js';
import { sampleFieldKeys } from './device-state.js';

const now = new Date();
const models = ['Qwen3-32B', 'DeepSeek-R1-Distill-Qwen-32B', 'Qwen3-8B', 'EigenLabs/Qwen3.8-27B-4bit-mtpt'];
let seed = 427;
const random = () => { seed = (seed * 16807) % 2147483647; return seed / 2147483647; };
const records = [];
for (let day = 89; day >= 0; day--) {
  const count = 20 + Math.floor(random() * 52);
  for (let index = 0; index < count; index++) {
    const created = new Date(+zonedMidnight(shiftDate(dateKey(now, 'Asia/Shanghai'), -day), 'Asia/Shanghai') + (index + random()) / count * 86_400_000);
    if (created > now) continue;
    const modelIndex = random() < 0.48 ? 0 : random() < 0.50 ? 1 : random() < 0.66 ? 2 : 3;
    records.push({ account_id: 'demo-account', record_id: `demo-${day}-${index}`, provider_id: 'demo-provider', provider_key: 'demo-provider', job_id: `task-${Math.floor(random() * 10000000)}`, model: models[modelIndex], amount_micro_usd: String(Math.floor((9000 + random() * 195000) * (modelIndex === 2 ? 0.3 : 1))), prompt_tokens: 120 + Math.floor(random() * 16000), completion_tokens: 60 + Math.floor(random() * 4000), created_at: created.toISOString() });
  }
}
records.sort((a, b) => b.created_at.localeCompare(a.created_at) || b.record_id.localeCompare(a.record_id));

const encodeCursor = value => btoa(String.fromCharCode(...new TextEncoder().encode(JSON.stringify(value)))).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
const decodeCursor = value => JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(atob(value.replace(/-/g, '+').replace(/_/g, '/')), character => character.charCodeAt(0))));

const demoHardware = { device_id: 'demo-device', hardware: { os: 'darwin', architecture: 'arm64', chip: 'Apple M4 Max', model: '演示 Mac', logical_cpus: 16, memory_total_bytes: 64 * 2 ** 30 } };
let demoNetworkRatesAutoSwitch = false;

function demoNetworkRates(clock) {
  const stamp = clock.toISOString();
  return {
    enabled: true,
    auto_switch: demoNetworkRatesAutoSwitch,
    auto_switch_since: demoNetworkRatesAutoSwitch ? new Date(+clock - 45 * 60_000).toISOString() : null,
    window_samples: 6,
    samples: 6,
    updated_at: stamp,
    snapshot_at: stamp,
    current_model: 'Qwen3-32B',
    recommendation: { model: 'DeepSeek-R1-Distill-Qwen-32B', usd_per_hour_micro: '142500', fallback: false },
    candidates: [
      { model: 'DeepSeek-R1-Distill-Qwen-32B', downloaded: true, avg_providers: 38.5, requests_per_hour: 118.2, tokens_per_hour: 365000, usd_per_hour_micro: '142500', price_source: 'official', insufficient: false },
      { model: 'Qwen3-32B', downloaded: true, avg_providers: 41.0, requests_per_hour: 96.4, tokens_per_hour: 310000, usd_per_hour_micro: '118200', price_source: 'official', insufficient: false },
      { model: 'Qwen3-8B', downloaded: false, avg_providers: 22.0, requests_per_hour: 140.0, tokens_per_hour: 420000, usd_per_hour_micro: '98000', price_source: 'official', insufficient: true },
      { model: 'gemma-4-26b-qat-4bit', downloaded: true, avg_providers: 12.0, requests_per_hour: 8.0, tokens_per_hour: 24000, usd_per_hour_micro: '8500', price_source: 'fallback', insufficient: false },
    ],
    switch: {
      next_eligible_at: new Date(+clock + 12 * 60_000).toISOString(),
      blocked_reason: demoNetworkRatesAutoSwitch ? 'already_optimal' : 'auto_switch_off',
      backoff_until: null,
    },
    last_error: null,
    interval_minutes: 5,
  };
}

function demoTokenTrend(clock) {
  const step = 5 * 60_000;
  const start = Math.floor((+clock - 24 * 3_600_000) / step) * step;
  const specs = [
    { model: 'DeepSeek-R1-Distill-Qwen-32B', tokens: (hours, recent) => 300000 + 18000 * Math.sin(hours / 3.2) + recent * 18000 },
    { model: 'Qwen3-32B', tokens: (hours, recent) => 330000 + 14000 * Math.sin(hours / 2.7 + 1.2) - recent * 22000 },
    { model: 'Qwen3-8B', tokens: hours => 410000 + 16000 * Math.sin(hours / 2.1 + 0.4) },
    { model: 'gemma-4-26b-qat-4bit', tokens: hours => 24000 + 2800 * Math.sin(hours / 1.8 + 2.1) },
  ];
  return {
    series: specs.map(spec => {
      const points = [];
      for (let at = start; at <= +clock; at += step) {
        const hours = (at - start) / 3_600_000;
        const recent = Math.max(0, hours - 20);
        points.push({ at: new Date(at).toISOString(), tokens: Math.max(0, Math.round(spec.tokens(hours, recent))) });
      }
      return { model: spec.model, points };
    }),
  };
}

function demoNetworkRatesSwitches(clock) {
  return {
    switches: [{
      id: 1,
      at: new Date(+clock - 3 * 3600_000).toISOString(),
      from_model: 'Qwen3-32B',
      to_model: 'DeepSeek-R1-Distill-Qwen-32B',
      reason: 'better_model',
      status: 'succeeded',
      error: null,
      job_id: 'demo-switch-1',
      summary: [
        { model: 'DeepSeek-R1-Distill-Qwen-32B', usd_per_hour_micro: '139000', avg_providers: 36.2 },
        { model: 'Qwen3-32B', usd_per_hour_micro: '110000', avg_providers: 39.8 },
      ],
    }],
  };
}
function stateSample(id) {
  const quality = Object.fromEntries(sampleFieldKeys().map(key => [key, 'ok']));
  quality.dispatch_tier = 'unsupported';
  quality.provider_connected = 'unsupported';
  const ready = id % 90 > 3;
  const state = id % 90 === 0 ? 'offline' : id % 90 === 1 ? 'blocked' : id % 90 < 4 ? 'degraded' : 'routable';
  const taskQuality = state === 'offline' ? 'not_running' : 'ok';
  quality.inference_active = taskQuality;
  quality.request_work_pending = taskQuality;
  if (state === 'offline') quality.is_idle = 'unavailable';
  return {
    id, device_id: 'demo-device', collected_at: new Date(id * 60_000).toISOString(), duration_ms: 1100,
    cpu_percent: Math.round(14 + Math.abs(Math.sin(id / 7)) * 32), provider_cpu_percent: Math.round(7 + Math.abs(Math.cos(id / 5)) * 90),
    memory_total_bytes: 64 * 2 ** 30, memory_available_bytes: Math.round((18 + Math.abs(Math.sin(id / 13)) * 9) * 2 ** 30),
    memory_pressure: id % 90 < 4 ? 'warning' : 'normal', swap_used_bytes: 256 * 2 ** 20, gpu_percent: ready ? Math.round(28 + Math.abs(Math.sin(id / 5)) * 58) : 0,
    thermal_state: id % 90 < 4 ? 'fair' : 'nominal', disk_available_bytes: 320 * 2 ** 30,
    provider_running: state !== 'offline', provider_connected: state !== 'offline', current_model: 'Qwen3-32B', model_ready: ready, dispatch_tier: null,
    inference_active: id % 3 === 0, request_work_pending: id % 3 === 0, is_idle: state === 'offline' ? null : id % 3 === 2,
    last_device_earning_at: new Date((id - (id % 3 === 2 ? 12 : 2)) * 60_000).toISOString(),
    last_earnings_sync_at: new Date(id * 60_000 - 10_000).toISOString(), earnings_provider_id: 'demo-provider', activity_error: state === 'offline' ? 'provider_not_running' : null,
    provider_pid: 1234, provider_version: '0.9.22 · 演示', provider_state_at: new Date(id * 60_000 - 2000).toISOString(), quality,
  };
}

function demoState(clock) {
  return { device: demoHardware, sample: stateSample(Math.floor(+clock / 60_000)), meta: { sample_interval_seconds: 60, data_stale: false, collection_enabled: true } };
}

function demoAnalysis(params, clock) {
  const allowed = ['from', 'to', 'timezone', 'granularity', 'model'];
  for (const key of params.keys()) if (!allowed.includes(key) || (key !== 'model' && params.getAll(key).length !== 1) || (key === 'model' && params.getAll(key).length > 8)) throw new AppError(400, 'invalid_request', '演示分析参数无效。');
  const timezone = params.get('timezone') || 'Asia/Shanghai';
  const from = params.get('from'), to = params.get('to'), granularity = params.get('granularity');
  if (!from || !to || !granularity || !['hour', 'day', 'month'].includes(granularity)) throw new AppError(400, 'invalid_request', '演示分析条件无效。');
  const modelsFilter = params.getAll('model');
  const inRange = records.filter(item => item.created_at >= from && item.created_at < to);
  const modelStats = new Map();
  for (const record of inRange) {
    const current = modelStats.get(record.model) || { model: record.model, amount_micro_usd: 0n, record_count: 0, buckets: new Set() };
    current.amount_micro_usd += BigInt(record.amount_micro_usd);
    current.record_count++;
    modelStats.set(record.model, current);
  }
  let filtered = inRange;
  if (modelsFilter.length) filtered = inRange.filter(item => modelsFilter.includes(item.model));
  const bucketKey = record => {
    if (granularity === 'hour') return new Date(Math.floor(Date.parse(record.created_at) / 3_600_000) * 3_600_000).toISOString();
    if (granularity === 'day') return zonedMidnight(dateKey(record.created_at, timezone), timezone).toISOString();
    const month = dateKey(record.created_at, timezone).slice(0, 7);
    return zonedMidnight(`${month}-01`, timezone).toISOString();
  };
  const bucketEnd = startIso => {
    const start = Date.parse(startIso);
    if (granularity === 'hour') return new Date(start + 3_600_000).toISOString();
    if (granularity === 'day') return zonedMidnight(shiftDate(dateKey(start, timezone), 1), timezone).toISOString();
    const month = dateKey(start, timezone).slice(0, 7);
    const next = new Date(`${month}-01T12:00:00Z`);
    next.setUTCMonth(next.getUTCMonth() + 1);
    return zonedMidnight(next.toISOString().slice(0, 10), timezone).toISOString();
  };
  for (const record of inRange) modelStats.get(record.model).buckets.add(bucketKey(record));
  const models = [...modelStats.values()].map(item => ({ model: item.model, amount_micro_usd: item.amount_micro_usd.toString(), record_count: item.record_count, active_buckets: item.buckets.size })).sort((a, b) => BigInt(b.amount_micro_usd) > BigInt(a.amount_micro_usd) ? 1 : -1);
  const seriesMap = new Map();
  for (const record of filtered) {
    const key = bucketKey(record);
    if (!seriesMap.has(key)) seriesMap.set(key, { from: key, to: bucketEnd(key), items: new Map() });
    const bucket = seriesMap.get(key);
    const row = bucket.items.get(record.model) || { model: record.model, amount_micro_usd: 0n, record_count: 0 };
    row.amount_micro_usd += BigInt(record.amount_micro_usd);
    row.record_count++;
    bucket.items.set(record.model, row);
  }
  const series = [...seriesMap.values()].sort((a, b) => a.from.localeCompare(b.from)).map(bucket => ({ from: bucket.from, to: bucket.to, items: [...bucket.items.values()].map(item => ({ ...item, amount_micro_usd: item.amount_micro_usd.toString() })) }));
  const hourMap = new Map(), weekdayMap = new Map();
  const weekdayFormatter = new Intl.DateTimeFormat('en-US', { timeZone: timezone, weekday: 'short' });
  for (const record of filtered) {
    const hour = Number(new Intl.DateTimeFormat('en-GB', { timeZone: timezone, hour: '2-digit', hourCycle: 'h23' }).format(new Date(record.created_at)));
    const weekday = ({ Mon: 1, Tue: 2, Wed: 3, Thu: 4, Fri: 5, Sat: 6, Sun: 7 })[weekdayFormatter.format(new Date(record.created_at))];
    const hourKey = `${hour}\0${record.model}`, weekKey = `${weekday}\0${hour}`;
    const hourRow = hourMap.get(hourKey) || { hour, model: record.model, amount_micro_usd: 0n, record_count: 0 };
    hourRow.amount_micro_usd += BigInt(record.amount_micro_usd);
    hourRow.record_count++;
    hourMap.set(hourKey, hourRow);
    const weekRow = weekdayMap.get(weekKey) || { weekday, hour, amount_micro_usd: 0n, record_count: 0 };
    weekRow.amount_micro_usd += BigInt(record.amount_micro_usd);
    weekRow.record_count++;
    weekdayMap.set(weekKey, weekRow);
  }
  const totalsAmount = filtered.reduce((sum, item) => sum + BigInt(item.amount_micro_usd), 0n);
  const meta = { last_successful_sync_at: new Date(+clock - 18_000).toISOString(), data_stale: false, retention_start: new Date(+clock - 365 * 86_400_000).toISOString(), effective_from: from, effective_to: to, range_truncated: false, coverage: { status: 'partial', start: records.at(-1)?.created_at, gap_count: 0, gaps: [], gaps_truncated: false } };
  return {
    timezone, granularity, from, to,
    totals: { amount_micro_usd: totalsAmount.toString(), record_count: filtered.length },
    models,
    series,
    hour_of_day: [...hourMap.values()].map(item => ({ ...item, amount_micro_usd: item.amount_micro_usd.toString() })),
    weekday_hour: [...weekdayMap.values()].map(item => ({ ...item, amount_micro_usd: item.amount_micro_usd.toString() })),
    meta,
  };
}

export function demoQuery(route, params = new URLSearchParams(), clock = new Date()) {
  if (route === 'alert-test') throw new AppError(403, 'demo_read_only', '演示设备不发送测试推送。');
  if (route === 'alerts') return { items: [{ id: 1, type: 'coordinator_disconnected', event: 'recovered', level: 'active', device_name: '演示 Mac', title: '演示 Mac · 协调器连接已恢复', body: '设备：演示 Mac\n告警：协调器连接中断\n已恢复，持续 4 小时 21 分钟。', created_at: clock.toISOString(), first_triggered_at: new Date(+clock - 261 * 60_000).toISOString(), last_push_at: clock.toISOString(), recovered: true, duration_seconds: 261 * 60, delivery_status: 'sent', attempts: 1, last_attempt_at: clock.toISOString(), next_attempt_at: null }] };

  if (route.startsWith('control') || route === 'network-rates-settings') throw new AppError(403, 'demo_read_only', '演示设备不执行远程操作。');
  if (route === 'long-task-restart') return { enabled: false };
  if (!['health', 'overview', 'earnings', 'stats', 'analysis', 'device-state', 'network-rates', 'network-rates-switches', 'network-rates-token-trend'].includes(route)) throw new AppError(404, 'not_found', '接口不存在。');
  if (route === 'device-state') return demoState(clock);
  if (route === 'network-rates') return demoNetworkRates(clock);
  if (route === 'network-rates-switches') return demoNetworkRatesSwitches(clock);
  if (route === 'network-rates-token-trend') return demoTokenTrend(clock);
  if (route === 'analysis') return demoAnalysis(params, clock);
  const timezone = params.get('timezone') || 'Asia/Shanghai';
  const from = params.get('from') || new Date(+clock - 365 * 86_400_000).toISOString();
  const to = params.get('to') || clock.toISOString();
  const meta = { last_successful_sync_at: new Date(+clock - 18_000).toISOString(), data_stale: false, retention_start: new Date(+clock - 365 * 86_400_000).toISOString(), effective_from: from, effective_to: to, range_truncated: false, coverage: { status: 'partial', start: records.at(-1)?.created_at, gap_count: 0, gaps: [], gaps_truncated: false } };
  if (route === 'health') return { version: '1.1.0', phase: 'P6', schema_version: 7, collection_state: 'idle', server_time: clock.toISOString(), next_collection_at: new Date(+clock + 45_000).toISOString(), last_successful_sync_at: meta.last_successful_sync_at, data_stale: false, last_error: null, last_attempt_at: meta.last_successful_sync_at, sync_mode: 'normal', recovery_reason: null, retry_not_before: null, retention_start: meta.retention_start, coverage: meta.coverage, cleanup: { state: 'idle', last_success_at: new Date(+clock - 2 * 86_400_000).toISOString(), last_error: null } };
  if (route === 'overview') return { account_id: 'demo-account', official: { record_count: 18204, total_micro_usd: '1846728300', available_balance_micro_usd: '286423800', withdrawable_balance_micro_usd: '265100000', observed_at: meta.last_successful_sync_at }, local_last_365_days: { record_count: records.length, amount_micro_usd: sumMoney(records).toString() }, meta };
  let selected = records.filter(item => item.created_at >= from && item.created_at < to && (!params.get('model') || item.model === params.get('model')));
  if (route === 'earnings') {
    let offset = 0;
    const cursor = params.get('cursor');
    if (cursor) {
      try {
        const page = decodeCursor(cursor);
        if (page.from !== from || page.to !== to || page.model !== (params.get('model') || '') || !Number.isInteger(page.offset) || page.offset < 0) throw new Error();
        offset = page.offset;
      } catch { throw new AppError(400, 'invalid_request', '演示翻页参数无效，请刷新明细。'); }
    }
    const limit = Math.min(500, Math.max(1, Number(params.get('limit') || 10)));
    const next = offset + limit < selected.length ? encodeCursor({ from, to, model: params.get('model') || '', offset: offset + limit }) : null;
    return { items: selected.slice(offset, offset + limit), next_cursor: next, meta };
  }
  const group = params.get('group_by') || 'day';
  const buckets = new Map();
  for (const record of selected) {
    const key = group === 'model' ? record.model : group === 'hour' ? `${dateKey(record.created_at, timezone)} ${new Intl.DateTimeFormat('en-GB', { timeZone: timezone, hour: '2-digit', hourCycle: 'h23' }).format(new Date(record.created_at))}:00` : group === 'month' ? dateKey(record.created_at, timezone).slice(0, 7) : dateKey(record.created_at, timezone);
    if (!buckets.has(key)) {
      const start = group === 'model' ? null : group === 'hour' ? new Date(Math.floor(Date.parse(record.created_at) / 3_600_000) * 3_600_000) : zonedMidnight(group === 'month' ? key + '-01' : key, timezone);
      let end = start === null ? null : group === 'hour' ? new Date(+start + 3_600_000) : group === 'day' ? zonedMidnight(shiftDate(key, 1), timezone) : null;
      if (group === 'month') {
        const next = new Date(`${key}-01T12:00:00Z`);
        next.setUTCMonth(next.getUTCMonth() + 1);
        end = zonedMidnight(next.toISOString().slice(0, 10), timezone);
      }
      buckets.set(key, { key, from: start?.toISOString() || null, to: end?.toISOString() || null, record_count: 0, amount_micro_usd: '0' });
    }
    const bucket = buckets.get(key);
    bucket.record_count++;
    bucket.amount_micro_usd = (BigInt(bucket.amount_micro_usd) + BigInt(record.amount_micro_usd)).toString();
  }
  return { group_by: group, timezone, items: [...buckets.values()].sort((a, b) => a.key.localeCompare(b.key)), meta };
}
