import { deviceFields, reading, activityStatus, activityReason, pressureStatus, sampleStale } from './device-state.js';

export function createDeviceUI({ query, selectedDevice, deviceChoices, selectDevice, dateTime, escape, onRefresh }) {
  const $ = selector => document.querySelector(selector);
  const latest = new Map(), pending = new Map();
  let selected, renderedDevice;
  const cell = (sample, key) => {
    const value = reading(sample, key);
    return `<span class="${value.known ? '' : 'reading-unknown'}" title="${escape(value.reason || value.text)}">${escape(value.text)}</span>`;
  };
  const details = sample => deviceFields.map(([key, label]) => `<div class="health-item"><span>${label}</span><strong>${cell(sample, key)}</strong></div>`).join('');

  function renderLatest() {
    const device = selectedDevice()?.id, response = latest.get(device);
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
    const element = $('#detail-device-select');
    if (element.innerHTML !== choices) element.innerHTML = choices;
    if (selected !== id) {
      selected = id;
      renderLatest();
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

  $('#detail-device-select').addEventListener('change', event => selectDevice(event.target.value));
  return { render, refresh };
}
