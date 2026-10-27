import { test } from 'node:test';
import assert from 'node:assert/strict';
import { syncNotices } from '../site/js/format.js';

const now = Date.parse('2026-10-19T12:00:00Z');
const hoursAgo = (h) => new Date(now - h * 36e5).toISOString();

test('syncNotices warns after three sync intervals, or a day when unknown', () => {
  assert.deepEqual(syncNotices({ generated_at: hoursAgo(1), sync_interval_minutes: 30 }, now), []);
  assert.equal(syncNotices({ generated_at: hoursAgo(2), sync_interval_minutes: 30 }, now).length, 1);
  assert.deepEqual(syncNotices({ generated_at: hoursAgo(20) }, now), []);
  assert.match(syncNotices({ generated_at: hoursAgo(30) }, now)[0], /longer than expected/);
});

test('syncNotices: Fitbit auth failures always show, other errors only when lasting', () => {
  const base = { generated_at: hoursAgo(0), sync_interval_minutes: 30 };
  assert.match(syncNotices({ ...base, health_sync: { failing: true, auth: true, last_success: hoursAgo(0.5) } }, now)[0], /google-auth/);
  assert.deepEqual(syncNotices({ ...base, health_sync: { failing: true, last_success: hoursAgo(0.5) } }, now), []);
  assert.match(syncNotices({ ...base, health_sync: { failing: true, last_success: hoursAgo(5) } }, now)[0], /isn't syncing/);
  assert.equal(syncNotices({ ...base, health_sync: { failing: true } }, now).length, 1);
});
