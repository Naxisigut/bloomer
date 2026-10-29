import { DASHBOARD_TIMEZONE, money, sumMoney, rollingRange } from './data.js';
import { createAnalysisPage } from './analysis-page.js';
import { createDeviceUI } from './device-ui.js';
import { createAlertUI } from './alert-ui.js';
import { createControlUI } from './control-ui.js';
import { createControlModelsStore } from './control-models.js';
import { createNetworkRatesUI } from './network-rates.js';
import { createNetworkSwitch } from './network-switch.js';
import { createContextMenu } from './context-menu.js';
import { displayModelName, displayModelNames } from './models.js';
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
  network: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c2.6 3.2 3.9 6.6 3.9 9s-1.3 5.8-3.9 9M12 3c-2.6 3.2-3.9 6.6-3.9 9s1.3 5.8 3.9 9"/>',
  refresh: '<path d="M20 7v5h-5M4 17v-5h5"/><path d="M6.1 6.1a8 8 0 0 1 13.2 3L20 12M4 12l.7 2.9a8 8 0 0 0 13.2 3"/>',
  add: '<path d="M12 5v14M5 12h14"/>',
  calendar: '<rect x="3.5" y="5" width="17" height="15.5" rx="3"/><path d="M8 3v4M16 3v4M3.5 10h17"/>',
  clock: '<circle cx="12" cy="13" r="8"/><path d="M12 9v4l2.5 2.5M9.5 2.5h5"/>',
  day: '<circle cx="12" cy="12" r="4"/><path d="M12 2.5v2M12 19.5v2M2.5 12h2M19.5 12h2M5.3 5.3l1.4 1.4M17.3 17.3l1.4 1.4M5.3 18.7l1.4-1.4M17.3 6.7l1.4-1.4"/>',
  wallet: '<path d="M19 7V5.5A1.5 1.5 0 0 0 17.5 4h-12A2.5 2.5 0 0 0 3 6.5v11A2.5 2.5 0 0 0 5.5 20h13a1.5 1.5 0 0 0 1.5-1.5V15"/><path d="M3 6.5A2.5 2.5 0 0 0 5.5 9h13A1.5 1.5 0 0 1 20 10.5V15h-4a2 2 0 0 1 0-4h4"/>',
  spark: '<path d="M12 3l1.9 5.1L19 10l-5.1 1.9L12 17l-1.9-5.1L5 10l5.1-1.9z"/><path d="M19 16l.8 2.2L22 19l-2.2.8L19 22l-.8-2.2L16 19l2.2-.8z"/>',
  activity: '<path d="M3 12h4l3-8 4 16 3-8h4"/>',
  thermo: '<path d="M14 14.8V5a2 2 0 0 0-4 0v9.8a4 4 0 1 0 4 0z"/><path d="M12 9v8"/>',
  memory: '<rect x="4" y="4" width="16" height="16" rx="2.5"/><rect x="8.5" y="8.5" width="7" height="7" rx="1"/><path d="M9 1.5V4M15 1.5V4M9 20v2.5M15 20v2.5M1.5 9H4M1.5 15H4M20 9h2.5M20 15h2.5"/>',
  cube: '<path d="M12 2.8 20 7v10l-8 4.2L4 17V7z"/><path d="m4 7 8 4.2L20 7M12 11.2v10"/>',
};
const pageCopy = {
  overview: ['今日概览', '实时查看设备状态与 Darkbloom 收益'],
  analysis: ['收益分析', '按时间与模型多维度分析收益，点击「查询」后更新，可导出明细'],
  network: ['全网数据', '对照组模型收益估算与推荐'],
  devices: ['我的设备', '管理连接、查看运行状态与远程操作 Darkbloom'],
  alerts: ['告警记录', '设备告警与推送历史，可发送测试推送'],
};
const todayLabel = new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, month: 'long', day: 'numeric', weekday: 'short' });
const icon = name => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[name]}</svg>`;
$$('[data-icon]').forEach(element => { element.innerHTML = icon(element.dataset.icon); });

function preference(key, value) {
  try { if (value === undefined) return localStorage.getItem(`bloomer.${key}`); localStorage.setItem(`bloomer.${key}`, value); } catch { return null; }
}

const connections = new DeviceStore();
const client = createAgentClient(connections);
const state = {
  devices: [], selected: null, view: 'overview', data: {}, health: new Map(),
  generation: 0, recentController: null, recentBusy: false, recentError: null,
  exporting: false, fetchedAt: null, errors: [], summaryErrors: [],
};
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
const beijingDateKey = value => new Intl.DateTimeFormat('en-CA', { timeZone: DASHBOARD_TIMEZONE, year: 'numeric', month: '2-digit', day: '2-digit' }).format(new Date(value));
function recentEarningsTime(value) {
  if (!value) return '尚无记录';
  const instant = new Date(value);
  const sameDay = beijingDateKey(instant) === beijingDateKey(correctedNow());
  if (sameDay) return new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(instant);
  return dateTime(value);
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
const analysisPage = createAnalysisPage({
  query, escape, correctedNow, currentView: () => state.view,
  setExporting: value => { state.exporting = value; },
});
const deviceUI = createDeviceUI({ query, selectedDevice, escape, onRefresh: () => { state.deviceFetchedAt = new Date(); updateClock(); } });

const alertUI = createAlertUI({ query, selectedDevice, currentView: () => state.view, escape, dateTime });
// Model discovery is global per device and shared by both model blocks, so
// re-entering the devices view never repeats the CLI probe.
const controlModels = createControlModelsStore({ query });
const controlUI = createControlUI({ submitControl: (id, body) => client.submit(id, body), submitLongTaskRestart: (id, enabled) => client.submitLongTaskRestart(id, enabled), models: controlModels, query, selectedDevice, currentView: () => state.view, escape, dateTime, refreshState: () => deviceUI.refresh() });
const contextMenu = createContextMenu();
// The switcher is created first because the network view renders its buttons;
// its callbacks close over `networkRatesUI`, which is assigned just after.
const networkSwitcher = createNetworkSwitch({
  submitControl: (id, body) => client.submit(id, body),
  selectedDevice,
  // A submit only toggles the row buttons, so a device change or a new job
  // repaints the buttons instead of the whole table.
  onChange: () => networkRatesUI.syncActionButtons(),
  // The job's outcome is read on the remote-control panel, so a download is
  // assumed to have changed the local list and the next visit re-reads it.
  onSubmitted: body => { if (body.action === 'download') controlModels.invalidate(state.selected); },
});
const networkRatesUI = createNetworkRatesUI({ query, selectedDevice, currentView: () => state.view, escape, switcher: networkSwitcher, contextMenu });

function renderDevices() {
  const active = state.selected;
  $('#nav-device-count').textContent = state.devices.length;
  const choices = deviceChoices();
  $('#device-select').innerHTML = choices.map(device => `<option value="${escape(device.id)}" ${device.id === active ? 'selected' : ''}>${escape(device.name)}</option>`).join('');
  $('#device-spot-name').textContent = choices.find(device => device.id === active)?.name || '—';
  $('#demo-banner').hidden = active !== 'demo';
  $('#device-cards').innerHTML = deviceChoices().map(device => {
    const health = state.health.get(device.id);
    return `<article class="device-card ${device.id === active ? 'selected' : ''}"><div class="device-card-header"><span class="device-card-avatar" aria-hidden="true">${icon('devices')}</span><h3>${escape(device.name)}${device.id === 'demo' ? ' · 演示' : ''}</h3></div><p class="device-url">${escape(device.url)}</p>${health?.error ? `<p class="device-card-note">${escape(health.error)}</p>` : ''}<div class="device-card-actions"><button class="button ${device.id === active ? 'button-dark' : 'button-white'}" data-select-device="${escape(device.id)}">${device.id === active ? '当前设备' : '切换到此设备'}</button>${device.id !== 'demo' ? `<button class="button remove-device" data-remove-device="${escape(device.id)}">移除</button>` : ''}</div></article>`;
  }).join('');
  deviceUI.render();
  controlUI.render();
  networkRatesUI.render();
  alertUI.render();
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
  if (meta?.coverage?.gap_count || health?.coverage?.gap_count) alerts.push(['', '部分历史收益可能尚未同步完整。']);
  if (state.view === 'overview' && state.recentError && !alerts.some(([, message]) => message === state.recentError)) alerts.push(['error', state.recentError]);
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
  for (const id of ['last-hour-amount', 'last-day-amount', 'available-balance']) {
    const element = $(`#${id}`);
    element.title = element.textContent;
  }
}

