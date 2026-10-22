export function clockOffsetFromHealth(serverTime, clientMidMs) {
  const parsed = Date.parse(serverTime);
  if (!Number.isFinite(parsed)) return 0;
  return parsed - clientMidMs;
}

export function correctedNow(offsetMs = 0) {
  return new Date(Date.now() + offsetMs);
}

export function enrichHealthTiming(health, requestStartedMs, requestEndedMs) {
  if (!health || health.error) return health;
  const mid = (requestStartedMs + requestEndedMs) / 2;
  return { ...health, clockOffset: clockOffsetFromHealth(health.server_time, mid) };
}
