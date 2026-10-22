import test from 'node:test';
import assert from 'node:assert/strict';
import { AppError } from '../public/errors.js';
import { isPermanentControlError, isTransientControlError, nextControlPollDelayMs, nextNetworkBackoffMs, shouldControlAutoPoll } from '../public/control-poll.js';

test('control polling stops on permanent errors but backs off on transient ones', () => {
  assert.ok(isPermanentControlError(new AppError(404, 'control_unsupported', 'x')));
  assert.ok(isPermanentControlError(new AppError(401, 'unauthorized', 'x')));
  assert.ok(isPermanentControlError(new AppError(502, 'invalid_response', 'x')));
  assert.ok(isTransientControlError(new AppError(502, 'device_unreachable', 'x')));
  assert.ok(isTransientControlError(new AppError(504, 'request_timeout', 'x')));
  assert.ok(isTransientControlError(new AppError(500, 'agent_error', 'x')));
  assert.equal(nextNetworkBackoffMs(0), 3000);
  assert.equal(nextNetworkBackoffMs(1), 6000);
  assert.equal(nextNetworkBackoffMs(4), 48_000);
  assert.equal(nextNetworkBackoffMs(10), 60_000);
});

test('running and pending polls widen with elapsed time', () => {
  const start = 1_000_000;
  assert.equal(nextControlPollDelayMs({ runningSinceMs: start, pending: false, networkFailStreak: 0, nowMs: start + 30_000 }), 3000);
  assert.equal(nextControlPollDelayMs({ runningSinceMs: start, pending: false, networkFailStreak: 0, nowMs: start + 120_000 }), 10_000);
  assert.equal(nextControlPollDelayMs({ runningSinceMs: start, pending: false, networkFailStreak: 0, nowMs: start + 700_000 }), 30_000);
  assert.equal(nextControlPollDelayMs({ runningSinceMs: start, pending: true, networkFailStreak: 0, nowMs: start + 700_000 }), 30_000);
});

test('shouldControlAutoPoll respects stop flag and retryable states', () => {
  assert.equal(shouldControlAutoPoll({ autoPollStopped: true, pending: false, running: false, awaitingStatus: true, transientError: false }), false);
  assert.equal(shouldControlAutoPoll({ autoPollStopped: false, pending: true, running: false, awaitingStatus: false, transientError: false }), true);
  assert.equal(shouldControlAutoPoll({ autoPollStopped: false, pending: false, running: false, awaitingStatus: false, transientError: true }), true);
});