function renderRecent() {
  const page = state.data.recent;
  const body = $('#recent-earnings-body');
  if (!body) return;
  const empty = state.recentBusy ? '正在读取收益记录…' : page ? '暂无收益明细' : '明细读取失败，请检查设备后刷新。';
  body.innerHTML = page?.items.length ? page.items.map(item => `<tr><td title="${escape(item.model)}">${escape(displayModelName(item.model))}</td><td class="amount-cell">${escape(money(item.amount_micro_usd, 6))}</td><td class="token-cell" title="输入 ${escape(number(item.prompt_tokens))} · 输出 ${escape(number(item.completion_tokens))}">${escape(number(item.prompt_tokens))} / ${escape(number(item.completion_tokens))}</td><td>${escape(recentEarningsTime(item.created_at))}</td></tr>`).join('') : `<tr><td colspan="4"><div class="empty-state">${empty}</div></td></tr>`;
  body.setAttribute('aria-busy', String(state.recentBusy));
}

function renderAll() { renderDevices(); renderMetrics(); renderRecent(); renderAlerts(); homeRevenue.render(); }

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
  state.errors = [...state.summaryErrors];
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
  state.errors = [...state.summaryErrors];
  renderMetrics(); renderAlerts();
}

async function loadRecentEarnings() {
  state.recentController?.abort();
  const controller = new AbortController();
  state.recentController = controller;
  state.recentBusy = true;
  state.recentError = null;
  const device = state.selected;
  renderRecent();
  try {
    const response = await query('earnings', { limit: 50 }, device, controller.signal);
    if (state.recentController !== controller || device !== state.selected) return;
    state.data.recent = { ...response, items: response.items.slice(0, 50) };
    state.fetchedAt = new Date();
  } catch (error) {
    if (state.recentController !== controller || error.name === 'AbortError') return;
    delete state.data.recent;
    state.recentError = error.message;
  } finally {
    if (state.recentController === controller) {
      state.recentBusy = false;
      state.recentController = null;
      renderRecent();
      renderAlerts();
      updateClock();
    }
  }
}

