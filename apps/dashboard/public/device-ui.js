import { deviceFields, reading, activityStatus, pressureStatus, sampleStale } from './device-state.js';

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
    const message = $('#device-state-message'), grid = $('#device-state-details');
    const sample = response?.sample;
    message.hidden = true; message.className = 'state-message'; grid.innerHTML = '';
    $('#device-state-time').textContent = sample ? `更新于 ${dateTime(sample.collected_at)}` : '';
    if (response?.error) { message.hidden = false; message.classList.add('warning'); message.textContent = response.error; return; }
    if (!response) { message.hidden = false; message.textContent = '正在读取…'; return; }
    if (!sample) { message.hidden = false; message.textContent = '暂无状态数据'; return; }
    const stale = sampleStale(response);
    if (stale) { message.hidden = false; message.classList.add('warning'); message.textContent = '状态数据较旧，以下为最近一次读数。'; }
    grid.innerHTML = details(sample);
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
