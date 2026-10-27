import test from 'node:test';
import assert from 'node:assert/strict';
import { displayModelName, displayModelNames, isRewardModel, REWARD_LABEL } from '../public/models.js';

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

test('reward pseudo models are recognised but never treated as a real model', () => {
  for (const id of ['base_reward', 'baseReward', 'base-reward', 'BASE_REWARD', ' base_reward ']) {
    assert.equal(isRewardModel(id), true, id);
    assert.equal(displayModelName(id), REWARD_LABEL);
  }
  for (const id of ['base_reward_v2', 'Qwen3-32B', 'reward_model', '', null, undefined, 'OrgA/base_reward']) {
    assert.equal(isRewardModel(id), false, String(id));
  }
  assert.equal(displayModelName('Qwen3-32B'), 'Qwen3-32B');
});

test('reward labels stay unique even though every reward row shares one label', () => {
  const labels = displayModelNames(['base_reward', 'base_bonus', 'Qwen3.5-9B']);
  assert.equal(labels.get('base_reward'), REWARD_LABEL);
  assert.equal(labels.get('base_bonus'), REWARD_LABEL);
  assert.equal(labels.get('Qwen3.5-9B'), 'Qwen3.5-9B');
});
