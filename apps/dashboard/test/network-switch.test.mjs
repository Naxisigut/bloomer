import test from 'node:test';
import assert from 'node:assert/strict';
import { modelAction, renderCandidatesTable } from '../public/network-rates.js';

const base = { model: 'a', downloaded: true, avg_providers: 1, requests_per_hour: 1, tokens_per_hour: 1, usd_per_hour_micro: '1', price_source: 'official', insufficient: false };

test('a downloaded, non-current model offers exactly one switch action', () => {
  assert.deepEqual(modelAction(base, { currentModel: 'other' }), { kind: 'switch', label: '切换到该模型', disabled: false, hint: '将替换整套托管模型。' });
});

test('the running model and undownloaded models never offer a switch', () => {
  const current = modelAction({ ...base, model: 'cur' }, { currentModel: 'cur' });
  assert.equal(current.kind, 'current');
  assert.equal(current.disabled, true);
  assert.match(current.hint, /当前运行模型/);

  const missing = modelAction({ ...base, downloaded: false }, { currentModel: 'other' });
  assert.equal(missing.kind, 'download');
  assert.equal(missing.label, '下载该模型');
  assert.equal(missing.disabled, false);
  assert.match(missing.hint, /不会自动切换/);
});

test('busy and demo devices disable the action but keep it visible', () => {
  for (const [context, expected] of [[{ busy: true }, /另一项操作/], [{ demo: true }, /演示设备/]]) {
    const action = modelAction(base, { currentModel: 'other', ...context });
    assert.equal(action.disabled, true);
    assert.equal(action.kind, 'switch');
    assert.match(action.hint, expected);
  }
  // A current model stays "current" even while another job runs: state wins over
  // the transient reason so the row does not imply it became switchable.
  assert.equal(modelAction({ ...base, model: 'cur' }, { currentModel: 'cur', busy: true }).kind, 'current');
});

test('the table pins an action column only when the page asks for one', () => {
  const escape = String;
  const withoutActions = renderCandidatesTable([base], escape);
  assert.doesNotMatch(withoutActions, /network-rates-action/);

  const html = renderCandidatesTable([base, { ...base, model: 'cur' }, { ...base, model: 'cold', downloaded: false }], escape, {
    currentModel: 'cur',
    actionFor: candidate => modelAction(candidate, { currentModel: 'cur' }),
  });
  assert.match(html, /class="network-rates-action">操作<\/th>/);
  assert.match(html, /data-network-action="switch" data-model="a"/);
  assert.match(html, /data-network-action="current" data-model="cur" disabled/);
  assert.match(html, /data-network-action="download" data-model="cold"/);
});

test('the action column escapes model ids and reasons', () => {
  const escape = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
  const html = renderCandidatesTable([{ ...base, model: '<img onerror=1>' }], escape, {
    currentModel: null,
    actionFor: candidate => modelAction(candidate, { demo: true }),
  });
  assert.ok(html.includes('data-model="&lt;img onerror=1&gt;"'));
  assert.doesNotMatch(html, /<img/);
});

// The page no longer reports a switch or download: it only submits the job and
// leaves the outcome to the remote-control panel.
function fakeDocument() {
  const listeners = {};
  const dialog = { open: false, returnValue: '', showModal() { this.open = true; }, close() { this.open = false; listeners.close?.(); }, addEventListener: (type, fn) => { listeners[type] = fn; } };
  const message = { textContent: '' };
  return {
    listeners,
    dialog,
    message,
    document: { querySelector: selector => selector === '#network-switch-dialog' ? dialog : selector === '#network-switch-message' ? message : null },
  };
}

async function confirmWith({ submitControl }) {
  const { createNetworkSwitch } = await import('../public/network-switch.js');
  const fake = fakeDocument();
  const previous = globalThis.document;
  globalThis.document = fake.document;
  try {
    const submitted = [];
    const switcher = createNetworkSwitch({ submitControl, selectedDevice: () => ({ name: 'Mock' }), onSubmitted: body => submitted.push(body) });
    switcher.setDevice('device-1');
    const opened = switcher.switchTo('qwen3.5-35b-a3b');
    fake.dialog.returnValue = 'confirm';
    fake.dialog.open = true;
    fake.listeners.close();
    await new Promise(resolve => setTimeout(resolve, 20));
    return { switcher, fake, submitted, opened };
  } finally {
    globalThis.document = previous;
  }
}

test('a confirmed switch submits the job without reporting a status anywhere', async () => {
  const calls = [];
  const { switcher, fake, submitted, opened } = await confirmWith({ submitControl: async (id, body) => { calls.push({ id, body }); return { status: 'running' }; } });
  assert.equal(opened, true);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].body.models, ['qwen3.5-35b-a3b']);
  assert.equal(calls[0].body.action, 'switch');
  assert.equal(submitted.length, 1);
  // The confirmation text points at the panel; nothing is written into a status line.
  assert.match(fake.message.textContent, /我的设备 → 远程操作/);
  assert.doesNotMatch(fake.message.textContent, /执行中|已完成|执行失败/);
  assert.equal(fake.document.querySelector('#network-switch-status'), null);
});

test('a rejected submit stays silent and releases the buttons', async () => {
  const { switcher, submitted } = await confirmWith({ submitControl: async () => { throw new Error('device unreachable'); } });
  assert.equal(submitted.length, 0);
  assert.equal(switcher.busy, false);
});

test('an accepted job keeps the rows locked until the next refresh settles it', async () => {
  const { switcher } = await confirmWith({ submitControl: async () => ({ status: 'running' }) });
  assert.equal(switcher.busy, true);
  switcher.settle();
  assert.equal(switcher.busy, false);
});
