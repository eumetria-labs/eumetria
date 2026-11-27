import { test } from 'node:test';
import assert from 'node:assert/strict';
import { calorieHit, correlate, fit, goalHits, goalStreaks, lastTimes, pairsOf, ranks, spearman, strength } from '../site/js/correlate.js';

const close = (a, b, eps = 1e-4) => assert.ok(Math.abs(a - b) < eps, `${a} ≉ ${b}`);

test('Spearman against values worked out by hand', () => {
  // d = −1, 1, −1, 1, 0: ρ = 1 − 6·4 / (5·24) = 0.8
  close(spearman([1, 2, 3, 4, 5], [2, 1, 4, 3, 5]), 0.8);
  close(spearman([1, 2, 3, 4, 5], [50, 40, 30, 20, 10]), -1);
  // Only the order counts: a curve that always rises is 1.
  close(spearman([1, 2, 3, 4, 5], [1, 4, 9, 16, 1000]), 1);
  // Ties share their rank: x ranks 1, 2.5, 2.5, 4 → 4.5 / √22.5
  assert.deepEqual(ranks([1, 2, 2, 3]), [1, 2.5, 2.5, 4]);
  close(spearman([1, 2, 2, 3], [1, 2, 3, 4]), 4.5 / Math.sqrt(22.5));
  assert.equal(spearman([3, 3, 3], [1, 2, 3]), null);
});

test('the line, the words and too few days', () => {
  const { a, b } = fit([{ x: 0, y: 1 }, { x: 1, y: 3 }, { x: 2, y: 5 }]);
  close(a, 1);
  close(b, 2);
  assert.equal(strength(0.05), 'none');
  assert.equal(strength(-0.25), 'weak');
  assert.equal(strength(0.45), 'moderate');
  assert.equal(strength(-0.7), 'strong');
  const nine = Array.from({ length: 9 }, (_, i) => ({ x: i, y: i }));
  assert.equal(correlate(nine).r, null);
  const ten = [...nine, { x: 9, y: 9 }];
  assert.equal(correlate(ten).r, 1);
  assert.equal(correlate(ten).n, 10);
});

const day = (date, extra) => ({ date, ...extra });

test('a lag pairs a day with the one after, gaps and skipped days left out', () => {
  const rows = [
    day('2026-11-01', { energy: 2000, nut: { alcohol: [30, 1] } }),
    day('2026-11-02', { energy: 2100, hrv: 40 }),
    day('2026-11-03', { energy: 1900, hrv: 50 }),
    // 11-04 missing: 11-03's next day isn't there
    day('2026-11-05', { energy: 2000, hrv: 45 }),
  ];
  assert.deepEqual(pairsOf(rows, 'alcohol', 'hrv', 1), [{ date: '2026-11-01', x: 30, y: 40 }, { date: '2026-11-02', x: 0, y: 50 }]);
  // The same day: 11-01 has no HRV.
  assert.deepEqual(pairsOf(rows, 'alcohol', 'hrv', 0).map((p) => p.date), ['2026-11-02', '2026-11-03', '2026-11-05']);
  // A skipped date drops the pair it is in, as x or as y.
  assert.deepEqual(pairsOf(rows, 'alcohol', 'hrv', 1, {}, (d) => d === '2026-11-03').map((p) => p.date), ['2026-11-01']);
  // Check-ins come from their own map.
  const checkins = new Map([['2026-11-02', { stress: 4 }]]);
  assert.deepEqual(pairsOf(rows, 'ci_stress', 'hrv', 1, { checkins }), [{ date: '2026-11-02', x: 4, y: 50 }]);
});

test('calorie hits follow the phase', () => {
  const d = { energy: 2150, energy_goal: 2000 };
  assert.equal(calorieHit(d, 'maintain'), true); // within 10 %
  assert.equal(calorieHit(d, 'cut'), false);
  assert.equal(calorieHit(d, 'bulk'), true);
  assert.equal(calorieHit({ energy: 1700, energy_goal: 2000 }, 'maintain'), false);
  assert.equal(calorieHit({ energy: 1700, energy_goal: 2000 }, 'cut'), true);
  assert.equal(calorieHit({ energy: 0, energy_goal: 2000 }, 'cut'), null);
  assert.equal(calorieHit({ energy: 1800 }, 'cut'), null);
});

