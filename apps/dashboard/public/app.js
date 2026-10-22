import { DASHBOARD_TIMEZONE, money, sumMoney, sumCount, timeRange, rollingRange, dateKey, shiftDate, zonedMidnight, trendGroups, csvRows } from './data.js';
import { CursorPager } from './pagination.js';
import { createDeviceUI } from './device-ui.js';
import { createAlertUI } from './alert-ui.js';
import { createControlUI } from './control-ui.js';
import { DeviceStore } from './connections.js';
import { createAgentClient } from './agent-client.js';
import { createHomeRevenueChart } from './home-revenue.js';

const $ = selector => document.querySelector(selector);
const $$ = selector => [...document.querySelectorAll(selector)];
const escape = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
const number = value => new Intl.NumberFormat('en-US').format(value);
const icons = {
  alerts: '<path d="M18 8a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9M10 21h4"/>',
  overview: '<rect x="3" y="3" width="7" height="7" rx="1.3"/><rect x="14" y="3" width="7" height="7" rx="1.3"/><rect x="3" y="14" width="7" height="7" rx="1.3"/><rect x="14" y="14" width="7" height="7" rx="1.3"/>',
  analysis: '<path d="M4 3v18h17M8 17v-5M13 17V8M18 17V4"/>',
  records: '<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h4"/>',
  devices: '<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4M7 13h4"/>',
  query: '<circle cx="10" cy="10" r="6"/><path d="m15 15 6 6M7 10h6M10 7v6"/>',
  refresh: '<path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 6.1a8 8 0 0 1 13.2 3L20 12M4 12l.7 2.9a8 8 0 0 0 13.2 3"/>',
  connect: '<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4M9 10.5h6M12 7.5v6"/>',
};
const icon = name => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[name]}</svg>`;
$$('[data-icon]').forEach(element => { element.innerHTML = icon(element.dataset.icon); });

function preference(key, value) {
  try { if (value === undefined) return localStorage.getItem(`bloomer.${key}`); localStorage.setItem(`bloomer.${key}`, value); } catch { return null; }
}

const connections = new DeviceStore();
const client = createAgentClient(connections);
const state = {
  devices: [], selected: null, period: '1',
  custom: {}, model: '', trendGroup: 'day', view: 'overview', data: {}, health: new Map(), page: 0,
  pageSize: Number(preference('pageSize') || 10), pager: null, pageController: null, pendingPage: null,
  generation: 0, busy: false, exporting: false, exportController: null, fetchedAt: null, errors: [], summaryErrors: [], filterErrors: [], range: null,
};
const earningsViews = new Map();
let earningsView = 'analysis';
const earningsFilterKeys = ['period', 'custom', 'model', 'trendGroup', 'range', 'page', 'pager', 'fetchedAt', 'filterErrors'];

function activateEarningsView(view) {
  if (view === earningsView) return false;
  const { trend, models, earnings, overview, lastHour, lastDay } = state.data;
  earningsViews.set(earningsView, {
    filters: Object.fromEntries(earningsFilterKeys.map(key => [key, state[key]])),
    data: { trend, models, earnings }, device: state.selected, needsLoad: state.busy || !earnings,
  });
  // In-flight responses for the previous menu must never replace this menu's data.
  state.generation++;
  state.pageController?.abort(); state.exportController?.abort();
  state.pageController = null; state.pendingPage = null; state.busy = false;
  $('#trend-chart').setAttribute('aria-busy', 'false');
  const saved = earningsViews.get(view);
  Object.assign(state, saved?.filters || {
    period: '1', custom: {}, model: '', trendGroup: 'day', range: null,
    page: 0, pager: null, fetchedAt: null, filterErrors: [],
  });
  const sameDevice = saved?.device === state.selected;
  if (!sameDevice) { state.model = ''; state.page = 0; state.pager = null; state.filterErrors = []; }
  state.range ||= timeRange(state.period, DASHBOARD_TIMEZONE, new Date(), state.custom);
  state.data = { overview, lastHour, lastDay, ...(sameDevice ? saved.data : {}) };
  state.errors = [...new Set([...state.summaryErrors, ...state.filterErrors])];
  earningsView = view;
  $('#period-select').value = state.period;
  $$('[data-period]').forEach(button => {
    const selected = button.dataset.period === state.period;
    button.classList.toggle('selected', selected); button.setAttribute('aria-pressed', String(selected));
  });
  $('#date-form').hidden = true; $('#date-error').textContent = ''; $('#export-status').hidden = true;
  return !sameDevice || saved.needsLoad;
}
if (!Number.isInteger(state.pageSize) || state.pageSize < 1 || state.pageSize > 500) state.pageSize = 10;
$('#page-size').value = state.pageSize;
const demoDevice = { id: 'demo', name: '演示设备', url: '演示数据', source: 'demo' };
const selectedDevice = () => state.selected === 'demo' ? demoDevice : state.devices.find(device => device.id === state.selected);
const deviceChoices = () => [...state.devices, demoDevice];
let toastTimer;
function toast(message) { $('#toast').textContent = message; $('#toast').hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => { $('#toast').hidden = true; }, 4200); }
function dateTime(value, short = false) {
  if (!value) return '尚无记录';
  return new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, month: '2-digit', day: '2-digit', ...(short ? {} : { year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }) }).format(new Date(value));
}
function relative(value) {
  if (!value) return '尚未成功采集';
  const seconds = Math.floor((Date.now() - Date.parse(value)) / 1000);
  if (seconds < 0) return '设备时间异常';
  if (seconds < 60) return `${seconds} 秒前`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小时前`;
  return `${Math.floor(seconds / 86400)} 天前`;
}

