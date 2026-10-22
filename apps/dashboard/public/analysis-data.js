import { DASHBOARD_TIMEZONE, dateKey, shiftDate, zonedMidnight } from './data.js';

const HOUR = 3_600_000;
const DAY = 86_400_000;

export const ANALYSIS_PERIODS = ['today', 'yesterday', '7', '30', '90', '365'];

export function analysisPeriodRange(period, timezone = DASHBOARD_TIMEZONE, now = new Date(), custom = {}) {
  const today = dateKey(now, timezone);
  if (period === 'today') return calendarRange(today, today, timezone, now);
  if (period === 'yesterday') {
    const day = shiftDate(today, -1);
    return calendarRange(day, day, timezone, now);
  }
  if (period === 'custom') {
    const start = custom.from, end = custom.to;
    if (!start || !end || start > end || end > today) throw new Error('请选择有效的日期范围，结束日期不能晚于今天。');
    return calendarRange(start, end, timezone, now);
  }
  if (!['7', '30', '90', '365'].includes(period)) throw new Error('无效的时间范围。');
  const start = shiftDate(today, -(Number(period) - 1));
  return calendarRange(start, today, timezone, now);
}

function calendarRange(start, end, timezone, now) {
  const from = zonedMidnight(start, timezone);
  const to = new Date(Math.min(+now, +zonedMidnight(shiftDate(end, 1), timezone)));
  if (+from >= +to) throw new Error('开始时间需要早于结束时间。');
  if (+to - +from > 366 * DAY) throw new Error('最多查询最近 365 天的明细。');
  return { from: from.toISOString(), to: to.toISOString() };
}

export function rangeSpanDays(range, timezone = DASHBOARD_TIMEZONE) {
  const first = dateKey(range.from, timezone);
  const last = dateKey(Date.parse(range.to) - 1, timezone);
  return (Date.parse(`${last}T00:00:00Z`) - Date.parse(`${first}T00:00:00Z`)) / DAY + 1;
}

export function defaultGranularity(range, timezone = DASHBOARD_TIMEZONE) {
  const days = rangeSpanDays(range, timezone);
  if (days <= 2) return 'hour';
  if (days <= 90) return 'day';
  return 'month';
}

export function allowedGranularities(range, timezone = DASHBOARD_TIMEZONE) {
  const days = rangeSpanDays(range, timezone);
  const values = ['day', 'month'];
  if (days <= 31) values.unshift('hour');
  return values;
}

export function buildAnalysisParams({ range, granularity, models = [], timezone = DASHBOARD_TIMEZONE }) {
  const params = new URLSearchParams({ from: range.from, to: range.to, timezone, granularity });
  for (const model of models.slice(0, 8)) params.append('model', model);
  return params;
}

const amountPattern = /^-?\d+$/;

export function validateAnalysisShape(body) {
  if (!body || typeof body !== 'object' || Array.isArray(body)) return false;
  if (typeof body.timezone !== 'string' || !['hour', 'day', 'month'].includes(body.granularity)) return false;
  if (!Number.isFinite(Date.parse(body.from)) || !Number.isFinite(Date.parse(body.to))) return false;
  if (!body.totals || !amountPattern.test(body.totals.amount_micro_usd) || !Number.isSafeInteger(body.totals.record_count) || body.totals.record_count < 0) return false;
  const row = item => item && typeof item.model === 'string' && amountPattern.test(item.amount_micro_usd) && Number.isSafeInteger(item.record_count) && item.record_count >= 0;
  if (!Array.isArray(body.models) || body.models.some(item => !row(item) || !Number.isSafeInteger(item.active_buckets) || item.active_buckets < 0)) return false;
  if (!Array.isArray(body.series) || body.series.some(bucket => !bucket || !Number.isFinite(Date.parse(bucket.from)) || !Number.isFinite(Date.parse(bucket.to)) || !Array.isArray(bucket.items) || bucket.items.some(item => !row(item)))) return false;
  if (!Array.isArray(body.hour_of_day) || body.hour_of_day.some(item => !row(item) || !Number.isInteger(item.hour) || item.hour < 0 || item.hour > 23)) return false;
  const weekdayRow = item => item && amountPattern.test(item.amount_micro_usd) && Number.isSafeInteger(item.record_count) && item.record_count >= 0 && Number.isInteger(item.weekday) && item.weekday >= 1 && item.weekday <= 7 && Number.isInteger(item.hour) && item.hour >= 0 && item.hour <= 23;
  if (!Array.isArray(body.weekday_hour) || body.weekday_hour.some(item => !weekdayRow(item))) return false;
  return true;
}

