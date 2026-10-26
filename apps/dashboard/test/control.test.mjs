import test from 'node:test';
import assert from 'node:assert/strict';
import { validateControl, validateLongTaskRestartSettings } from '../public/agent-client.js';
import { demoQuery } from '../public/demo.js';
const request = { request_id: '0123456789abcdef', action: 'switch', models: ['org/model-a', 'model-b'] };
test('control validation refuses shell/options/paths and unknown or conflicting inputs', () => {
  assert.deepEqual(validateControl(request), request);
  assert.deepEqual(validateControl({ request_id: request.request_id, action: 'restart' }), { request_id: request.request_id, action: 'restart' });
  for (const value of [null, [], { ...request, force: true }, { ...request, request_id: 'short' }, { ...request, action: 'exec' }, { ...request, models: [] }, { ...request, models: ['--force'] }, { ...request, models: ['a/../private'] }, { ...request, models: ['x;shutdown'] }, { ...request, models: ['a', 'a'] }, { ...request, action: 'download' }, { ...request, action: 'stop' }]) assert.throws(() => validateControl(value), { code: 'invalid_request' });
  assert.deepEqual(validateLongTaskRestartSettings({ enabled: true }), { enabled: true });
  assert.deepEqual(demoQuery('long-task-restart'), { enabled: false });
  for (const value of [null, [], { enabled: 'yes' }, { enabled: true, force: true }]) assert.throws(() => validateLongTaskRestartSettings(value), { code: 'invalid_request' });
});
