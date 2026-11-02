import { DASHBOARD_TIMEZONE, rollingRange, sumCount, sumMoney } from './data.js';

const rollingTotal = (range, page) => ({ ...range, record_count: sumCount(page.items), amount_micro_usd: sumMoney(page.items).toString() });

// Older Agents lack /v1/account/summary; they keep the three-request path for
// the rest of the page session.
export function createSummaryLoader({ query }) {
  const legacy = new Set();
  async function loadLegacy(device, now) {
    const hour = rollingRange(1, now), day = rollingRange(24, now), params = { group_by: 'day', timezone: DASHBOARD_TIMEZONE };
    const [overview, lastHour, lastDay] = await Promise.all([
      query('overview', null, device),
      query('stats', { ...hour, ...params }, device),
      query('stats', { ...day, ...params }, device),
    ]);
    return { ...overview, last_hour: rollingTotal(hour, lastHour), last_24_hours: rollingTotal(day, lastDay) };
  }
  return async function load(device, now) {
    if (legacy.has(device)) return loadLegacy(device, now);
    try {
      return await query('summary', null, device);
    } catch (error) {
      if (error?.code !== 'summary_unsupported') throw error;
      legacy.add(device);
      return loadLegacy(device, now);
    }
  };
}
