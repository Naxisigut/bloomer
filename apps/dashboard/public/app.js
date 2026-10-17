import { money, sumMoney, sumCount, timeRange, dateKey, csvRows } from './data.js';

const $ = selector => document.querySelector(selector);
const $$ = selector => [...document.querySelectorAll(selector)];
const escape = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
const number = value => new Intl.NumberFormat('en-US').format(value);
const colors = ['#365c43', '#8ba86c', '#bdce9a', '#dce5c7', '#e9eddf'];
const icons = {
  overview: '<rect x="3" y="3" width="7" height="7" rx="1.3"/><rect x="14" y="3" width="7" height="7" rx="1.3"/><rect x="3" y="14" width="7" height="7" rx="1.3"/><rect x="14" y="14" width="7" height="7" rx="1.3"/>',
  records: '<rect x="5" y="3" width="14" height="18" rx="2"/><path d="M9 8h6M9 12h6M9 16h4"/>',
  devices: '<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8M12 17v4M7 13h4"/>',
  shield: '<path d="M12 3 4 6v6c0 4 4 7 8 9 4-2 8-5 8-9V6l-8-3Z"/><path d="m8 12 3 3 5-6"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  refresh: '<path d="M20 7v5h-5M4 17v-5h5"/><path d="M6 7a7 7 0 0 1 12-1l2 3M4 15l2 3a7 7 0 0 0 12-1"/>',
  calendar: '<rect x="3" y="5" width="18" height="16" rx="2"/><path d="M7 3v4M17 3v4M3 11h18M8 15h2M14 15h2"/>',
  growth: '<path d="m3 17 6-6 4 4 8-10M15 5h6v6"/>',
  wallet: '<path d="M20 7H6a2 2 0 0 1 0-4h12v4M4 5v14a2 2 0 0 0 2 2h14V7"/><path d="M20 11h-5v6h5M17 14h.1"/>',
  balance: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 10h18M7 15h4"/>',
  donut: '<path d="M12 3v9h9A9 9 0 1 1 8 4M16 3a9 9 0 0 1 5 5h-5V3Z"/>',
  download: '<path d="M12 3v12m-5-5 5 5 5-5M4 16v5h16v-5"/>',
  alert: '<path d="m12 3 10 18H2L12 3Z"/><path d="M12 9v5M12 17h.01"/>',
};
const icon = name => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[name] || icons.alert}</svg>`;
$$('[data-icon]').forEach(element => { element.innerHTML = icon(element.dataset.icon); });

function preference(key, value) {
  try { if (value === undefined) return localStorage.getItem(`bloomer.${key}`); localStorage.setItem(`bloomer.${key}`, value); } catch { return null; }
}

const state = {
  devices: [], selected: preference('device') || null, period: '30', timezone: preference('timezone') || 'Asia/Shanghai',
  custom: {}, model: '', chart: 'amount', view: 'overview', data: {}, health: new Map(), cursors: [null], page: 0,
  generation: 0, busy: false, exporting: false, exportController: null, fetchedAt: null, errors: [], range: null,
};
try { new Intl.DateTimeFormat('en', { timeZone: state.timezone }); } catch { state.timezone = 'Asia/Shanghai'; }
if (![...$('#timezone').options].some(option => option.value === state.timezone)) $('#timezone').add(new Option(state.timezone, state.timezone));
$('#timezone').value = state.timezone;
const demoDevice = { id: 'demo', name: 'Mac mini · 演示设备', url: '演示工作区 · 所有收益均为示例', source: 'demo' };
const selectedDevice = () => state.selected === 'demo' ? demoDevice : state.devices.find(device => device.id === state.selected);
const deviceChoices = () => [...state.devices, demoDevice];
let toastTimer;
function toast(message) { $('#toast').textContent = message; $('#toast').hidden = false; clearTimeout(toastTimer); toastTimer = setTimeout(() => { $('#toast').hidden = true; }, 4200); }
function dateTime(value, short = false) {
  if (!value) return '尚无记录';
  return new Intl.DateTimeFormat('zh-CN', { timeZone: state.timezone, month: '2-digit', day: '2-digit', ...(short ? {} : { year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }) }).format(new Date(value));
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

async function api(path, options = {}) {
  const response = await fetch(path, { cache: 'no-store', ...options, headers: { ...(options.body ? { 'Content-Type': 'application/json' } : {}), ...options.headers } });
  const body = await response.json();
  if (!response.ok) { const error = new Error(body.message || '请求暂时无法完成。'); error.code = body.code; throw error; }
  return body;
}
const query = (route, params, device = state.selected, signal) => api(`/api/devices/${encodeURIComponent(device)}/${route}${params ? '?' + new URLSearchParams(params) : ''}`, { signal });

function resetQuery() {
  state.exportController?.abort();
  state.cursors = [null]; state.page = 0; state.model = ''; state.data = {}; state.errors = [];
  state.range = timeRange(state.period, state.timezone, new Date(), state.custom);
  $('#model-filter').innerHTML = '<option value="">全部模型</option>';
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
  $('#device-status').textContent = status.label;
  $('#device-status').className = `status-tag ${status.className}`;
  $('#device-cards').innerHTML = deviceChoices().map(device => {
    const health = state.health.get(device.id);
    const status = healthStatus(health);
    return `<article class="device-card ${device.id === active ? 'selected' : ''}"><div class="device-card-header"><span>${icon('devices')}</span><h3>${escape(device.name)}</h3><span class="status-tag ${status.className}">${status.label}</span></div><p class="device-url">${escape(device.url)}</p><div class="device-stat"><span>最近成功采集</span><strong>${escape(relative(health?.last_successful_sync_at))}</strong></div><div class="device-stat"><span>Agent 版本</span><strong>${escape(health?.version || '—')}${device.id === 'demo' ? ' · 演示' : ''}</strong></div>${health?.error ? `<p class="device-url">${escape(health.error)}</p>` : ''}<div class="device-card-actions"><button class="button ${device.id === active ? 'button-dark' : 'button-white'}" data-select-device="${escape(device.id)}">${device.id === active ? '当前数据来源' : '查看收益'}</button>${device.source !== 'local' && device.id !== 'demo' ? `<button class="button remove-device" data-remove-device="${escape(device.id)}">移除</button>` : ''}</div></article>`;
  }).join('');
  renderHealth();
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
  $('#alerts').innerHTML = alerts.map(([type, message]) => `<div class="alert ${type}">${icon('alert')}<span>${escape(message)}</span></div>`).join('');
  $('#coverage-tag').textContent = meta?.coverage ? `历史覆盖 · ${meta.coverage.gap_count ? '疑似缺口' : '已保存部分历史'}` : '等待历史信息';
}

function renderMetrics() {
  const { trend, models, overview } = state.data;
  const amount = trend ? sumMoney(trend.items) : null;
  $('#period-amount').textContent = money(amount);
  $('#period-count').textContent = trend ? number(sumCount(trend.items)) : '—';
  $('#period-record-hint').textContent = `来自本地保存的收益明细${state.model ? ' · 已筛选' : ''}`;
  $('#model-count').textContent = models ? `${models.items.length} 个模型产生收益记录` : '等待模型数据';
  $('#official-amount').textContent = money(overview?.official?.total_micro_usd);
  $('#available-balance').textContent = money(overview?.official?.available_balance_micro_usd);
  $('#withdrawable-balance').textContent = money(overview?.official?.withdrawable_balance_micro_usd);
  $('#chart-total').textContent = state.chart === 'amount' ? money(amount) : trend ? `${number(sumCount(trend.items))} 笔` : '—';
  $('#chart-subtitle').textContent = state.chart === 'amount' ? '所选时段已记录收益' : '所选时段任务记录';
  $('#chart-legend').textContent = state.chart === 'amount' ? '收益 (USD)' : '任务记录';
  $('#chart-timezone').textContent = state.timezone;
  $('#chart-grain').textContent = state.period === '7' ? '按小时' : '按日';
  const start = state.range?.from;
  $('#range-label').textContent = start ? `${dateTime(start, true)} — ${dateTime(new Date(Date.parse(state.range.to) - 1), true)}` : '选择日期';
  $('#table-timezone').textContent = state.timezone === 'Asia/Shanghai' ? 'UTC+8' : state.timezone;
}

function renderTrend() {
  const items = state.data.trend?.items;
  if (!items?.length) { $('#trend-chart').innerHTML = '<div class="empty-state"><strong>这里即将长出收益。</strong>当前条件下没有已保存的收益记录。</div>'; return; }
  const width = 720, height = 220, left = 47, right = 8, top = 10, bottom = 27;
  const plotWidth = width - left - right, plotHeight = height - top - bottom;
  // Exact integer sums are kept above. Conversion is only used for SVG geometry.
  const values = items.map(item => state.chart === 'amount' ? Number(BigInt(item.amount_micro_usd)) / 1e6 : item.record_count);
  const min = Math.min(0, ...values), max = Math.max(0, ...values);
  const low = min < 0 ? min * 1.15 : 0, high = max > 0 ? max * 1.15 : 1;
  const y = value => top + (high - value) / (high - low) * plotHeight;
  const from = Date.parse(state.range.from), to = Date.parse(state.range.to);
  const granularity = state.period === '7' ? 3_600_000 : 86_400_000;
  const count = Math.max(1, (to - from) / granularity);
  const barWidth = Math.min(22, Math.max(1, plotWidth / count * 0.65));
  let svg = '';
  for (let step = 0; step <= 4; step++) {
    const value = low + (high - low) * step / 4;
    const label = state.chart === 'amount' ? `$${Math.abs(value) >= 1000 ? (value / 1000).toFixed(1) + 'k' : value.toFixed(Math.abs(value) < 1 ? 2 : 0)}` : Math.round(value).toString();
    svg += `<line class="chart-grid" x1="${left}" x2="${width - right}" y1="${y(value)}" y2="${y(value)}"/><text class="chart-label" x="${left - 10}" y="${y(value) + 3}" text-anchor="end">${escape(label)}</text>`;
  }
  svg += `<defs><clipPath id="chart-clip"><rect x="${left}" y="0" width="${plotWidth}" height="${height - bottom}"/></clipPath></defs><g clip-path="url(#chart-clip)">`;
  items.forEach((item, index) => {
    const timestamp = (Math.max(from, Date.parse(item.from)) + Math.min(to, Date.parse(item.to))) / 2;
    const x = left + (timestamp - from) / (to - from) * plotWidth - barWidth / 2;
    const value = values[index];
    const title = `${item.key} · ${state.chart === 'amount' ? money(item.amount_micro_usd, 4) : number(item.record_count) + ' 笔'}`;
    svg += `<rect class="chart-bar" data-point="${index}" tabindex="0" role="img" aria-label="${escape(title)}" x="${x}" y="${Math.min(y(value), y(0))}" width="${barWidth}" height="${Math.max(1, Math.abs(y(value) - y(0)))}" rx="2"><title>${escape(title)}</title></rect>`;
  });
  svg += '</g>';
  for (let step = 0; step <= 5; step++) {
    const instant = from + (to - from) * step / 5;
    svg += `<text class="chart-label" x="${left + plotWidth * step / 5}" y="${height - 5}" text-anchor="${step === 0 ? 'start' : step === 5 ? 'end' : 'middle'}">${escape(dateTime(instant, true))}</text>`;
  }
  $('#trend-chart').innerHTML = `<svg viewBox="0 0 ${width} ${height}" role="group" aria-label="${state.chart === 'amount' ? '收益' : '任务'}趋势，空白区间没有已保存记录">${svg}</svg><div class="chart-tooltip" hidden></div>`;
  const showTooltip = event => {
    const bar = event.target.closest('[data-point]');
    if (!bar) return;
    const item = items[Number(bar.dataset.point)];
    const tooltip = $('#trend-chart .chart-tooltip');
    tooltip.innerHTML = `${escape(item.key)}<strong>${escape(money(item.amount_micro_usd, 4))}</strong>${number(item.record_count)} 笔任务记录`;
    tooltip.hidden = false;
  };
  $('#trend-chart').onpointerover = showTooltip;
  $('#trend-chart').onfocusin = showTooltip;
  $('#trend-chart').onpointerleave = () => { $('#trend-chart .chart-tooltip').hidden = true; };
  $('#trend-chart').onfocusout = () => { $('#trend-chart .chart-tooltip').hidden = true; };
}

function renderModels() {
  const items = state.data.models?.items;
  if (!items?.length) { $('#model-chart').innerHTML = '<div class="empty-state">暂无模型记录</div>'; $('#model-list').innerHTML = ''; return; }
  const sorted = [...items].sort((a, b) => BigInt(a.amount_micro_usd) > BigInt(b.amount_micro_usd) ? -1 : BigInt(a.amount_micro_usd) < BigInt(b.amount_micro_usd) ? 1 : 0);
  const positiveTotal = sorted.reduce((sum, item) => sum + (BigInt(item.amount_micro_usd) > 0n ? BigInt(item.amount_micro_usd) : 0n), 0n);
  const top = sorted.slice(0, 4);
  if (sorted.length > 4) top.push({ key: '其他模型', amount_micro_usd: sumMoney(sorted.slice(4)).toString() });
  let offset = 0;
  const rings = top.map((item, index) => {
    const amount = BigInt(item.amount_micro_usd);
    const share = positiveTotal && amount > 0n ? Number(amount * 10000n / positiveTotal) / 100 : 0;
    const markup = `<circle cx="80" cy="80" r="61" pathLength="100" fill="none" stroke="${colors[index]}" stroke-width="15" stroke-dasharray="${Math.max(0, share - 1.5)} ${100 - Math.max(0, share - 1.5)}" stroke-dashoffset="${-offset}"><title>${escape(item.key)} · ${escape(money(item.amount_micro_usd))}</title></circle>`;
    offset += share; return markup;
  }).join('');
  $('#model-chart').innerHTML = `<svg viewBox="0 0 160 160" role="img" aria-label="模型正收益占比分布"><circle cx="80" cy="80" r="61" fill="none" stroke="#f1f4eb" stroke-width="15"/>${rings}</svg><div class="donut-center"><strong>${items.length.toString().padStart(2, '0')}</strong><span>贡献模型</span></div>`;
  $('#model-list').innerHTML = top.map((item, index) => `<div class="model-item"><i style="background:${colors[index]}"></i><span class="model-name" title="${escape(item.key)}">${escape(item.key)}</span><span class="model-share">${positiveTotal > 0n && BigInt(item.amount_micro_usd) > 0n ? Number(BigInt(item.amount_micro_usd) * 1000n / positiveTotal) / 10 : 0}%</span><span class="model-amount">${escape(money(item.amount_micro_usd))}</span></div>`).join('');
  if (sorted.some(item => BigInt(item.amount_micro_usd) < 0n)) $('.model-footer').textContent = '环形图按正收益占比绘制；列表保留调整金额';
  else $('.model-footer').textContent = '按所选时段的已记录收益统计';
}

function renderRecords() {
  const page = state.data.earnings;
  $('#records-body').innerHTML = page?.items.length ? page.items.map(item => `<tr><td><div class="model-cell"><span class="model-avatar">${escape(item.model.charAt(0).toUpperCase())}</span><div class="model-text"><strong title="${escape(item.model)}">${escape(item.model)}</strong><small title="记录 ${escape(item.record_id)} · Provider ${escape(item.provider_id)}">${escape(item.job_id || item.record_id)}</small></div></div></td><td class="amount-cell">${escape(money(item.amount_micro_usd, 6))}</td><td>${number(item.prompt_tokens)}</td><td>${number(item.completion_tokens)}</td><td>${escape(dateTime(item.created_at))}</td><td><span class="saved-tag">已记录</span></td></tr>`).join('') : `<tr><td colspan="6"><div class="empty-state"><strong>${page ? '还没有符合条件的收益' : '暂时无法读取明细'}</strong>${page ? '试着换个时间范围或模型，看看其他收益。' : '请检查设备连接和采集状态，然后刷新。'}</div></td></tr>`;
  $('#records-count').textContent = state.data.trend ? number(sumCount(state.data.trend.items)) : '—';
  $('#page-number').textContent = String(state.page + 1).padStart(2, '0');
  $('#page-info').textContent = page?.items.length ? `第 ${state.page * 10 + 1}–${state.page * 10 + page.items.length} 条 · 按时间倒序${state.page ? ' · 翻页时暂停自动刷新明细' : ''}` : '每页 10 条 · 按时间倒序';
  $('#prev-page').disabled = state.busy || !state.page;
  $('#next-page').disabled = state.busy || !page?.next_cursor;
  $('#export-csv').disabled = state.busy || state.exporting || !page?.items.length;
}

function renderAll() { renderDevices(); renderMetrics(); renderTrend(); renderModels(); renderRecords(); renderAlerts(); }

async function loadData({ reset = false } = {}) {
  const generation = ++state.generation;
  if (reset) { resetQuery(); renderAll(); }
  state.busy = true; document.body.classList.add('loading'); $('#refresh').disabled = true; renderRecords();
  const params = { ...state.range, ...(state.model ? { model: state.model } : {}) };
  const jobs = [
    ['health', query('health')], ['overview', query('overview')],
    ['trend', query('stats', { ...params, group_by: state.period === '7' ? 'hour' : 'day', timezone: state.timezone })],
    ['models', query('stats', { ...state.range, group_by: 'model', timezone: state.timezone })],
    ['earnings', query('earnings', { ...params, limit: 10, ...(state.cursors[state.page] ? { cursor: state.cursors[state.page] } : {}) })],
  ];
  const results = await Promise.allSettled(jobs.map(([, promise]) => promise));
  if (generation !== state.generation) return;
  const errors = [];
  results.forEach((result, index) => {
    const key = jobs[index][0];
    if (result.status === 'fulfilled') {
      if (key === 'health') state.health.set(state.selected, result.value);
      else state.data[key] = result.value;
    } else {
      if (key === 'health') state.health.set(state.selected, { error: result.reason.message });
      else { delete state.data[key]; errors.push(result.reason.message); }
    }
  });
  state.errors = [...new Set(errors)];
  state.busy = false; document.body.classList.remove('loading'); $('#refresh').disabled = false;
  if (results.some(result => result.status === 'fulfilled')) state.fetchedAt = new Date();
  const models = state.data.models?.items || [];
  const choices = [...new Set([...models.map(item => item.key), ...(state.model ? [state.model] : [])])].sort();
  $('#model-filter').innerHTML = '<option value="">全部模型</option>' + choices.map(model => `<option value="${escape(model)}" ${state.model === model ? 'selected' : ''}>${escape(model)}</option>`).join('');
  renderAll(); updateClock();
}

async function loadDeviceList() {
  const response = await api('/api/devices'); state.devices = response.devices;
  if (!deviceChoices().some(device => device.id === state.selected)) state.selected = state.devices[0]?.id || 'demo';
  preference('device', state.selected); renderDevices();
}

async function checkDevices() {
  const ids = deviceChoices().map(device => device.id);
  const results = await Promise.allSettled(ids.map(id => query('health', null, id)));
  results.forEach((result, index) => state.health.set(ids[index], result.status === 'fulfilled' ? result.value : { error: result.reason.message }));
  renderDevices(); renderAlerts();
}

function setView(view) {
  state.view = view; $('#main').dataset.view = view;
  const titles = { overview: ['收益概览', '看看你的算力，今天又带来了什么。'], records: ['收益明细', '让每一笔收益，有据可查。'], devices: ['我的设备', '设备安心运行，收益持续生长。'] };
  $('#page-title').innerHTML = `${titles[view][0]}<span class="title-flower">✳</span>`;
  $('#page-description').textContent = titles[view][1]; $('#breadcrumb-current').textContent = titles[view][0];
  document.title = `Bloomer · ${titles[view][0]}`;
  $$('.nav-item').forEach(button => { button.classList.toggle('active', button.dataset.view === view); if (button.dataset.view === view) button.setAttribute('aria-current', 'page'); else button.removeAttribute('aria-current'); });
  $('#records-title').innerHTML = `${view === 'records' ? '收益记录' : '最近收益'}<span id="records-count" class="count-badge">${state.data.trend ? number(sumCount(state.data.trend.items)) : '—'}</span>`;
  if (view === 'devices') checkDevices();
  history.replaceState(null, '', `#${view}`);
}