const query = (route, params, device = state.selected, signal) => client.query(route, params, device, signal);
const homeRevenue = createHomeRevenueChart({ query, selectedDevice, currentView: () => state.view, escape });
const deviceUI = createDeviceUI({ query, selectedDevice, deviceChoices, selectDevice, currentView: () => state.view, dateTime, escape, preference, onRefresh: () => { state.deviceFetchedAt = new Date(); updateClock(); } });

const alertUI = createAlertUI({ query, selectedDevice, deviceChoices, selectDevice, currentView: () => state.view, escape, dateTime });
const controlUI = createControlUI({ submitControl: (id, body) => client.submit(id, body), query, selectedDevice, currentView: () => state.view, escape, dateTime, refreshState: () => deviceUI.refresh() });

function resetQuery({ clearModel = false, preserveSummary = false } = {}) {
  state.exportController?.abort();
  const { overview, lastHour, lastDay } = state.data;
  state.page = 0; state.data = preserveSummary ? { overview, lastHour, lastDay } : {};
  if (!preserveSummary) state.summaryErrors = [];
  state.filterErrors = [];
  state.errors = [...state.summaryErrors];
  if (clearModel) state.model = '';
  state.range = timeRange(state.period, DASHBOARD_TIMEZONE, new Date(), state.custom);
}

function renderModelFilters() {
  const models = state.data.models?.items || [];
  const choices = [...new Set([...models.map(item => item.key), ...(state.model ? [state.model] : [])])].sort();
  const options = '<option value="">全部模型</option>' + choices.map(model => `<option value="${escape(model)}" ${state.model === model ? 'selected' : ''}>${escape(model)}</option>`).join('');
  $$('[data-model-filter]').forEach(select => { select.innerHTML = options; select.disabled = state.busy; });
}

function syncTrendGrouping() {
  const groups = trendGroups(state.range, DASHBOARD_TIMEZONE);
  if (!groups.includes(state.trendGroup)) state.trendGroup = groups[0];
  $('#trend-grouping').hidden = groups.length === 1;
  $('#chart-grain').textContent = ({ hour: '按小时', day: '按天', month: '按月' })[state.trendGroup];
  $$('[data-group]').forEach(button => {
    const selected = button.dataset.group === state.trendGroup;
    button.classList.toggle('selected', selected);
    button.setAttribute('aria-pressed', String(selected));
    button.disabled = state.busy;
  });
}

function healthStatus(health) {
  if (!health || health.error) return { label: health?.error ? '连接失败' : '检测中', className: health?.error ? 'offline' : 'warning' };
  if (health.collection_state === 'disabled') return { label: '采集已关闭', className: 'warning' };
  if (health.collection_state === 'rate_limited') return { label: '限流等待', className: 'warning' };
  if (health.data_stale || health.collection_state === 'degraded') return { label: health.last_successful_sync_at ? '数据已过期' : '等待首次采集', className: 'warning' };
  return { label: health.collection_state === 'collecting' ? '正在采集' : '采集正常', className: '' };
}

function renderDevices() {
  const active = state.selected;
  $('#nav-device-count').textContent = state.devices.length;
  $('#device-select').innerHTML = deviceChoices().map(device => `<option value="${escape(device.id)}" ${device.id === active ? 'selected' : ''}>${escape(device.name)}</option>`).join('');
  $('#demo-banner').hidden = active !== 'demo';
  const currentHealth = state.health.get(active);
  const status = healthStatus(currentHealth);
  const collectionDot = $('#device-status');
  collectionDot.className = `collection-dot${status.className ? ' offline' : ''}`;
  collectionDot.setAttribute('aria-label', `收益采集状态：${status.label}`);
  collectionDot.title = `收益采集状态：${status.label}`;
  $('#device-cards').innerHTML = deviceChoices().map(device => {
    const health = state.health.get(device.id);
    const status = healthStatus(health);
    return `<article class="device-card ${device.id === active ? 'selected' : ''}"><div class="device-card-header"><h3>${escape(device.name)}</h3><span class="status-tag ${status.className}">${status.label}</span></div><p class="device-url">${escape(device.url)}</p><div class="device-stat"><span>最近成功采集</span><strong>${escape(relative(health?.last_successful_sync_at))}</strong></div><div class="device-stat"><span>Agent 版本</span><strong>${escape(health?.version || '—')}${device.id === 'demo' ? ' · 演示' : ''}</strong></div>${health?.error ? `<p class="device-url">${escape(health.error)}</p>` : ''}<div class="device-card-actions"><button class="button ${device.id === active ? 'button-dark' : 'button-white'}" data-select-device="${escape(device.id)}">${device.id === active ? '当前数据来源' : '查看收益'}</button>${device.id !== 'demo' ? `<button class="button remove-device" data-remove-device="${escape(device.id)}">移除</button>` : ''}</div></article>`;
  }).join('');
  renderHealth();
  deviceUI.render();
  controlUI.render();
  alertUI.render();
}

