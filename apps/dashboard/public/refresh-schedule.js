export const AUTO_REFRESH_DEFAULT_MS = 60_000;
export const AUTO_REFRESH_MIN_MS = 15_000;
export const AUTO_REFRESH_MAX_MS = 90_000;
export const COLLECTION_ALIGN_SLACK_MS = 8_000;
const FAIL_BACKOFF_MS = [60_000, 120_000, 300_000];

function clamp(ms, min, max) {
  return Math.min(max, Math.max(min, ms));
}

export function nextAutoRefreshDelayMs({ health, failStreak, nowMs = Date.now() }) {
  if (failStreak > 0) return FAIL_BACKOFF_MS[Math.min(failStreak - 1, FAIL_BACKOFF_MS.length - 1)];
  const nextAt = health?.next_collection_at;
  if (nextAt && Number.isFinite(Date.parse(nextAt))) {
    const offset = health.clockOffset ?? 0;
    const browserTarget = Date.parse(nextAt) - offset + COLLECTION_ALIGN_SLACK_MS;
    return clamp(browserTarget - nowMs, AUTO_REFRESH_MIN_MS, AUTO_REFRESH_MAX_MS);
  }
  return AUTO_REFRESH_DEFAULT_MS;
}