function bucketStarts(granularity, range, timezone) {
  const from = Date.parse(range.from), to = Date.parse(range.to);
  const starts = [];
  if (granularity === 'hour') {
    let instant = Math.floor(from / HOUR) * HOUR;
    if (instant < from) instant += HOUR;
    for (; instant < to; instant += HOUR) starts.push(instant);
    return starts;
  }
  if (granularity === 'day') {
    let day = dateKey(from, timezone), endDay = dateKey(to - 1, timezone);
    while (day <= endDay) {
      starts.push(+zonedMidnight(day, timezone));
      day = shiftDate(day, 1);
    }
    return starts;
  }
  let month = dateKey(from, timezone).slice(0, 7);
  const endMonth = dateKey(to - 1, timezone).slice(0, 7);
  while (month <= endMonth) {
    starts.push(+zonedMidnight(`${month}-01`, timezone));
    const next = new Date(`${month}-01T12:00:00Z`);
    next.setUTCMonth(next.getUTCMonth() + 1);
    month = next.toISOString().slice(0, 7);
  }
  return starts;
}

function bucketEnd(granularity, startInstant, timezone) {
  if (granularity === 'hour') return startInstant + HOUR;
  if (granularity === 'day') return +zonedMidnight(shiftDate(dateKey(startInstant, timezone), 1), timezone);
  const month = dateKey(startInstant, timezone).slice(0, 7);
  const next = new Date(`${month}-01T12:00:00Z`);
  next.setUTCMonth(next.getUTCMonth() + 1);
  return +zonedMidnight(next.toISOString().slice(0, 10), timezone);
}

function mergeBucketItems(items) {
  const byModel = new Map();
  for (const item of items) {
    const current = byModel.get(item.model) || { model: item.model, amount_micro_usd: 0n, record_count: 0 };
    current.amount_micro_usd += BigInt(item.amount_micro_usd);
    current.record_count += item.record_count;
    byModel.set(item.model, current);
  }
  return [...byModel.values()].map(item => ({ ...item, amount_micro_usd: item.amount_micro_usd.toString() }));
}

export function fillSeriesBuckets(response, now = new Date()) {
  const { granularity, from, to, series } = response;
  const timezone = response.timezone || DASHBOARD_TIMEZONE;
  const range = { from, to };
  const saved = new Map(series.map(bucket => [Date.parse(bucket.from), bucket.items]));
  const nowMs = +now;
  const filled = bucketStarts(granularity, range, timezone).map(start => {
    const end = bucketEnd(granularity, start, timezone);
    const past = end <= nowMs;
    const items = saved.get(start) || (past ? [] : null);
    if (items === null) return null;
    const normalized = mergeBucketItems(items);
    return { from: new Date(start).toISOString(), to: new Date(end).toISOString(), items: normalized, total_micro_usd: normalized.reduce((sum, item) => sum + BigInt(item.amount_micro_usd), 0n).toString(), record_count: normalized.reduce((sum, item) => sum + item.record_count, 0) };
  }).filter(Boolean);
  return filled;
}

export function cumulativeSeries(buckets, models) {
  const totals = new Map(models.map(model => [model, 0n]));
  let combined = 0n;
  return buckets.map(bucket => {
    const point = { from: bucket.from, to: bucket.to, byModel: {}, combined_micro_usd: '0' };
    for (const model of models) {
      const amount = BigInt(bucket.items.find(item => item.model === model)?.amount_micro_usd || '0');
      totals.set(model, totals.get(model) + amount);
      point.byModel[model] = totals.get(model).toString();
    }
    for (const item of bucket.items) combined += BigInt(item.amount_micro_usd);
    point.combined_micro_usd = combined.toString();
    return point;
  });
}

