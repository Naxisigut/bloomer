import { DASHBOARD_TIMEZONE } from './data.js';
import { deviceFields, reading, activityStatus, activityReason, pressureStatus, sampleStale, localInput, inputRange, DeviceHistoryPager } from './device-state.js';

export function createDeviceUI({ query, selectedDevice, deviceChoices, selectDevice, currentView, dateTime, escape, preference, onRefresh }) {
  const $ = selector => document.querySelector(selector);
  const latest = new Map(), pending = new Map();
  let selected, renderedDevice, pager = null, page = null, pageIndex = 0, busy = false, error = '', controller = null, generation = 0;
  const savedSize = Number(preference('statePageSize') || 50);
  $('#state-query-limit').value = [25, 50, 100, 200, 500].includes(savedSize) ? savedSize : 50;
  const historyTime = value => new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23' }).format(new Date(value));
  const cell = (sample, key) => {
    const value = reading(sample, key);
    return `<span class="${value.known ? '' : 'reading-unknown'}" title="${escape(value.reason || value.text)}">${escape(value.text)}</span>`;
  };
  const details = sample => deviceFields.map(([key, label]) => `<div class="health-item"><span>${label}</span><strong>${cell(sample, key)}</strong></div>`).join('');

  function renderLatest() {
    const device = selectedDevice()?.id, response = latest.get(device);
    // Wait for the replacement sample instead of briefly marking cached data stale.
    if (response && device === renderedDevice && pending.has(device)) return;
    renderedDevice = device;
    const status = activityStatus(response);
    const statusElement = $('#activity-status');
    statusElement.textContent = status.label; statusElement.className = `status-tag ${status.className}`; statusElement.title = status.detail;
    for (const [selector, key] of [['#thermal-status', 'thermal_state'], ['#memory-pressure-status', 'memory_pressure']]) {
      const value = pressureStatus(response, key), element = $(selector);
      element.textContent = value.label; element.className = `status-tag ${value.className}`; element.title = value.detail;
    }
    $('#state-device-label').textContent = selectedDevice()?.name || '当前设备';
    const message = $('#device-state-message'), grid = $('#device-state-details');
    const sample = response?.sample;
    message.hidden = false; message.className = 'state-message'; grid.innerHTML = '';
    $('#device-state-time').textContent = sample ? `采集于 ${dateTime(sample.collected_at)}` : '每 60 秒更新';
    if (response?.error) { message.classList.add('warning'); message.textContent = response.error; return; }
    if (!response) { message.textContent = '正在读取设备状态…'; return; }
    if (!sample) { message.textContent = '尚无设备状态样本；等待设备端完成首次状态采集。'; return; }
    const stale = sampleStale(response);
    message.hidden = !stale;
    if (stale) { message.classList.add('warning'); message.textContent = '最新状态已过期；以下为最近保存的读数。'; }
    else if (status.className) { message.hidden = false; message.classList.add('warning'); message.textContent = status.detail; }
    const hardware = response.device?.hardware;
    const extra = [
      ['空闲状态说明', status.detail],
      ['该设备最新收益', sample.last_device_earning_at ? dateTime(sample.last_device_earning_at) : '未取得'],
      ['收益最后同步', sample.last_earnings_sync_at ? dateTime(sample.last_earnings_sync_at) : '未取得'],
      ...(hardware ? [['芯片', hardware.chip || '未知'], ['设备型号', hardware.model || '未知'], ['逻辑 CPU 数', hardware.logical_cpus ?? '未知']] : []),
      ['Darkbloom 版本', sample.provider_version || '未提供'], ['进程 PID', sample.provider_pid ?? '未提供'],
      ['本地状态文件更新时间', sample.provider_state_at ? dateTime(sample.provider_state_at) : '未提供'],
      ['收益 Provider ID', sample.earnings_provider_id || '未取得'],
    ];
    grid.innerHTML = details(sample) + extra.map(([label, value]) => `<div class="health-item"><span>${label}</span><strong>${escape(value)}</strong></div>`).join('');
  }

  function syncDevices() {
    const id = selectedDevice()?.id;
    const choices = deviceChoices().map(device => `<option value="${escape(device.id)}" ${device.id === id ? 'selected' : ''}>${escape(device.name)}</option>`).join('');
    for (const selector of ['#detail-device-select', '#state-query-device']) {
      const element = $(selector);
      if (element.innerHTML !== choices) element.innerHTML = choices;
    }
    if (selected !== id) {
      selected = id; controller?.abort(); generation++; busy = false; pager = null; page = null; pageIndex = 0; error = '';
      $('#state-sample-dialog').close();
      renderHistory();
      if (currentView() === 'query') queryHistory();
    }
  }

  function render() { syncDevices(); renderLatest(); }
  async function refresh() {
    const device = selectedDevice()?.id;
    if (!device) return;
    if (pending.has(device)) return pending.get(device);
    const work = (async () => {
      try {
        const response = await query('device-state', null, device);
        if (!response || !Object.hasOwn(response, 'sample') || (response.sample !== null && (typeof response.sample !== 'object' || !Number.isFinite(Date.parse(response.sample.collected_at))))) throw new Error('设备返回的状态数据无效。');
        latest.set(device, response);
        if (device === selectedDevice()?.id) onRefresh?.();
      } catch (failure) { latest.set(device, { error: failure.message, code: failure.code }); }
      finally { pending.delete(device); render(); }
    })();
    pending.set(device, work);
    return work;
  }

  function preset(hours) {
    const now = new Date();
    $('#state-query-from').value = localInput(+now - hours * 3_600_000, DASHBOARD_TIMEZONE);
    $('#state-query-to').value = localInput(now, DASHBOARD_TIMEZONE);
  }
  preset(24);

  function renderHistory() {
    $('#state-query-error').textContent = error;
    $('#state-query-error').hidden = !error;
    document.querySelectorAll('#state-query-form button, #state-query-form input, #state-query-limit').forEach(element => { element.disabled = busy; });
    $('#state-query-submit').textContent = busy ? '查询中…' : '查询';
    $('#state-prev-page').disabled = busy || pageIndex === 0 || !page;
    $('#state-next-page').disabled = busy || !page?.next_before_id;
    $('#state-page-number').textContent = `第 ${pageIndex + 1} 页`;
    $('#state-query-panel').setAttribute('aria-busy', String(busy));
    $('#state-page-info').textContent = page ? `本页 ${page.items.length} 条 · 每页 ${pager.limit} 条` : busy ? '正在读取设备状态…' : '等待查询';
    $('#state-query-range').textContent = page && pager ? `${historyTime(pager.range.from)} — ${historyTime(pager.range.to)} · 中国标准时间 UTC+8` : '选择设备和时间范围，查询每次采集的状态。';
    const messages = [];
    if (page?.meta?.range_truncated) messages.push('部分时间早于保留期，结果已按设备保留范围截取。');
    if (page && !page.items.length) messages.push('该时间段没有保存的设备状态；无记录不代表零负载或设备离线。');
    $('#state-query-note').textContent = messages.join(' ');
    $('#state-query-note').hidden = !messages.length;
    $('#state-history-body').innerHTML = page?.items.length ? page.items.map(sample => `<tr>
      <td>${escape(historyTime(sample.collected_at))}</td><td>${cell(sample, 'is_idle')}</td><td>${cell(sample, 'inference_active')}<small>${cell(sample, 'request_work_pending')}</small></td>
      <td class="state-model-cell">${cell(sample, 'current_model')}<small>${cell(sample, 'model_ready')}</small></td><td>${sample.last_device_earning_at ? escape(historyTime(sample.last_device_earning_at)) : '未取得'}</td>
      <td>${cell(sample, 'cpu_percent')}<small>进程 ${cell(sample, 'provider_cpu_percent')}</small></td><td>${cell(sample, 'gpu_percent')}</td>
      <td>${cell(sample, 'memory_available_bytes')}<small>总量 ${cell(sample, 'memory_total_bytes')}</small></td>
      <td>${cell(sample, 'memory_pressure')}<small>Swap ${cell(sample, 'swap_used_bytes')}</small></td>
      <td>${cell(sample, 'thermal_state')}<small>CPU ${cell(sample, 'cpu_temperature_c')} / GPU ${cell(sample, 'gpu_temperature_c')}</small></td>
      <td>${cell(sample, 'disk_available_bytes')}</td><td><button type="button" class="button button-white state-row-detail" data-state-sample="${sample.id}">详情</button></td>
    </tr>`).join('') : `<tr><td colspan="12"><div class="empty-state">${page ? '当前时间段无状态记录' : busy ? '正在读取设备状态…' : error ? '设备状态查询不可用' : '请选择查询条件'}</div></td></tr>`;
  }

  async function loadHistory(index, candidate = pager) {
    if (!candidate) return;
    controller?.abort(); controller = new AbortController();
    const request = ++generation, signal = controller.signal;
    busy = true; error = ''; renderHistory();
    try {
      const response = await candidate.page(index, signal);
      if (request !== generation) return;
      pager = candidate; page = response; pageIndex = index;
    } catch (failure) { if (request === generation && failure.name !== 'AbortError') error = failure.message; }
    finally { if (request === generation) { busy = false; controller = null; renderHistory(); } }
  }

  function queryHistory() {
    if (busy || !$('#state-query-form').reportValidity()) return;
    try {
      const range = inputRange($('#state-query-from').value, $('#state-query-to').value, DASHBOARD_TIMEZONE);
      const limit = Number($('#state-query-limit').value), device = selectedDevice()?.id;
      const candidate = new DeviceHistoryPager((params, signal) => query('device-history', params, device, signal), range, limit);
      return loadHistory(0, candidate);
    } catch (failure) { error = failure.message; renderHistory(); }
  }
  $('#state-query-form').addEventListener('submit', event => { event.preventDefault(); queryHistory(); });
  for (const hours of [24, 168]) $(`[data-state-hours="${hours}"]`).addEventListener('click', () => { preset(hours); queryHistory(); });
  $('#state-query-limit').addEventListener('change', () => preference('statePageSize', $('#state-query-limit').value));
  $('#state-prev-page').addEventListener('click', () => loadHistory(pageIndex - 1));
  $('#state-next-page').addEventListener('click', () => loadHistory(pageIndex + 1));
  for (const selector of ['#detail-device-select', '#state-query-device']) $(selector).addEventListener('change', event => selectDevice(event.target.value));
  $('#state-history-body').addEventListener('click', event => {
    const id = event.target.closest('[data-state-sample]')?.dataset.stateSample;
    const sample = page?.items.find(item => item.id === Number(id));
    if (!sample) return;
    $('#state-sample-title').textContent = `${selectedDevice()?.name || '设备'} · ${historyTime(sample.collected_at)}`;
    const extra = [['空闲状态说明', activityReason(sample)], ['该设备最新收益', sample.last_device_earning_at ? historyTime(sample.last_device_earning_at) : '未取得'], ['收益最后同步', sample.last_earnings_sync_at ? historyTime(sample.last_earnings_sync_at) : '未取得'], ['收益 Provider ID', sample.earnings_provider_id || '未取得'], ['采集耗时', `${sample.duration_ms ?? '未知'} ms`], ['Darkbloom 版本', sample.provider_version || '未提供'], ['进程 PID', sample.provider_pid ?? '未提供'], ['本地状态文件更新时间', sample.provider_state_at ? historyTime(sample.provider_state_at) : '未提供']];
    $('#state-sample-details').innerHTML = details(sample) + extra.map(([label, value]) => `<div class="health-item"><span>${label}</span><strong>${escape(value)}</strong></div>`).join('');
    $('#state-sample-dialog').showModal();
  });
  $('#close-state-sample').addEventListener('click', () => $('#state-sample-dialog').close());
  renderHistory();
  return {
    render, refresh, queryHistory,
    enterQuery() { render(); if (!page && !busy) queryHistory(); refresh(); },
    leaveQuery() { controller?.abort(); generation++; busy = false; renderHistory(); },
  };
}
