export function money(value, digits = 2) {
  if (value === undefined || value === null) return '—';
  let amount = BigInt(value);
  const negative = amount < 0n;
  if (negative) amount = -amount;
  const divisor = 10n ** BigInt(6 - digits);
  const rounded = (amount + divisor / 2n) / divisor;
  const scale = 10n ** BigInt(digits);
  return `${negative && rounded !== 0n ? '−' : ''}$${new Intl.NumberFormat('en-US').format(rounded / scale)}${digits ? '.' + String(rounded % scale).padStart(digits, '0') : ''}`;
}

export const sumMoney = items => items.reduce((sum, item) => sum + BigInt(item.amount_micro_usd), 0n);
export const sumCount = items => items.reduce((sum, item) => sum + item.record_count, 0);

export function dateKey(value, timezone) {
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(new Date(value));
  const get = type => parts.find(part => part.type === type).value;
  return `${get('year')}-${get('month')}-${get('day')}`;
}

export function shiftDate(date, days) {
  const value = new Date(`${date}T12:00:00Z`);
  value.setUTCDate(value.getUTCDate() + days);
  return value.toISOString().slice(0, 10);
}

export function zonedMidnight(date, timezone) {
  const target = Date.parse(`${date}T00:00:00Z`);
  if (!Number.isFinite(target)) throw new Error('无效日期');
  let value = target;
  const formatter = new Intl.DateTimeFormat('en-GB', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' });
  for (let i = 0; i < 4; i++) {
    const parts = Object.fromEntries(formatter.formatToParts(new Date(value)).map(part => [part.type, part.value]));
    const represented = Date.UTC(Number(parts.year), Number(parts.month) - 1, Number(parts.day), Number(parts.hour), Number(parts.minute), Number(parts.second));
    const correction = target - represented;
    if (!correction) return new Date(value);
    value += correction;
  }
  return new Date(value);
}

export function timeRange(period, timezone, now = new Date(), custom = {}) {
  const today = dateKey(now, timezone);
  const start = period === 'custom' ? custom.from : shiftDate(today, -(Number(period) - 1));
  const end = period === 'custom' ? custom.to : today;
  if (!start || !end || start > end || end > today) throw new Error('请选择有效的日期范围，结束日期不能晚于今天。');
  const from = zonedMidnight(start, timezone);
  const to = new Date(Math.min(+now, +zonedMidnight(shiftDate(end, 1), timezone)));
  if (+from >= +to) throw new Error('开始时间需要早于结束时间。');
  if (+to - +from > 366 * 86_400_000) throw new Error('最多查询最近 365 天的明细。');
  return { from: from.toISOString(), to: to.toISOString() };
}

export function csvCell(value) {
  let text = String(value ?? '');
  if (/^[\s]*[=+\-@]/.test(text)) text = "'" + text;
  return '"' + text.replaceAll('"', '""') + '"';
}

export function csvRows(items) {
  const columns = ['记录 ID', '时间 (UTC)', '模型', '收益 (USD)', '输入 Token', '输出 Token', 'Provider ID', '任务 ID'];
  const rows = items.map(item => [item.record_id, item.created_at, item.model, money(item.amount_micro_usd, 6).replace('$', '').replaceAll(',', '').replace('−', '-'), item.prompt_tokens, item.completion_tokens, item.provider_id, item.job_id]);
  return '\uFEFF' + [columns, ...rows].map(row => row.map(csvCell).join(',')).join('\r\n');
}
