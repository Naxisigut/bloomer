import { randomID } from './identifiers.js';
import { displayModelName } from './models.js';

// Switching or downloading from the network view goes through the same control
// job API as the remote-control panel, so the resulting record and its status
// appear in both places. This module owns only the confirmation dialog and the
// submit call: the job is not polled or reported here, and the network view
// picks up the new state on its regular refresh.
const actionLabels = { switch: '切换模型', download: '下载模型' };
const PANEL_HINT = '「我的设备 → 远程操作」';

export function createNetworkSwitch({ submitControl, selectedDevice, onChange, onSubmitted }) {
  const dialog = document.querySelector('#network-switch-dialog');
  let deviceID = null, pending = null, submitting = false, awaiting = false, generation = 0;

  const modelLabel = model => displayModelName(model) || model;
  const notify = () => onChange?.();
  // A submitted job is not polled here, so the buttons stay locked until the
  // network view's next refresh shows the result.
  const busy = () => submitting || awaiting;

  function reset() {
    generation++;
    pending = null;
    submitting = false;
    awaiting = false;
    // Clearing the return value first keeps a dialog closed by a device change
    // from firing `close` as if the user had confirmed it.
    if (dialog?.open) { dialog.returnValue = ''; dialog.close(); }
  }

  async function submit(body) {
    const token = generation;
    submitting = true;
    notify();
    let accepted = false;
    try {
      await submitControl(deviceID, body);
      accepted = true;
      if (token !== generation) return;
      onSubmitted?.(body);
    } catch {
      // A failure here may still have been accepted by the device, and the job
      // record is the only reliable answer, so nothing is shown on this page.
    } finally {
      if (token === generation) { submitting = false; awaiting = accepted; notify(); }
    }
  }

  // The dialog is the only confirmation point, so no row click or menu item can
  // post a control job on its own.
  function open(action, model) {
    if (!dialog || !model || busy() || !deviceID || deviceID === 'demo') return false;
    pending = { action, request_id: randomID(), models: [model] };
    const note = action === 'download'
      ? '下载将占用设备网络和磁盘空间，完成后不会自动切换。'
      : '将替换整套托管模型；等待已接收任务完成后生效。';
    document.querySelector('#network-switch-message').textContent = `设备：${selectedDevice()?.name || ''}\n操作：${actionLabels[action]}\n模型：${modelLabel(model)}\n\n${note}\n执行记录与状态会出现在${PANEL_HINT}。`;
    dialog.returnValue = '';
    dialog.showModal();
    return true;
  }

  dialog?.addEventListener('close', () => {
    const confirmed = dialog.returnValue === 'confirm';
    const body = pending;
    pending = null;
    if (!confirmed || !body || !deviceID) return;
    void submit(body);
  });

  return {
    get busy() { return busy(); },
    // A refresh brings the device's own state back, which is the only signal this
    // page uses for a submitted job, so it also releases the row buttons.
    settle() {
      if (!awaiting) return;
      awaiting = false;
      notify();
    },
    setDevice(id) {
      if (id === deviceID) return;
      reset();
      deviceID = id;
    },
    switchTo: model => open('switch', model),
    download: model => open('download', model),
  };
}