async function selectDevice(id) { state.selected = id; preference('device', id); await loadData({ reset: true }); }
$$('.nav-item').forEach(button => button.addEventListener('click', () => setView(button.dataset.view)));
$('#device-select').addEventListener('change', event => selectDevice(event.target.value));
$('#refresh').addEventListener('click', () => { state.cursors = [null]; state.page = 0; state.range = timeRange(state.period, state.timezone, new Date(), state.custom); loadData(); if (state.view === 'devices') checkDevices(); });
$$('[data-period]').forEach(button => button.addEventListener('click', () => {
  state.period = button.dataset.period;
  $$('[data-period]').forEach(item => { item.classList.toggle('selected', item === button); item.setAttribute('aria-pressed', String(item === button)); });
  $('#date-form').hidden = true; loadData({ reset: true });
}));
$('#timezone').addEventListener('change', event => { state.timezone = event.target.value; preference('timezone', state.timezone); loadData({ reset: true }); });
$$('[data-chart]').forEach(button => button.addEventListener('click', () => {
  state.chart = button.dataset.chart;
  $$('[data-chart]').forEach(item => { item.classList.toggle('selected', item === button); item.setAttribute('aria-pressed', String(item === button)); });
  renderMetrics(); renderTrend();
}));
$('#custom-range').addEventListener('click', () => {
  const form = $('#date-form'); form.hidden = !form.hidden;
  form.elements.from.value = state.custom.from || dateKey(state.range.from, state.timezone);
  form.elements.to.value = state.custom.to || dateKey(Date.parse(state.range.to) - 1, state.timezone);
  form.elements.from.max = form.elements.to.max = dateKey(new Date(), state.timezone);
  $('#date-error').textContent = '';
} );
$('#cancel-range').addEventListener('click', () => { $('#date-form').hidden = true; });
$('#date-form').addEventListener('submit', event => {
  event.preventDefault(); const form = event.target;
  const custom = { from: form.elements.from.value, to: form.elements.to.value };
  try { timeRange('custom', state.timezone, new Date(), custom); } catch (error) { $('#date-error').textContent = error.message; return; }
  state.period = 'custom'; state.custom = custom;
  $$('[data-period]').forEach(button => { button.classList.remove('selected'); button.setAttribute('aria-pressed', 'false'); });
  form.hidden = true; loadData({ reset: true });
});
$('#model-filter').addEventListener('change', event => { state.exportController?.abort(); state.model = event.target.value; state.cursors = [null]; state.page = 0; state.data.earnings = null; loadData(); });

