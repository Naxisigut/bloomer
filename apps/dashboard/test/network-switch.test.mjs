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
