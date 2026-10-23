import { DASHBOARD_TIMEZONE } from './data.js';
import { AppError } from './errors.js';
import { smoothLinePath } from './home-revenue.js';
import { displayModelName, displayModelNames } from './models.js';

const BLOCKED_REASONS = {
  auto_switch_off: '未开启自动切换',
  warming_up: '开启后观察期',
  insufficient_samples: '样本不足',
  min_interval: '距上次切换不足 30 分钟',
  control_busy: '有操作执行中',
  current_model_unknown: '当前模型未知',
  models_unavailable: '无法读取已下载模型',
  fallback_not_downloaded: '兜底模型未下载',
  backoff: '失败退避中',
  already_optimal: '当前已是推荐模型',
  below_threshold: '优势不足 20%',
  disabled: '未启用采集',
};

const LAST_ERRORS = {
  stats_unavailable: '公开统计暂不可用',
  pricing_unavailable: '官方价格暂不可用',
  models_unavailable: '无法读取已下载模型',
};

const RECENT_SWITCH_LIMIT = 5;
const SWITCH_REASONS = { better_model: '更优模型', fallback: '兜底切换' };
const SWITCH_STATUSES = { running: '执行中', succeeded: '已完成', failed: '失败', unconfirmed: '结果未确认' };

export function blockedReasonLabel(code) {
  if (code == null || code === '') return '';
  return Object.hasOwn(BLOCKED_REASONS, code) ? BLOCKED_REASONS[code] : code;
}

export function lastErrorLabel(code) {
  if (code == null || code === '') return '';
  return Object.hasOwn(LAST_ERRORS, code) ? LAST_ERRORS[code] : code;
}

export function switchReasonLabel(code) {
  return Object.hasOwn(SWITCH_REASONS, code) ? SWITCH_REASONS[code] : code;
}

export function switchStatusLabel(code) {
  return Object.hasOwn(SWITCH_STATUSES, code) ? SWITCH_STATUSES[code] : code;
}

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

export function validateNetworkRatesSettings(body) {
  if (!body || typeof body !== 'object' || Array.isArray(body) || Object.keys(body).some(key => key !== 'auto_switch') || typeof body.auto_switch !== 'boolean') {
    throw new AppError(400, 'invalid_request', '自动切换设置无效。');
  }
  return { auto_switch: body.auto_switch };
}

const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
const micro = value => typeof value === 'string' && /^-?\d+$/.test(value);

function validateCandidate(candidate) {
  return object(candidate) && typeof candidate.model === 'string' && typeof candidate.downloaded === 'boolean'
    && Number.isFinite(candidate.avg_providers) && Number.isFinite(candidate.requests_per_hour)
    && Number.isFinite(candidate.tokens_per_hour) && micro(candidate.usd_per_hour_micro)
    && ['official', 'fallback'].includes(candidate.price_source) && typeof candidate.insufficient === 'boolean';
}

