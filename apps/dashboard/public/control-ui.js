import { randomID } from './identifiers.js';
import { isPermanentControlError, isTransientControlError, nextControlPollDelayMs, shouldControlAutoPoll } from './control-poll.js';

const actions = { start: '开启 Darkbloom', stop: '关闭 Darkbloom', restart: '重启 Darkbloom', switch: '切换模型', download: '下载模型' };
const statuses = { running: '执行中', succeeded: '已完成', failed: '执行失败', unconfirmed: '结果未确认' };
const reasons = {
  command_failed: '命令未完成。请检查设备端 Darkbloom 状态、版本、账户登录、磁盘空间和模型兼容性。关闭或切换超时后可能仍在等待任务结束，请核对后再操作。',
  command_timeout: '超过执行时限，实际状态可能已经改变，请核对设备状态。',
  agent_interrupted: 'Agent 中途退出，实际状态可能已经改变，请核对后再操作。',
  history_unavailable: '操作记录保存失败，请在设备端核对结果。',
};

export function createControlUI({ submitControl, query, selectedDevice, currentView, escape, dateTime, refreshState }) {
  const $ = selector => document.querySelector(selector);
  let deviceID, status = null, models = null, loading = false, submitting = false, pending = null, confirmation = null, error = '', requestGeneration = 0;
  let lastFailure = null, autoPollStopped = false, networkFailStreak = 0, runningSinceMs = null, pendingSinceMs = null, pollTimer = null;
  const dialog = $('#control-confirm-dialog');
  function remember(value) {
    pending = value;
    try { if (value) localStorage.setItem(`bloomer.control.${deviceID}`, JSON.stringify(value)); else localStorage.removeItem(`bloomer.control.${deviceID}`); } catch { /* API deduplication also protects live retries. */ }
  }
  function syncRunningSince() {
    const running = status?.jobs?.find(job => job.status === 'running');
    if (running) {
      const created = Date.parse(running.created_at);
      runningSinceMs = Number.isFinite(created) ? created : runningSinceMs ?? Date.now();
    } else runningSinceMs = null;
  }
  function clearPollTimer() { if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; } }
  function schedulePoll(delayMs) {
    clearPollTimer();
    pollTimer = setTimeout(() => { pollTimer = null; tickPoll(); }, delayMs);
  }
  function resetPollState() { autoPollStopped = false; networkFailStreak = 0; lastFailure = null; }
  function pollDelayMs() {
    if (pending && !pendingSinceMs) pendingSinceMs = Date.now();
    if (!pending) pendingSinceMs = null;
    const activeSince = runningSinceMs ?? pendingSinceMs;
    return nextControlPollDelayMs({ runningSinceMs: activeSince, pending: Boolean(pending), networkFailStreak, nowMs: Date.now() });
  }
  function maybeSchedulePoll() {
    clearPollTimer();
    if (currentView() !== 'devices' || document.hidden || dialog.open || deviceID === 'demo') return;
    const running = status?.jobs?.some(job => job.status === 'running');
    if (!shouldControlAutoPoll({ autoPollStopped, pending: Boolean(pending), running, awaitingStatus: !status && !autoPollStopped, transientError: !status && lastFailure && isTransientControlError(lastFailure) })) return;
    schedulePoll(pollDelayMs());
  }
  async function tickPoll() {
    if (currentView() !== 'devices' || document.hidden || dialog.open) return;
    const wasRunning = status?.jobs?.some(job => job.status === 'running');
    await refresh();
    if (wasRunning && status && !status.jobs?.some(job => job.status === 'running')) { await refresh(true); refreshState(); }
    maybeSchedulePoll();
  }
  function render() {
    const device = selectedDevice();
    if (device?.id !== deviceID) {
      deviceID = device?.id; status = null; models = null; loading = false; submitting = false; error = ''; confirmation = null; requestGeneration++;
      resetPollState(); runningSinceMs = null; pendingSinceMs = null; clearPollTimer();
      if (dialog.open) dialog.close();
      try { pending = JSON.parse(localStorage.getItem(`bloomer.control.${deviceID}`) || 'null'); } catch { pending = null; }
      $('#control-local-models').innerHTML = '';
      $('#control-download-model').innerHTML = '<option value="">请先刷新模型目录</option>';
      if (currentView() === 'devices') queueMicrotask(() => refresh(true));
    }
    const demo = deviceID === 'demo', running = status?.jobs?.some(job => job.status === 'running');
    $('#control-device-label').textContent = device?.name || '当前设备';
    $('#control-message').textContent = demo ? '演示设备不执行远程操作。连接真实设备后可使用。' : error || (loading ? '正在读取设备的操作能力与模型…' : !status ? '正在检查远程操作能力…' : !status.enabled ? '设备端已禁用远程操作。' : !status.available ? '远程操作不可用，请检查设备端 Darkbloom 安装与 Agent 配置。' : pending ? '上次提交结果尚未确认。请核对操作记录，或使用相同请求编号重试。' : running ? '设备正在执行操作，完成后可提交下一项。' : '可远程操作此设备的 Darkbloom。');
    $('#control-panel').setAttribute('aria-busy', String(loading || submitting));
    $('#control-refresh').disabled = demo || loading || submitting;
    const disabled = demo || loading || submitting || pending || running || !status?.available;
    const chosen = [...$('#control-local-models').selectedOptions].length;
    document.querySelectorAll('[data-control]').forEach(button => { button.disabled = Boolean(disabled || (['switch', 'start'].includes(button.dataset.control) && !chosen) || (button.dataset.control === 'download' && !$('#control-download-model').value)); });
    $('#control-local-models').disabled = Boolean(disabled || models?.local_error);
    $('#control-download-model').disabled = Boolean(disabled || models?.catalog_error);
    $('#control-local-error').textContent = models?.local_error ? '本地模型读取失败，可刷新重试。' : models && !models.local?.length ? '没有可用的本地模型，请先下载。' : '';
    $('#control-catalog-error').textContent = models?.catalog_error ? '官方目录暂不可用；本地模型仍可选择。请检查设备网络后刷新。' : '';
    $('#control-retry').hidden = !pending || demo;
    $('#control-retry').disabled = loading || submitting || Boolean(running) || !status?.available;
    $('#control-jobs').innerHTML = status?.jobs?.length ? status.jobs.map(job => `<div class="control-job"><span>${escape(actions[job.action] || job.action)}${job.models?.length ? `<small>${job.models.map(escape).join('、')}</small>` : ''}<small>${escape(dateTime(job.created_at))} · ${escape(job.id)}</small>${job.error ? `<small>${escape(reasons[job.error] || '请核对设备端执行结果。')}</small>` : ''}</span><span class="status-tag ${job.status === 'failed' || job.status === 'unconfirmed' ? 'warning' : ''}">${escape(statuses[job.status] || '未知')}</span></div>`).join('') : '<p class="control-note">暂无操作记录</p>';
  }
  async function refresh(withModels = false) {
    render();
    if (!deviceID || deviceID === 'demo' || loading || submitting) return;
    const id = deviceID, generation = ++requestGeneration;
    loading = true; error = ''; render();
    try {
      const response = await query('control', null, id);
      if (id !== deviceID || generation !== requestGeneration) return;
      status = response;
      lastFailure = null; networkFailStreak = 0; autoPollStopped = false;
      syncRunningSince();
      if (pending && status.jobs?.some(job => job.request_id === pending.request_id)) remember(null);
      if (withModels && status.available) {
        const result = await query('control-models', null, id);
        if (id !== deviceID || generation !== requestGeneration) return;
        models = result;
        const selected = new Set([...$('#control-local-models').selectedOptions].map(option => option.value));
        $('#control-local-models').innerHTML = models.local.map(model => `<option value="${escape(model.id)}" ${selected.has(model.id) ? 'selected' : ''}>${escape(model.display_name || model.id)}</option>`).join('');
        const download = $('#control-download-model').value;
        $('#control-download-model').innerHTML = '<option value="">选择要下载的模型</option>' + models.catalog.map(model => `<option value="${escape(model.id)}" ${download === model.id ? 'selected' : ''}>${escape(model.display_name || model.id)}${model.size_gb ? ` · ${escape(model.size_gb)} GB` : ''}</option>`).join('');
      }
    } catch (failure) {
      if (id === deviceID && generation === requestGeneration) {
        error = failure.message;
        lastFailure = failure;
        if (isPermanentControlError(failure)) { autoPollStopped = true; status = status ?? { enabled: false, available: false, jobs: [] }; }
        else if (isTransientControlError(failure)) { networkFailStreak++; status = null; }
        else { status = null; }
      }
    }
    finally { if (id === deviceID && generation === requestGeneration) { loading = false; render(); maybeSchedulePoll(); } }
  }
  async function submit(body, id) {
    if (id !== deviceID || submitting) return;
    const generation = requestGeneration;
    remember(body); submitting = true; error = ''; render();
    try {
      await submitControl(id, body);
      if (id !== deviceID || generation !== requestGeneration) return;
      remember(null);
    } catch (failure) {
      if (id !== deviceID || generation !== requestGeneration) return;
      // A network failure can happen after acceptance. Keep the request ID for
      // reconciliation and safe retries, including after reloading the page.
      if (failure.code && failure.status < 500 && !['device_unreachable', 'internal_error', 'invalid_response'].includes(failure.code)) remember(null);
      error = `${failure.message}${pending ? ' 提交结果未确认，请先核对操作记录。' : ''}`;
    } finally {
      if (id === deviceID && generation === requestGeneration) { submitting = false; render(); if (!error) await refresh(); }
    }
  }
  document.querySelectorAll('[data-control]').forEach(button => button.addEventListener('click', () => {
    const action = button.dataset.control;
    const chosen = ['start', 'switch'].includes(action) ? [...$('#control-local-models').selectedOptions].map(option => option.value) : action === 'download' ? [$('#control-download-model').value] : [];
    confirmation = { id: deviceID, body: { request_id: randomID(), action, ...(chosen.length ? { models: chosen } : {}) } };
    $('#control-confirm-message').textContent = `设备：${selectedDevice().name}\n操作：${actions[action]}${chosen.length ? '\n模型：' + chosen.join('、') : ''}\n\n${action === 'download' ? '下载将占用设备网络和磁盘空间。' : action === 'switch' ? '将替换整套托管模型；等待已接收任务完成后生效。' : action === 'start' ? '将使用所选模型启动服务，沿用设备上的其他配置。' : '将先等待已接收的任务完成，再关闭或重启服务。'}`;
    dialog.returnValue = ''; dialog.showModal();
  }));
  dialog.addEventListener('close', () => {
    const selected = confirmation; confirmation = null;
    if (dialog.returnValue === 'confirm' && selected?.id === deviceID) submit(selected.body, selected.id);
  });
  $('#control-retry').addEventListener('click', async () => {
    const id = deviceID; await refresh();
    if (id === deviceID && pending && status?.available && !status.jobs.some(job => job.status === 'running')) submit(pending, id);
  });
  $('#control-local-models').addEventListener('change', render);
  $('#control-download-model').addEventListener('change', render);
  $('#control-refresh').addEventListener('click', () => { resetPollState(); refresh(true); });
  document.addEventListener('visibilitychange', () => { if (!document.hidden) maybeSchedulePoll(); else clearPollTimer(); });
  return { render, refresh };
}
