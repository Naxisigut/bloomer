import { DASHBOARD_TIMEZONE, money, sumMoney, sumCount, timeRange, rollingRange, dateKey, csvRows } from './data.js';
import { createAnalysisPage } from './analysis-page.js';
import { CursorPager } from './pagination.js';
import { createDeviceUI } from './device-ui.js';
import { createAlertUI } from './alert-ui.js';
import { createControlUI } from './control-ui.js';
import { DeviceStore } from './connections.js';
import { createAgentClient } from './agent-client.js';
import { createHomeRevenueChart } from './home-revenue.js';
import { enrichHealthTiming, correctedNow as deviceCorrectedNow } from './clock.js';
import { nextAutoRefreshDelayMs } from './refresh-schedule.js';

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
  custom: {}, model: '', view: 'overview', data: {}, health: new Map(), page: 0,
  pageSize: Number(preference('pageSize') || 10), pager: null, pageController: null, pendingPage: null,
  generation: 0, busy: false, exporting: false, exportController: null, fetchedAt: null, errors: [], summaryErrors: [], filterErrors: [], range: null, recordsLoaded: false,
};
if (!Number.isInteger(state.pageSize) || state.pageSize < 1 || state.pageSize > 500) state.pageSize = 10;
$('#page-size').value = state.pageSize;
const demoDevice = { id: 'demo', name: '演示设备', url: '演示数据', source: 'demo' };
const selectedDevice = () => state.selected === 'demo' ? demoDevice : state.devices.find(device => device.id === state.selected);
const deviceChoices = () => [...state.devices, demoDevice];
let toastTimer;
let autoRefreshTimer = null, autoRefreshFailStreak = 0, nextAutoRefreshAt = null;
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
const deviceClockOffset = (id = state.selected) => state.health.get(id)?.clockOffset ?? 0;
const correctedNow = (id = state.selected) => deviceCorrectedNow(deviceClockOffset(id));
async function fetchHealth(id) {
  const started = Date.now();
  const value = await query('health', null, id);
  return enrichHealthTiming(value, started, Date.now());
}
const homeRevenue = createHomeRevenueChart({ query, selectedDevice, currentView: () => state.view, escape, correctedNow: () => correctedNow() });
function syncSelectedDevice(id) {
  try { connections.select(id); } catch (error) { toast(error.message); return; }
  state.selected = id;
  analysisPage.onGlobalDeviceChange(id);
  renderDevices();
}
const analysisPage = createAnalysisPage({
  query, deviceChoices, syncDevice: syncSelectedDevice, escape, correctedNow, currentView: () => state.view,
});
const deviceUI = createDeviceUI({ query, selectedDevice, deviceChoices, selectDevice, dateTime, escape, onRefresh: () => { state.deviceFetchedAt = new Date(); updateClock(); } });

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