export function validateNetworkRatesResponse(body) {
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

export function validateNetworkRatesSwitchesResponse(body) {
  if (!object(body) || !Array.isArray(body.switches)) throw new Error('invalid');
  for (const item of body.switches) {
    if (!object(item) || !Number.isSafeInteger(item.id) || !Number.isFinite(Date.parse(item.at))
      || typeof item.from_model !== 'string' || typeof item.to_model !== 'string' || typeof item.reason !== 'string'
      || !['running', 'succeeded', 'failed', 'unconfirmed'].includes(item.status)
      || !(item.error === null || typeof item.error === 'string') || typeof item.job_id !== 'string'
      || !Array.isArray(item.summary) || item.summary.some(row => !object(row) || typeof row.model !== 'string' || !micro(row.usd_per_hour_micro) || !Number.isFinite(row.avg_providers))) {
      throw new Error('invalid');
    }
  }
  return body;
}

function candidateRowClass(candidate) {
  if (candidate.insufficient || !candidate.downloaded) return 'network-rates-muted';
  return '';
}

export function renderCandidatesTable(candidates, escape) {
  if (!candidates?.length) return '<p class="control-note">暂无候选模型数据</p>';
  const labels = displayModelNames(candidates.map(candidate => candidate.model));
  const rows = candidates.map(candidate => {
    const muted = candidateRowClass(candidate);
    const priceNote = candidate.price_source === 'fallback' ? '（兜底价）' : '';
    return `<tr${muted ? ` class="${muted}"` : ''}><td title="${escape(candidate.model)}">${escape(labels.get(candidate.model))}${candidate.insufficient ? ' · 样本不足' : ''}</td><td>${candidate.downloaded ? '是' : '否'}</td><td>${escape(String(Math.round(candidate.avg_providers * 10) / 10))}</td><td>${escape(new Intl.NumberFormat('en-US', { maximumFractionDigits: 1 }).format(candidate.requests_per_hour))}</td><td>${escape(new Intl.NumberFormat('en-US', { maximumFractionDigits: 0 }).format(candidate.tokens_per_hour))}</td><td>${escape(formatUsdPerHourMicro(candidate.usd_per_hour_micro))}</td><td>${escape(priceSourceLabel(candidate.price_source))}${priceNote ? ` ${escape(priceNote)}` : ''}</td></tr>`;
  }).join('');
  return `<div class="table-scroll network-rates-table-wrap"><table><thead><tr><th>模型</th><th>已下载</th><th>对照组均台数</th><th>每台每小时接单</th><th>每台每小时输出 token</th><th>估算 $/h</th><th>价格来源</th></tr></thead><tbody>${rows}</tbody></table></div>`;
}

export function renderSwitchRecords(switches, escape, dateTime) {
  if (!switches?.length) return '<p class="control-note">暂无切换记录</p>';
  return switches.slice(0, RECENT_SWITCH_LIMIT).map(item => {
    const summary = item.summary?.length
      ? `<ul class="network-rates-summary">${item.summary.map(row => `<li title="${escape(row.model)}">${escape(displayModelName(row.model))} · ${escape(formatUsdPerHourMicro(row.usd_per_hour_micro))} · 均 ${escape(String(Math.round(row.avg_providers * 10) / 10))} 台</li>`).join('')}</ul>`
      : '<p class="control-note">无候选摘要</p>';
    return `<details class="network-rates-switch"><summary><span class="network-rates-switch-head"><strong>${escape(dateTime(item.at))}</strong><span title="${escape(item.from_model)} → ${escape(item.to_model)}">${escape(displayModelName(item.from_model))} → ${escape(displayModelName(item.to_model))}</span><span>${escape(switchReasonLabel(item.reason))}</span><span class="status-tag ${item.status === 'failed' || item.status === 'unconfirmed' ? 'warning' : ''}">${escape(switchStatusLabel(item.status))}</span>${item.error ? `<span>${escape(item.error)}</span>` : ''}</span></summary>${summary}</details>`;
  }).join('');
}

export function renderNetworkRatesStatus(rates, escape, dateTime) {
  if (!rates) return '';
  const parts = [`样本 ${escape(String(rates.samples))} / ${escape(String(rates.window_samples))}`];
  if (rates.switch?.next_eligible_at) parts.push(`下次可切换 ${escape(dateTime(rates.switch.next_eligible_at))}`);
  if (rates.switch?.blocked_reason) parts.push(`阻止原因：${escape(blockedReasonLabel(rates.switch.blocked_reason))}`);
  if (rates.switch?.backoff_until) parts.push(`退避至 ${escape(dateTime(rates.switch.backoff_until))}`);
  if (rates.last_error) parts.push(`采集异常：${escape(lastErrorLabel(rates.last_error))}`);
  return parts.join(' · ');
}

const TOKEN_COLORS = ['#5b8def', '#f2994a', '#8b7cf6', '#2bb3a3', '#e5677e', '#9ab843', '#c08a4f', '#d4a72c'];
const tokenCompact = new Intl.NumberFormat('zh-CN', { notation: 'compact', maximumFractionDigits: 1 });
const tokenExact = new Intl.NumberFormat('zh-CN');
const tokenClock = new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
const tokenAxisClock = new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });

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

