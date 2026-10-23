import { collectionStatusItems } from './collection-status.js';
import { deviceStateSections, reading, activityStatus, pressureStatus, taskStatusField, memoryField, memoryReading, runningModelLabel } from './device-state.js';
import { displayModelName } from './models.js';

export function createDeviceUI({ query, selectedDevice, escape, onRefresh }) {
  const $ = selector => document.querySelector(selector);
  const latest = new Map(), pending = new Map(), collectionInputs = new Map();
  let selected, renderedDevice;
  const cell = (sample, key) => {
    const value = reading(sample, key);
    const text = key === 'current_model' && value.known ? displayModelName(value.text) : value.text;
    const title = key === 'current_model' && value.known ? value.text : (value.reason || value.text);
    return `<span class="${value.known ? '' : 'reading-unknown'}" title="${escape(title)}">${escape(text)}</span>`;
  };
  const taskStatusCell = response => {
    const status = activityStatus(response);
    const muted = ['未知', '未运行', '状态未提供', '状态读取失败', '等待状态采集', '读取状态中'].includes(status.label);
    return `<span class="${muted ? 'reading-unknown' : ''}" title="${escape(status.detail)}">${escape(status.label)}</span>`;
  };
  const memoryCell = sample => {
    const value = memoryReading(sample);
    return `<span class="${value.known ? '' : 'reading-unknown'}" title="${escape(value.reason || value.text)}">${escape(value.text)}</span>`;
  };
  const fieldRow = (key, label, sample, response) => {
    const content = key === taskStatusField ? taskStatusCell(response) : key === memoryField ? memoryCell(sample) : cell(sample, key);
    return `<div class="health-item"><span>${label}</span><strong>${content}</strong></div>`;
  };
  const statusTagRow = ({ label, text, className, detail }) => `<div class="health-item"><span>${escape(label)}</span><strong class="status-tag ${className}" title="${escape(detail)}">${escape(text)}</strong></div>`;
  const agentVersionRow = health => {
    const text = health?.error ? '—' : health?.version || '—';
    const title = health?.error || '';
    const muted = text === '—';
    return `<div class="health-item"><span>Agent 版本</span><strong class="${muted ? 'reading-unknown' : ''}" title="${escape(title)}">${escape(text)}</strong></div>`;
  };
  const agentRows = (health, deviceState, networkRates, response) => {
    const items = collectionStatusItems({ health, deviceState: deviceState ? response : undefined, networkRates });
    return agentVersionRow(health) + items.map(statusTagRow).join('');
  };
  const stateSection = (title, rowsHtml) => `<section class="device-state-section"><h3 class="device-state-section-title">${escape(title)}</h3><div class="health-details">${rowsHtml}</div></section>`;
  const detailsSections = (sample, response, health, networkRates) => {
    const sections = deviceStateSections.map(({ title, fields }) => stateSection(title, fields.map(([key, label]) => fieldRow(key, label, sample, response)).join('')));
    sections.push(stateSection('Agent 状态', agentRows(health, true, networkRates, response)));
    return sections.join('');
  };
  const agentSectionOnly = (health, networkRates, response) => stateSection('Agent 状态', agentRows(health, !!response, networkRates, response));

  function renderLatest() {
    const device = selectedDevice()?.id, response = latest.get(device);
    if (response && device === renderedDevice && pending.has(device)) return;
    renderedDevice = device;
    const status = activityStatus(response);
    const runningModel = runningModelLabel(response), modelElement = $('#current-running-model');
    modelElement.textContent = runningModel.text; modelElement.title = runningModel.title;
    const statusElement = $('#activity-status');
    statusElement.textContent = status.label; statusElement.className = `status-tag ${status.className}`; statusElement.title = status.detail;
    for (const [selector, key] of [['#thermal-status', 'thermal_state'], ['#memory-pressure-status', 'memory_pressure']]) {
      const value = pressureStatus(response, key), element = $(selector);
      element.textContent = value.label; element.className = `status-tag ${value.className}`; element.title = value.detail;
    }
    const inputs = collectionInputs.get(device);
    const health = inputs?.health, networkRates = inputs?.networkRates;
    const message = $('#device-state-message'), grid = $('#device-state-details');
    const sample = response?.sample;
    message.hidden = true; message.className = 'state-message';
    if (response?.error) { message.hidden = false; message.classList.add('warning'); message.textContent = response.error; grid.innerHTML = agentSectionOnly(health, networkRates, response); return; }
    if (!response) { message.hidden = false; message.textContent = '正在读取…'; grid.innerHTML = agentSectionOnly(health, networkRates, response); return; }
    if (!sample) { message.hidden = false; message.textContent = '暂无状态数据'; grid.innerHTML = agentSectionOnly(health, networkRates, response); return; }
    grid.innerHTML = detailsSections(sample, response, health, networkRates);
  }

  function syncDevices() {
    const id = selectedDevice()?.id;
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
      const [stateResult, healthResult, ratesResult] = await Promise.allSettled([
        query('device-state', null, device),
        query('health', null, device),
        query('network-rates', null, device),
      ]);
      if (stateResult.status === 'fulfilled') {
        const response = stateResult.value;
        if (!response || !Object.hasOwn(response, 'sample') || (response.sample !== null && (typeof response.sample !== 'object' || !Number.isFinite(Date.parse(response.sample.collected_at))))) {
          latest.set(device, { error: '设备返回的状态数据无效。' });
        } else {
          latest.set(device, response);
          if (device === selectedDevice()?.id) onRefresh?.();
        }
      } else latest.set(device, { error: stateResult.reason.message, code: stateResult.reason.code });
      const settled = result => result.status === 'fulfilled' ? result.value : { error: result.reason.message, code: result.reason.code };
      collectionInputs.set(device, { health: settled(healthResult), networkRates: settled(ratesResult) });
      pending.delete(device);
      render();
    })();
    pending.set(device, work);
    return work;
  }

  return { render, refresh };
}