async function loadData() {
  if (state.view === 'analysis') return;
  deviceUI.refresh();
  if (state.view === 'overview') {
    homeRevenue.refresh();
    loadRecentEarnings();
  }
  await loadSummary();
  renderAll();
  updateClock();
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
  const titles = { overview: '首页', analysis: '收益分析', network: '全网数据', devices: '我的设备', alerts: '告警记录' };
  document.title = `Bloomer · ${titles[view]}`;
  [$('#page-title').textContent, $('#page-subtitle').textContent] = pageCopy[view];
  $('.page-heading').hidden = view === 'overview';
  $$('.nav-item').forEach(button => { button.classList.toggle('active', button.dataset.view === view); if (button.dataset.view === view) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current'); });
  if (view === 'devices') { checkDevices(); deviceUI.refresh(); controlUI.refresh(); }
  if (view === 'network') networkRatesUI.refresh();
  if (view === 'alerts') alertUI.refresh();
  if (view === 'overview' && load) { homeRevenue.refresh(); loadRecentEarnings(); }
  if (view === 'analysis') {
    analysisPage.onEnter(state.selected);
    renderAlerts();
  } else renderAll();
  renderAlerts(); updateClock();
  history.replaceState(null, '', `#${view}`);
  if (changed) window.scrollTo({ top: 0, behavior: 'auto' });
  if (view === 'devices' || view === 'network') syncDevicesSticky();
}

async function selectDevice(id) {
  try { connections.select(id); } catch (error) { toast(error.message); }
  state.selected = id;
  delete state.data.recent;
  state.recentError = null;
  analysisPage.onGlobalDeviceChange(id);
  if (state.view === 'analysis') { renderDevices(); renderRecent(); await alertUI.refreshIfVisible(); return; }
  await loadData();
  await alertUI.refreshIfVisible();
  await networkRatesUI.refreshIfVisible();
}
const deviceTabs = $$('[data-device-tab]');
function selectDeviceTab(tab, { focus = false } = {}) {
  const active = deviceTabs.find(button => button.dataset.deviceTab === tab) || deviceTabs[0];
  deviceTabs.forEach(button => {
    const selected = button === active;
    button.classList.toggle('selected', selected);
    button.setAttribute('aria-selected', String(selected));
    button.tabIndex = selected ? 0 : -1;
    $(`#${button.getAttribute('aria-controls')}`).hidden = !selected;
  });
  if (focus) active.focus();
  preference('deviceTab', active.dataset.deviceTab);
}
const stackedSidebar = matchMedia('(max-width:900px)');
const devicesStickyTop = () => stackedSidebar.matches ? $('.sidebar').offsetHeight : 0;
function syncDevicesSticky() {
  const top = `${devicesStickyTop()}px`;
  $('#devices-view').style.setProperty('--devices-sticky-top', top);
  $('#network-rates-panel').style.setProperty('--devices-sticky-top', top);
}
function showDeviceTab(tab, options) {
  const top = devicesStickyTop(), offset = $('#devices-sticky-sentinel').getBoundingClientRect().top - top;
  selectDeviceTab(tab, options);
  if (offset < 0) window.scrollTo({ top: window.scrollY + offset, behavior: 'auto' });
}
deviceTabs.forEach(button => button.addEventListener('click', () => showDeviceTab(button.dataset.deviceTab)));
$('#device-tabs').addEventListener('keydown', event => {
  const step = { ArrowRight: 1, ArrowLeft: -1 }[event.key];
  if (!step) return;
  event.preventDefault();
  const index = deviceTabs.findIndex(button => button.classList.contains('selected'));
  showDeviceTab(deviceTabs[(index + step + deviceTabs.length) % deviceTabs.length].dataset.deviceTab, { focus: true });
});
selectDeviceTab(preference('deviceTab') || 'state');
window.addEventListener('resize', syncDevicesSticky);
$$('.nav-item').forEach(button => button.addEventListener('click', () => setView(button.dataset.view)));
$('#device-select').addEventListener('change', event => selectDevice(event.target.value));
$('#refresh').addEventListener('click', async () => {
  if (state.view === 'alerts') { alertUI.refresh(); return; }
  if (state.view === 'overview') {
    deviceUI.refresh(); resetAutoRefreshBackoff(); await checkCurrentDevice(); await refreshSummary(); homeRevenue.refresh(); loadRecentEarnings();
    scheduleAutoRefresh(nextAutoRefreshDelayMs({ health: state.health.get(state.selected), failStreak: autoRefreshFailStreak }));
    return;
  }
  if (state.view === 'network') {
    resetAutoRefreshBackoff(); await refreshSummary(); networkRatesUI.refresh();
    return;
  }
  if (state.view === 'devices') {
    deviceUI.refresh(); resetAutoRefreshBackoff(); await checkDevices(); await refreshSummary(); controlUI.refresh();
    return;
  }
  loadData();
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
      result.className = 'success'; result.textContent = `连接成功 · Agent ${response.health.version}`;
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
    try { connections.remove(remove.dataset.removeDevice); await loadDeviceList(); delete state.data.recent; state.recentError = null; await loadData(); toast('设备连接已移除。'); } catch (error) { toast(error.message); }
  }, { once: true });
});