export function renderTokenTrendFigure(series, { width, height, escape, insufficient = new Set() }) {
  const lines = (series || []).map((item, index) => {
    const points = (item?.points || [])
      .map(point => ({ instant: Date.parse(point.at), tokens: point.tokens }))
      .filter(point => Number.isFinite(point.instant) && Number.isSafeInteger(point.tokens) && point.tokens >= 0)
      .sort((a, b) => a.instant - b.instant);
    return {
      model: typeof item?.model === 'string' ? item.model : '',
      color: TOKEN_COLORS[index % TOKEN_COLORS.length],
      dashed: insufficient.has(item?.model),
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
    svg += `<text class="chart-label" x="${round(x(instant))}" y="${height - 8}" text-anchor="middle">${escape(tokenAxisClock.format(new Date(instant)))}</text>`;
  }
  for (const line of drawn) {
    const path = line.coords.length > 1 ? smoothLinePath(line.coords) : '';
    const dash = line.dashed ? ' stroke-dasharray="6 4"' : '';
    if (path) svg += `<path class="network-token-line" stroke="${line.color}"${dash} d="${path}"><title>${escape(line.model)}</title></path>`;
    if (line.coords.length === 1) svg += `<circle fill="${line.color}" cx="${line.coords[0].x}" cy="${line.coords[0].y}" r="3.5"><title>${escape(line.model)}</title></circle>`;
  }
  const values = drawn.flatMap(line => line.coords.map(point => point.tokens));
  const low = Math.min(...values), peak = Math.max(...values);
  const legend = drawn.map(line => `<span class="network-token-legend-item${line.dashed ? ' is-insufficient' : ''}" style="--series-color:${line.color}" title="${escape(line.model)}"><i aria-hidden="true"></i><span>${escape(displayModelName(line.model))}</span></span>`).join('');
  return {
    series: drawn,
    instants: instants.map(instant => ({ instant, x: round(x(instant)), timeLabel: tokenClock.format(new Date(instant)) })),
    legend,
    markup: `<svg viewBox="0 0 ${width} ${height}" role="img" aria-label="近 24 小时各模型每台每小时输出 token，最低 ${escape(tokenExact.format(low))}，最高 ${escape(tokenExact.format(peak))}">${svg}</svg><div class="chart-crosshair" hidden></div><div class="chart-tooltip home-revenue-tooltip" hidden></div>${drawn.map((_, index) => `<div class="network-token-dot" data-token-dot="${index}" hidden></div>`).join('')}`,
  };
}

export function createNetworkRatesUI({ query, submitSettings, selectedDevice, deviceChoices, selectDevice, currentView, escape, dateTime }) {
  const $ = selector => document.querySelector(selector);
  let deviceID, rates = null, switches = [], trend = null, trendError = '', loading = false, submitting = false, error = '', unsupported = false, generation = 0, pendingToggle = null;
  const dialog = $('#network-rates-confirm-dialog');
  const chart = $('#network-token-chart');
  const legend = $('#network-token-legend');

  function render() {
    const device = selectedDevice();
    if (device?.id !== deviceID) {
      deviceID = device?.id; rates = null; switches = []; trend = null; trendError = ''; loading = false; submitting = false; error = ''; unsupported = false; generation++;
      if (dialog?.open) dialog.close();
    }
    const demo = deviceID === 'demo';
    $('#network-rates-device').innerHTML = deviceChoices().map(item => `<option value="${escape(item.id)}" ${item.id === deviceID ? 'selected' : ''}>${escape(item.name)}</option>`).join('');
    const toggle = $('#network-rates-auto-switch');
    toggle.checked = Boolean(rates?.auto_switch);
    toggle.disabled = demo || loading || submitting || unsupported || !rates?.enabled;
    $('#network-rates-panel').setAttribute('aria-busy', String(loading || submitting));
    $('#network-rates-refresh').disabled = demo || loading || submitting;
    const message = $('#network-rates-message');
    if (unsupported) {
      message.hidden = false; message.className = 'state-message warning';
      message.textContent = '设备 Agent 版本过旧，不支持此功能。请升级设备端 Agent 后重试。';
    } else {
      message.hidden = !error;
      message.className = `state-message${error ? ' warning' : ''}`;
      message.textContent = error;
    }
    $('#network-rates-status').textContent = unsupported ? '' : rates ? renderNetworkRatesStatus(rates, escape, dateTime) : loading ? '正在读取全网模型收益…' : '';
    $('#network-rates-candidates').innerHTML = unsupported ? '' : rates ? renderCandidatesTable(rates.candidates, escape) : '';
    $('.network-token-card').hidden = unsupported;
    renderTokenChart();
    if (unsupported) $('#network-rates-switches').innerHTML = '';
    else if (loading && !switches.length) $('#network-rates-switches').innerHTML = '<p class="control-note">正在读取切换记录…</p>';
    else $('#network-rates-switches').innerHTML = renderSwitchRecords(switches, escape, dateTime);
  }

  function renderTokenChart() {
    if (legend) { legend.hidden = true; legend.innerHTML = ''; }
    if (!chart || unsupported || chart.clientWidth < 40) return;
    const empty = '近 24 小时暂无各模型输出 token 样本';
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
    const width = Math.max(560, chart.clientWidth);
    const height = chart.clientHeight || 220;
    const insufficient = new Set((rates?.candidates || []).filter(candidate => candidate.insufficient).map(candidate => candidate.model));
    const figure = renderTokenTrendFigure(trend, { width, height, escape, insufficient });
    if (!figure) {
      chart.innerHTML = `<div class="empty-state">${empty}</div>`;
      return;
    }
    if (legend) { legend.hidden = false; legend.innerHTML = figure.legend; }
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
          const [ratesResponse, switchesResponse, trendResponse] = await Promise.all([query('network-rates', null, deviceID), query('network-rates-switches', null, deviceID), readTrend(deviceID)]);
          if (selectedDevice()?.id === deviceID) { rates = ratesResponse; switches = switchesResponse.switches; trend = trendResponse.series; trendError = trendResponse.error; unsupported = false; error = ''; }
        } catch (failure) {
          if (selectedDevice()?.id === deviceID) { error = failure.message; rates = null; switches = []; trend = null; trendError = ''; }
        } finally {
          if (selectedDevice()?.id === deviceID) { loading = false; render(); }
        }
      }
      return;
    }
    const id = deviceID, request = ++generation;
    loading = true; error = ''; unsupported = false; render();
    try {
      const [ratesResponse, switchesResponse, trendResponse] = await Promise.all([query('network-rates', null, id), query('network-rates-switches', null, id), readTrend(id)]);
      if (request !== generation || selectedDevice()?.id !== id) return;
      rates = ratesResponse; switches = switchesResponse.switches; trend = trendResponse.series; trendError = trendResponse.error;
    } catch (failure) {
      if (request !== generation || selectedDevice()?.id !== id) return;
      trend = null; trendError = '';
      if (failure.code === 'network_rates_unsupported') { unsupported = true; rates = null; switches = []; }
      else { error = failure.message; rates = null; switches = []; }
    } finally {
      if (request === generation && selectedDevice()?.id === id) { loading = false; render(); }
    }
  }

  async function applyToggle(auto_switch) {
    const id = deviceID;
    if (!id || id === 'demo' || submitting) return;
    const request = generation;
    submitting = true; error = ''; render();
    try {
      rates = await submitSettings(id, auto_switch);
      if (request !== generation || selectedDevice()?.id !== id) return;
      const switchesResponse = await query('network-rates-switches', null, id);
      if (request !== generation || selectedDevice()?.id !== id) return;
      switches = switchesResponse.switches;
    } catch (failure) {
      if (request !== generation || selectedDevice()?.id !== id) return;
      error = failure.message;
    } finally {
      if (request === generation && selectedDevice()?.id === id) { submitting = false; render(); }
    }
  }

  $('#network-rates-device')?.addEventListener('change', event => selectDevice(event.target.value));
  $('#network-rates-auto-switch')?.addEventListener('change', event => {
    const input = event.target;
    const next = input.checked;
    input.checked = !next;
    pendingToggle = next;
    $('#network-rates-confirm-message').textContent = next
      ? '确认开启自动切换最优模型？\n\n开启后 Agent 将按规则在估算收益明显更高时自动切换模型；切换与远程操作互斥，并受样本与间隔限制。'
      : '确认关闭自动切换？\n\n关闭后仍会采集公开统计并展示估算与推荐，但不会再自动切换模型。';
    dialog.returnValue = ''; dialog.showModal();
  });
  dialog?.addEventListener('close', () => {
    const next = pendingToggle; pendingToggle = null;
    if (dialog.returnValue === 'confirm' && typeof next === 'boolean') applyToggle(next);
    else render();
  });
  $('#network-rates-refresh')?.addEventListener('click', refresh);
  if (chart) new ResizeObserver(() => renderTokenChart()).observe(chart);

  return { render, refresh, refreshIfVisible() { if (currentView() === 'network') return refresh(); } };
}
