import { randomID } from './identifiers.js';
import { displayModelName } from './models.js';
import { isPermanentControlError, isTransientControlError, nextControlPollDelayMs, shouldControlAutoPoll } from './control-poll.js';

const actions = { start: '开启 Darkbloom', stop: '关闭 Darkbloom', restart: '重启 Darkbloom', force_restart: '强制重启 Darkbloom', idle_restart: '无任务自动重启', switch: '切换模型', download: '下载模型' };
const RECENT_JOB_LIMIT = 5;
const statuses = { running: '执行中', succeeded: '已完成', failed: '执行失败', unconfirmed: '结果未确认' };
const reasons = {
  command_failed: '命令未完成。请检查设备端 Darkbloom 状态、版本、账户登录、磁盘空间和模型兼容性。关闭或切换超时后可能仍在等待任务结束，请核对后再操作。',
  command_timeout: '超过执行时限，实际状态可能已经改变，请核对设备状态。',
  agent_interrupted: 'Agent 中途退出，实际状态可能已经改变，请核对后再操作。',
  history_unavailable: '操作记录保存失败，请在设备端核对结果。',
};
const localErrorText = code => code === 'control_busy' ? '设备正在读取模型，请稍后点击「获取最新」重试。' : '本地模型读取失败，可点击「获取最新」重试。';

function renderControlJob(job, escape, dateTime) {
  const time = escape(dateTime(job.created_at));
  const meta = job.models?.length
    ? `<span class="control-job-meta"><span class="control-job-time">${time}</span><span class="control-job-model-names" title="${escape(job.models.join('、'))}">${job.models.map(id => escape(displayModelName(id))).join('、')}</span></span>`
    : `<small>${time}</small>`;
  const error = job.error ? `<small>${escape(reasons[job.error] || '请核对设备端执行结果。')}</small>` : '';
  const tagClass = job.status === 'failed' || job.status === 'unconfirmed' ? ' warning' : '';
  return `<div class="control-job"><span>${escape(actions[job.action] || job.action)}${meta}${error}</span><span class="status-tag${tagClass}">${escape(statuses[job.status] || '未知')}</span></div>`;
}