function updateClock() {
  $('#today-label').textContent = todayLabel.format(correctedNow());
  if (state.view === 'devices') return;
  const retry = autoRefreshFailStreak > 0 && nextAutoRefreshAt ? ` · 下次重试 ${dateTime(nextAutoRefreshAt)}` : '';
  const intervalHint = autoRefreshFailStreak > 0 ? '' : ' · 自动更新已对齐采集节奏';
  $('#last-refresh').textContent = state.fetchedAt ? `最近刷新 ${dateTime(state.fetchedAt)}${intervalHint}${retry}` : '等待首次刷新';
}

try {
  if (location.protocol === 'file:') throw new Error('请通过普通静态服务器打开看板，例如 http://127.0.0.1:18080；不支持 file:// 运行。');
  connections.load();
  await loadDeviceList();
  const view = location.hash.slice(1);
  if (view === 'query' || view === 'records') { history.replaceState(null, '', '#overview'); setView('overview', { load: false }); }
  else if (['overview', 'analysis', 'network', 'devices', 'alerts'].includes(view)) setView(view, { load: false });
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
  if (document.hidden || state.exporting || $('#connect-dialog').open || $('#control-confirm-dialog').open || $('#remove-dialog').open) return;
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
    loadRecentEarnings();
    return;
  }
  if (state.view === 'network') {
    await refreshSummary(); await networkRatesUI.refresh();
    return;
  }
  if (state.view === 'devices') {
    await checkDevices(); await refreshSummary();
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
