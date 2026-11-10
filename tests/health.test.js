import { test } from 'node:test';
import assert from 'node:assert/strict';
import { trainingDays, trainingSummary, wodBestResults, bestEntries, platesFor, bestDays, bestsReadiness, betterResult, fmtClock, parseClock, wodBestDays, wodBoard, dayActivities, liftBoard, loadLabel, oneRepMax, percentages, nightBreaks, personalBests, speedLabel, trainingWeek, stageRow, workoutName, zoneOf, zoneRuns, zoneShares } from '../site/js/health.js';

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

test('loadLabel names the training load', () => {
  assert.equal(loadLabel(1), 'balanced');
  assert.equal(loadLabel(1.6), 'sharp increase');
  assert.equal(loadLabel(0.6), 'lighter than usual');
});

test('liftBoard keeps one best per lift and rep count, with its gain', () => {
  const e = (id, lift, kg, reps, date) => ({ id, lift, kg, reps, date });
  const rows = liftBoard([
    e(1, 'Power Clean', 70, 1, '2026-05-01'),
    e(2, 'power clean', 75, 1, '2026-08-01'),
    e(3, 'Power Clean', 72.5, 1, '2026-10-01'), // not a best: after it, and lighter
    e(4, 'Power Clean', 75, 1, '2026-11-01'), // equals the best: the first one counts
    e(5, 'Back Squat', 100, 5, '2026-11-05'),
    e(6, 'Back Squat', 130, 1, '2026-02-01'),
  ], '2026-11-10');
  assert.deepEqual(rows.map((r) => [r.lift, r.reps, r.best.kg, r.best.date, r.gain, r.recent]), [
    ['Back Squat', 5, 100, '2026-11-05', null, true],
    ['Power Clean', 1, 75, '2026-08-01', 5, false],
    ['Back Squat', 1, 130, '2026-02-01', null, false],
  ]);
  // The latest spelling names the lift.
  assert.equal(rows[1].lift, 'Power Clean');
  assert.equal(rows[1].entries.length, 4);
  assert.deepEqual(liftBoard([], '2026-11-10'), []);
});

test('one-rep max estimate and percentages', () => {
  assert.equal(oneRepMax(100, 1), 100);
  assert.equal(Math.round(oneRepMax(100, 3) * 1e6) / 1e6, 110);
  const p = percentages(83);
  assert.deepEqual(p[0], { pct: 50, kg: 41.5 });
  assert.deepEqual(p.at(-1), { pct: 95, kg: 79 });
});

test('bests count only when they beat an earlier result, and meet that morning’s Readiness', () => {
  const e = (id, lift, kg, date, reps = 1) => ({ id, lift, kg, reps, date });
  const entries = [
    e(1, 'Snatch', 60, '2026-10-01'), e(2, 'snatch', 62, '2026-10-05'), e(3, 'Snatch', 61, '2026-10-06'),
    e(4, 'Snatch', 65, '2026-10-09'), e(5, 'Back Squat', 100, '2026-10-02', 5), e(6, 'Back Squat', 105, '2026-10-07', 5),
    e(7, 'Back Squat', 120, '2026-10-03'), // a different rep count: a first result
  ];
  assert.deepEqual(bestDays(entries), ['2026-10-05', '2026-10-07', '2026-10-09']);
  const byDate = new Map();
  for (let d = 1; d <= 10; d++) {
    const date = `2026-10-${String(d).padStart(2, '0')}`;
    byDate.set(date, { date, scores: { readiness: { value: { 5: 90, 7: 86, 9: 88 }[d] || 70 } } });
  }
  // Mornings 5 to 9: 90, 70, 86, 70, 88.
  assert.deepEqual(bestsReadiness(bestDays(entries), byDate), { n: 3, avg: 88, usual: 81 });
  assert.equal(bestsReadiness(bestDays(entries.slice(0, 4)), byDate), null);
});

