import test from 'node:test';
import assert from 'node:assert/strict';
import { nextAutoRefreshDelayMs, AUTO_REFRESH_DEFAULT_MS, AUTO_REFRESH_MIN_MS, AUTO_REFRESH_MAX_MS, COLLECTION_ALIGN_SLACK_MS } from '../public/refresh-schedule.js';

test('auto refresh backoff widens on repeated failures then resets', () => {
  assert.equal(nextAutoRefreshDelayMs({ health: null, failStreak: 0 }), AUTO_REFRESH_DEFAULT_MS);
  assert.equal(nextAutoRefreshDelayMs({ health: null, failStreak: 1 }), 60_000);
  assert.equal(nextAutoRefreshDelayMs({ health: null, failStreak: 2 }), 120_000);
  assert.equal(nextAutoRefreshDelayMs({ health: null, failStreak: 5 }), 300_000);
});

test('collection alignment converts agent time and clamps delay', () => {
  const nowMs = 1_000_000;
  const offset = 10_000;
  const nextAt = new Date(nowMs + 20_000 + offset).toISOString();
  const delay = nextAutoRefreshDelayMs({ health: { next_collection_at: nextAt, clockOffset: offset }, failStreak: 0, nowMs });
  assert.equal(delay, Math.min(AUTO_REFRESH_MAX_MS, Math.max(AUTO_REFRESH_MIN_MS, 20_000 + COLLECTION_ALIGN_SLACK_MS)));
  const far = nextAutoRefreshDelayMs({ health: { next_collection_at: new Date(nowMs + 600_000).toISOString(), clockOffset: 0 }, failStreak: 0, nowMs });
  assert.equal(far, AUTO_REFRESH_MAX_MS);
});