test('goal hit rates count only days with data', () => {
  const rows = [
    day('2026-11-01', { energy: 2000, energy_goal: 2000, energy_base: 2000, protein: 150, protein_goal: 140, steps: 12000, sleep: { asleep: 470 } }),
    day('2026-11-02', { energy: 2500, energy_goal: 2000, energy_base: 2000, protein: 100, protein_goal: 140, steps: 4000, nut: { alcohol: [20, 1] } }),
    day('2026-11-03', { steps: 9000, sleep: { asleep: 400 } }), // nothing logged
  ];
  const phases = [{ kind: 'cut', start: '2026-11-02', end: '' }];
  const hits = Object.fromEntries(goalHits(rows, { goals: { steps: 10000 }, sleepGoal: 450, phases, today: '2026-11-03', alcoholKnown: true }).map((h) => [h.key, h]));
  assert.deepEqual([hits.protein.hit, hits.protein.of], [1, 2]);
  // 11-01 maintain and on goal; 11-02 in the cut and over.
  assert.deepEqual([hits.energy.hit, hits.energy.of], [1, 2]);
  assert.deepEqual(hits.energy.kinds.sort(), ['cut', 'maintain']);
  assert.deepEqual([hits.steps.hit, hits.steps.of], [1, 3]);
  assert.deepEqual([hits.sleep.hit, hits.sleep.of], [1, 2]);
  assert.deepEqual([hits.alcohol_free.hit, hits.alcohol_free.of], [1, 2]);
  // Without a steps goal, a sleep goal or any alcohol logged: left out.
  const some = goalHits(rows, {}).map((h) => h.key);
  assert.deepEqual(some, ['protein', 'energy']);
  assert.deepEqual(goalHits([], { goals: { steps: 1 }, sleepGoal: 1 }), []);
});

test('goalStreaks: the streak now, the longest, and the last miss', () => {
  const day = (date, protein, alcohol = 0) => ({ date, energy: 2000, energy_goal: 2000, protein, protein_goal: 100, nut: { alcohol: [alcohol, 1] } });
  const days = [
    day('2026-11-01', 120), day('2026-11-02', 120), day('2026-11-03', 120), // 3 in a row
    day('2026-11-04', 80, 24), // missed, and a drink
    // 11-05 missing: ends any streak, and no food logged
    day('2026-11-06', 110), day('2026-11-07', 110),
    day('2026-11-08', 50), // today: still running, left out
  ];
  const by = Object.fromEntries(goalStreaks(days, { today: '2026-11-08', alcoholKnown: true }).map((x) => [x.key, x]));
  assert.deepEqual([by.protein.n, by.protein.from], [2, '2026-11-06']);
  assert.deepEqual(by.protein.best, { n: 3, from: '2026-11-01', to: '2026-11-03' });
  assert.equal(by.protein.miss, '2026-11-04');
  assert.deepEqual([by.logged.n, by.logged.miss, by.logged.best.n], [2, '2026-11-05', 4]);
  assert.deepEqual([by.alcohol_free.n, by.alcohol_free.miss, by.alcohol_free.grams], [2, '2026-11-04', 24]);
  // A drink today can't be undone: it ends the streak now.
  days[6] = day('2026-11-08', 50, 12);
  const a = goalStreaks(days, { today: '2026-11-08', alcoholKnown: true }).find((x) => x.key === 'alcohol_free');
  assert.deepEqual([a.n, a.miss, a.grams, a.best.n], [0, '2026-11-08', 12, 3]);
  // Without a steps goal or alcohol logged: left out.
  assert.deepEqual(goalStreaks(days, {}).map((x) => x.key), ['logged', 'protein', 'energy']);
  assert.deepEqual(goalStreaks([], {}), []);
});

test('lastTimes: the last training and weigh-in, today included', () => {
  const days = [
    { date: '2026-11-01', weight: 80.2, workouts: [{ id: 'a', type: 'RUNNING', dur: 1800 }] },
    { date: '2026-11-05', workouts: [{ id: 'b', type: 'STRENGTH_TRAINING', dur: 3600 }, { id: 'c', type: 'WALKING', dur: 1800 }] },
    { date: '2026-11-08', weight: 79.6, workouts: [{ id: 'd', type: 'WALKING', dur: 3600 }] }, // a walk isn't training
  ];
  const [tr, we] = lastTimes(days, { today: '2026-11-08' });
  assert.deepEqual([tr.key, tr.date, tr.what, tr.recent], ['trained', '2026-11-05', ['Strength training'], 2]);
  assert.deepEqual([we.key, we.date, we.value], ['weighed', '2026-11-08', 79.6]);
  // A noted lift counts, with no workout to name; walks count when picked.
  assert.deepEqual(lastTimes(days, { noted: ['2026-11-07'], today: '2026-11-08' })[0].date, '2026-11-07');
  assert.equal(lastTimes(days, { types: ['WALKING'], today: '2026-11-08' })[0].date, '2026-11-08');
  // Only 28 days back count as recent.
  assert.equal(lastTimes(days, { today: '2026-11-30' })[0].recent, 1);
  assert.deepEqual(lastTimes([{ date: '2026-11-01' }]), []);
});