async function pageTo(index) {
  if (state.busy) return;
  const generation = ++state.generation;
  const priorPage = state.page;
  if (index > state.page) state.cursors[index] = state.data.earnings.next_cursor;
  state.page = index; state.busy = true; renderRecords();
  try {
    const params = { ...state.range, ...(state.model ? { model: state.model } : {}), limit: 10, ...(state.cursors[index] ? { cursor: state.cursors[index] } : {}) };
    const response = await query('earnings', params);
    if (generation !== state.generation) return;
    state.data.earnings = response;
  } catch (error) { if (generation === state.generation) { state.page = priorPage; toast(error.message); } }
  finally { if (generation === state.generation) { state.busy = false; renderRecords(); renderAlerts(); } }
}
$('#next-page').addEventListener('click', () => pageTo(state.page + 1));
$('#prev-page').addEventListener('click', () => pageTo(state.page - 1));

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
    const link = document.createElement('a'); link.href = url; link.download = `bloomer-${state.selected === 'demo' ? 'demo-' : ''}${range.from.slice(0, 10)}.csv`; link.click(); setTimeout(() => URL.revokeObjectURL(url), 1000);
    status.textContent = `已导出 ${number(items.length)} 条明细 · USD 精确到 6 位小数 · 时间采用 UTC。`;
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
    const response = await api(save ? '/api/devices' : '/api/devices/test', { method: 'POST', body: JSON.stringify(body) });
    if (save) {
      form.elements.token.value = ''; $('#connect-dialog').close(); await loadDeviceList(); await selectDevice(response.id); toast('设备已连接，真实收益正在载入。');
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
    try { await api(`/api/devices/${remove.dataset.removeDevice}`, { method: 'DELETE' }); await loadDeviceList(); await loadData({ reset: true }); toast('设备连接已移除。'); } catch (error) { toast(error.message); }
  }, { once: true });
});

function updateClock() {
  $('#clock-date').textContent = new Intl.DateTimeFormat('zh-CN', { timeZone: state.timezone, year: 'numeric', month: 'long', day: 'numeric' }).format(new Date());
  $('#last-refresh').textContent = state.fetchedAt ? `最近刷新 ${dateTime(state.fetchedAt)} · 每 60 秒自动更新` : '等待首次刷新';
}

try {
  await loadDeviceList(); resetQuery();
  const view = location.hash.slice(1); if (['overview', 'records', 'devices'].includes(view)) setView(view);
  await loadData();
} catch (error) { $('#alerts').innerHTML = `<div class="alert error">${escape(error.message)}</div>`; }
setInterval(() => {
  updateClock(); renderDevices();
  if (document.hidden || state.busy || state.exporting || $('#connect-dialog').open) return;
  if (state.view === 'devices') { checkDevices(); return; }
  if (state.page === 0) {
    state.range = timeRange(state.period, state.timezone, new Date(), state.custom); loadData();
  } else checkDevices();
}, 60_000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) updateClock(); });
