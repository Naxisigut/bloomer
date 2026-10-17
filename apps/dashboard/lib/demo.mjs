import { dateKey, zonedMidnight, shiftDate, sumMoney } from '../public/data.js';
import { AppError } from './config.mjs';

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

export function demoQuery(route, params, clock = new Date()) {
  const timezone = params.get('timezone') || 'Asia/Shanghai';
  const from = params.get('from') || new Date(+clock - 365 * 86_400_000).toISOString();
  const to = params.get('to') || clock.toISOString();
  const meta = { last_successful_sync_at: new Date(+clock - 18_000).toISOString(), data_stale: false, retention_start: new Date(+clock - 365 * 86_400_000).toISOString(), effective_from: from, effective_to: to, range_truncated: false, coverage: { status: 'partial', start: records.at(-1)?.created_at, gap_count: 0, gaps: [], gaps_truncated: false } };
  if (route === 'health') return { version: '1.0.1', phase: 'P6', schema_version: 2, collection_state: 'idle', last_successful_sync_at: meta.last_successful_sync_at, data_stale: false, last_error: null, last_attempt_at: meta.last_successful_sync_at, sync_mode: 'normal', recovery_reason: null, retry_not_before: null, retention_start: meta.retention_start, coverage: meta.coverage, cleanup: { state: 'idle', last_success_at: new Date(+clock - 2 * 86_400_000).toISOString(), last_error: null } };
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
    const key = group === 'model' ? record.model : group === 'hour' ? `${dateKey(record.created_at, timezone)} ${new Intl.DateTimeFormat('en-GB', { timeZone: timezone, hour: '2-digit', hourCycle: 'h23' }).format(new Date(record.created_at))}:00` : dateKey(record.created_at, timezone);
    if (!buckets.has(key)) {
      const start = group === 'model' ? null : group === 'hour' ? new Date(Math.floor(Date.parse(record.created_at) / 3_600_000) * 3_600_000) : zonedMidnight(key, timezone);
      const end = start === null ? null : group === 'hour' ? new Date(+start + 3_600_000) : zonedMidnight(shiftDate(key, 1), timezone);
      buckets.set(key, { key, from: start?.toISOString() || null, to: end?.toISOString() || null, record_count: 0, amount_micro_usd: '0' });
    }
    const bucket = buckets.get(key);
    bucket.record_count++;
    bucket.amount_micro_usd = (BigInt(bucket.amount_micro_usd) + BigInt(record.amount_micro_usd)).toString();
  }
  return { group_by: group, timezone, items: [...buckets.values()].sort((a, b) => a.key.localeCompare(b.key)), meta };
}