export function createControlUI({ submitControl, submitLongTaskRestart, submitNoTaskRestart, models: modelsStore, query, selectedDevice, currentView, escape, dateTime, refreshState }) {
  const $ = selector => document.querySelector(selector);
  let deviceID, status = null, modelsLoading = false, loading = false, submitting = false, pending = null, confirmation = null, error = '', requestGeneration = 0;
  let lastFailure = null, autoPollStopped = false, networkFailStreak = 0, runningSinceMs = null, pendingSinceMs = null, pollTimer = null;
  let longTaskEnabled = false, longTaskUnsupported = false, longTaskError = '', longTaskSubmitting = false, pendingLongTask = null;
  let noTaskEnabled = false, noTaskUnsupported = false, noTaskError = '', noTaskSubmitting = false, pendingNoTask = null;
  function settingsBusy() { return longTaskSubmitting || noTaskSubmitting; }
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
  // Polling refreshes the job list only. Model discovery is global data and is
  // never re-fetched on a timer; the block buttons and the panel refresh do that.
  // A successful download is the one exception: it changes the local list.
  async function tickPoll() {
    if (currentView() !== 'devices' || document.hidden || dialog.open) return;
    const running = status?.jobs?.find(job => job.status === 'running');
    await refresh();
    const finished = running && status?.jobs?.find(job => job.request_id === running.request_id);
    if (running && status && !status.jobs?.some(job => job.status === 'running')) {
      if (finished?.action === 'download' && finished.status === 'succeeded') await refreshModels();
      refreshState();
    }
    maybeSchedulePoll();
  }
  function renderModels() {
    const local = $('#control-local-models'), catalog = $('#control-download-model');
    const chosen = new Set([...local.selectedOptions].map(option => option.value));
    const snapshot = modelsStore?.get(deviceID) || null;
    local.innerHTML = snapshot ? snapshot.local.map(model => `<option value="${escape(model.id)}" title="${escape(model.id)}" ${chosen.has(model.id) ? 'selected' : ''}>${escape(model.display_name || displayModelName(model.id))}</option>`).join('') : '';
    const download = catalog.value;
    catalog.innerHTML = snapshot
      ? '<option value="">选择要下载的模型</option>' + snapshot.catalog.map(model => `<option value="${escape(model.id)}" title="${escape(model.id)}" ${download === model.id ? 'selected' : ''}>${escape(model.display_name || displayModelName(model.id))}${model.size_gb ? ` · ${escape(model.size_gb)} GB` : ''}</option>`).join('')
      : '<option value="">请先获取最新模型目录</option>';
  }
  function render() {
    const device = selectedDevice();
    if (device?.id !== deviceID) {
      deviceID = device?.id; status = null; modelsLoading = false; loading = false; submitting = false; error = ''; confirmation = null; requestGeneration++;
      longTaskEnabled = false; longTaskUnsupported = false; longTaskError = ''; longTaskSubmitting = false; pendingLongTask = null;
      noTaskEnabled = false; noTaskUnsupported = false; noTaskError = ''; noTaskSubmitting = false; pendingNoTask = null;
      resetPollState(); runningSinceMs = null; pendingSinceMs = null; clearPollTimer();
      if (dialog.open) dialog.close();
      try { pending = JSON.parse(localStorage.getItem(`bloomer.control.${deviceID}`) || 'null'); } catch { pending = null; }
      renderModels();
      if (currentView() === 'devices') queueMicrotask(() => refresh());
    }
    const demo = deviceID === 'demo', running = status?.jobs?.some(job => job.status === 'running');
    const snapshot = modelsStore?.get(deviceID) || null;
    const longTaskToggle = $('#long-task-restart');
    if (longTaskToggle) {
      longTaskToggle.checked = longTaskEnabled;
      longTaskToggle.disabled = demo || longTaskUnsupported || settingsBusy() || loading || submitting;
      $('#long-task-restart-note').textContent = demo
        ? '演示设备不会因任务超时强制重启。'
        : longTaskUnsupported
          ? '当前设备 Agent 不支持此开关，请升级 Agent。'
          : longTaskError || '开启后，当前模型为 Qwen3.5 9B 且同一任务连续执行超过 10 分钟时，Agent 会强制重启 Darkbloom，未完成任务会被中断。';
    }
    const noTaskToggle = $('#no-task-restart');
    if (noTaskToggle) {
      noTaskToggle.checked = noTaskEnabled;
      noTaskToggle.disabled = demo || noTaskUnsupported || settingsBusy() || loading || submitting;
      $('#no-task-restart-note').textContent = demo
        ? '演示设备不会因没有接到任务自动重启。'
        : noTaskUnsupported
          ? '当前设备 Agent 不支持此开关，请升级 Agent。'
          : noTaskError || '开启后，Darkbloom 连续 10 分钟没有接到新任务时，Agent 会自动重启。重启后重新计时，不会连续重启。';
    }
    $('#control-panel').setAttribute('aria-busy', String(loading || submitting));
    $('#control-refresh').disabled = demo || loading || submitting;
    // Each block refreshes its own list; both read the same discovery response.
    $('#control-local-refresh').disabled = demo || submitting || modelsLoading || !status?.available;
    $('#control-catalog-refresh').disabled = demo || submitting || modelsLoading || !status?.available;
    const disabled = demo || loading || submitting || pending || running || !status?.available;
    const chosen = [...$('#control-local-models').selectedOptions].length;
    const localBlocked = Boolean(disabled || modelsLoading || snapshot?.localError);
    const catalogBlocked = Boolean(disabled || modelsLoading || snapshot?.catalogError);
    document.querySelectorAll('[data-control]').forEach(button => {
      // Stop and restart need no model selection, so a pending discovery must not
      // block them; only the model-dependent actions wait for the snapshot.
      button.disabled = Boolean(disabled || (['switch', 'start'].includes(button.dataset.control) && (!chosen || localBlocked)) || (button.dataset.control === 'download' && (!$('#control-download-model').value || catalogBlocked)));
    });
    $('#control-local-models').disabled = localBlocked;
    $('#control-download-model').disabled = catalogBlocked;
    const catalogUnavailable = '官方目录暂不可用；本地模型仍可选择。请检查设备网络后点击「获取最新」重试。';
    $('#control-local-error').textContent = snapshot?.error ? `模型读取失败：${snapshot.error} 可点击「获取最新」重试。`
      : modelsLoading && !snapshot ? '正在读取本地模型…'
        : snapshot?.localError ? localErrorText(snapshot.localError)
          : snapshot && !snapshot.local.length ? '没有可用的本地模型，请先下载。' : '';
    $('#control-catalog-error').textContent = modelsLoading && !snapshot ? '正在读取官方目录…'
      : snapshot?.error || snapshot?.catalogError ? catalogUnavailable : '';
    $('#control-retry').hidden = !pending || demo;
    $('#control-retry').disabled = loading || submitting || Boolean(running) || !status?.available;
    $('#control-jobs').innerHTML = status?.jobs?.length ? status.jobs.slice(0, RECENT_JOB_LIMIT).map(job => renderControlJob(job, escape, dateTime)).join('') : '<p class="control-note">暂无操作记录</p>';
    // `error` covers the panel-level failures that have no per-field note, notably
    // an unreachable device or a rejected submission. Without it the panel used to
    // go silently inert.
    const errorElement = $('#control-error');
    errorElement.textContent = error;
    errorElement.hidden = !error;
  }
  async function loadLongTask(id, generation) {
    try {
      const settings = await query('long-task-restart', null, id);
      if (id !== deviceID || generation !== requestGeneration) return;
      longTaskEnabled = Boolean(settings.enabled);
      longTaskUnsupported = false;
      longTaskError = '';
    } catch (failure) {
      if (id !== deviceID || generation !== requestGeneration) return;
      if (failure.code === 'long_task_restart_unsupported') { longTaskUnsupported = true; longTaskError = ''; }
      else longTaskError = failure.message;
    }
  }
  async function loadNoTask(id, generation) {
    try {
      const settings = await query('no-task-restart', null, id);
      if (id !== deviceID || generation !== requestGeneration) return;
      noTaskEnabled = Boolean(settings.enabled);
      noTaskUnsupported = false;
      noTaskError = '';
    } catch (failure) {
      if (id !== deviceID || generation !== requestGeneration) return;
      if (failure.code === 'no_task_restart_unsupported') { noTaskUnsupported = true; noTaskError = ''; }
      else noTaskError = failure.message;
    }
  }
  async function applyLongTask(enabled) {
    const id = deviceID;
    if (!id || id === 'demo' || longTaskSubmitting || !submitLongTaskRestart) return;
    const generation = requestGeneration;
    const previous = longTaskEnabled;
    longTaskEnabled = enabled;
    longTaskSubmitting = true; longTaskError = ''; render();
    try {
      const settings = await submitLongTaskRestart(id, enabled);
      if (id !== deviceID || generation !== requestGeneration) return;
      longTaskEnabled = Boolean(settings.enabled);
    } catch (failure) {
      if (id !== deviceID || generation !== requestGeneration) return;
      longTaskEnabled = previous;
      longTaskError = failure.message;
    } finally {
      if (id === deviceID && generation === requestGeneration) { longTaskSubmitting = false; render(); }
    }
  }
  async function applyNoTask(enabled) {
    const id = deviceID;
    if (!id || id === 'demo' || noTaskSubmitting || !submitNoTaskRestart) return;
    const generation = requestGeneration;
    const previous = noTaskEnabled;
    noTaskEnabled = enabled;
    noTaskSubmitting = true; noTaskError = ''; render();
    try {
      const settings = await submitNoTaskRestart(id, enabled);
      if (id !== deviceID || generation !== requestGeneration) return;
      noTaskEnabled = Boolean(settings.enabled);
    } catch (failure) {
      if (id !== deviceID || generation !== requestGeneration) return;
      noTaskEnabled = previous;
      noTaskError = failure.message;
    } finally {
      if (id === deviceID && generation === requestGeneration) { noTaskSubmitting = false; render(); }
    }
  }
  // Discovery runs only when this device has no snapshot, or when a caller asks
  // for fresh lists. Cached lists render immediately and never block stop/restart.
  async function discover(id, generation, force) {
    if (!id || id === 'demo' || !modelsStore) return;
    if (!force && modelsStore.get(id)) { renderModels(); render(); return; }
    modelsLoading = true; render();
    await modelsStore.load(id);
    if (id === deviceID && generation === requestGeneration) { modelsLoading = false; renderModels(); render(); }
  }
  async function refresh({ models = false } = {}) {
    render();
    if (!deviceID || loading || submitting || settingsBusy()) return;
    if (deviceID === 'demo') {
      longTaskEnabled = false; longTaskUnsupported = false; longTaskError = '';
      noTaskEnabled = false; noTaskUnsupported = false; noTaskError = '';
      render();
      return;
    }
    const id = deviceID, generation = ++requestGeneration;
    loading = true; error = ''; render();
    const switchDone = loadLongTask(id, generation);
    const noTaskDone = loadNoTask(id, generation);
    const modelsDone = discover(id, generation, models);
    try {
      const response = await query('control', null, id);
      if (id !== deviceID || generation !== requestGeneration) return;
      status = response;
      lastFailure = null; networkFailStreak = 0; autoPollStopped = false;
      syncRunningSince();
      if (pending && status.jobs?.some(job => job.request_id === pending.request_id)) remember(null);
    } catch (failure) {
      if (id === deviceID && generation === requestGeneration) {
        error = failure.message;
        lastFailure = failure;
        if (isPermanentControlError(failure)) { autoPollStopped = true; status = status ?? { enabled: false, available: false, jobs: [] }; }
        else if (isTransientControlError(failure)) { networkFailStreak++; status = null; }
        else { status = null; }
      }
    }
    finally {
      if (id === deviceID && generation === requestGeneration) {
        await switchDone; await noTaskDone; await modelsDone;
        loading = false; render(); maybeSchedulePoll();
      }
    }
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
  async function refreshModels() {
    const id = deviceID, generation = requestGeneration;
    if (!id || id === 'demo' || !modelsStore || modelsLoading) return;
    modelsLoading = true; render();
    await modelsStore.load(id);
    if (id === deviceID && generation === requestGeneration) { modelsLoading = false; renderModels(); render(); }
  }
  $('#control-local-refresh').addEventListener('click', refreshModels);
  $('#control-catalog-refresh').addEventListener('click', refreshModels);
  $('#control-local-models').addEventListener('change', render);
  $('#control-download-model').addEventListener('change', render);
  // The panel refresh reloads the job list and the model lists together.
  $('#control-refresh').addEventListener('click', () => { resetPollState(); refresh({ models: true }).then(refreshState); });
  const longDialog = $('#long-task-restart-dialog');
  $('#long-task-restart')?.addEventListener('change', event => {
    const input = event.target;
    const next = input.checked;
    input.checked = !next;
    pendingLongTask = next;
    $('#long-task-restart-confirm-message').textContent = next
      ? '确认开启 Qwen3.5 9B 超时强制重启？\n\n开启后，若当前模型是 Qwen3.5 9B，且同一任务连续执行超过 10 分钟，Agent 会强制重启 Darkbloom。未完成的任务会被中断。其他模型不会触发。'
      : '确认关闭 Qwen3.5 9B 超时强制重启？\n\n关闭后不再因该模型任务超时自动重启。';
    longDialog.returnValue = ''; longDialog.showModal();
  });
  longDialog?.addEventListener('close', () => {
    const next = pendingLongTask; pendingLongTask = null;
    if (longDialog.returnValue === 'confirm' && next !== null) applyLongTask(next);
  });
  const noTaskDialog = $('#no-task-restart-dialog');
  $('#no-task-restart')?.addEventListener('change', event => {
    const input = event.target;
    const next = input.checked;
    input.checked = !next;
    pendingNoTask = next;
    $('#no-task-restart-confirm-message').textContent = next
      ? '确认开启连续 10 分钟无任务自动重启？\n\n开启后，若 Darkbloom 连续 10 分钟没有接到新任务，Agent 会自动重启。重启后重新计时，不会连续重启。'
      : '确认关闭连续 10 分钟无任务自动重启？\n\n关闭后不再因没有接到任务自动重启。';
    noTaskDialog.returnValue = ''; noTaskDialog.showModal();
  });
  noTaskDialog?.addEventListener('close', () => {
    const next = pendingNoTask; pendingNoTask = null;
    if (noTaskDialog.returnValue === 'confirm' && next !== null) applyNoTask(next);
  });
  document.addEventListener('visibilitychange', () => { if (!document.hidden) maybeSchedulePoll(); else clearPollTimer(); });
  return { render, refresh, refreshModels };
}
