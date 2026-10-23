import test from 'node:test';
import assert from 'node:assert/strict';
import { displayModelName, displayModelNames } from '../public/models.js';

test('display names keep short ids and drop a single org prefix', () => {
  assert.equal(displayModelName('Qwen3.5-9B'), 'Qwen3.5-9B');
  assert.equal(displayModelName('EigenLabs/Qwen3.8-27B-4bit-mtpt'), 'Qwen3.8-27B-4bit-mtpt');
  assert.equal(displayModelName('org/name/extra'), 'extra');
});

test('display names ignore empty path segments and non-strings', () => {
  assert.equal(displayModelName('name/'), 'name');
  assert.equal(displayModelName('/name'), 'name');
  assert.equal(displayModelName('org//model'), 'model');
  assert.equal(displayModelName(''), '');
  assert.equal(displayModelName(null), '');
});

test('label maps stay unique when two ids share a short name', () => {
  const labels = displayModelNames([
    'EigenLabs/Qwen3.8-27B-4bit-mtpt',
    'Qwen3.5-9B',
    'OrgA/same',
    'OrgB/same',
  ]);
  assert.equal(labels.get('EigenLabs/Qwen3.8-27B-4bit-mtpt'), 'Qwen3.8-27B-4bit-mtpt');
  assert.equal(labels.get('Qwen3.5-9B'), 'Qwen3.5-9B');
  assert.equal(labels.get('OrgA/same'), 'OrgA/same');
  assert.equal(labels.get('OrgB/same'), 'OrgB/same');
});
