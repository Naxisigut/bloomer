import { randomID } from './identifiers.js';
import { runningPollIntervalMs } from './control-poll.js';
import { displayModelName } from './models.js';

// Switching or downloading from the network view goes through the same control
// job API as the remote-control panel, so the resulting record and its status
// appear in both places. This module owns only the confirmation dialog and the
// status line; the buttons themselves are rendered by the page that shows them.
const actionLabels = { switch: '切换模型', download: '下载模型' };
const statusLabels = { running: '执行中', succeeded: '已完成', failed: '执行失败', unconfirmed: '结果未确认' };
const TERMINAL = new Set(['succeeded', 'failed', 'unconfirmed']);
const POLL_LIMIT_MS = 16 * 60_000;
const POLL_FAILURE_LIMIT = 3;
const AMBIGUOUS_CODES = new Set(['device_unreachable', 'request_timeout', 'internal_error', 'invalid_response']);
const PANEL_HINT = '「我的设备 → 远程操作」';

export function createNetworkSwitch({ query, submitControl, selectedDevice, onChange, onSettled }) {
  const $ = selector => document.querySelector(selector);
  const dialog = $('#network-switch-dialog');
  let deviceID = null, pending = null, current = null, requestID = '';
  let submitting = false, status = '', submitError = '', pollError = '';
  let startedAt = 0, pollFailures = 0, generation = 0, timer = null;

  const modelLabel = model => displayModelName(model) || model;
  const clearTimer = () => { if (timer) { clearTimeout(timer); timer = null; } };
  const notify = () => onChange?.();
  const busy = () => submitting || status === 'running';

  function message() {
    if (submitError) return submitError;
    if (submitting) return `正在提交${actionLabels[pending?.action] || '操作'}：${modelLabel(pending?.model)}…`;
    if (!current || !status) return '';
    const head = `${actionLabels[current.action]}${statusLabels[status] || '未知'}：${modelLabel(current.model)}。`;
    if (status === 'succeeded') return `${head}模型清单已更新，可在${PANEL_HINT}查看记录。`;
    if (status === 'running') return `${head}记录与状态可在${PANEL_HINT}查看。`;
    return `${head}实际状态可能已改变，请在${PANEL_HINT}核对后再操作。`;
  }

  function warning() {
    return Boolean(submitError || pollError || status === 'failed' || status === 'unconfirmed');
  }

  function render() {
    const element = $('#network-switch-status');
    if (!element) return;
    const text = pollError || message();
    element.textContent = text;
    element.hidden = !text;
    element.className = `state-message${warning() ? ' warning' : ''}`;
  }

  function reset() {
    clearTimer(); generation++;
    pending = null; current = null; requestID = '';
    submitting = false; status = ''; submitError = ''; pollError = '';
    startedAt = 0; pollFailures = 0;
    // Clearing the return value first keeps a dialog closed by a device change
    // from firing `close` as if the user had confirmed it.
    if (dialog?.open) { dialog.returnValue = ''; dialog.close(); }
  }

  function schedulePoll(token) {
    clearTimer();
    timer = setTimeout(() => { timer = null; void poll(token); }, runningPollIntervalMs(startedAt));
  }

  // A finished job changes what the table should show: a successful download adds
  // the model to the local list, a successful switch changes `current_model`.
  function finish() {
    clearTimer();
    notify();
    onSettled?.(current, status);
  }

  // Polling reads the same job list the remote-control panel shows, so a switch
  // started here keeps reporting while the user stays on the network page.
  async function poll(token) {
    if (token !== generation || !current) return;
    if (document.hidden) { schedulePoll(token); return; }
    try {
      const body = await query('control', null, deviceID);
      if (token !== generation) return;
      const job = body.jobs?.find(item => item.request_id === requestID);
      if (job) { status = job.status; pollFailures = 0; }
    } catch (failure) {
      if (token !== generation) return;
      pollFailures += 1;
      if (pollFailures >= POLL_FAILURE_LIMIT) {
        pollError = `无法读取执行状态：${failure.message} 请在${PANEL_HINT}核对。`;
        clearTimer(); notify(); return;
      }
      notify(); schedulePoll(token); return;
    }
    if (terminal()) { finish(); return; }
    if (Date.now() - startedAt > POLL_LIMIT_MS) { status = 'unconfirmed'; finish(); return; }
    notify(); schedulePoll(token);
  }

  const terminal = () => TERMINAL.has(status);

  async function submit(body) {
    const token = generation;
    submitting = true; submitError = ''; pollError = ''; status = '';
    notify();
    try {
      const job = await submitControl(deviceID, { request_id: body.request_id, action: body.action, models: [body.models[0]] });
      if (token !== generation) return;
      requestID = body.request_id;
      status = job.status;
      startedAt = Date.now(); pollFailures = 0; submitting = false;
      notify();
      if (status === 'running') schedulePoll(token);
      else if (terminal()) finish();
    } catch (failure) {
      if (token !== generation) return;
      submitting = false;
      // A failure without a status means the request may still have been
      // accepted, so the user is sent to the record instead of retrying blindly.
      const ambiguous = !failure.status || failure.status >= 500 || AMBIGUOUS_CODES.has(failure.code);
      submitError = ambiguous
        ? `${failure.message} 提交结果未确认，请前往${PANEL_HINT}核对记录后再操作。`
        : failure.message;
      notify();
    }
  }

  // The dialog is the only confirmation point, so no row click or menu item can
  // post a control job on its own.
  function open(action, model) {
    if (!dialog || !model || busy() || !deviceID || deviceID === 'demo') return false;
    pending = { action, model, request_id: randomID(), models: [model] };
    const note = action === 'download'
      ? '下载将占用设备网络和磁盘空间，完成后不会自动切换。'
      : '将替换整套托管模型；等待已接收任务完成后生效。';
    $('#network-switch-message').textContent = `设备：${selectedDevice()?.name || ''}\n操作：${actionLabels[action]}\n模型：${modelLabel(model)}\n\n${note}\n执行记录与状态会出现在${PANEL_HINT}。`;
    dialog.returnValue = '';
    dialog.showModal();
    return true;
  }

  dialog?.addEventListener('close', () => {
    const confirmed = dialog.returnValue === 'confirm';
    const body = pending;
    pending = null;
    if (!confirmed || !body || !deviceID) return;
    current = { action: body.action, model: body.model };
    void submit(body);
  });

  return {
    get busy() { return busy(); },
    get status() { return status; },
    render,
    reset,
    setDevice(id) {
      if (id === deviceID) return;
      reset();
      deviceID = id;
    },
    switchTo: model => open('switch', model),
    download: model => open('download', model),
  };
}