function syncRecordsPeriodUi() {
  $$('#records-period [data-period]').forEach(button => {
    const selected = button.dataset.period === state.period;
    button.classList.toggle('selected', selected);
    button.setAttribute('aria-pressed', String(selected));
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
  if (state.view === 'alerts') { $('#alerts').innerHTML = ''; return; }
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
  const { lastHour, lastDay, overview } = state.data;
  $('#last-hour-amount').textContent = money(lastHour ? sumMoney(lastHour.items) : null, 4);
  $('#last-day-amount').textContent = money(lastDay ? sumMoney(lastDay.items) : null, 4);
  $('#available-balance').textContent = money(overview?.official?.available_balance_micro_usd);
  $$('.summary-amount').forEach(element => { element.title = element.textContent; });
  const start = state.range?.from;
  $('#range-label').textContent = start ? `${dateTime(start, true)} — ${dateTime(new Date(Date.parse(state.range.to) - 1), true)}` : '选择日期';
  syncRecordsPeriodUi();
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

function renderAll() { renderDevices(); renderMetrics(); renderRecords(); renderAlerts(); homeRevenue.render(); }

function summaryJobs(device = state.selected) {
  const now = correctedNow(device), params = { group_by: 'day', timezone: DASHBOARD_TIMEZONE };
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

async function loadSummary({ device = state.selected } = {}) {
  const generation = state.generation;
  const jobs = [['health', fetchHealth(device)], ...summaryJobs(device)];
  const results = await Promise.allSettled(jobs.map(([, promise]) => promise));
  if (generation !== state.generation || device !== state.selected) return;
  state.summaryErrors = [];
  results.forEach((result, index) => {
    const key = jobs[index][0];
    if (result.status === 'fulfilled') {
      if (key === 'health') state.health.set(device, result.value);
      else state.data[key] = result.value;
    } else {
      if (key === 'health') state.health.set(device, { error: result.reason.message });
      else { delete state.data[key]; state.summaryErrors.push(result.reason.message); }
    }
  });
  state.errors = [...new Set([...state.summaryErrors, ...state.filterErrors])];
  renderMetrics(); renderAlerts();
}

async function loadRecordsData({ reset = false, clearModel = false } = {}) {
  deviceUI.refresh();
  state.pageController?.abort();
  state.pageController = null; state.pendingPage = null;
  const generation = ++state.generation;
  state.busy = true;
  if (reset) {
    resetQuery({ clearModel, preserveSummary: true });
    renderAll();
  }
  state.page = 0;
  renderRecords();
  const params = { ...state.range, ...(state.model ? { model: state.model } : {}) };
  const device = state.selected;
  const pager = new CursorPager(({ limit, cursor, signal }) => query('earnings', { ...params, limit, ...(cursor ? { cursor } : {}) }, device, signal));
  state.pager = pager;
  const jobs = [
    ['trend', query('stats', { ...params, group_by: 'day', timezone: DASHBOARD_TIMEZONE })],
    ['models', query('stats', { ...state.range, group_by: 'model', timezone: DASHBOARD_TIMEZONE })],
    ['earnings', pager.page(0, state.pageSize)],
  ];
  const results = await Promise.allSettled(jobs.map(([, promise]) => promise));
  if (generation !== state.generation) return;
  const errors = [];
  results.forEach((result, index) => {
    const key = jobs[index][0];
    if (result.status === 'fulfilled') state.data[key] = result.value;
    else { delete state.data[key]; errors.push(result.reason.message); }
  });
  state.filterErrors = errors;
  state.errors = [...new Set([...state.summaryErrors, ...state.filterErrors])];
  state.busy = false;
  state.recordsLoaded = true;
  if (results.some(result => result.status === 'fulfilled')) state.fetchedAt = new Date();
  renderRecords(); renderAlerts(); updateClock();
}

async function loadData({ reset = false, clearModel = false } = {}) {
  if (state.view === 'analysis') return;
  deviceUI.refresh();
  if (state.view === 'overview') homeRevenue.refresh();
  await loadSummary();
  if (state.view === 'records') await loadRecordsData({ reset, clearModel });
  else if (reset) resetQuery({ clearModel, preserveSummary: true });
  renderAll(); updateClock();
}

async function loadDeviceList() {
  state.devices = connections.list(); state.selected = connections.selected;
  renderDevices();
}

async function checkDevices() {
  const ids = deviceChoices().map(device => device.id);
  const results = await Promise.allSettled(ids.map(id => fetchHealth(id)));
  results.forEach((result, index) => state.health.set(ids[index], result.status === 'fulfilled' ? result.value : { error: result.reason.message }));
  renderDevices(); renderAlerts();
}

async function checkCurrentDevice() {
  const id = state.selected;
  try {
    state.health.set(id, await fetchHealth(id));
  } catch (error) {
    state.health.set(id, { error: error.message });
  }
  renderDevices(); renderAlerts();
}

function setView(view, { load = true } = {}) {
  const changed = state.view !== view;
  state.view = view; $('#main').dataset.view = view;
  $('#overview-topbar').hidden = view !== 'overview';
  const titles = { overview: '首页', analysis: '收益分析', records: '收益明细', devices: '我的设备', alerts: '告警记录' };
  document.title = `Bloomer · ${titles[view]}`;
  $$('.nav-item').forEach(button => { button.classList.toggle('active', button.dataset.view === view); if (button.dataset.view === view) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current'); });
  if (view === 'devices') { checkDevices(); deviceUI.refresh(); controlUI.refresh(true); }
  if (view === 'alerts') alertUI.refresh();
  if (view === 'overview' && load) homeRevenue.refresh();
  if (view === 'analysis') {
    analysisPage.onEnter(state.selected);
    renderAlerts();
  } else if (view === 'records') {
    renderAll();
    if (load && (!state.recordsLoaded || changed)) loadRecordsData({ reset: !state.recordsLoaded });
  } else renderAll();
  renderAlerts(); updateClock();
  history.replaceState(null, '', `#${view}`);
  if (changed) window.scrollTo({ top: 0, behavior: 'auto' });
}

async function selectDevice(id) {
  try { connections.select(id); } catch (error) { toast(error.message); }
  state.selected = id;
  state.recordsLoaded = false;
  analysisPage.onGlobalDeviceChange(id);
  if (state.view === 'analysis') { renderDevices(); await alertUI.refreshIfVisible(); return; }
  await loadData({ reset: true, clearModel: true });
  await alertUI.refreshIfVisible();
}
$$('.nav-item').forEach(button => button.addEventListener('click', () => setView(button.dataset.view)));
$('#device-select').addEventListener('change', event => selectDevice(event.target.value));
$('#refresh').addEventListener('click', async () => {
  if (state.view === 'alerts') { alertUI.refresh(); return; }
  if (state.view === 'overview') {
    deviceUI.refresh(); resetAutoRefreshBackoff(); await checkCurrentDevice(); await refreshSummary(); homeRevenue.refresh();
    scheduleAutoRefresh(nextAutoRefreshDelayMs({ health: state.health.get(state.selected), failStreak: autoRefreshFailStreak }));
    return;
  }
  if (state.view === 'devices') {
    deviceUI.refresh(); resetAutoRefreshBackoff(); await checkDevices(); await refreshSummary(); controlUI.refresh(true);
    return;
  }
  if (state.view === 'records') { state.range = timeRange(state.period, DASHBOARD_TIMEZONE, correctedNow(), state.custom); loadRecordsData(); return; }
  loadData();
});
function selectPeriod(period) {
  state.period = period;
  syncRecordsPeriodUi();
  $('#date-form').hidden = true;
  loadRecordsData({ reset: true });
}
$$('#records-period [data-period]').forEach(button => button.addEventListener('click', () => selectPeriod(button.dataset.period)));
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
  form.hidden = true; loadRecordsData({ reset: true });
});
$$('[data-model-filter]').forEach(select => select.addEventListener('change', event => {
  state.exportController?.abort(); state.model = event.target.value;
  for (const key of ['earnings', 'trend']) delete state.data[key];
  loadRecordsData({ reset: true });
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
  state.data.earnings = null; loadRecordsData({ reset: true });
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
    const started = Date.now();
    const response = await client.connect(body, save);
    response.health = enrichHealthTiming(response.health, started, Date.now());
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
  if (state.view === 'devices') {
    $('#last-refresh').textContent = state.deviceFetchedAt ? `最新状态刷新 ${dateTime(state.deviceFetchedAt)} · 每 60 秒更新` : '等待设备状态刷新';
    return;
  }
  const retry = autoRefreshFailStreak > 0 && nextAutoRefreshAt ? ` · 下次重试 ${dateTime(nextAutoRefreshAt)}` : '';
  const intervalHint = autoRefreshFailStreak > 0 ? '' : ' · 自动更新已对齐采集节奏';
  $('#last-refresh').textContent = state.fetchedAt ? `最近刷新 ${dateTime(state.fetchedAt)}${intervalHint}${retry}` : '等待首次刷新';
}

// Apply topbar visibility before restoring browser connections, including direct page links.
$('#overview-topbar').hidden = ['analysis', 'records', 'devices', 'alerts'].includes(location.hash.slice(1));
try {
  if (location.protocol === 'file:') throw new Error('请通过普通静态服务器打开看板，例如 http://127.0.0.1:18080；不支持 file:// 运行。');
  connections.load();
  await loadDeviceList(); resetQuery();
  const view = location.hash.slice(1);
  if (view === 'query') { history.replaceState(null, '', '#overview'); setView('overview', { load: false }); }
  else if (['overview', 'analysis', 'records', 'devices', 'alerts'].includes(view)) setView(view, { load: false });
  await loadData();
  scheduleAutoRefresh(nextAutoRefreshDelayMs({ health: state.health.get(state.selected), failStreak: 0 }));
} catch (error) { $('#alerts').innerHTML = `<div class="alert error">${escape(error.message)}</div>`; }
function resetAutoRefreshBackoff() { autoRefreshFailStreak = 0; }
function scheduleAutoRefresh(delayMs) {
  if (autoRefreshTimer) clearTimeout(autoRefreshTimer);
  nextAutoRefreshAt = new Date(Date.now() + delayMs);
  autoRefreshTimer = setTimeout(() => { autoRefreshTimer = null; runAutoRefreshCycle(); }, delayMs);
  updateClock();
}
async function runAutoRefreshCycle() {
  await autoRefresh();
  if (document.hidden) return;
  const health = state.health.get(state.selected);
  scheduleAutoRefresh(nextAutoRefreshDelayMs({ health, failStreak: autoRefreshFailStreak }));
}
async function autoRefresh() {
  updateClock();
  if (document.hidden || state.busy || state.exporting || $('#connect-dialog').open || $('#control-confirm-dialog').open || $('#remove-dialog').open) return;
  if (state.view === 'alerts') { await alertUI.refresh(); return; }
  deviceUI.refresh();
  if (state.view === 'overview') {
    let failed = false;
    try { await checkCurrentDevice(); } catch { failed = true; }
    await refreshSummary();
    if (state.summaryErrors.length) failed = true;
    if (state.health.get(state.selected)?.error) failed = true;
    autoRefreshFailStreak = failed ? autoRefreshFailStreak + 1 : 0;
    homeRevenue.refresh();
    return;
  }
  if (state.view === 'devices') {
    await checkDevices(); await refreshSummary();
    return;
  }
  if (state.view === 'records' && state.page === 0) {
    state.range = timeRange(state.period, DASHBOARD_TIMEZONE, correctedNow(), state.custom);
    loadRecordsData();
    return;
  }
  if (state.view === 'analysis') return;
  await checkCurrentDevice(); await refreshSummary();
}
document.addEventListener('visibilitychange', () => {
  if (document.hidden) { if (autoRefreshTimer) clearTimeout(autoRefreshTimer); autoRefreshTimer = null; return; }
  if (autoRefreshTimer) clearTimeout(autoRefreshTimer);
  autoRefreshTimer = null;
  runAutoRefreshCycle();
});
