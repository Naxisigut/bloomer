export function displayModelName(id) {
  if (typeof id !== 'string' || !id) return '';
  const parts = id.split('/').filter(Boolean);
  return parts.at(-1) || id;
}

export function displayModelNames(ids) {
  const list = [...ids];
  const shorts = list.map(displayModelName);
  const counts = new Map();
  for (const short of shorts) counts.set(short, (counts.get(short) || 0) + 1);
  return new Map(list.map((id, index) => [id, counts.get(shorts[index]) > 1 ? id : shorts[index]]));
}
