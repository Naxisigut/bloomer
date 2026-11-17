import { DASHBOARD_TIMEZONE } from './data.js';
import { smoothLinePath } from './home-revenue.js';
import { displayModelName, displayModelNames } from './models.js';

export function priceSourceLabel(source) {
  if (source === 'fallback') return '兜底价';
  if (source === 'official') return '官方价';
  return source;
}

export function formatUsdPerHourMicro(microStr) {
  if (microStr === undefined || microStr === null) return '—';
  let amount = BigInt(microStr);
  const negative = amount < 0n;
  if (negative) amount = -amount;
  const asNumber = Number(amount) / 1e6;
  if (!Number.isFinite(asNumber)) return '—';
  if (asNumber === 0 || Math.abs(asNumber) >= 0.01) {
    return `${negative && asNumber !== 0 ? '−' : ''}$${asNumber.toFixed(2)}/h`;
  }
  const trimmed = asNumber.toFixed(6).replace(/\.?0+$/, '');
  return `${negative ? '−' : ''}$${trimmed}/h`;
}

export const NETWORK_RATES_REFRESH_MS = 2 * 60_000;

export function formatRefreshCountdown(remainingMs) {
  const totalSeconds = Math.max(0, Math.ceil(remainingMs / 1000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${String(seconds).padStart(2, '0')}`;
}

// A refresh already running for this device is the refresh. A different device starts its own.
export function networkRefreshPlan(inflightDeviceID, deviceID) {
  if (inflightDeviceID != null && inflightDeviceID === deviceID) return 'join';
  return 'start';
}

const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const micro = value => typeof value === 'string' && /^-?\d+$/.test(value);

const absent = value => value === undefined || value === null;
const optionalShare = value => absent(value) || (Number.isFinite(value) && value >= 0 && value <= 1);
const optionalMicro = value => absent(value) || micro(value);

function validateWindow(window) {
  return absent(window) || (object(window) && Number.isInteger(window.samples) && Number.isFinite(window.avg_providers)
    && optionalShare(window.busy_ratio) && Number.isFinite(window.requests_per_hour) && Number.isFinite(window.tokens_per_hour)
    && micro(window.usd_per_hour_micro) && optionalMicro(window.trimmed_usd_per_hour_micro));
}

function validateCandidate(candidate) {
  return object(candidate) && typeof candidate.model === 'string' && typeof candidate.downloaded === 'boolean'
    && Number.isFinite(candidate.avg_providers) && Number.isFinite(candidate.requests_per_hour)
    && Number.isFinite(candidate.tokens_per_hour) && micro(candidate.usd_per_hour_micro)
    && ['official', 'fallback'].includes(candidate.price_source) && typeof candidate.insufficient === 'boolean'
    && optionalShare(candidate.busy_ratio) && optionalMicro(candidate.trimmed_usd_per_hour_micro) && validateWindow(candidate.hour)
    && optionalMicro(candidate.effective_price_micro) && (absent(candidate.input_ratio) || (Number.isFinite(candidate.input_ratio) && candidate.input_ratio >= 0))
    && (absent(candidate.ratio_source) || ['own', 'network', 'none'].includes(candidate.ratio_source))
    && (absent(candidate.cohort) || ['tier', 'all'].includes(candidate.cohort))
    && (absent(candidate.tier_avg_providers) || Number.isFinite(candidate.tier_avg_providers))
    && (absent(candidate.all_avg_providers) || Number.isFinite(candidate.all_avg_providers))
    && validateWindow(candidate.all) && validateWindow(candidate.tier);
}

export function validateNetworkRatesResponse(body) {
  if (object(body) && (!(absent(body.long_window_minutes) || Number.isInteger(body.long_window_minutes))
    || !(absent(body.switch_advantage_ratio) || Number.isFinite(body.switch_advantage_ratio))
    || !(absent(body.chip_tiers) || (Array.isArray(body.chip_tiers) && body.chip_tiers.every(tier => typeof tier === 'string')))
    || !(absent(body.memory_gb) || Number.isInteger(body.memory_gb))
    || !(absent(body.min_providers) || Number.isInteger(body.min_providers)))) {
    throw new Error('invalid');
  }
  if (!object(body) || typeof body.enabled !== 'boolean' || typeof body.auto_switch !== 'boolean'
    || !(body.auto_switch_since === null || Number.isFinite(Date.parse(body.auto_switch_since)))
    || !Number.isInteger(body.window_samples) || !Number.isInteger(body.samples)
    || !(body.updated_at === null || Number.isFinite(Date.parse(body.updated_at)))
    || !(body.snapshot_at === null || Number.isFinite(Date.parse(body.snapshot_at)))
    || !(body.current_model === null || typeof body.current_model === 'string')
    || !object(body.switch)
    || !(body.switch.next_eligible_at === null || Number.isFinite(Date.parse(body.switch.next_eligible_at)))
    || !(body.switch.blocked_reason === null || typeof body.switch.blocked_reason === 'string')
    || !(body.switch.backoff_until === null || Number.isFinite(Date.parse(body.switch.backoff_until)))
    || !(body.last_error === null || typeof body.last_error === 'string')
    || !Array.isArray(body.candidates) || body.candidates.some(candidate => !validateCandidate(candidate))) {
    throw new Error('invalid');
  }
  if (body.recommendation !== null) {
    if (!object(body.recommendation) || typeof body.recommendation.model !== 'string' || !micro(body.recommendation.usd_per_hour_micro) || typeof body.recommendation.fallback !== 'boolean') throw new Error('invalid');
  }
  return body;
}

function candidateRowClass(candidate) {
  if (!candidate.downloaded) return 'network-rates-muted';
  return '';
}

// One action per row, chosen from the candidate's own state. A model that is
// already running cannot be switched to again, and an undownloaded model can
// only be downloaded — the row never offers both.
export function modelAction(candidate, { currentModel = null, busy = false, demo = false } = {}) {
  const blocked = demo ? '演示设备不执行远程操作。' : busy ? '正在执行另一项操作，请等待结束后再试。' : '';
  if (candidate.model === currentModel) return { kind: 'current', label: '当前模型', disabled: true, hint: '已是当前运行模型。' };
  if (!candidate.downloaded) return { kind: 'download', label: '下载该模型', disabled: Boolean(blocked), hint: blocked || '下载完成后可切换，不会自动切换。' };
  return { kind: 'switch', label: '切换到该模型', disabled: Boolean(blocked), hint: blocked || '将替换整套托管模型。' };
}

export function formatUsdPerMillionMicro(microStr) {
  if (absent(microStr)) return '—';
  const amount = Number(BigInt(microStr)) / 1e6;
  if (!Number.isFinite(amount)) return '—';
  return `$${amount.toFixed(amount >= 1 ? 2 : 3)}/M`;
}

export function formatShare(value) {
  return absent(value) ? '—' : `${Math.round(value * 100)}%`;
}

export function relativeToCurrent(usdMicro, currentMicro) {
  if (absent(usdMicro) || absent(currentMicro)) return null;
  const current = Number(currentMicro);
  if (!(current > 0)) return null;
  return Number(usdMicro) / current - 1;
}

function formatRelative(ratio) {
  const percent = Math.round(ratio * 100);
  return `${percent > 0 ? '+' : percent < 0 ? '−' : '±'}${Math.abs(percent)}%`;
}

function windowLabel(minutes) {
  if (!(minutes > 0)) return '';
  return minutes % 60 === 0 ? `近 ${minutes / 60} 小时` : `近 ${minutes} 分钟`;
}

export function candidateTableOptions(rates, scope) {
  const chipTiers = Array.isArray(rates?.chip_tiers) ? rates.chip_tiers : [];
  const options = {
    currentModel: rates?.current_model ?? null,
    advantageRatio: Number.isFinite(rates?.switch_advantage_ratio) ? rates.switch_advantage_ratio : 0.2,
    shortLabel: windowLabel((rates?.window_samples || 0) * (rates?.interval_minutes || 0)) || '近 10 分钟',
    minProviders: Number.isInteger(rates?.min_providers) ? rates.min_providers : 3,
  };
  if (scope === 'all' || scope === 'tier') {
    const resolved = resolveNetworkScope(scope, rates);
    options.scope = resolved;
    options.scopeLabel = resolved === 'tier' ? (chipTiers.length ? `同档 ${chipTiers.join('/')}` : '同档') : '全组';
  }
  return options;
}

export const NETWORK_SCOPE_STORAGE_KEY = 'bloomer.networkScope';

export function storedNetworkScope() {
  try {
    const value = localStorage.getItem(NETWORK_SCOPE_STORAGE_KEY);
    return value === 'all' || value === 'tier' ? value : 'tier';
  } catch {
    return 'tier';
  }
}

export function resolveNetworkScope(preferred, rates) {
  if (Array.isArray(rates?.chip_tiers) && rates.chip_tiers.length === 0) return 'all';
  return preferred === 'all' ? 'all' : 'tier';
}

export function networkScopeToggleAvailable(rates, trend) {
  if (!Array.isArray(rates?.chip_tiers) || !rates.chip_tiers.length) return false;
  if ((rates.candidates || []).some(candidate => object(candidate.all) || object(candidate.tier))) return true;
  return Boolean(trend && (Array.isArray(trend.all) || Array.isArray(trend.tier)));
}

export function candidateForScope(candidate, scope, minProviders = 3) {
  if (!candidate) return candidate;
  const window = scope === 'tier' ? candidate.tier : candidate.all;
  if (!window) return candidate;
  const avg = window.avg_providers;
  return {
    ...candidate,
    avg_providers: avg,
    busy_ratio: window.busy_ratio,
    requests_per_hour: window.requests_per_hour,
    tokens_per_hour: window.tokens_per_hour,
    usd_per_hour_micro: window.usd_per_hour_micro,
    trimmed_usd_per_hour_micro: window.trimmed_usd_per_hour_micro,
    insufficient: Number.isFinite(avg) ? avg < minProviders : candidate.insufficient,
    cohort: scope,
  };
}

export function sortCandidatesByUsd(candidates) {
  return [...candidates || []].sort((left, right) => {
    const usdLeft = Number(left?.usd_per_hour_micro);
    const usdRight = Number(right?.usd_per_hour_micro);
    const a = Number.isFinite(usdLeft) ? usdLeft : -Infinity;
    const b = Number.isFinite(usdRight) ? usdRight : -Infinity;
    if (a !== b) return b - a;
    return String(left?.model || '').localeCompare(String(right?.model || ''));
  });
}

function shortWindow(candidate) {
  return {
    busy_ratio: candidate.busy_ratio, requests_per_hour: candidate.requests_per_hour, tokens_per_hour: candidate.tokens_per_hour,
    usd_per_hour_micro: candidate.usd_per_hour_micro, trimmed_usd_per_hour_micro: candidate.trimmed_usd_per_hour_micro,
  };
}

const tokenRate = new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 });
const requestRate = new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 });

function windowCells(window, current, isCurrent, advantageRatio, escape) {
  if (!window) return '<td>—</td><td>—</td><td>—</td><td>—</td>';
  const perRequest = window.requests_per_hour > 0 ? `，平均每单 ${tokenRate.format(window.tokens_per_hour / window.requests_per_hour)} token` : '';
  const tokenTitle = `每台每小时接单 ${requestRate.format(window.requests_per_hour)}${perRequest}`;
  let relative = '';
  if (!isCurrent) {
    const ratio = relativeToCurrent(window.usd_per_hour_micro, current?.usd_per_hour_micro);
    if (ratio !== null) {
      const tone = ratio >= advantageRatio ? ' network-rates-gain' : ratio < 0 ? ' network-rates-loss' : '';
      relative = `<small class="network-rates-sub${tone}" title="相对当前模型">${escape(formatRelative(ratio))}</small>`;
    }
  }
  return `<td>${escape(formatShare(window.busy_ratio))}</td>`
    + `<td title="${escape(tokenTitle)}">${escape(tokenRate.format(window.tokens_per_hour))}</td>`
    + `<td>${escape(formatUsdPerHourMicro(window.usd_per_hour_micro))}${relative}</td>`
    + `<td>${escape(formatUsdPerHourMicro(window.trimmed_usd_per_hour_micro))}</td>`;
}

function formatCount(value) {
  return Number.isFinite(value) ? String(Math.round(value * 10) / 10) : '—';
}

function priceNote(candidate) {
  const ratio = Number.isFinite(candidate.input_ratio) ? candidate.input_ratio.toFixed(1) : '';
  const parts = [];
  if (candidate.ratio_source === 'own') parts.push(`本机输入比 ${ratio}`);
  else if (candidate.ratio_source === 'network') parts.push(`全网输入比 ${ratio}`);
  else if (candidate.ratio_source === 'none') parts.push('仅输出价');
  if (candidate.price_source === 'fallback') parts.push(priceSourceLabel('fallback'));
  return parts.join(' · ');
}

export function renderCandidatesTable(candidates, escape, options = {}) {
  if (!candidates?.length) return '<p class="control-note">暂无候选模型数据</p>';
  const { currentModel = null, advantageRatio = 0.2, shortLabel = '近 10 分钟', actionFor = null, scope = null, minProviders = 3 } = options;
  const scoped = scope === 'all' || scope === 'tier'
    ? sortCandidatesByUsd(candidates.map(candidate => candidateForScope(candidate, scope, minProviders)))
    : candidates;
  const labels = displayModelNames(scoped.map(candidate => candidate.model));
  const current = scoped.find(candidate => candidate.model === currentModel);
  const rows = scoped.map(candidate => {
    const muted = candidateRowClass(candidate);
    const isCurrent = candidate.model === currentModel;
    const tags = (isCurrent ? ' <span class="network-rates-tag">当前</span>' : '') + (candidate.downloaded ? '' : ' <span class="network-rates-tag">未下载</span>');
    const note = priceNote(candidate);
    const allProviders = Number.isFinite(candidate.all_avg_providers) ? candidate.all_avg_providers : candidate.avg_providers;
    const providers = `${formatCount(candidate.tier_avg_providers)}/${formatCount(allProviders)}`;
    const action = actionFor?.(candidate) || null;
    const actionCell = action
      ? `<td class="network-rates-action"><button type="button" class="button button-white network-rates-action-button" data-network-action="${escape(action.kind)}" data-model="${escape(candidate.model)}"${action.disabled ? ` disabled title="${escape(action.hint)}"` : ` title="${escape(action.hint)}"`}>${escape(action.label)}</button></td>`
      : '';
    return `<tr${muted ? ` class="${muted}"` : ''}>`
      + `<td title="${escape(candidate.model)}">${escape(labels.get(candidate.model))}${tags}</td>`
      + `<td title="${escape(`同档 ${formatCount(candidate.tier_avg_providers)} 台 · 全组 ${formatCount(allProviders)} 台`)}">${escape(providers)}</td>`
      + `<td title="输出价 + 输入比 × 输入价（每百万输出 token）">${escape(formatUsdPerMillionMicro(candidate.effective_price_micro))}${note ? `<small class="network-rates-sub">${escape(note)}</small>` : ''}</td>`
      + windowCells(shortWindow(candidate), current && shortWindow(current), isCurrent, advantageRatio, escape)
      + actionCell
      + '</tr>';
  }).join('');
  // The action column is pinned to the right edge so it stays reachable while the
  // metric columns scroll horizontally on narrow screens.
  const actionHead = actionFor ? '<th class="network-rates-action">操作</th>' : '';
  return `<div class="table-scroll network-rates-table-wrap"><table>`
    + `<thead><tr><th>模型</th><th>同档数/全组数</th><th>综合单价</th><th>有单率</th><th>每台输出 token/h</th><th>估算 $/h</th><th title="每轮去掉输出最多的一台后再平均">去头 $/h</th>${actionHead}</tr></thead>`
    + `<tbody>${rows}</tbody></table></div>`
    + `<p class="control-note network-rates-table-note">${options.scopeLabel ? `以上为${escape(shortLabel)}（${escape(options.scopeLabel)}）的数据。` : `以上为${escape(shortLabel)}的数据。`}</p>`;
}

const TOKEN_COLORS = ['#5b8def', '#f2994a', '#8b7cf6', '#2bb3a3', '#e5677e', '#9ab843', '#c08a4f', '#d4a72c'];
export const TOKEN_TREND_WINDOW_MS = 24 * 3_600_000;

export function tokenTrendSeriesEnd(series) {
  let end = 0;
  for (const item of series || []) {
    for (const point of item?.points || []) {
      const instant = Date.parse(point.at);
      if (Number.isFinite(instant) && instant > end) end = instant;
    }
  }
  return end;
}

export function tokenTrendWindow(series) {
  const end = tokenTrendSeriesEnd(series);
  if (!end) return null;
  return { start: end - TOKEN_TREND_WINDOW_MS, end };
}

export function tokenTrendRecentRange(window, hours = 2) {
  if (!window) return null;
  return clampTokenTrendRange({ start: window.end - hours * 3_600_000, end: window.end }, window);
}

export function clampTokenTrendRange(range, window) {
  let start = range?.start;
  let end = range?.end;
  if (!Number.isFinite(start) || !Number.isFinite(end)) return { start: window.start, end: window.end };
  if (start > end) [start, end] = [end, start];
  start = Math.min(Math.max(start, window.start), window.end);
  end = Math.min(Math.max(end, window.start), window.end);
  if (start > end) start = end;
  return { start, end };
}

export function tokenTrendRangeCoversWindow(range, window) {
  return Boolean(range && window && range.start <= window.start && range.end >= window.end);
}

export function filterTokenTrendByRange(series, range) {
  if (!series?.length) return series;
  const window = tokenTrendWindow(series);
  if (!window) return series;
  const clamped = clampTokenTrendRange(range || window, window);
  if (tokenTrendRangeCoversWindow(clamped, window)) return series;
  return series.map(item => ({
    model: item.model,
    points: (item.points || []).filter(point => {
      const instant = Date.parse(point.at);
      return Number.isFinite(instant) && instant >= clamped.start && instant <= clamped.end;
    }),
  })).filter(item => item.points.length);
}
const tokenCompact = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 });
const tokenExact = new Intl.NumberFormat('zh-CN');
const tokenClock = new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
const tokenAxisClock = new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
const tokenInputClock = new Intl.DateTimeFormat('en-CA', { timeZone: DASHBOARD_TIMEZONE, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });

export function formatTokenTrendInput(instant) {
  const parts = Object.fromEntries(tokenInputClock.formatToParts(new Date(instant)).map(part => [part.type, part.value]));
  return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`;
}

export function parseTokenTrendInput(value) {
  if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) return NaN;
  return Date.parse(`${value}:00+08:00`);
}

export function tokenTrendRangeFromInputs(fromValue, toValue, window) {
  const parsedFrom = parseTokenTrendInput(fromValue);
  const parsedTo = parseTokenTrendInput(toValue);
  if (!Number.isFinite(parsedFrom) || !Number.isFinite(parsedTo) || !window) return null;
  const startValue = parsedFrom <= parsedTo ? fromValue : toValue;
  const endValue = parsedFrom <= parsedTo ? toValue : fromValue;
  let start = Math.min(parsedFrom, parsedTo);
  let end = Math.max(parsedFrom, parsedTo);
  if (startValue === formatTokenTrendInput(window.start)) start = window.start;
  if (endValue === formatTokenTrendInput(window.end)) end = window.end;
  else end += 60_000 - 1;
  return clampTokenTrendRange({ start, end }, window);
}

export function tokenTrendRangeMatches(range, target) {
  if (!range || !target) return false;
  return formatTokenTrendInput(range.start) === formatTokenTrendInput(target.start)
    && formatTokenTrendInput(range.end) === formatTokenTrendInput(target.end);
}

export function tokenTrendRangeLabel(range, window) {
  if (window && range && tokenTrendRangeCoversWindow(range, window)) return '近 24 小时';
  if (!range || !window || tokenTrendRangeMatches(range, tokenTrendRecentRange(window))) return '前 2 小时';
  return `${tokenClock.format(new Date(range.start))} – ${tokenClock.format(new Date(range.end))}`;
}

function validateTokenTrendSeries(series) {
  if (!object(series) || typeof series.model !== 'string' || !Array.isArray(series.points) || series.points.length > 2000) throw new Error('invalid');
  for (const point of series.points) {
    if (!object(point) || !Number.isFinite(Date.parse(point.at)) || !Number.isSafeInteger(point.tokens) || point.tokens < 0) throw new Error('invalid');
    if (Object.hasOwn(point, 'total') && (!Number.isSafeInteger(point.total) || point.total < 0)) throw new Error('invalid');
  }
}

function validateTokenTrendGroup(group) {
  if (absent(group)) return;
  if (!Array.isArray(group) || group.length > 256) throw new Error('invalid');
  for (const series of group) validateTokenTrendSeries(series);
}

export function validateNetworkRatesTokenTrendResponse(body) {
  if (!object(body) || Object.hasOwn(body, 'points') || !Array.isArray(body.series) || body.series.length > 256) throw new Error('invalid');
  for (const series of body.series) validateTokenTrendSeries(series);
  validateTokenTrendGroup(body.all);
  validateTokenTrendGroup(body.tier);
  return body;
}

export function tokenTrendFromPayload(payload, scope) {
  if (Array.isArray(payload)) return payload;
  if (scope === 'tier' && Array.isArray(payload?.tier)) return payload.tier;
  if (scope === 'all' && Array.isArray(payload?.all)) return payload.all;
  return payload?.series || [];
}

export function tokenTrendWindowFromPayload(payload) {
  if (Array.isArray(payload)) return tokenTrendWindow(payload);
  const end = Math.max(tokenTrendSeriesEnd(payload?.series), tokenTrendSeriesEnd(payload?.all), tokenTrendSeriesEnd(payload?.tier));
  if (!end) return null;
  return { start: end - TOKEN_TREND_WINDOW_MS, end };
}

export function tokenTrendModels(payload) {
  const groups = Array.isArray(payload) ? [payload] : [payload?.series, payload?.all, payload?.tier];
  const models = [];
  for (const group of groups) {
    for (const item of group || []) {
      if (item?.model) models.push(item.model);
    }
  }
  return models;
}

export function tokenTrendSeriesForMetric(series, metric = 'tokens') {
  if (metric !== 'total') return series || [];
  return (series || []).map(item => ({
    model: item.model,
    points: (item.points || []).filter(point => Number.isSafeInteger(point.total) && point.total >= 0).map(point => ({ at: point.at, tokens: point.total })),
  })).filter(item => item.points.length);
}

export function tokenTrendAxis(values) {
  let low = 0, high = 0;
  for (const value of values) {
    if (value < low) low = value;
    if (value > high) high = value;
  }
  const span = Math.max(1, high - low);
  const pad = Math.max(1, Math.round(span * 0.12));
  low = Math.max(0, low - (low === 0 ? 0 : pad));
  high += pad;
  if (low === high) high = low + 1;
  const rough = Math.max(1, Math.ceil((high - low) / 4));
  const magnitude = 10 ** Math.floor(Math.log10(rough));
  const step = [1, 2, 5, 10].map(factor => factor * magnitude).find(value => value >= rough);
  const first = Math.ceil(low / step) * step;
  const last = Math.floor((high + step * 0.001) / step) * step;
  const ticks = [];
  for (let value = first; value <= last; value += step) ticks.push(value);
  if (ticks.length < 2) return { low, high, ticks: [low, high] };
  return { low: Math.min(low, ticks[0]), high: Math.max(high, ticks.at(-1)), ticks };
}

export function tokenTrendColorFor(model, models) {
  const index = models.indexOf(model);
  return TOKEN_COLORS[(index >= 0 ? index : 0) % TOKEN_COLORS.length];
}

export function renderTokenTrendModelChoices(models, selected, { escape, insufficient = new Set(), colorFor }) {
  const labels = displayModelNames(models);
  const color = typeof colorFor === 'function' ? colorFor : model => tokenTrendColorFor(model, models);
  return models.map(model => {
    const dashed = insufficient.has(model);
    return `<label class="home-model-choice${dashed ? ' is-insufficient' : ''}" style="--series-color:${color(model)}" title="${escape(model)}"><input type="checkbox" data-network-token-model value="${escape(model)}" ${selected.has(model) ? 'checked' : ''}><i aria-hidden="true"></i><span>${escape(labels.get(model))}</span></label>`;
  }).join('');
}

export function renderTokenTrendFigure(series, { width, height, escape, insufficient = new Set(), colorFor, periodLabel = '所选时段', metricLabel = '各模型每台每小时输出 token' }) {
  const lines = (series || []).map((item, index) => {
    const points = (item?.points || [])
      .map(point => ({ instant: Date.parse(point.at), tokens: point.tokens }))
      .filter(point => Number.isFinite(point.instant) && Number.isSafeInteger(point.tokens) && point.tokens >= 0)
      .sort((a, b) => a.instant - b.instant);
    const model = typeof item?.model === 'string' ? item.model : '';
    return {
      model,
      color: typeof colorFor === 'function' ? colorFor(model) : TOKEN_COLORS[index % TOKEN_COLORS.length],
      dashed: insufficient.has(model),
      points,
    };
  }).filter(line => line.points.length);
  if (!lines.length) return null;
  const axis = tokenTrendAxis(lines.flatMap(line => line.points.map(point => point.tokens)));
  const left = Math.max(48, ...axis.ticks.map(tick => tokenCompact.format(tick).length * 7 + 14));
  const right = 16, top = 16, bottom = 28;
  const plotWidth = Math.max(1, width - left - right), plotHeight = Math.max(1, height - top - bottom);
  const y = value => top + (axis.high - value) / (axis.high - axis.low) * plotHeight;
  const instants = [...new Set(lines.flatMap(line => line.points.map(point => point.instant)))].sort((a, b) => a - b);
  const start = instants[0], end = instants.at(-1), span = Math.max(1, end - start);
  const axisTime = tokenAxisClock;
  const x = instant => instants.length === 1 ? left + plotWidth / 2 : left + (instant - start) / span * plotWidth;
  const round = value => Math.round(value * 10) / 10;
  const drawn = lines.map(line => ({
    ...line,
    coords: line.points.map(point => ({
      x: round(x(point.instant)), y: round(y(point.tokens)), tokens: point.tokens, instant: point.instant,
    })),
  }));
  let svg = '';
  for (const tick of axis.ticks) {
    const yPos = round(y(tick));
    svg += `<line class="chart-grid" x1="${left}" x2="${width - right}" y1="${yPos}" y2="${yPos}"/><text class="chart-label" x="${left - 8}" y="${yPos + 3}" text-anchor="end">${escape(tokenCompact.format(tick))}</text>`;
  }
  const labelCount = plotWidth < 420 ? 4 : 6;
  const seen = new Set();
  for (let index = 0; index < labelCount; index++) {
    const instant = instants[Math.round(index * (instants.length - 1) / Math.max(1, labelCount - 1))];
    if (instant == null || seen.has(instant)) continue;
    seen.add(instant);
    svg += `<text class="chart-label" x="${round(x(instant))}" y="${height - 8}" text-anchor="middle">${escape(axisTime.format(new Date(instant)))}</text>`;
  }
  for (const line of drawn) {
    const path = line.coords.length > 1 ? smoothLinePath(line.coords) : '';
    const dash = line.dashed ? ' stroke-dasharray="6 4"' : '';
    if (path) svg += `<path class="network-token-line" stroke="${line.color}"${dash} d="${path}"><title>${escape(line.model)}</title></path>`;
    if (line.coords.length === 1) svg += `<circle fill="${line.color}" cx="${line.coords[0].x}" cy="${line.coords[0].y}" r="3.5"><title>${escape(line.model)}</title></circle>`;
  }
  const values = drawn.flatMap(line => line.coords.map(point => point.tokens));
  const low = Math.min(...values), peak = Math.max(...values);
  return {
    series: drawn,
    instants: instants.map(instant => ({ instant, x: round(x(instant)), timeLabel: tokenClock.format(new Date(instant)) })),
    markup: `<svg viewBox="0 0 ${width} ${height}" role="img" aria-label="${escape(periodLabel)}${escape(metricLabel)}，最低 ${escape(tokenExact.format(low))}，最高 ${escape(tokenExact.format(peak))}">${svg}</svg><div class="chart-crosshair" hidden></div><div class="chart-tooltip home-revenue-tooltip" hidden></div>${drawn.map((_, index) => `<div class="network-token-dot" data-token-dot="${index}" hidden></div>`).join('')}`,
  };
}

export function createNetworkRatesUI({ query, selectedDevice, escape, switcher = null, contextMenu = null }) {
  const $ = selector => document.querySelector(selector);
  let deviceID, rates = null, trend = null, trendError = '', loading = false, error = '', unsupported = false, generation = 0;
  let tokenModels = [], tokenSelected = new Set(), tokenRange = null, networkScope = storedNetworkScope();
  const chart = $('#network-token-chart');
  const totalChart = $('#network-token-total-chart');
  const tokenChoices = $('#network-token-models');
  const tokenFrom = $('#network-token-from');
  const tokenTo = $('#network-token-to');
  const tokenColorFor = model => tokenTrendColorFor(model, tokenModels);
  const activeScope = () => resolveNetworkScope(networkScope, rates);
  const scopedCandidates = () => (rates?.candidates || []).map(candidate => candidateForScope(candidate, activeScope(), Number.isInteger(rates?.min_providers) ? rates.min_providers : 3));
  const tokenInsufficient = () => new Set(scopedCandidates().filter(candidate => candidate.insufficient).map(candidate => candidate.model));
  const visibleTrend = () => tokenTrendFromPayload(trend, activeScope());

  function syncTokenModels(models) {
    const fromTrend = [...new Set(models || [])];
    tokenModels = [...new Set([...fromTrend, ...tokenSelected])].sort();
    if (!tokenSelected.size && fromTrend.length) fromTrend.forEach(model => tokenSelected.add(model));
  }

  function renderScopeToggle() {
    const group = $('#network-scope');
    if (!group) return;
    const available = !unsupported && networkScopeToggleAvailable(rates, trend);
    group.hidden = !available;
    const selected = activeScope();
    const tiers = Array.isArray(rates?.chip_tiers) ? rates.chip_tiers.join('/') : '';
    group.querySelectorAll('[data-network-scope]').forEach(button => {
      const on = button.dataset.networkScope === selected;
      button.classList.toggle('selected', on);
      button.setAttribute('aria-pressed', String(on));
      button.title = button.dataset.networkScope === 'tier'
        ? (tiers ? `所有模型使用同档 ${tiers} 数据` : '所有模型使用同档数据')
        : '所有模型使用全组数据';
    });
  }

  function activeTokenWindow() {
    return tokenTrendWindowFromPayload(trend);
  }

  function activeTokenRange() {
    const window = activeTokenWindow();
    if (!window) return null;
    if (!tokenRange) return tokenTrendRecentRange(window);
    return clampTokenTrendRange(tokenRange, window);
  }

  function renderTokenPeriodControls() {
    const window = activeTokenWindow();
    const range = activeTokenRange();
    const disabled = !window || loading;
    if (!tokenFrom || !tokenTo) return;
    tokenFrom.disabled = tokenTo.disabled = disabled;
    if (!window || !range) {
      tokenFrom.value = '';
      tokenTo.value = '';
      tokenFrom.removeAttribute('min');
      tokenFrom.removeAttribute('max');
      tokenTo.removeAttribute('min');
      tokenTo.removeAttribute('max');
      return;
    }
    const min = formatTokenTrendInput(window.start);
    const max = formatTokenTrendInput(window.end);
    tokenFrom.min = tokenTo.min = min;
    tokenFrom.max = tokenTo.max = max;
    tokenFrom.value = formatTokenTrendInput(range.start);
    tokenTo.value = formatTokenTrendInput(range.end);
  }

  function renderTokenModelChoices() {
    if (!tokenChoices) return;
    if (unsupported || !tokenModels.length) {
      tokenChoices.hidden = true;
      tokenChoices.innerHTML = '';
      return;
    }
    tokenChoices.hidden = false;
    tokenChoices.innerHTML = renderTokenTrendModelChoices(tokenModels, tokenSelected, { escape, insufficient: tokenInsufficient(), colorFor: tokenColorFor });
  }

  function render() {
    const device = selectedDevice();
    if (device?.id !== deviceID) {
      deviceID = device?.id; rates = null; trend = null; trendError = ''; loading = false; error = ''; unsupported = false; generation++;
      tokenModels = []; tokenSelected = new Set(); tokenRange = null;
      // A switch submitted for the previous device must not block the new one.
      switcher?.setDevice(deviceID);
    }
    renderTokenPeriodControls();
    renderScopeToggle();
    const demo = deviceID === 'demo';
    $('#network-rates-panel').setAttribute('aria-busy', String(loading));
    $('#network-rates-refresh').disabled = loading;
    const message = $('#network-rates-message');
    if (unsupported) {
      message.hidden = false; message.className = 'state-message warning';
      message.textContent = '设备 Agent 版本过旧，不支持此功能。请升级设备端 Agent 后重试。';
    } else {
      message.hidden = !error;
      message.className = `state-message${error ? ' warning' : ''}`;
      message.textContent = error;
    }
    $('#network-rates-candidates').innerHTML = unsupported ? '' : rates ? renderCandidatesTable(rates.candidates, escape, {
      ...candidateTableOptions(rates, activeScope()),
      actionFor: candidate => modelAction(candidate, { currentModel: rates.current_model, busy: switcher?.busy, demo }),
    }) : '';
    document.querySelectorAll('.network-token-card').forEach(card => { card.hidden = unsupported; });
    $('#network-token-period').hidden = unsupported;
    const tokenModelsFieldset = $('.network-token-models');
    if (tokenModelsFieldset) tokenModelsFieldset.hidden = unsupported;
    $('#network-chart-unsupported').hidden = !unsupported;
    renderTokenModelChoices();
    renderTrendCharts();
  }

  // Only the buttons depend on whether a submit is in flight; rewriting the whole
  // table would drop focus and scroll position.
  function syncActionButtons() {
    if (!rates) return;
    const demo = deviceID === 'demo';
    document.querySelectorAll('#network-rates-candidates [data-network-action]').forEach(button => {
      const candidate = rates.candidates.find(item => item.model === button.dataset.model) || { model: button.dataset.model, downloaded: true };
      const action = modelAction(candidate, { currentModel: rates.current_model, busy: switcher?.busy, demo });
      button.dataset.networkAction = action.kind;
      button.disabled = action.disabled;
      button.title = action.hint;
      button.textContent = action.label;
    });
  }

  // Both entry points submit the same single-model control job, so the row button
  // and the chip menu never disagree about what a click does.
  function openModelAction(kind, model) {
    if (!switcher || !model) return;
    if (kind === 'download') switcher.download(model);
    else switcher.switchTo(model);
  }

  // The chip menu only ever offers a switch, so its item keeps the promised name.
  // An undownloaded or already-running model keeps the item visible but disabled
  // with the reason, rather than turning into a download from a chart.
  function chipActionFor(model) {
    const candidate = (rates?.candidates || []).find(item => item.model === model);
    const blocked = deviceID === 'demo' ? '演示设备不执行远程操作。'
      : switcher?.busy ? '正在执行另一项操作，请等待结束后再试。'
        : candidate && !candidate.downloaded ? '该模型尚未下载，请先在「模型估算」下载。'
          : '';
    const current = model === rates?.current_model;
    return {
      kind: 'switch',
      label: '切换到该模型',
      disabled: Boolean(blocked || current),
      hint: current ? '已是当前运行模型。' : blocked || '将替换整套托管模型。',
    };
  }

  function paintTokenChart(el, { metric, metricLabel, sampleNoun, loadingText }) {
    if (!el || unsupported || el.clientWidth < 40) return;
    const series = tokenTrendSeriesForMetric(visibleTrend(), metric);
    const empty = !series?.length
      ? `近 24 小时暂无各模型${sampleNoun}样本`
      : tokenRange ? `所选时段暂无各模型${sampleNoun}样本` : `前 2 小时暂无各模型${sampleNoun}样本`;
    const noneSelected = '勾选模型以查看对应折线。';
    if (trendError) {
      el.innerHTML = `<div class="empty-state">${escape(trendError)}</div>`;
      return;
    }
    if (!trend) {
      el.innerHTML = `<div class="empty-state">${loading ? loadingText : ''}</div>`;
      return;
    }
    if (!series.length) {
      el.innerHTML = `<div class="empty-state">${empty}</div>`;
      return;
    }
    if (!tokenSelected.size) {
      el.innerHTML = `<div class="empty-state">${noneSelected}</div>`;
      return;
    }
    const window = activeTokenWindow();
    const range = activeTokenRange();
    const windowed = filterTokenTrendByRange(series, range);
    const visible = windowed.filter(item => tokenSelected.has(item.model));
    const width = Math.max(560, el.clientWidth);
    const height = el.clientHeight || 320;
    const figure = renderTokenTrendFigure(visible, {
      width, height, escape, insufficient: tokenInsufficient(), colorFor: tokenColorFor,
      periodLabel: tokenTrendRangeLabel(range, window),
      metricLabel,
    });
    if (!figure) {
      el.innerHTML = `<div class="empty-state">${empty}</div>`;
      return;
    }
    el.innerHTML = figure.markup;
    const tooltip = el.querySelector('.chart-tooltip');
    const crosshair = el.querySelector('.chart-crosshair');
    const svg = el.querySelector('svg');
    const show = event => {
      const matrix = svg.getScreenCTM();
      if (!matrix) return;
      const pointer = svg.createSVGPoint();
      pointer.x = event.clientX;
      pointer.y = event.clientY;
      const local = pointer.matrixTransform(matrix.inverse());
      let nearest = figure.instants[0], best = Infinity;
      for (const item of figure.instants) {
        const distance = Math.abs(item.x - local.x);
        if (distance < best) { best = distance; nearest = item; }
      }
      const rows = figure.series.map((line, index) => ({ line, index, point: line.coords.find(coord => coord.instant === nearest.instant) }));
      tooltip.innerHTML = `<span class="tooltip-time">${escape(nearest.timeLabel)}</span>` + rows.map(row => `<span class="tooltip-row"><i style="background:${row.line.color}"></i>${escape(displayModelName(row.line.model))}<strong>${escape(row.point ? tokenExact.format(row.point.tokens) : '—')}</strong></span>`).join('');
      tooltip.hidden = false;
      const bounds = el.getBoundingClientRect();
      const svgBounds = svg.getBoundingClientRect();
      const scaleX = svgBounds.width / width;
      const scaleY = svgBounds.height / height;
      const scroll = el.scrollLeft;
      const center = svgBounds.left - bounds.left + nearest.x * scaleX;
      let topAnchor = Infinity;
      for (const row of rows) {
        const dot = el.querySelector(`[data-token-dot="${row.index}"]`);
        if (!dot) continue;
        if (!row.point) { dot.hidden = true; continue; }
        const dotTop = svgBounds.top - bounds.top + row.point.y * scaleY;
        topAnchor = Math.min(topAnchor, dotTop);
        dot.hidden = false;
        dot.style.background = row.line.color;
        dot.style.left = `${center + scroll}px`;
        dot.style.top = `${dotTop}px`;
      }
      if (!Number.isFinite(topAnchor)) topAnchor = 16;
      crosshair.style.left = `${center + scroll}px`;
      crosshair.hidden = false;
      const gap = 10, tooltipWidth = tooltip.offsetWidth, tooltipHeight = tooltip.offsetHeight;
      const left = center - tooltipWidth / 2;
      const above = topAnchor - tooltipHeight - gap;
      tooltip.style.left = `${scroll + Math.max(gap, Math.min(left, el.clientWidth - tooltipWidth - gap))}px`;
      tooltip.style.top = `${above >= gap ? above : Math.min(topAnchor + gap, el.clientHeight - tooltipHeight - gap)}px`;
    };
    const hide = () => {
      tooltip.hidden = true;
      crosshair.hidden = true;
      el.querySelectorAll('[data-token-dot]').forEach(dot => { dot.hidden = true; });
    };
    el.onpointermove = show;
    el.onpointerleave = hide;
  }

  function renderTokenChart() {
    paintTokenChart(chart, {
      metric: 'tokens',
      metricLabel: '各模型每台每小时输出 token',
      sampleNoun: '输出 token',
      loadingText: '正在读取输出 token 趋势…',
    });
  }

  function renderTokenTotalChart() {
    paintTokenChart(totalChart, {
      metric: 'total',
      metricLabel: '各模型组总每小时输出 token',
      sampleNoun: '组总输出 token',
      loadingText: '正在读取组总输出 token 趋势…',
    });
  }

  function renderTrendCharts() {
    renderTokenChart();
    renderTokenTotalChart();
  }

  function readTrend(id, signal) {
    return query('network-rates-token-trend', null, id, signal).then(
      value => ({ trend: value, error: '' }),
      failure => {
        if (signal?.aborted || failure?.name === 'AbortError') throw failure;
        return { trend: { series: [], all: [], tier: [] }, error: failure.code === 'network_rates_unsupported' ? '设备 Agent 版本过旧，暂无输出 token 趋势。' : failure.message };
      },
    );
  }

  const countdown = $('#network-rates-countdown');
  let refreshFlight = null;
  let refreshTimer = null;
  let nextRefreshAt = 0;
  let polling = false;

  function paintCountdown() {
    if (!countdown) return;
    countdown.textContent = nextRefreshAt ? `${formatRefreshCountdown(nextRefreshAt - Date.now())} 后刷新` : '';
  }

  function armRefresh(from = Date.now()) {
    nextRefreshAt = from + NETWORK_RATES_REFRESH_MS;
    if (refreshTimer) clearTimeout(refreshTimer);
    refreshTimer = setTimeout(() => {
      refreshTimer = null;
      refresh();
    }, Math.max(0, nextRefreshAt - Date.now()));
    paintCountdown();
  }

  async function loadRates(id, request, signal) {
    loading = true; error = ''; unsupported = false; render();
    try {
      const [ratesResponse, trendResponse] = await Promise.all([
        query('network-rates', null, id, signal),
        readTrend(id, signal),
      ]);
      if (signal.aborted || request !== generation || selectedDevice()?.id !== id) return;
      rates = ratesResponse; trend = trendResponse.trend; trendError = trendResponse.error; unsupported = false; error = '';
      syncTokenModels(tokenTrendModels(trend));
    } catch (failure) {
      if (signal.aborted || failure?.name === 'AbortError' || request !== generation || selectedDevice()?.id !== id) return;
      trend = null; trendError = '';
      if (failure.code === 'network_rates_unsupported') { unsupported = true; rates = null; }
      else { error = failure.message; rates = null; }
    } finally {
      if (!signal.aborted && request === generation && selectedDevice()?.id === id) {
        loading = false; render();
        // This refresh is the only signal the page uses for a submitted job, so it
        // also releases the row buttons. It happens even on a failed read: a locked
        // row would be worse than an early retry, and the device rejects a second job.
        switcher?.settle();
      }
    }
  }

  // Manual and automatic refreshes share this call. An in-flight request for the
  // same device is reused; the countdown always restarts from this call.
  async function refresh() {
    armRefresh();
    render();
    const id = deviceID;
    if (!id) return;
    if (refreshFlight && networkRefreshPlan(refreshFlight.deviceID, id) === 'join') return refreshFlight.promise;
    refreshFlight?.controller.abort();
    const controller = new AbortController();
    const request = ++generation;
    const promise = loadRates(id, request, controller.signal);
    refreshFlight = { deviceID: id, promise, controller };
    try {
      return await promise;
    } finally {
      if (refreshFlight?.controller === controller) refreshFlight = null;
    }
  }

  function start() {
    if (polling) return;
    polling = true;
    setInterval(paintCountdown, 1000);
    return refresh();
  }

  $('#network-rates-refresh')?.addEventListener('click', refresh);
  $('#network-scope')?.addEventListener('click', event => {
    const button = event.target.closest('[data-network-scope]');
    if (!button) return;
    const next = button.dataset.networkScope === 'all' ? 'all' : 'tier';
    if (next === networkScope) return;
    networkScope = next;
    try { localStorage.setItem(NETWORK_SCOPE_STORAGE_KEY, networkScope); } catch { /* 忽略无法写入的偏好 */ }
    render();
  });
  const applyTokenRange = () => {
    const window = activeTokenWindow();
    if (!window || loading) return;
    const next = tokenTrendRangeFromInputs(tokenFrom.value, tokenTo.value, window);
    if (!next) {
      renderTokenPeriodControls();
      return;
    }
    tokenRange = tokenTrendRangeMatches(next, tokenTrendRecentRange(window)) ? null : next;
    renderTokenPeriodControls();
    renderTrendCharts();
  };
  tokenFrom?.addEventListener('change', applyTokenRange);
  tokenTo?.addEventListener('change', applyTokenRange);
  tokenChoices?.addEventListener('change', event => {
    const input = event.target;
    if (!input.hasAttribute('data-network-token-model')) return;
    if (input.checked) tokenSelected.add(input.value);
    else tokenSelected.delete(input.value);
    renderTrendCharts();
  });
  // Right-clicking a chip offers the same switch as the table row. The chip keeps
  // its checkbox behaviour; only the menu is new.
  tokenChoices?.addEventListener('contextmenu', event => {
    const chip = event.target.closest('.home-model-choice');
    const input = chip?.querySelector('[data-network-token-model]');
    if (!input) return;
    event.preventDefault();
    const action = chipActionFor(input.value);
    contextMenu?.open({
      x: event.clientX, y: event.clientY, label: `模型操作：${displayModelName(input.value)}`,
      items: [{
        label: action.label,
        disabled: action.disabled,
        hint: action.hint,
        onSelect: () => openModelAction(action.kind, input.value),
      }],
    });
  });
  // A row button always submits a single model; the kind comes from the same
  // helper that rendered the label, so a stale row can only re-send its own action.
  $('#network-rates-candidates')?.addEventListener('click', event => {
    const button = event.target.closest('[data-network-action]');
    if (!button || button.disabled) return;
    openModelAction(button.dataset.networkAction, button.dataset.model);
  });
  if (chart) new ResizeObserver(() => renderTokenChart()).observe(chart);
  if (totalChart) new ResizeObserver(() => renderTokenTotalChart()).observe(totalChart);

  const networkTabs = [...document.querySelectorAll('[data-network-tab]')];
  function storedNetworkTab() {
    try { return localStorage.getItem('bloomer.networkTab'); } catch { return null; }
  }
  function selectNetworkTab(tab, { focus = false } = {}) {
    if (!networkTabs.length) return;
    const active = networkTabs.find(button => button.dataset.networkTab === tab) || networkTabs[0];
    networkTabs.forEach(button => {
      const selected = button === active;
      button.classList.toggle('selected', selected);
      button.setAttribute('aria-selected', String(selected));
      button.tabIndex = selected ? 0 : -1;
      const pane = document.getElementById(button.getAttribute('aria-controls'));
      if (pane) pane.hidden = !selected;
    });
    if (focus) active.focus();
    try { localStorage.setItem('bloomer.networkTab', active.dataset.networkTab); } catch { /* 忽略无法写入的偏好 */ }
    const trendTab = active.dataset.networkTab === 'chart' || active.dataset.networkTab === 'total';
    const shared = $('#network-token-shared');
    if (shared) shared.hidden = !trendTab;
    if (active.dataset.networkTab === 'chart') {
      renderTokenChart();
      if (chart && chart.clientWidth < 40) requestAnimationFrame(() => renderTokenChart());
    } else if (active.dataset.networkTab === 'total') {
      renderTokenTotalChart();
      if (totalChart && totalChart.clientWidth < 40) requestAnimationFrame(() => renderTokenTotalChart());
    }
  }
  function showNetworkTab(tab, options) {
    const sentinel = $('#network-sticky-sentinel');
    const top = matchMedia('(max-width:900px)').matches ? ($('.sidebar')?.offsetHeight || 0) : 0;
    const offset = sentinel ? sentinel.getBoundingClientRect().top - top : 0;
    selectNetworkTab(tab, options);
    if (offset < 0) window.scrollTo({ top: window.scrollY + offset, behavior: 'auto' });
  }
  networkTabs.forEach(button => button.addEventListener('click', () => showNetworkTab(button.dataset.networkTab)));
  $('#network-tabs')?.addEventListener('keydown', event => {
    const step = { ArrowRight: 1, ArrowLeft: -1 }[event.key];
    if (!step) return;
    event.preventDefault();
    const index = networkTabs.findIndex(button => button.classList.contains('selected'));
    showNetworkTab(networkTabs[(index + step + networkTabs.length) % networkTabs.length].dataset.networkTab, { focus: true });
  });
  selectNetworkTab(storedNetworkTab() || 'table');

  return { render, refresh, start, syncActionButtons };
}
