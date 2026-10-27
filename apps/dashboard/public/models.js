// Reward payouts arrive as ordinary earnings rows whose `model` is a pseudo id
// such as `base_reward`. They are not tasks and must not be compared with models.
const rewardKeys = new Set(['basereward', 'basebonus', 'bonus', 'reward', 'subsidy', 'incentive']);
const rewardKey = id => String(id ?? '').trim().toLowerCase().replace(/[-_.\s]+/g, '');

export const REWARD_LABEL = '基础奖励';

export function isRewardModel(id) {
  const key = rewardKey(id);
  return key !== '' && rewardKeys.has(key);
}

export function displayModelName(id) {
  if (typeof id !== 'string' || !id) return '';
  if (isRewardModel(id)) return REWARD_LABEL;
  const parts = id.split('/').filter(Boolean);
  return parts.at(-1) || id;
}

export function displayModelNames(ids) {
  const list = [...ids];
  const shorts = list.map(id => isRewardModel(id) ? `\0${id}` : displayModelName(id));
  const counts = new Map();
  for (const short of shorts) counts.set(short, (counts.get(short) || 0) + 1);
  return new Map(list.map((id, index) => [id, isRewardModel(id) ? REWARD_LABEL : counts.get(shorts[index]) > 1 ? id : shorts[index]]));
}
