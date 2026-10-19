import { dateKey, zonedMidnight, shiftDate, sumMoney } from '../public/data.js';
import { AppError } from './config.mjs';
import { deviceFields } from '../public/device-state.js';

const now = new Date();
const models = ['Qwen3-32B', 'DeepSeek-R1-Distill-Qwen-32B', 'Qwen3-8B', 'Llama-3.3-70B-Instruct'];
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

const demoHardware = { device_id: 'demo-device', hardware: { os: 'darwin', architecture: 'arm64', chip: 'Apple M4 Max', model: '演示 Mac', logical_cpus: 16, memory_total_bytes: 64 * 2 ** 30 } };
function stateSample(id) {
  const quality = Object.fromEntries(deviceFields.map(([key]) => [key, 'ok']));
  for (const key of ['cpu_temperature_c', 'gpu_temperature_c']) quality[key] = 'unsupported';
  quality.dispatch_tier = 'unsupported';
  quality.provider_connected = 'unsupported';
  const ready = id % 90 > 3;
  const state = id % 90 === 0 ? 'offline' : id % 90 === 1 ? 'blocked' : id % 90 < 4 ? 'degraded' : 'routable';
  if (state === 'offline') quality.is_idle = 'unavailable';
  return {
    id, device_id: 'demo-device', collected_at: new Date(id * 60_000).toISOString(), duration_ms: 1100,
    cpu_percent: Math.round(14 + Math.abs(Math.sin(id / 7)) * 32), provider_cpu_percent: Math.round(7 + Math.abs(Math.cos(id / 5)) * 90),
    memory_total_bytes: 64 * 2 ** 30, memory_available_bytes: Math.round((18 + Math.abs(Math.sin(id / 13)) * 9) * 2 ** 30),
    memory_pressure: id % 90 < 4 ? 'warning' : 'normal', swap_used_bytes: 256 * 2 ** 20, gpu_percent: ready ? Math.round(28 + Math.abs(Math.sin(id / 5)) * 58) : 0,
    cpu_temperature_c: null, gpu_temperature_c: null, thermal_state: id % 90 < 4 ? 'fair' : 'nominal', disk_available_bytes: 320 * 2 ** 30,
    provider_running: state !== 'offline', provider_connected: state !== 'offline', current_model: 'Qwen3-32B', model_ready: ready, dispatch_tier: null,
    inference_active: id % 3 === 0, request_work_pending: id % 3 === 0, is_idle: state === 'offline' ? null : id % 3 === 2,
    last_device_earning_at: new Date((id - (id % 3 === 2 ? 12 : 2)) * 60_000).toISOString(),
    last_earnings_sync_at: new Date(id * 60_000 - 10_000).toISOString(), earnings_provider_id: 'demo-provider', activity_error: state === 'offline' ? 'provider_not_running' : null,
    provider_pid: 1234, provider_version: '0.9.22 · 演示', provider_state_at: new Date(id * 60_000 - 2000).toISOString(), quality,
  };
}

function demoState(route, params, clock) {
  const allowed = route === 'device-history' ? ['from', 'to', 'limit', 'before_id'] : [];
  for (const key of params.keys()) if (!allowed.includes(key) || params.getAll(key).length !== 1) throw new AppError(400, 'invalid_request', '演示状态查询参数无效。');
  const cutoff = +clock - 7 * 86_400_000;
  const first = params.has('from') ? Date.parse(params.get('from')) : +clock - 86_400_000;
  const last = params.has('to') ? Date.parse(params.get('to')) : +clock;
  const limit = params.has('limit') ? Number(params.get('limit')) : 100;
  const before = params.has('before_id') ? Number(params.get('before_id')) : Infinity;
  if (!Number.isFinite(first) || !Number.isFinite(last) || first >= last || !Number.isInteger(limit) || limit < 1 || limit > 500 || (params.has('before_id') && (!Number.isSafeInteger(before) || before <= 0))) throw new AppError(400, 'invalid_request', '演示状态查询条件无效。');
  const meta = { sample_interval_seconds: 60, retention_start: new Date(cutoff).toISOString(), effective_from: new Date(Math.max(first, cutoff)).toISOString(), effective_to: new Date(last).toISOString(), range_truncated: first < cutoff };
  if (route === 'device-state') return { device: demoHardware, sample: stateSample(Math.floor(+clock / 60_000)), meta: { ...meta, data_stale: false } };
  const lowest = Math.ceil(Math.max(cutoff, first) / 60_000);
  const highest = Math.min(Math.floor(+clock / 60_000), Math.ceil(last / 60_000) - 1, before - 1);
  const items = [];
  for (let id = highest; id >= lowest && items.length < limit; id--) items.push(stateSample(id));
  return { device: demoHardware, items, next_before_id: items.length && items.at(-1).id > lowest ? items.at(-1).id : null, meta };
}

export function demoQuery(route, params, clock = new Date()) {
  if (route === 'device-state' || route === 'device-history') return demoState(route, params, clock);
  const timezone = params.get('timezone') || 'Asia/Shanghai';
  const from = params.get('from') || new Date(+clock - 365 * 86_400_000).toISOString();
  const to = params.get('to') || clock.toISOString();
  const meta = { last_successful_sync_at: new Date(+clock - 18_000).toISOString(), data_stale: false, retention_start: new Date(+clock - 365 * 86_400_000).toISOString(), effective_from: from, effective_to: to, range_truncated: false, coverage: { status: 'partial', start: records.at(-1)?.created_at, gap_count: 0, gaps: [], gaps_truncated: false } };
  if (route === 'health') return { version: '1.0.4', phase: 'P6', schema_version: 5, collection_state: 'idle', last_successful_sync_at: meta.last_successful_sync_at, data_stale: false, last_error: null, last_attempt_at: meta.last_successful_sync_at, sync_mode: 'normal', recovery_reason: null, retry_not_before: null, retention_start: meta.retention_start, coverage: meta.coverage, cleanup: { state: 'idle', last_success_at: new Date(+clock - 2 * 86_400_000).toISOString(), last_error: null } };
  if (route === 'overview') return { account_id: 'demo-account', official: { record_count: 18204, total_micro_usd: '1846728300', available_balance_micro_usd: '286423800', withdrawable_balance_micro_usd: '265100000', observed_at: meta.last_successful_sync_at }, local_last_365_days: { record_count: records.length, amount_micro_usd: sumMoney(records).toString() }, meta };
  let selected = records.filter(item => item.created_at >= from && item.created_at < to && (!params.get('model') || item.model === params.get('model')));
  if (route === 'earnings') {
    let offset = 0;
    const cursor = params.get('cursor');
    if (cursor) {
      try {
        const page = JSON.parse(Buffer.from(cursor, 'base64url').toString());
        if (page.from !== from || page.to !== to || page.model !== (params.get('model') || '') || !Number.isInteger(page.offset) || page.offset < 0) throw new Error();
        offset = page.offset;
      } catch { throw new AppError(400, 'invalid_request', '演示翻页参数无效，请刷新明细。'); }
    }
    const limit = Math.min(500, Math.max(1, Number(params.get('limit') || 10)));
    const next = offset + limit < selected.length ? Buffer.from(JSON.stringify({ from, to, model: params.get('model') || '', offset: offset + limit })).toString('base64url') : null;
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
