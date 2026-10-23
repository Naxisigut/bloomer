import * as echarts from './vendor/echarts.esm.min.mjs';
import { DASHBOARD_TIMEZONE, money, dateKey, shiftDate, csvRows } from './data.js';
import {
  analysisPeriodRange, allowedGranularities, defaultGranularity, buildAnalysisParams, deriveAnalysisView,
} from './analysis-data.js';
import {
  trendOption, cumulativeOption, modelShareOption, taskAvgOption, hourDistributionOption, weekdayHourHeatmap, modelHourHeatmap, modelCompareOption, chartTheme,
} from './analysis-charts.js';
import { displayModelNames } from './models.js';

echarts.registerTheme('bloomer', chartTheme);

const number = value => new Intl.NumberFormat('en-US').format(value);

export function createAnalysisPage({ query, deviceChoices, syncDevice, escape, correctedNow, currentView, setExporting = () => {} }) {
  const $ = selector => document.querySelector(selector);
  const root = $('#analysis');
  const charts = new Map();
  const state = {
    device: null, period: '7', custom: {}, granularity: 'day', allModels: true, models: new Set(),
    response: null, derived: null, meta: null, error: null, busy: false, controller: null, generation: 0,
    queried: false, trendMode: 'bar', exporting: false,
  };
  let exportController = null;
  let exportCancelReason = '';

  function renderDeviceOptions() {
    const select = $('#analysis-device');
    if (!select) return;
    select.innerHTML = deviceChoices().map(device => `<option value="${escape(device.id)}" ${device.id === state.device ? 'selected' : ''}>${escape(device.name)}</option>`).join('');
  }

  function syncGranularityControls(range) {
    const allowed = allowedGranularities(range);
    if (!allowed.includes(state.granularity)) state.granularity = defaultGranularity(range);
    $$('#analysis-granularity [data-granularity]').forEach(button => {
      const value = button.dataset.granularity;
      const ok = allowed.includes(value);
      button.disabled = state.busy || !ok;
      button.classList.toggle('selected', value === state.granularity);
      button.setAttribute('aria-pressed', String(value === state.granularity));
    });
  }

  function renderModelTags() {
    const host = $('#analysis-model-tags');
    if (!host) return;
    const list = state.response?.models?.map(item => item.model) || [];
    const choices = [...new Set([...list, ...state.models])];
    const labels = displayModelNames(choices);
    host.innerHTML = `<button type="button" class="model-tag ${state.allModels ? 'selected' : ''}" data-all-models aria-pressed="${state.allModels}">全部模型</button>` + choices.map(model => {
      const selected = !state.allModels && state.models.has(model);
      const missing = state.response && !list.includes(model);
      return `<button type="button" class="model-tag ${selected ? 'selected' : ''}" data-model="${escape(model)}" title="${escape(model)}" aria-pressed="${selected}"><span class="model-tag-label">${escape(labels.get(model))}</span>${missing ? '<span class="tag-note">无数据</span>' : ''}</button>`;
    }).join('');
  }

  function renderMetrics() {
    const metrics = state.derived?.metrics;
    $('#analysis-metric-total').textContent = metrics ? money(metrics.totalAmount) : '—';
    $('#analysis-metric-count').textContent = metrics ? new Intl.NumberFormat('zh-CN').format(metrics.recordCount) : '—';
    $('#analysis-metric-avg').textContent = metrics ? money(metrics.avgPerRecord, 4) : '—';
    $('#analysis-metric-daily').textContent = metrics ? money(metrics.dailyAvg, 4) : '—';
    const peak = metrics?.peakHour;
    $('#analysis-metric-peak').textContent = peak ? `${String(peak.hour).padStart(2, '0')}:00 · ${money(peak.daily_avg_micro_usd, 4)}/日` : '—';
  }

  function renderShareTable() {
    const body = $('#analysis-share-body');
    if (!body) return;
    const rows = state.derived?.modelShares || [];
    const labels = displayModelNames(rows.map(row => row.model));
    body.innerHTML = rows.length ? rows.map(row => `<tr><td title="${escape(row.model)}">${escape(labels.get(row.model))}</td><td class="amount-cell">${escape(money(row.amount_micro_usd))}</td><td>${row.share.toFixed(1)}%</td><td>${new Intl.NumberFormat('zh-CN').format(row.record_count)}</td><td class="amount-cell">${escape(money(row.avgPerRecord, 4))}</td></tr>`).join('') : `<tr><td colspan="5"><div class="empty-state">暂无模型数据</div></td></tr>`;
  }

  function setChart(name, option) {
    const element = $(`#analysis-chart-${name}`);
    if (!element) return;
    let chart = charts.get(name);
    if (!chart) { chart = echarts.init(element, 'bloomer', { renderer: 'canvas' }); charts.set(name, chart); }
    chart.setOption(option, true);
  }

  function renderCharts() {
    if (!state.derived || !state.response) {
      for (const name of charts.keys()) charts.get(name)?.clear();
      return;
    }
    const { filledBuckets, cumulative, modelShares, hourDaily } = state.derived;
    const filter = state.queriedModels || [];
    const modelOrder = filter.length ? state.derived.modelOrder.filter(model => filter.includes(model)) : state.derived.modelOrder;
    const hasData = state.response.totals.record_count > 0;
    setChart('trend', hasData ? trendOption({ filledBuckets, modelOrder, mode: state.trendMode }) : { title: { text: '暂无收益数据', left: 'center', top: 'middle', textStyle: { color: '#8a8f98', fontSize: 13, fontWeight: 400 } }, series: [] });
    setChart('cumulative', hasData ? cumulativeOption({ cumulative, modelOrder }) : { series: [] });
    setChart('share', modelShareOption(modelShares));
    setChart('task-avg', hasData ? taskAvgOption(filledBuckets) : { series: [] });
    setChart('hour', hourDistributionOption(hourDaily, modelOrder));
    setChart('weekday', weekdayHourHeatmap(state.response.weekday_hour));
    setChart('model-hour', modelHourHeatmap(state.response.hour_of_day, modelOrder));
    setChart('model-compare', modelCompareOption(state.response.models.filter(item => modelOrder.includes(item.model)), state.derived.metrics.spanDays, state.response.granularity));
    renderShareTable();
  }

  function renderStatus() {
    $('#analysis-query').disabled = state.busy;
    const exportButton = $('#analysis-export');
    if (exportButton) exportButton.disabled = state.exporting;
    $('#analysis-cancel').hidden = !state.controller;
    $('#analysis-status').hidden = !state.error && !state.busy;
    $('#analysis-status').className = `state-message ${state.error ? 'warning' : ''}`;
    $('#analysis-status').textContent = state.busy ? '正在查询分析数据…' : state.error || '';
    $('#analysis-truncated').hidden = !(state.meta?.range_truncated);
    try { syncGranularityControls(currentRange()); } catch { /* Invalid custom dates are reported by the date form. */ }
    renderModelTags();
    renderMetrics();
    renderCharts();
  }

  function currentRange() {
    return analysisPeriodRange(state.period, DASHBOARD_TIMEZONE, correctedNow(state.device), state.custom);
  }

  async function runQuery() {
    state.controller?.abort();
    const controller = new AbortController();
    state.controller = controller;
    state.busy = true;
    state.error = null;
    const generation = ++state.generation;
    renderStatus();
    try {
      const range = currentRange();
      syncGranularityControls(range);
      const models = state.allModels ? [] : [...state.models].slice(0, 8);
      const params = buildAnalysisParams({ range, granularity: state.granularity, models });
      const response = await query('analysis', params, state.device, controller.signal);
      if (generation !== state.generation) return;
      state.response = response;
      state.meta = response.meta;
      state.derived = deriveAnalysisView(response, { now: correctedNow(state.device), selectedModels: models });
      state.queriedModels = models;
      state.queried = true;
      state.error = null;
    } catch (error) {
      if (generation !== state.generation || error.name === 'AbortError') return;
      state.error = error.message;
    } finally {
      if (generation === state.generation) {
        state.busy = false;
        state.controller = null;
        renderStatus();
      }
    }
  }

  function showExportStatus(message) {
    const status = $('#analysis-export-status');
    if (!status) return;
    status.hidden = false;
    status.textContent = message;
  }

  function abortExport(reason = '筛选已改变，本次导出已取消。') {
    exportCancelReason = reason;
    exportController?.abort();
  }

  async function exportDetails() {
    if (state.exporting) return;
    let range;
    try { range = currentRange(); } catch (error) { showExportStatus(error.message); return; }
    const device = state.device;
    if (!device) { showExportStatus('请先选择设备。'); return; }
    const models = state.allModels ? [] : [...state.models].slice(0, 8);
    const targets = models.length ? models : [''];
    exportController?.abort();
    const controller = new AbortController();
    exportController = controller;
    exportCancelReason = '';
    state.exporting = true;
    setExporting(true);
    renderStatus();
    const items = [];
    const seen = new Set();
    try {
      for (const model of targets) {
        let cursor = null;
        do {
          showExportStatus(`正在导出所选条件的全部明细…已读取 ${number(items.length)} 条`);
          const response = await query('earnings', { from: range.from, to: range.to, limit: 500, ...(model ? { model } : {}), ...(cursor ? { cursor } : {}) }, device, controller.signal);
          for (const item of response.items) if (!seen.has(item.record_id)) { seen.add(item.record_id); items.push(item); }
          if (items.length > 100000) throw new Error('所选范围超过 100,000 条，请缩小时间范围后导出。');
          if (response.next_cursor === cursor && cursor) throw new Error('设备返回了重复游标，请刷新后重试。');
          cursor = response.next_cursor;
        } while (cursor);
      }
      if (targets.length > 1) items.sort((a, b) => b.created_at.localeCompare(a.created_at) || b.record_id.localeCompare(a.record_id));
      if (!items.length) { showExportStatus('当前筛选没有可导出的明细。'); return; }
      const url = URL.createObjectURL(new Blob([csvRows(items)], { type: 'text/csv;charset=utf-8' }));
      const link = document.createElement('a');
      link.href = url;
      link.download = `bloomer-${device === 'demo' ? 'demo-' : ''}${dateKey(range.from, DASHBOARD_TIMEZONE)}.csv`;
      link.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
      showExportStatus(`已导出 ${number(items.length)} 条明细 · USD 精确到 6 位小数 · 时间采用北京时间（UTC+8）。`);
    } catch (error) {
      showExportStatus(error.name === 'AbortError' ? (exportCancelReason || '本次导出已取消。') : error.message);
    } finally {
      if (exportController === controller) {
        state.exporting = false;
        exportController = null;
        setExporting(false);
        renderStatus();
      }
    }
  }

  function bindFilters() {
    $('#analysis-device')?.addEventListener('change', event => {
      const next = event.target.value;
      if (state.device && next !== state.device) abortExport('设备已切换，本次导出已取消。');
      state.device = next;
      syncDevice(state.device, { fromAnalysis: true });
      renderDeviceOptions();
    });
    $$('#analysis-period [data-period]').forEach(button => button.addEventListener('click', () => {
      state.period = button.dataset.period;
      $$('#analysis-period [data-period]').forEach(item => { item.classList.toggle('selected', item === button); item.setAttribute('aria-pressed', String(item === button)); });
      $('#analysis-date-form').hidden = true;
      syncGranularityControls(currentRange());
    }));
    $('#analysis-custom-range')?.addEventListener('click', () => {
      const form = $('#analysis-date-form');
      form.hidden = !form.hidden;
      const range = currentRange();
      form.elements.from.value = state.custom.from || dateKey(range.from, DASHBOARD_TIMEZONE);
      form.elements.to.value = state.custom.to || dateKey(Date.parse(range.to) - 1, DASHBOARD_TIMEZONE);
      form.elements.from.max = form.elements.to.max = dateKey(correctedNow(state.device), DASHBOARD_TIMEZONE);
      $('#analysis-date-error').textContent = '';
    });
    $('#analysis-date-form')?.addEventListener('submit', event => {
      event.preventDefault();
      const form = event.target;
      state.custom = { from: form.elements.from.value, to: form.elements.to.value };
      try { analysisPeriodRange('custom', DASHBOARD_TIMEZONE, correctedNow(state.device), state.custom); } catch (error) { $('#analysis-date-error').textContent = error.message; return; }
      state.period = 'custom';
      $$('#analysis-period [data-period]').forEach(button => button.classList.remove('selected'));
      form.hidden = true;
      syncGranularityControls(currentRange());
    });
    $$('#analysis-granularity [data-granularity]').forEach(button => button.addEventListener('click', () => {
      if (button.disabled || state.granularity === button.dataset.granularity) return;
      state.granularity = button.dataset.granularity;
      syncGranularityControls(currentRange());
    }));
    $('#analysis-model-tags')?.addEventListener('click', event => {
      const all = event.target.closest('[data-all-models]');
      const tag = event.target.closest('[data-model]');
      if (all) { state.allModels = true; state.models.clear(); renderModelTags(); return; }
      if (!tag) return;
      state.allModels = false;
      const model = tag.dataset.model;
      if (state.models.has(model)) state.models.delete(model); else if (state.models.size < 8) state.models.add(model);
      if (!state.models.size) state.allModels = true;
      renderModelTags();
    });
    $('#analysis-query')?.addEventListener('click', () => runQuery());
    $('#analysis-export')?.addEventListener('click', () => exportDetails());
    $('#analysis-cancel')?.addEventListener('click', () => state.controller?.abort());
    $$('[data-trend-mode]').forEach(button => button.addEventListener('click', () => {
      state.trendMode = button.dataset.trendMode;
      $$('[data-trend-mode]').forEach(item => item.classList.toggle('selected', item === button));
      renderCharts();
    }));
  }

  function resize() {
    if (currentView() !== 'analysis') return;
    for (const chart of charts.values()) chart.resize();
  }

  function onEnter(deviceId) {
    state.device ||= deviceId;
    renderDeviceOptions();
    syncGranularityControls(currentRange());
    renderStatus();
    if (!state.queried) runQuery();
    else renderCharts();
    requestAnimationFrame(resize);
  }

  function onGlobalDeviceChange(deviceId) {
    if (state.device && state.device !== deviceId) abortExport('设备已切换，本次导出已取消。');
    state.device = deviceId;
    renderDeviceOptions();
  }

  function dispose() {
    state.controller?.abort();
    abortExport();
    for (const chart of charts.values()) chart.dispose();
    charts.clear();
  }

  bindFilters();
  if (root) new ResizeObserver(() => resize()).observe(root);

  return { onEnter, onGlobalDeviceChange, resize, dispose, runQuery, state: () => state };
}

function $$(selector) { return [...document.querySelectorAll(selector)]; }
