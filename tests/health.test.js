import { test } from 'node:test';
import assert from 'node:assert/strict';
import { dayActivities, nightBreaks, personalBests, speedLabel, trainingWeek, stageRow, workoutName, zoneOf, zoneRuns, zoneShares } from '../site/js/health.js';

test('zoneOf uses the lower bounds of moderate, vigorous and peak', () => {
  const limits = [109, 133, 163];
  assert.equal(zoneOf(80, limits), 0);
  assert.equal(zoneOf(109, limits), 1);
  assert.equal(zoneOf(150, limits), 2);
  assert.equal(zoneOf(170, limits), 3);
  assert.equal(zoneOf(170, null), -1);
});

test('zoneShares add up to 100 percent', () => {
  const s = zoneShares([1000, 1000, 1000, 0]);
  assert.deepEqual(s.map((z) => z.pct), [34, 33, 33, 0]);
  assert.deepEqual(zoneShares([3300, 300, 0, 0]).map((z) => [z.min, z.pct]), [[55, 92], [5, 8], [0, 0], [0, 0]]);
  assert.deepEqual(zoneShares([0, 0, 0, 0]).map((z) => z.pct), [0, 0, 0, 0]);
});

test('workout names are English', () => {
  assert.equal(workoutName('BIKING'), 'Cycling');
  assert.equal(workoutName('FRISBEE_PLAYING_GENERAL'), 'Frisbee');
  assert.equal(workoutName('SOME_NEW_THING'), 'Some new thing');
});

test('pace for walking and running, speed otherwise', () => {
  assert.equal(speedLabel({ type: 'WALKING', dist: 1049, dur: 827 }), '13:08/km');
  assert.equal(speedLabel({ type: 'BIKING', dist: 5691, dur: 1745 }), '11,7 km/h');
  assert.equal(speedLabel({ type: 'CROSSFIT', dur: 3600 }), '');
});

test('dayActivities links Yazio activities to Fitbit workouts', () => {
  const day = {
    activities: [{ name: 'cycling', w: '1' }, { name: 'Yoga' }],
    workouts: [{ id: '1', type: 'BIKING' }, { id: '2', type: 'CROSSFIT' }],
  };
  const rows = dayActivities(day);
  assert.equal(rows.length, 3);
  assert.equal(rows[0].w.id, '1');
  assert.equal(rows[1].w, null);
  assert.equal(rows[2].a, null);
  assert.equal(rows[2].w.id, '2');
});

test('hypnogram rows: awake on top, deep at the bottom', () => {
  assert.deepEqual([0, 3, 1, 2].map(stageRow), [0, 1, 2, 3]);
});

test('zoneRuns splits a route by heart rate zone without gaps', () => {
  const route = [[1, 1, 0, 500, 100], [2, 2, 10, 500, 105], [3, 3, 20, 500, 140], [4, 4, 30, 500, 0]];
  const runs = zoneRuns(route, [109, 133, 163]);
  assert.deepEqual(runs.map((r) => r.zone), [0, 2, -1]);
  assert.deepEqual(runs[0].pts, [[1, 1], [2, 2], [3, 3]]);
  assert.deepEqual(runs[1].pts, [[3, 3], [4, 4]]);
  assert.equal(zoneRuns(route, null).length, 1);
});

test('nightBreaks leaves out awake time before falling asleep and after the final wake-up', () => {
  // Stages of 2026-10-11 (awake, light, deep, …): awake only at the start.
  const oct11 = [[0, 8, 0], [8, 12, 1], [20, 65, 2], [85, 6, 1], [91, 32, 3], [123, 20, 1]];
  assert.deepEqual(nightBreaks(oct11), { awakeMin: 0, awakeCount: 0, toDeep: 20 });
  const night = [[0, 3, 0], [3, 10, 1], [13, 20, 2], [33, 7, 0], [40, 30, 3], [70, 2, 0]];
  assert.deepEqual(nightBreaks(night), { awakeMin: 7, awakeCount: 1, toDeep: 13 });
  assert.equal(nightBreaks([]), null);
  assert.equal(nightBreaks([[0, 400, 4]]).toDeep, null); // no stage tracking
});

test('trainingWeek compares the last 7 days with the 4 weeks before', () => {
  const days = [
    { date: '2026-09-10', workouts: [{ dur: 600 }] },
    { date: '2026-09-20', workouts: [{ dur: 3600, dist: 10000 }], azm: [10, 20, 10] },
    { date: '2026-10-12', workouts: [{ dur: 1800, dist: 5000 }] }, // 8 days back: previous weeks
    { date: '2026-10-14', workouts: [{ dur: 1200 }, { dur: 600 }], azm: [5, 5, 0] },
    { date: '2026-10-19', workouts: [{ dur: 900, dist: 2000 }] },
  ];
  const { cur, prev } = trainingWeek(days, '2026-10-19');
  assert.deepEqual(cur, { sessions: 3, dur: 2700, dist: 2000, azm: 10 });
  assert.deepEqual(prev, { sessions: 0.5, dur: 1350, dist: 3750, azm: 10 });
  assert.equal(trainingWeek(days.slice(2), '2026-10-19').prev, null);
});

test('personalBests keeps the best workout per record and type', () => {
  const days = [
    { date: '2026-10-01', workouts: [{ id: 'a', type: 'WALKING', dur: 1400, dist: 1800, energy: 120 }] },
    { date: '2026-10-02', workouts: [{ id: 'b', type: 'WALKING', dur: 900, dist: 2500, energy: 90, elev: 40 }, { id: 'c', type: 'CROSSFIT', dur: 3600, energy: 456, azm: 68 }] },
  ];
  const got = Object.fromEntries(personalBests(days).map((b) => [b.key, b.w.id]));
  assert.deepEqual(got, { dur: 'c', energy: 'c', 'dist:Walking': 'b', elev: 'b', azm: 'c' });
});