export function deriveMetrics(response, filledBuckets, now = new Date()) {
  const totalAmount = BigInt(response.totals.amount_micro_usd);
  const recordCount = response.totals.record_count;
  const spanDays = Math.max(1, rangeSpanDays({ from: response.from, to: response.to }, response.timezone));
  const avgPerRecord = recordCount ? totalAmount / BigInt(recordCount) : 0n;
  const dailyAvg = totalAmount / BigInt(spanDays);
  const activeBuckets = filledBuckets.filter(bucket => bucket.record_count > 0);
  const activeHours = response.granularity === 'hour' ? activeBuckets.length : activeBuckets.length * (response.granularity === 'day' ? 24 : 24 * 30);
  const perActiveHour = activeHours ? totalAmount / BigInt(activeHours) : 0n;
  let peak = null;
  for (const bucket of filledBuckets) {
    const amount = BigInt(bucket.total_micro_usd);
    if (!peak || amount > peak.amount) peak = { from: bucket.from, to: bucket.to, amount_micro_usd: bucket.total_micro_usd };
  }
  const hourTotals = aggregateHourOfDay(response.hour_of_day, spanDays);
  let peakHour = null;
  for (const [hour, amount] of hourTotals.entries()) {
    if (!peakHour || amount > peakHour.amount) peakHour = { hour, amount_micro_usd: amount.toString(), daily_avg_micro_usd: (amount / BigInt(spanDays)).toString() };
  }
  return {
    totalAmount: totalAmount.toString(),
    recordCount,
    avgPerRecord: avgPerRecord.toString(),
    dailyAvg: dailyAvg.toString(),
    perActiveHour: perActiveHour.toString(),
    peakBucket: peak,
    peakHour,
    spanDays,
    now: now.toISOString(),
  };
}

export function aggregateHourOfDay(hourOfDay, spanDays) {
  const totals = new Map();
  for (const item of hourOfDay) {
    totals.set(item.hour, (totals.get(item.hour) || 0n) + BigInt(item.amount_micro_usd));
  }
  return totals;
}

export function hourOfDayDailyAverage(hourOfDay, spanDays, models) {
  const byHourModel = new Map();
  for (const item of hourOfDay) {
    if (models?.length && !models.includes(item.model)) continue;
    const key = `${item.hour}\0${item.model}`;
    byHourModel.set(key, (byHourModel.get(key) || 0n) + BigInt(item.amount_micro_usd));
  }
  const days = BigInt(Math.max(1, spanDays));
  const rows = [];
  for (const [key, amount] of byHourModel) {
    const [hour, model] = key.split('\0');
    rows.push({ hour: Number(hour), model, amount_micro_usd: amount.toString(), daily_avg_micro_usd: (amount / days).toString() });
  }
  return rows.sort((a, b) => a.hour - b.hour || a.model.localeCompare(b.model));
}

export function modelShareRows(models, totalAmount) {
  const total = BigInt(totalAmount);
  return models.map(item => ({
    model: item.model,
    amount_micro_usd: item.amount_micro_usd,
    record_count: item.record_count,
    share: total > 0n ? Number((BigInt(item.amount_micro_usd) * 10000n) / total) / 100 : 0,
    avgPerRecord: item.record_count ? (BigInt(item.amount_micro_usd) / BigInt(item.record_count)).toString() : '0',
  }));
}

export function deriveAnalysisView(response, { now = new Date(), selectedModels = [] } = {}) {
  const filledBuckets = fillSeriesBuckets(response, now);
  const modelOrder = response.models.map(item => item.model);
  const metrics = deriveMetrics(response, filledBuckets, now);
  return {
    filledBuckets,
    cumulative: cumulativeSeries(filledBuckets, modelOrder),
    metrics,
    modelShares: modelShareRows(response.models, response.totals.amount_micro_usd),
    hourDaily: hourOfDayDailyAverage(response.hour_of_day, metrics.spanDays, selectedModels.length ? selectedModels : null),
    selectedModels,
    modelOrder,
  };
}

export function microToChartNumber(value) {
  return Number(BigInt(value)) / 1e6;
}
