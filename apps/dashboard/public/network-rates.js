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
    && (absent(candidate.cohort) || ['tier', 'all'].includes(candidate.cohort));
}

export function validateNetworkRatesResponse(body) {
  if (object(body) && (!(absent(body.long_window_minutes) || Number.isInteger(body.long_window_minutes))
    || !(absent(body.switch_advantage_ratio) || Number.isFinite(body.switch_advantage_ratio))
    || !(absent(body.chip_tiers) || (Array.isArray(body.chip_tiers) && body.chip_tiers.every(tier => typeof tier === 'string')))
    || !(absent(body.memory_gb) || Number.isInteger(body.memory_gb)))) {
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
  if (candidate.insufficient || !candidate.downloaded) return 'network-rates-muted';
  return '';
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

export function candidateTableOptions(rates) {
  return {
    currentModel: rates?.current_model ?? null,
    advantageRatio: Number.isFinite(rates?.switch_advantage_ratio) ? rates.switch_advantage_ratio : 0.2,
    chipTiers: Array.isArray(rates?.chip_tiers) ? rates.chip_tiers : [],
    shortLabel: windowLabel((rates?.window_samples || 0) * (rates?.interval_minutes || 0)) || '短窗口',
    longLabel: windowLabel(rates?.long_window_minutes) || '近 1 小时',
  };
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

function cohortLabel(candidate, chipTiers) {
  if (candidate.cohort === 'tier') return `同档 ${chipTiers.join('/')}`.trim();
  if (candidate.cohort === 'all') return chipTiers.length ? '全组（同档不足）' : '全组';
  return '';
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
  const { currentModel = null, advantageRatio = 0.2, chipTiers = [], shortLabel = '短窗口', longLabel = '近 1 小时' } = options;
  const labels = displayModelNames(candidates.map(candidate => candidate.model));
  const current = candidates.find(candidate => candidate.model === currentModel);
  const rows = candidates.map(candidate => {
    const muted = candidateRowClass(candidate);
    const isCurrent = candidate.model === currentModel;
    const tags = (isCurrent ? ' <span class="network-rates-tag">当前</span>' : '') + (candidate.downloaded ? '' : ' <span class="network-rates-tag">未下载</span>');
    const cohort = cohortLabel(candidate, chipTiers);
    const note = priceNote(candidate);
    const hourProviders = candidate.hour ? `；${longLabel}均 ${Math.round(candidate.hour.avg_providers * 10) / 10} 台` : '';
    return `<tr${muted ? ` class="${muted}"` : ''}>`
      + `<td title="${escape(candidate.model)}">${escape(labels.get(candidate.model))}${tags}</td>`
      + `<td title="${escape(`${shortLabel}均 ${Math.round(candidate.avg_providers * 10) / 10} 台${hourProviders}`)}">${escape(String(Math.round(candidate.avg_providers * 10) / 10))}${cohort ? `<small class="network-rates-sub">${escape(cohort)}</small>` : ''}</td>`
      + `<td title="输出价 + 输入比 × 输入价（每百万输出 token）">${escape(formatUsdPerMillionMicro(candidate.effective_price_micro))}${note ? `<small class="network-rates-sub">${escape(note)}</small>` : ''}</td>`
      + windowCells(shortWindow(candidate), current && shortWindow(current), isCurrent, advantageRatio, escape)
      + windowCells(candidate.hour, current?.hour, isCurrent, advantageRatio, escape)
      + '</tr>';
  }).join('');
  const windowHead = '<th>有单率</th><th>每台输出 token/h</th><th>估算 $/h</th><th title="每轮去掉输出最多的一台后再平均">去头 $/h</th>';
  return `<div class="table-scroll network-rates-table-wrap"><table><thead>`
    + `<tr><th rowspan="2">模型</th><th rowspan="2">台数</th><th rowspan="2">综合单价</th><th colspan="4" class="network-rates-window">${escape(shortLabel)}</th><th colspan="4" class="network-rates-window">${escape(longLabel)}</th></tr>`
    + `<tr>${windowHead}${windowHead}</tr>`
    + `</thead><tbody>${rows}</tbody></table></div>`;
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

export function tokenTrendRecentRange(window, hours = 3) {
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
  if (!range || !window || tokenTrendRangeMatches(range, tokenTrendRecentRange(window))) return '前 3 小时';
  return `${tokenClock.format(new Date(range.start))} – ${tokenClock.format(new Date(range.end))}`;
}

export function validateNetworkRatesTokenTrendResponse(body) {
  if (!object(body) || Object.hasOwn(body, 'points') || !Array.isArray(body.series) || body.series.length > 256) throw new Error('invalid');
  for (const series of body.series) {
    if (!object(series) || typeof series.model !== 'string' || !Array.isArray(series.points) || series.points.length > 2000) throw new Error('invalid');
    for (const point of series.points) {
      if (!object(point) || !Number.isFinite(Date.parse(point.at)) || !Number.isSafeInteger(point.tokens) || point.tokens < 0) throw new Error('invalid');
    }
  }
  return body;
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

export function renderTokenTrendFigure(series, { width, height, escape, insufficient = new Set(), colorFor, periodLabel = '所选时段' }) {
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
    markup: `<svg viewBox="0 0 ${width} ${height}" role="img" aria-label="${escape(periodLabel)}各模型每台每小时输出 token，最低 ${escape(tokenExact.format(low))}，最高 ${escape(tokenExact.format(peak))}">${svg}</svg><div class="chart-crosshair" hidden></div><div class="chart-tooltip home-revenue-tooltip" hidden></div>${drawn.map((_, index) => `<div class="network-token-dot" data-token-dot="${index}" hidden></div>`).join('')}`,
  };
}

export function createNetworkRatesUI({ query, selectedDevice, currentView, escape }) {
  const $ = selector => document.querySelector(selector);
  let deviceID, rates = null, trend = null, trendError = '', loading = false, error = '', unsupported = false, generation = 0;
  let tokenModels = [], tokenSelected = new Set(), tokenRange = null;
  const chart = $('#network-token-chart');
  const tokenChoices = $('#network-token-models');
  const tokenFrom = $('#network-token-from');
  const tokenTo = $('#network-token-to');
  const tokenColorFor = model => tokenTrendColorFor(model, tokenModels);
  const tokenInsufficient = () => new Set((rates?.candidates || []).filter(candidate => candidate.insufficient).map(candidate => candidate.model));

  function syncTokenModels(series) {
    const fromTrend = (series || []).map(item => item.model).filter(Boolean);
    tokenModels = [...new Set([...fromTrend, ...tokenSelected])].sort();
    if (!tokenSelected.size && fromTrend.length) fromTrend.forEach(model => tokenSelected.add(model));
  }

  function activeTokenWindow() {
    return tokenTrendWindow(trend);
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
    }
    renderTokenPeriodControls();
    const demo = deviceID === 'demo';
    $('#network-rates-panel').setAttribute('aria-busy', String(loading));
    $('#network-rates-refresh').disabled = demo || loading;
    const message = $('#network-rates-message');
    if (unsupported) {
      message.hidden = false; message.className = 'state-message warning';
      message.textContent = '设备 Agent 版本过旧，不支持此功能。请升级设备端 Agent 后重试。';
    } else {
      message.hidden = !error;
      message.className = `state-message${error ? ' warning' : ''}`;
      message.textContent = error;
    }
    $('#network-rates-candidates').innerHTML = unsupported ? '' : rates ? renderCandidatesTable(rates.candidates, escape, candidateTableOptions(rates)) : '';
    $('.network-token-card').hidden = unsupported;
    $('#network-token-period').hidden = unsupported;
    $('#network-chart-unsupported').hidden = !unsupported;
    renderTokenModelChoices();
    renderTokenChart();
  }

  function renderTokenChart() {
    if (!chart || unsupported || chart.clientWidth < 40) return;
    const empty = !trend?.length
      ? '近 24 小时暂无各模型输出 token 样本'
      : tokenRange ? '所选时段暂无各模型输出 token 样本' : '前 3 小时暂无各模型输出 token 样本';
    const noneSelected = '勾选模型以查看对应折线。';
    if (trendError) {
      chart.innerHTML = `<div class="empty-state">${escape(trendError)}</div>`;
      return;
    }
    if (!trend) {
      chart.innerHTML = `<div class="empty-state">${loading ? '正在读取输出 token 趋势…' : ''}</div>`;
      return;
    }
    if (!trend.length) {
      chart.innerHTML = `<div class="empty-state">${empty}</div>`;
      return;
    }
    if (!tokenSelected.size) {
      chart.innerHTML = `<div class="empty-state">${noneSelected}</div>`;
      return;
    }
    const window = activeTokenWindow();
    const range = activeTokenRange();
    const windowed = filterTokenTrendByRange(trend, range);
    const visible = windowed.filter(item => tokenSelected.has(item.model));
    const width = Math.max(560, chart.clientWidth);
    const height = chart.clientHeight || 220;
    const figure = renderTokenTrendFigure(visible, {
      width, height, escape, insufficient: tokenInsufficient(), colorFor: tokenColorFor,
      periodLabel: tokenTrendRangeLabel(range, window),
    });
    if (!figure) {
      chart.innerHTML = `<div class="empty-state">${empty}</div>`;
      return;
    }
    chart.innerHTML = figure.markup;
    const tooltip = chart.querySelector('.chart-tooltip');
    const crosshair = chart.querySelector('.chart-crosshair');
    const svg = chart.querySelector('svg');
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
      const bounds = chart.getBoundingClientRect();
      const svgBounds = svg.getBoundingClientRect();
      const scaleX = svgBounds.width / width;
      const scaleY = svgBounds.height / height;
      const scroll = chart.scrollLeft;
      const center = svgBounds.left - bounds.left + nearest.x * scaleX;
      let topAnchor = Infinity;
      for (const row of rows) {
        const dot = chart.querySelector(`[data-token-dot="${row.index}"]`);
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
      tooltip.style.left = `${scroll + Math.max(gap, Math.min(left, chart.clientWidth - tooltipWidth - gap))}px`;
      tooltip.style.top = `${above >= gap ? above : Math.min(topAnchor + gap, chart.clientHeight - tooltipHeight - gap)}px`;
    };
    const hide = () => {
      tooltip.hidden = true;
      crosshair.hidden = true;
      chart.querySelectorAll('[data-token-dot]').forEach(dot => { dot.hidden = true; });
    };
    chart.onpointermove = show;
    chart.onpointerleave = hide;
  }

  function readTrend(id) {
    return query('network-rates-token-trend', null, id).then(
      value => ({ series: value.series, error: '' }),
      failure => ({ series: [], error: failure.code === 'network_rates_unsupported' ? '设备 Agent 版本过旧，暂无输出 token 趋势。' : failure.message }),
    );
  }

  async function refresh() {
    render();
    if (!deviceID || deviceID === 'demo') {
      if (deviceID === 'demo') {
        loading = true; render();
        try {
          const [ratesResponse, trendResponse] = await Promise.all([query('network-rates', null, deviceID), readTrend(deviceID)]);
          if (selectedDevice()?.id === deviceID) {
            rates = ratesResponse; trend = trendResponse.series; trendError = trendResponse.error; unsupported = false; error = '';
            syncTokenModels(trend);
          }
        } catch (failure) {
          if (selectedDevice()?.id === deviceID) { error = failure.message; rates = null; trend = null; trendError = ''; }
        } finally {
          if (selectedDevice()?.id === deviceID) { loading = false; render(); }
        }
      }
      return;
    }
    const id = deviceID, request = ++generation;
    loading = true; error = ''; unsupported = false; render();
    try {
      const [ratesResponse, trendResponse] = await Promise.all([query('network-rates', null, id), readTrend(id)]);
      if (request !== generation || selectedDevice()?.id !== id) return;
      rates = ratesResponse; trend = trendResponse.series; trendError = trendResponse.error;
      syncTokenModels(trend);
    } catch (failure) {
      if (request !== generation || selectedDevice()?.id !== id) return;
      trend = null; trendError = '';
      if (failure.code === 'network_rates_unsupported') { unsupported = true; rates = null; }
      else { error = failure.message; rates = null; }
    } finally {
      if (request === generation && selectedDevice()?.id === id) { loading = false; render(); }
    }
  }

  $('#network-rates-refresh')?.addEventListener('click', refresh);
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
    renderTokenChart();
  };
  tokenFrom?.addEventListener('change', applyTokenRange);
  tokenTo?.addEventListener('change', applyTokenRange);
  tokenChoices?.addEventListener('change', event => {
    const input = event.target;
    if (!input.hasAttribute('data-network-token-model')) return;
    if (input.checked) tokenSelected.add(input.value);
    else tokenSelected.delete(input.value);
    renderTokenChart();
  });
  if (chart) new ResizeObserver(() => renderTokenChart()).observe(chart);

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
    if (active.dataset.networkTab === 'chart') {
      renderTokenChart();
      if (chart && chart.clientWidth < 40) requestAnimationFrame(() => renderTokenChart());
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

  return { render, refresh, refreshIfVisible() { if (currentView() === 'network') return refresh(); } };
}
