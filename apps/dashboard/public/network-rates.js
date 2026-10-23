import { AppError } from './errors.js';
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

export function formatRecommendationTopbar({ rates, unsupported, error }) {
  if (unsupported || error || !rates) return '—';
  const rec = rates.recommendation;
  if (!rec) return '—';
  const name = `${displayModelName(rec.model)}${rec.fallback ? '（兜底）' : ''}`;
  return `${name} · ${formatUsdPerHourMicro(rec.usd_per_hour_micro)}`;
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
  return switches.map(item => {
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

export function createNetworkRatesUI({ query, submitSettings, selectedDevice, currentView, escape, dateTime }) {
  const $ = selector => document.querySelector(selector);
  let deviceID, rates = null, switches = [], loading = false, submitting = false, error = '', unsupported = false, generation = 0, pendingToggle = null;
  const dialog = $('#network-rates-confirm-dialog');

  function render() {
    const device = selectedDevice();
    if (device?.id !== deviceID) {
      deviceID = device?.id; rates = null; switches = []; loading = false; submitting = false; error = ''; unsupported = false; generation++;
      if (dialog?.open) dialog.close();
    }
    const demo = deviceID === 'demo';
    $('#network-rates-device-label').textContent = device?.name || '当前设备';
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
    if (unsupported) $('#network-rates-switches').innerHTML = '';
    else if (loading && !switches.length) $('#network-rates-switches').innerHTML = '<p class="control-note">正在读取切换记录…</p>';
    else $('#network-rates-switches').innerHTML = renderSwitchRecords(switches, escape, dateTime);
  }

  async function refresh() {
    render();
    if (!deviceID || deviceID === 'demo') {
      if (deviceID === 'demo') {
        loading = true; render();
        try {
          const [ratesResponse, switchesResponse] = await Promise.all([query('network-rates', null, deviceID), query('network-rates-switches', null, deviceID)]);
          if (selectedDevice()?.id === deviceID) { rates = ratesResponse; switches = switchesResponse.switches; unsupported = false; error = ''; }
        } catch (failure) {
          if (selectedDevice()?.id === deviceID) { error = failure.message; rates = null; switches = []; }
        } finally {
          if (selectedDevice()?.id === deviceID) { loading = false; render(); }
        }
      }
      return;
    }
    const id = deviceID, request = ++generation;
    loading = true; error = ''; unsupported = false; render();
    try {
      const [ratesResponse, switchesResponse] = await Promise.all([query('network-rates', null, id), query('network-rates-switches', null, id)]);
      if (request !== generation || selectedDevice()?.id !== id) return;
      rates = ratesResponse; switches = switchesResponse.switches;
    } catch (failure) {
      if (request !== generation || selectedDevice()?.id !== id) return;
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

  return { render, refresh };
}
