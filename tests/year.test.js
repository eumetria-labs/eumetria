import { test } from 'node:test';
import assert from 'node:assert/strict';
import { yearSummary, yearsOf } from '../site/js/year.js';

const days = [
  { date: '2025-12-31', energy: 3000, weight: 90, foods: [{ n: 'Cake' }] },
  { date: '2026-01-01', energy: 2000, energy_goal: 2100, protein: 150, weight: 80, steps: 8000, foods: [{ n: 'Oats' }, { n: 'Eggs' }],
    sleep: { asleep: 420 }, scores: { sleep: { value: 80 }, readiness: { value: 70 } },
    workouts: [{ type: 'RUNNING', dur: 1800, dist: 5000 }] },
  { date: '2026-01-02', energy: 2200, energy_goal: 2100, protein: 130, steps: 12000, foods: [{ n: 'Oats' }],
    sleep: { asleep: 480 }, scores: { sleep: { value: 90 }, readiness: { value: 90 } },
    workouts: [{ type: 'WALKING', dur: 3600, dist: 4000 }] },
  { date: '2026-02-01', weight: 78, steps: 6000, scores: { sleep: { value: 95 } } },
];
const lifts = [
  { id: 1, lift: 'Back Squat', kg: 100, reps: 1, date: '2025-11-01' },
  { id: 2, lift: 'Back Squat', kg: 110, reps: 1, date: '2026-01-02' },
  { id: 3, lift: 'Deadlift', kg: 150, reps: 1, date: '2026-01-05' }, // a first result: no best yet
];

test('the years with data, newest first', () => {
  assert.deepEqual(yearsOf(days), ['2026', '2025']);
});

test('a year in numbers counts only that year', () => {
  const y = yearSummary(days, '2026', { lifts });
  assert.equal(y.n, 3);
  assert.deepEqual(y.nutrition, { logged: 2, kcal: 2100, protein: 140 });
  assert.deepEqual(y.weight, { first: 80, firstDate: '2026-01-01', last: 78, lastDate: '2026-02-01', min: 78, n: 2 });
  assert.deepEqual(y.steps, { total: 26000, avg: 26000 / 3, top: { date: '2026-01-02', steps: 12000 } });
  // A walk is a workout but not a day trained; a noted lift result is (Jan 5).
  assert.equal(y.training.days, 3);
  assert.equal(y.training.workouts, 2);
  assert.equal(y.training.hours, 1.5);
  assert.equal(y.training.km, 9);
  assert.equal(y.training.longest.type, 'WALKING');
  assert.deepEqual(y.bests.lifts, [{ date: '2026-01-02', name: 'Back Squat', reps: 1, kg: 110, n: 1, from: 100 }]);
  assert.equal(y.bests.n, 1);
  assert.equal(y.sleep.asleep, 450);
  assert.equal(y.sleep.readiness, 80);
  assert.deepEqual(y.sleep.bestMonth, { month: 1, score: 95 });
  assert.equal(y.months[0].trained, 3);
  assert.deepEqual(y.foods, [['Oats', 2], ['Eggs', 1]]);
});

test('parts the data lacks stay empty', () => {
  const y = yearSummary([{ date: '2024-05-01', steps: 100 }], '2024');
  assert.equal(y.nutrition, null);
  assert.equal(y.weight, null);
  assert.equal(y.training, null);
  assert.equal(y.bests, null);
  assert.equal(y.sleep, null);
  assert.deepEqual(y.foods, []);
});