function renderHealth() {
  const health = state.health.get(state.selected);
  $('#health-device-label').textContent = selectedDevice()?.name || '当前设备';
  if (!health || health.error) { $('#health-details').innerHTML = `<div class="empty-state">${escape(health?.error || '正在检查采集状态…')}</div>`; return; }
  const states = { idle: '等待下轮采集', collecting: '采集中', disabled: '已关闭', degraded: '降级运行', rate_limited: '限流等待' };
  const coverage = health.coverage;
  const items = [
    ['采集状态', states[health.collection_state] || health.collection_state], ['最近成功采集', dateTime(health.last_successful_sync_at)],
    ['最近尝试', dateTime(health.last_attempt_at)], ['同步方式', ({ initial: '首次导入', normal: '常规采集', recovery: '恢复同步' })[health.sync_mode] || '—'],
    ['限流等待至', health.retry_not_before ? dateTime(health.retry_not_before) : '无限流等待'], ['最近错误', health.last_error || '无'],
    ['历史覆盖起点', dateTime(coverage?.start)], ['疑似历史缺口', `${coverage?.gap_count || 0} 段`], ['最近清理成功', dateTime(health.cleanup?.last_success_at)],
    ['清理状态', ({ idle: '正常待命', running: '清理中', failed: '清理失败' })[health.cleanup?.state] || '—'], ['恢复原因', health.recovery_reason || '无'], ['保留期起点', dateTime(health.retention_start)],
  ];
  $('#health-details').innerHTML = items.map(([label, value]) => `<div class="health-item"><span>${escape(label)}</span><strong>${escape(value)}</strong></div>`).join('') + (coverage?.gaps?.length ? `<div class="health-item wide"><span>疑似缺口区间${coverage.gaps_truncated ? '（仅展示最近 100 段）' : ''}</span><div class="health-gaps">${coverage.gaps.map(gap => `${escape(dateTime(gap.from))} — ${escape(dateTime(gap.to))} · ${escape(gap.reason)}`).join('<br>')}</div></div>` : '');
}

function renderAlerts() {
  if (state.view === 'query' || state.view === 'alerts') { $('#alerts').innerHTML = ''; return; }
  const health = state.health.get(state.selected);
  const metaList = Object.values(state.data).map(item => item?.meta).filter(Boolean);
  const meta = state.data.overview?.meta || metaList[0];
  const alerts = state.errors.map(error => ['error', error]);
  if (health?.error && !state.errors.includes(health.error)) alerts.push(['error', health.error]);
  if ((health?.data_stale || metaList.some(item => item.data_stale)) && !health?.error) alerts.push(['', `数据尚未更新。最近成功采集：${health?.last_successful_sync_at || meta?.last_successful_sync_at ? relative(health?.last_successful_sync_at || meta?.last_successful_sync_at) : '尚无'}；当前显示设备保存的最近数据。`]);
  if (health?.collection_state === 'disabled') alerts.push(['', '设备的自动采集已关闭；现有数据仍可查看。']);
  if (health?.collection_state === 'rate_limited') alerts.push(['', `设备正在等待上游限流解除${health.retry_not_before ? `，最早重试时间 ${dateTime(health.retry_not_before)}` : ''}。`]);
  if (meta?.coverage?.gap_count || health?.coverage?.gap_count) alerts.push(['', `已检测到 ${meta?.coverage?.gap_count || health.coverage.gap_count} 段疑似历史缺口，本地收益可能不完整。可在“我的设备”查看区间。`]);
  if (metaList.some(item => item.range_truncated)) alerts.push(['info', '部分查询时间超出明细保留范围，已按滚动 365 天截取。']);
  if (health?.cleanup?.state === 'failed') alerts.push(['', '最近一次过期数据清理失败，设备将继续重试。']);
  $('#alerts').innerHTML = alerts.map(([type, message]) => `<div class="alert ${type}">${escape(message)}</div>`).join('');
  $('#coverage-tag').textContent = meta?.coverage ? `历史覆盖 · ${meta.coverage.gap_count ? '疑似缺口' : '已保存部分历史'}` : '等待历史信息';
}

function renderMetrics() {
  const { trend, lastHour, lastDay, overview } = state.data;
  const amount = trend ? sumMoney(trend.items) : null;
  $('#last-hour-amount').textContent = money(lastHour ? sumMoney(lastHour.items) : null, 4);
  $('#last-day-amount').textContent = money(lastDay ? sumMoney(lastDay.items) : null, 4);
  $('#available-balance').textContent = money(overview?.official?.available_balance_micro_usd);
  $$('.summary-amount').forEach(element => { element.title = element.textContent; });
  $('#chart-total').textContent = money(amount);
  syncTrendGrouping();
  const start = state.range?.from;
  $('#range-label').textContent = start ? `${dateTime(start, true)} — ${dateTime(new Date(Date.parse(state.range.to) - 1), true)}` : '选择日期';
}

