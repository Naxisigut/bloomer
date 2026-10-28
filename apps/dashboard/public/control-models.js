// Model discovery spawns two device CLI processes and may reach the official
// catalog over the network, so the dashboard keeps one snapshot per device for
// the whole session. Entering the devices view reads the snapshot; fetching a
// new one is always an explicit user action.
//
// A failed read is also stored, as an `error` snapshot. That keeps an automatic
// entry into the view from retrying a broken device on every visit, while the
// "获取最新" buttons still force a new attempt.
export function createControlModelsStore({ query }) {
  const entries = new Map(), inflight = new Map();
  function get(id) { return entries.get(id) || null; }
  function invalidate(id) { entries.delete(id); }
  function load(id) {
    if (!id || id === 'demo') return Promise.resolve(null);
    if (inflight.has(id)) return inflight.get(id);
    const work = (async () => {
      let snapshot;
      try {
        const body = await query('control-models', null, id);
        snapshot = { local: body.local, catalog: body.catalog, localError: body.local_error || '', catalogError: body.catalog_error || '', error: '', fetchedAt: Date.now() };
      } catch (failure) {
        snapshot = { local: [], catalog: [], localError: '', catalogError: '', error: failure.message, fetchedAt: Date.now() };
      }
      entries.set(id, snapshot);
      if (inflight.get(id) === work) inflight.delete(id);
      return snapshot;
    })();
    inflight.set(id, work);
    return work;
  }
  // Cached failure snapshots are returned as-is; only `load` retries.
  return { get, load, invalidate };
}
