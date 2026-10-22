const PERMANENT_CODES = new Set(['control_unsupported', 'control_disabled', 'control_unavailable', 'unauthorized', 'invalid_response']);
const TRANSIENT_CODES = new Set(['device_unreachable', 'request_timeout', 'agent_error']);

export function isPermanentControlError(error) {
  if (!error) return false;
  const code = error.code || error;
  if (TRANSIENT_CODES.has(code)) return false;
  if (PERMANENT_CODES.has(code)) return true;
  const status = error.status;
  if (Number.isInteger(status) && status >= 400 && status < 500) return true;
  return false;
}

export function isTransientControlError(error) {
  if (!error) return false;
  const code = error.code || error;
  if (TRANSIENT_CODES.has(code)) return true;
  const status = error.status;
  return Number.isInteger(status) && status >= 500;
}

export function nextNetworkBackoffMs(failStreak, baseMs = 3000, maxMs = 60_000) {
  if (failStreak <= 0) return baseMs;
  return Math.min(maxMs, baseMs * 2 ** failStreak);
}

export function runningPollIntervalMs(runningSinceMs, nowMs = Date.now()) {
  if (!runningSinceMs) return 3000;
  const elapsed = nowMs - runningSinceMs;
  if (elapsed < 60_000) return 3000;
  if (elapsed < 10 * 60_000) return 10_000;
  return 30_000;
}

export function nextControlPollDelayMs({ runningSinceMs, pending, networkFailStreak, nowMs = Date.now() }) {
  if (networkFailStreak > 0) return nextNetworkBackoffMs(networkFailStreak);
  if (pending || runningSinceMs) return runningPollIntervalMs(runningSinceMs, nowMs);
  return 3000;
}

export function shouldControlAutoPoll({ autoPollStopped, pending, running, awaitingStatus, transientError }) {
  if (autoPollStopped) return false;
  if (pending || running) return true;
  if (awaitingStatus) return true;
  if (transientError) return true;
  return false;
}