function renderTrend() {
  if (state.view !== 'analysis') return;
  const items = state.data.trend?.items;
  if (!items?.length) { $('#trend-chart').innerHTML = `<div class="empty-state">${items ? '暂无收益记录' : state.busy ? '正在读取收益趋势…' : '趋势数据不可用'}</div>`; return; }
  const hourFormatter = new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
  const pointLabel = item => state.trendGroup === 'hour' ? hourFormatter.format(new Date(item.from)) : item.key;
  const width = $('#trend-chart').clientWidth || 720, height = $('#trend-chart').clientHeight || 220;
  const left = 47, right = 8, top = 10, bottom = 27;
  const plotWidth = width - left - right, plotHeight = height - top - bottom;
  // Exact integer sums are kept above. Conversion is only used for SVG geometry.
  const values = items.map(item => Number(BigInt(item.amount_micro_usd)) / 1e6);
  const min = Math.min(0, ...values), max = Math.max(0, ...values);
  const low = min < 0 ? min * 1.15 : 0, high = max > 0 ? max * 1.15 : 1;
  const y = value => top + (high - value) / (high - low) * plotHeight;
  const from = Date.parse(state.range.from), to = Date.parse(state.range.to);
  let svg = '';
  for (let step = 0; step <= 4; step++) {
    const value = low + (high - low) * step / 4;
    const label = `$${Math.abs(value) >= 1000 ? (value / 1000).toFixed(1) + 'k' : value.toFixed(Math.abs(value) < 1 ? 2 : 0)}`;
    svg += `<line class="chart-grid" x1="${left}" x2="${width - right}" y1="${y(value)}" y2="${y(value)}"/><text class="chart-label" x="${left - 10}" y="${y(value) + 3}" text-anchor="end">${escape(label)}</text>`;
  }
  svg += `<defs><clipPath id="chart-clip"><rect x="${left}" y="0" width="${plotWidth}" height="${height - bottom}"/></clipPath></defs><g clip-path="url(#chart-clip)">`;
  items.forEach((item, index) => {
    const start = Math.max(from, Date.parse(item.from)), end = Math.min(to, Date.parse(item.to));
    const timestamp = (start + end) / 2;
    const barWidth = Math.min(28, Math.max(1, (end - start) / (to - from) * plotWidth * 0.65));
    const x = left + (timestamp - from) / (to - from) * plotWidth - barWidth / 2;
    const value = values[index];
    const title = `${pointLabel(item)} · ${money(item.amount_micro_usd, 4)}`;
    svg += `<rect class="chart-bar" data-point="${index}" tabindex="0" role="img" aria-label="${escape(title)}" x="${x}" y="${Math.min(y(value), y(0))}" width="${barWidth}" height="${Math.max(1, Math.abs(y(value) - y(0)))}" rx="2"></rect>`;
  });
  svg += '</g>';
  const ticks = trendTicks(from, to, Math.max(2, Math.floor(plotWidth / (state.trendGroup === 'month' ? 80 : 48))));
  ticks.forEach((tick, index) => {
    const x = left + (tick.instant - from) / (to - from) * plotWidth;
    svg += `<text class="chart-label" x="${x}" y="${height - 5}" text-anchor="${index === 0 ? 'start' : index === ticks.length - 1 ? 'end' : 'middle'}">${escape(tick.label)}</text>`;
  });
  $('#trend-chart').innerHTML = `<svg viewBox="0 0 ${width} ${height}" role="group" aria-label="收益趋势，空白区间没有已保存记录">${svg}</svg><div class="chart-tooltip" hidden></div>`;
  const showTooltip = event => {
    const bar = event.target.closest('[data-point]');
    if (!bar) return;
    const item = items[Number(bar.dataset.point)];
    const tooltip = $('#trend-chart .chart-tooltip');
    tooltip.innerHTML = `${escape(pointLabel(item))}<strong>${escape(money(item.amount_micro_usd, 4))}</strong>`;
    tooltip.hidden = false;
    const chart = $('#trend-chart'), bounds = chart.getBoundingClientRect(), barBounds = bar.getBoundingClientRect();
    const gap = 8, tooltipWidth = tooltip.offsetWidth, tooltipHeight = tooltip.offsetHeight;
    let x = barBounds.left - bounds.left + barBounds.width / 2 - tooltipWidth / 2;
    let y = barBounds.top - bounds.top - tooltipHeight - gap;
    // Tall bars leave no room above; place the label beside the bar instead.
    if (y < gap) {
      x = barBounds.right - bounds.left + gap;
      if (x + tooltipWidth > chart.clientWidth - gap) x = barBounds.left - bounds.left - tooltipWidth - gap;
      y = barBounds.top - bounds.top;
    }
    tooltip.style.left = `${Math.max(gap, Math.min(x, chart.clientWidth - tooltipWidth - gap))}px`;
    tooltip.style.top = `${Math.max(gap, Math.min(y, chart.clientHeight - tooltipHeight - gap))}px`;
  };
  $('#trend-chart').onpointerover = showTooltip;
  const hideTooltip = () => { const tooltip = $('#trend-chart .chart-tooltip'); if (tooltip) tooltip.hidden = true; };
  $('#trend-chart').onpointerleave = hideTooltip;
  $('#trend-chart').onpointerout = event => { if (event.target.closest('[data-point]')) hideTooltip(); };
  $$('#trend-chart [data-point]').forEach(bar => { bar.onfocus = showTooltip; bar.onblur = hideTooltip; });
}