test('benchmark workouts: a best per workout, Rx and scaled apart', () => {
  const fran = { id: 1, name: 'Fran', scoring: 'time' }, cindy = { id: 2, name: 'Cindy', scoring: 'rounds' }, grace = { id: 3, name: 'Grace', scoring: 'time' };
  const r = (id, workout, value, date, more = {}) => ({ id, workout, value, date, ...more });
  const results = [
    r(1, 1, 300, '2026-09-01'), r(2, 1, 280, '2026-10-01'), r(3, 1, 290, '2026-11-05'),
    r(4, 1, 200, '2026-11-01', { scaled: true }),
    r(5, 2, 12, '2026-08-01', { reps: 9 }), r(6, 2, 13, '2026-09-01', { reps: 0 }), r(7, 2, 13, '2026-11-08', { reps: 4 }),
  ];
  const rows = wodBoard([fran, cindy, grace], results, '2026-11-10');
  assert.deepEqual(rows.map((x) => [x.key, x.best?.id ?? null, x.prev?.id ?? null, x.recent]), [
    ['2|rx', 7, 6, true], ['1|scaled', 4, null, true], ['1|rx', 2, 1, false], ['3|rx', null, null, false],
  ]);
  assert.deepEqual(wodBestDays([fran, cindy, grace], results), ['2026-09-01', '2026-10-01', '2026-11-08']);
  assert.ok(betterResult('time', { value: 1 }, { value: 2 }) && betterResult('load', { value: 2 }, { value: 1 }));
  assert.ok(!betterResult('rounds', { value: 3, reps: 1 }, { value: 3, reps: 1 }));
});

test('clock times read and written', () => {
  assert.deepEqual(['4:12', '1:02:05', '12', ' 0:45 '].map(parseClock), [252, 3725, 720, 45]);
  assert.deepEqual(['4:72', '4:2', '1:72:00', 'x', '', '1:2:3:4'].map(parseClock), [null, null, null, null, null, null]);
  assert.deepEqual([252, 3725, 59].map(fmtClock), ['4:12', '1:02:05', '0:59']);
});

test('plates per side, the heaviest first', () => {
  assert.deepEqual(platesFor(100), { side: [25, 15], loaded: 100 });
  assert.deepEqual(platesFor(82.5), { side: [25, 5, 1.25], loaded: 82.5 });
  assert.deepEqual(platesFor(61, 15), { side: [20, 2.5, 0.5], loaded: 61 });
  assert.deepEqual(platesFor(20), { side: [], loaded: 20 });
  assert.deepEqual(platesFor(15), { side: [], loaded: 20 }); // less than the bar
  assert.deepEqual(platesFor(100.5), { side: [25, 15], loaded: 100 }); // 0,25 a side can't be loaded
});

test('capped results rank below finished times and count reps', () => {
  const fin = { value: 1500 }, c180 = { value: 180, capped: true }, c200 = { value: 200, capped: true };
  assert.ok(betterResult('time', fin, c200) && !betterResult('time', c200, fin));
  assert.ok(betterResult('time', c200, c180) && !betterResult('time', c180, c200));
  const chipper = { id: 1, name: 'Chipper', scoring: 'time' };
  const rs = [{ id: 1, workout: 1, value: 150, capped: true, date: '2026-09-01' }, { id: 2, workout: 1, value: 1190, date: '2026-10-01' }, { id: 3, workout: 1, value: 190, capped: true, date: '2026-11-01' }];
  assert.deepEqual(wodBestResults([chipper], rs).map((r) => r.id), [2]);
  assert.deepEqual(bestEntries([{ id: 1, lift: 'Snatch', kg: 60, reps: 1, date: '2026-01-01' }, { id: 2, lift: 'Snatch', kg: 65, reps: 1, date: '2026-02-01' }]).map((e) => e.id), [2]);
});

test('training days: workouts without walks, noted results and bests', () => {
  const days = [
    { date: '2026-11-02', workouts: [{ type: 'WEIGHTS', dur: 3600 }, { type: 'WALKING', dur: 1800 }] },
    { date: '2026-11-03', workouts: [{ type: 'WALKING', dur: 3600 }] },
    { date: '2026-11-04', workouts: [{ type: 'RUNNING', dur: 1800 }, { type: 'HIIT', dur: 1200 }] },
  ];
  const trained = trainingDays(days, ['2026-11-04', '2026-11-12'], ['2026-11-12']);
  assert.deepEqual(Object.fromEntries(trained), {
    '2026-11-02': { min: 60, sessions: 1, noted: false, pb: false },
    '2026-11-04': { min: 50, sessions: 2, noted: true, pb: false },
    '2026-11-12': { min: 0, sessions: 0, noted: true, pb: true },
  });
  // Two weeks from Monday Nov 2 to Sunday Nov 15: 3 days, both weeks active.
  assert.deepEqual(trainingSummary(trained, '2026-11-02', '2026-11-15'), { days: 3, perWeek: 1.5, pbs: 1, longestWeeks: 2 });
  assert.equal(trainingSummary(trained, '2026-10-01', '2026-10-31').days, 0);
});