function trendTicks(from, to, maxTicks) {
  const group = state.trendGroup, ticks = [];
  let start = group === 'month' ? +zonedMidnight(dateKey(from, DASHBOARD_TIMEZONE).slice(0, 7) + '-01', DASHBOARD_TIMEZONE) : from;
  const formatter = new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, ...(group === 'hour' ? { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' } : group === 'month' ? { year: 'numeric', month: '2-digit' } : { month: '2-digit', day: '2-digit' }) });
  while (start < to) {
    const date = dateKey(start, DASHBOARD_TIMEZONE);
    const nextMonth = new Date(`${date.slice(0, 7)}-01T12:00:00Z`);
    nextMonth.setUTCMonth(nextMonth.getUTCMonth() + 1);
    const end = group === 'hour' ? start + 3_600_000 : +zonedMidnight(group === 'month' ? nextMonth.toISOString().slice(0, 10) : shiftDate(date, 1), DASHBOARD_TIMEZONE);
    ticks.push({ instant: (Math.max(from, start) + Math.min(to, end)) / 2, label: formatter.format(new Date(start)) });
    start = end;
  }
  const count = Math.min(6, maxTicks, ticks.length);
  return Array.from({ length: count }, (_, index) => ticks[count === 1 ? 0 : Math.round(index * (ticks.length - 1) / (count - 1))]);
}

function renderRecords() {
  renderModelFilters();
  const page = state.data.earnings;
  const total = state.data.trend ? sumCount(state.data.trend.items) : null;
  const pages = total === null ? null : Math.max(1, Math.ceil(total / state.pageSize));
  $('#records-body').innerHTML = page?.items.length ? page.items.map(item => `<tr><td><div class="model-text"><strong title="${escape(item.model)}">${escape(item.model)}</strong><small title="记录 ${escape(item.record_id)} · Provider ${escape(item.provider_id)}">${escape(item.job_id || item.record_id)}</small></div></td><td class="amount-cell">${escape(money(item.amount_micro_usd, 6))}</td><td>${number(item.prompt_tokens)}</td><td>${number(item.completion_tokens)}</td><td>${escape(dateTime(item.created_at))}</td></tr>`).join('') : `<tr><td colspan="5"><div class="empty-state">${page ? '当前筛选无记录' : state.busy ? '正在读取记录…' : '明细读取失败，请检查设备后刷新。'}</div></td></tr>`;
  $('#records-body').setAttribute('aria-busy', String(state.busy));
  $('#records-count').textContent = total === null ? '—' : number(total);
  $('#page-number').textContent = `${state.page + 1} / ${pages === null ? '—' : number(pages)}`;
  const shown = page?.items.length ? `第 ${number(state.page * state.pageSize + 1)}–${number(state.page * state.pageSize + page.items.length)} 条` : '无记录';
  $('#page-info').textContent = `${shown}${total === null ? '' : ` / 共 ${number(total)} 条`}${state.page ? ' · 自动刷新已暂停' : ''}`;
  $('#prev-page').disabled = state.busy || !state.page;
  $('#next-page').disabled = state.busy || !page?.next_cursor;
  $('#page-size').disabled = state.busy;
  $('#page-size-form button').disabled = state.busy;
  const jump = $('#page-input');
  jump.disabled = state.busy || !page || pages === null;
  $('#page-jump-form button[type="submit"]').disabled = jump.disabled;
  if (pages === null) jump.removeAttribute('max'); else jump.max = pages;
  if (document.activeElement !== jump) jump.value = state.page + 1;
  $('#pagination-status').hidden = state.pendingPage === null;
  $('#pagination-status').textContent = state.pendingPage === null ? '' : `正在前往第 ${number(state.pendingPage + 1)} 页…`;
  $('#cancel-page').hidden = !state.pageController;
  $('#refresh').disabled = state.busy;
  const refreshLabel = state.busy && !state.pageController ? '刷新中…' : '刷新数据';
  $('#refresh').setAttribute('aria-label', refreshLabel);
  $('#refresh').setAttribute('aria-busy', String(state.busy && !state.pageController));
  $('#refresh').title = refreshLabel;
  $('#export-csv').disabled = state.busy || state.exporting || !page?.items.length;
}

function renderAll() { renderDevices(); renderMetrics(); renderTrend(); renderRecords(); renderAlerts(); homeRevenue.render(); }

function summaryJobs(device = state.selected) {
  const now = new Date(), params = { group_by: 'day', timezone: DASHBOARD_TIMEZONE };
  return [
    ['overview', query('overview', null, device)],
    ['lastHour', query('stats', { ...rollingRange(1, now), ...params }, device)],
    ['lastDay', query('stats', { ...rollingRange(24, now), ...params }, device)],
  ];
}

async function refreshSummary() {
  const device = state.selected, generation = state.generation, jobs = summaryJobs(device);
  const results = await Promise.allSettled(jobs.map(([, promise]) => promise));
  if (device !== state.selected || generation !== state.generation) return;
  state.summaryErrors = [];
  results.forEach((result, index) => {
    const key = jobs[index][0];
    if (result.status === 'fulfilled') state.data[key] = result.value;
    else { delete state.data[key]; state.summaryErrors.push(result.reason.message); }
  });
  state.errors = [...new Set([...state.summaryErrors, ...state.filterErrors])];
  renderMetrics(); renderAlerts();
}

async function loadData({ reset = false, clearModel = false, filtersOnly = false } = {}) {
  if (!filtersOnly) deviceUI.refresh();
  if (!filtersOnly && state.view === 'overview') homeRevenue.refresh();
  state.pageController?.abort();
  state.pageController = null; state.pendingPage = null;
  const generation = ++state.generation;
  // An early filter change must still finish the device's first summary load.
  filtersOnly = filtersOnly && ['overview', 'lastHour', 'lastDay'].some(key => state.data[key]);
  state.busy = true;
  if (reset) { resetQuery({ clearModel, preserveSummary: filtersOnly }); renderAll(); }
  state.page = 0;
  renderRecords();
  syncTrendGrouping();
  $('#trend-chart').setAttribute('aria-busy', 'true');
  const params = { ...state.range, ...(state.model ? { model: state.model } : {}) };
  const device = state.selected;
  const pager = new CursorPager(({ limit, cursor, signal }) => query('earnings', { ...params, limit, ...(cursor ? { cursor } : {}) }, device, signal));
  state.pager = pager;
  const jobs = [
    ...(!filtersOnly ? [
      ['health', query('health')], ...summaryJobs(device),
    ] : []),
    ['trend', query('stats', { ...params, group_by: state.trendGroup, timezone: DASHBOARD_TIMEZONE })],
    ['models', query('stats', { ...state.range, group_by: 'model', timezone: DASHBOARD_TIMEZONE })],
    ['earnings', pager.page(0, state.pageSize)],
  ];
  const results = await Promise.allSettled(jobs.map(([, promise]) => promise));
  if (generation !== state.generation) return;
  const errors = [];
  if (!filtersOnly) state.summaryErrors = [];
  results.forEach((result, index) => {
    const key = jobs[index][0];
    if (result.status === 'fulfilled') {
      if (key === 'health') state.health.set(state.selected, result.value);
      else state.data[key] = result.value;
    } else {
      if (key === 'health') state.health.set(state.selected, { error: result.reason.message });
      else {
        delete state.data[key];
        (['overview', 'lastHour', 'lastDay'].includes(key) ? state.summaryErrors : errors).push(result.reason.message);
      }
    }
  });
  state.filterErrors = errors;
  state.errors = [...new Set([...state.summaryErrors, ...state.filterErrors])];
  state.busy = false;
  $('#trend-chart').setAttribute('aria-busy', 'false');
  if (results.some(result => result.status === 'fulfilled')) state.fetchedAt = new Date();
  renderAll(); updateClock();
}

async function loadDeviceList() {
  state.devices = connections.list(); state.selected = connections.selected;
  renderDevices();
}

async function checkDevices() {
  const ids = deviceChoices().map(device => device.id);
  const results = await Promise.allSettled(ids.map(id => query('health', null, id)));
  results.forEach((result, index) => state.health.set(ids[index], result.status === 'fulfilled' ? result.value : { error: result.reason.message }));
  renderDevices(); renderAlerts();
}

function setView(view, { load = true } = {}) {
  const changed = state.view !== view;
  if (state.view === 'query' && view !== 'query') deviceUI.leaveQuery();
  state.view = view; $('#main').dataset.view = view;
  const needsLoad = ['analysis', 'records'].includes(view) && activateEarningsView(view);
  $('#overview-topbar').hidden = view !== 'overview';
  const titles = { overview: '首页', analysis: '收益分析', records: '收益明细', devices: '我的设备', query: '数据查询', alerts: '告警记录' };
  const slot = $(view === 'records' ? '#records-filters-slot' : '#trend-filters-slot');
  if ($('#earnings-filters').parentElement !== slot) slot.append($('#earnings-filters'));
  document.title = `Bloomer · ${titles[view]}`;
  $$('.nav-item').forEach(button => { button.classList.toggle('active', button.dataset.view === view); if (button.dataset.view === view) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current'); });
  if (view === 'devices') { checkDevices(); deviceUI.refresh(); controlUI.refresh(true); }
  if (view === 'alerts') alertUI.refresh();
  if (view === 'query') deviceUI.enterQuery();
  if (view === 'overview' && load) homeRevenue.refresh();
  if (view === 'analysis' || view === 'records') {
    renderAll();
    if (needsLoad && load) loadData({ reset: true, filtersOnly: true });
  }
  renderAlerts(); updateClock();
  history.replaceState(null, '', `#${view}`);
  if (changed) window.scrollTo({ top: 0, behavior: 'auto' });
}

async function selectDevice(id) {
  try { connections.select(id); } catch (error) { toast(error.message); }
  state.selected = id; await loadData({ reset: true, clearModel: true }); await alertUI.refreshIfVisible();
}
$$('.nav-item').forEach(button => button.addEventListener('click', () => setView(button.dataset.view)));
$('#device-select').addEventListener('change', event => selectDevice(event.target.value));
$('#refresh').addEventListener('click', () => {
  if (state.view === 'alerts') { alertUI.refresh(); return; }
  if (state.view === 'query') { deviceUI.refresh(); deviceUI.queryHistory(); return; }
  if (state.view === 'overview' || state.view === 'devices') {
    deviceUI.refresh(); checkDevices(); refreshSummary();
    if (state.view === 'overview') homeRevenue.refresh();
    if (state.view === 'devices') controlUI.refresh(true);
    return;
  }
  state.range = timeRange(state.period, DASHBOARD_TIMEZONE, new Date(), state.custom); loadData();
});
function selectPeriod(period) {
  state.period = period;
  $('#period-select').value = period;
  $$('[data-period]').forEach(button => { const selected = button.dataset.period === period; button.classList.toggle('selected', selected); button.setAttribute('aria-pressed', String(selected)); });
  $('#date-form').hidden = true; loadData({ reset: true, filtersOnly: true });
}
$('#period-select').addEventListener('change', event => selectPeriod(event.target.value));
$$('[data-period]').forEach(button => button.addEventListener('click', () => selectPeriod(button.dataset.period)));
$$('[data-group]').forEach(button => button.addEventListener('click', () => {
  const group = button.dataset.group;
  if (state.busy || state.trendGroup === group || !trendGroups(state.range, DASHBOARD_TIMEZONE).includes(group)) return;
  state.trendGroup = group;
  delete state.data.trend;
  loadData({ filtersOnly: true }); renderMetrics(); renderTrend();
}));
$('#custom-range').addEventListener('click', () => {
  const form = $('#date-form'); form.hidden = !form.hidden;
  form.elements.from.value = state.custom.from || dateKey(state.range.from, DASHBOARD_TIMEZONE);
  form.elements.to.value = state.custom.to || dateKey(Date.parse(state.range.to) - 1, DASHBOARD_TIMEZONE);
  form.elements.from.max = form.elements.to.max = dateKey(new Date(), DASHBOARD_TIMEZONE);
  $('#date-error').textContent = '';
} );
$('#cancel-range').addEventListener('click', () => { $('#date-form').hidden = true; });
$('#date-form').addEventListener('submit', event => {
  event.preventDefault(); const form = event.target;
  const custom = { from: form.elements.from.value, to: form.elements.to.value };
  try { timeRange('custom', DASHBOARD_TIMEZONE, new Date(), custom); } catch (error) { $('#date-error').textContent = error.message; return; }
  state.period = 'custom'; state.custom = custom;
  $('#period-select').value = 'custom';
  $$('[data-period]').forEach(button => { button.classList.remove('selected'); button.setAttribute('aria-pressed', 'false'); });
  form.hidden = true; loadData({ reset: true, filtersOnly: true });
});
$$('[data-model-filter]').forEach(select => select.addEventListener('change', event => {
  state.exportController?.abort(); state.model = event.target.value;
  for (const key of ['earnings', 'trend']) delete state.data[key];
  loadData({ filtersOnly: true }); renderMetrics(); renderTrend();
}));

async function pageTo(index) {
  if (state.busy || !state.pager) return;
  const generation = ++state.generation;
  const controller = new AbortController();
  state.pageController = controller; state.pendingPage = index;
  state.busy = true; renderRecords();
  try {
    const response = await state.pager.page(index, state.pageSize, { signal: controller.signal });
    if (generation !== state.generation) return;
    state.data.earnings = response;
    state.page = response.index;
    if (response.index !== index) toast(`已到最后一页（第 ${response.index + 1} 页）。`);
  } catch (error) { if (generation === state.generation && error.name !== 'AbortError') toast(error.message); }
  finally {
    if (generation === state.generation) {
      state.busy = false; state.pageController = null; state.pendingPage = null;
      $('#page-input').value = state.page + 1;
      renderRecords(); renderAlerts();
    }
  }
}
$('#next-page').addEventListener('click', () => pageTo(state.page + 1));
$('#prev-page').addEventListener('click', () => pageTo(state.page - 1));
$('#page-jump-form').addEventListener('submit', event => {
  event.preventDefault();
  if (event.target.reportValidity()) pageTo(Number($('#page-input').value) - 1);
});
$('#page-size-form').addEventListener('submit', event => {
  event.preventDefault();
  if (!event.target.reportValidity() || state.busy) return;
  const size = Number($('#page-size').value);
  if (size === state.pageSize) return;
  state.exportController?.abort();
  state.pageSize = size; preference('pageSize', size);
  state.data.earnings = null; loadData();
});
$('#cancel-page').addEventListener('click', () => state.pageController?.abort());

$('#export-csv').addEventListener('click', async () => {
  if (state.exporting) return;
  state.exporting = true; state.exportController = new AbortController(); renderRecords();
  const status = $('#export-status'); status.hidden = false;
  const selected = state.selected, range = { ...state.range }, model = state.model;
  const items = [], seen = new Set(); let cursor = null;
  try {
    do {
      status.textContent = `正在导出所选条件的全部明细…已读取 ${number(items.length)} 条`;
      const response = await query('earnings', { ...range, ...(model ? { model } : {}), limit: 500, ...(cursor ? { cursor } : {}) }, selected, state.exportController.signal);
      for (const item of response.items) if (!seen.has(item.record_id)) { seen.add(item.record_id); items.push(item); }
      if (items.length > 100000) throw new Error('所选范围超过 100,000 条，请缩小时间范围后导出。');
      if (response.next_cursor === cursor && cursor) throw new Error('设备返回了重复游标，请刷新后重试。');
      cursor = response.next_cursor;
    } while (cursor);
    const url = URL.createObjectURL(new Blob([csvRows(items)], { type: 'text/csv;charset=utf-8' }));
    const link = document.createElement('a'); link.href = url; link.download = `bloomer-${selected === 'demo' ? 'demo-' : ''}${dateKey(range.from, DASHBOARD_TIMEZONE)}.csv`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
    status.textContent = `已导出 ${number(items.length)} 条明细 · USD 精确到 6 位小数 · 时间采用北京时间（UTC+8）。`;
  } catch (error) { status.textContent = error.name === 'AbortError' ? '筛选已改变，本次导出已取消。' : error.message; }
  finally { state.exporting = false; state.exportController = null; renderRecords(); }
});

$$('[data-action="connect"]').forEach(button => button.addEventListener('click', () => {
  $('#connect-form').reset(); $('#connect-form').elements.token.type = 'password'; $('#toggle-token').textContent = '显示'; $('#toggle-token').setAttribute('aria-pressed', 'false'); $('#connection-result').hidden = true;
  $('#connect-dialog').showModal();
}));
$('#close-dialog').addEventListener('click', () => $('#connect-dialog').close());
$('#connect-dialog').addEventListener('close', () => { $('#connect-form').elements.token.value = ''; });
$('#toggle-token').addEventListener('click', event => {
  const input = $('#connect-form').elements.token; input.type = input.type === 'password' ? 'text' : 'password';
  event.target.textContent = input.type === 'password' ? '显示' : '隐藏'; event.target.setAttribute('aria-label', input.type === 'password' ? '显示令牌' : '隐藏令牌'); event.target.setAttribute('aria-pressed', String(input.type === 'text'));
});
let connecting = false;
async function connect(save) {
  const form = $('#connect-form'); if (connecting || !form.reportValidity()) return;
  connecting = true; $('#test-connection').disabled = $('#save-device').disabled = true;
  const result = $('#connection-result'); result.hidden = false; result.className = ''; result.textContent = '正在连接设备，请稍候…';
  try {
    const body = Object.fromEntries(new FormData(form));
    const response = await client.connect(body, save);
    if (save) {
      form.elements.token.value = ''; $('#connect-dialog').close(); await loadDeviceList(); await selectDevice(response.device.id); toast('设备已连接。');
    } else {
      result.className = 'success'; result.textContent = `连接成功 · Agent ${response.health.version} · ${healthStatus(response.health).label}`;
    }
  } catch (error) { result.textContent = error.message; }
  finally { connecting = false; $('#test-connection').disabled = $('#save-device').disabled = false; }
}
$('#test-connection').addEventListener('click', () => connect(false));
$('#connect-form').addEventListener('submit', event => { event.preventDefault(); connect(true); });
$('#device-cards').addEventListener('click', async event => {
  const select = event.target.closest('[data-select-device]');
  if (select) { await selectDevice(select.dataset.selectDevice); setView('overview'); return; }
  const remove = event.target.closest('[data-remove-device]');
  if (!remove) return;
  const dialog = $('#remove-dialog'); dialog.returnValue = 'cancel'; dialog.showModal();
  dialog.addEventListener('close', async () => {
    if (dialog.returnValue !== 'confirm') return;
    try { connections.remove(remove.dataset.removeDevice); await loadDeviceList(); await loadData({ reset: true, clearModel: true }); toast('设备连接已移除。'); } catch (error) { toast(error.message); }
  }, { once: true });
});

$('#import-connections').addEventListener('click', () => $('#connection-file').click());
$('#connection-file').addEventListener('change', async event => {
  const file = event.target.files[0]; event.target.value = '';
  if (!file) return;
  $('#import-connections').disabled = true;
  try {
    if (file.size > 256_000) throw new Error('连接文件过大，最多支持 30 台设备。');
    let input;
    try { input = JSON.parse(await file.text()); } catch { throw new Error('请使用有效的设备连接 JSON 文件。'); }
    toast('正在逐台测试连接，全部通过后保存…');
    await client.importConnections(input);
    await loadDeviceList(); await loadData({ reset: true, clearModel: true }); toast('设备连接已导入此浏览器。');
  } catch (error) { toast(error.message); }
  finally { $('#import-connections').disabled = false; }
});

function updateClock() {
  if (state.view === 'query' || state.view === 'devices') {
    $('#last-refresh').textContent = state.deviceFetchedAt ? `最新状态刷新 ${dateTime(state.deviceFetchedAt)} · 每 60 秒更新${state.view === 'query' ? '；历史明细按查询范围展示' : ''}` : '等待设备状态刷新';
    return;
  }
  $('#last-refresh').textContent = state.fetchedAt ? `最近刷新 ${dateTime(state.fetchedAt)} · 每 60 秒自动更新` : '等待首次刷新';
}

// Apply topbar visibility before restoring browser connections, including direct page links.
$('#overview-topbar').hidden = ['analysis', 'records', 'devices', 'query', 'alerts'].includes(location.hash.slice(1));
try {
  if (location.protocol === 'file:') throw new Error('请通过普通静态服务器打开看板，例如 http://127.0.0.1:18080；不支持 file:// 运行。');
  connections.load();
  await loadDeviceList(); resetQuery();
  const view = location.hash.slice(1); if (['overview', 'analysis', 'records', 'devices', 'query', 'alerts'].includes(view)) setView(view, { load: false });
  await loadData();
} catch (error) { $('#alerts').innerHTML = `<div class="alert error">${escape(error.message)}</div>`; }
function autoRefresh() {
  updateClock();
  if (document.hidden || state.busy || state.exporting || $('#connect-dialog').open || $('#control-confirm-dialog').open || $('#remove-dialog').open) return;
  if (state.view === 'alerts') { alertUI.refresh(); return; }
  deviceUI.refresh();
  if (state.view === 'query') return;
  if (state.view === 'overview' || state.view === 'devices') {
    checkDevices(); refreshSummary();
    if (state.view === 'overview') homeRevenue.refresh();
    return;
  }
  if (state.view === 'analysis' || state.page === 0) {
    state.range = timeRange(state.period, DASHBOARD_TIMEZONE, new Date(), state.custom); loadData();
  } else { checkDevices(); refreshSummary(); }
}
setInterval(autoRefresh, 60_000);
document.addEventListener('visibilitychange', () => {
  if (!document.hidden) autoRefresh();
});
new ResizeObserver(() => renderTrend()).observe($('#trend-chart'));
