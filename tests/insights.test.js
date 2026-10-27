import { test } from 'node:test';
import assert from 'node:assert/strict';
import { energyBalance, goalForecast, macroGoal, median, nightsAfter, proteinPerKg, slope, weekdayAverages, KCAL_PER_KG, goalChangesIn, rangeSummary, weightForecast, weightGoalAt, weightGoalLine, highlights } from '../site/js/insights.js';
import { addDays, iso, parse } from '../site/js/format.js';

test('macroGoal scales the base goal with activity, like the Yazio app', () => {
  const day = { protein_goal: 83.2, energy_goal: 2006, energy_base: 1706 };
  assert.ok(Math.abs(macroGoal(day, 'protein') - 83.2 * 2006 / 1706) < 1e-9);
  assert.equal(macroGoal({ energy_goal: 1706, energy_base: 1706 }, 'protein', { protein: 83 }), 83);
  assert.equal(macroGoal(null, 'fat', { fat: 55 }), 55);
  assert.equal(macroGoal(null, 'fat', {}), null);
});

test('slope', () => {
  assert.equal(slope([[0, 1], [1, 3], [2, 5]]), 2);
  assert.equal(slope([[0, 1]]), null);
});

// 28 days eating 1800 kcal while losing 0.5 kg/week.
function history({ intake = 1800, kgPerWeek = -0.5, loggedDays = 28, weighEvery = 2 } = {}) {
  const end = parse('2026-10-13');
  const byDate = new Map();
  for (let k = 0; k < 28; k++) {
    const date = iso(addDays(end, -k));
    const day = { date };
    if (k < loggedDays) day.energy = intake;
    if (k % weighEvery === 0) day.weight = 80 + (kgPerWeek / 7) * (27 - k);
    byDate.set(date, day);
  }
  return byDate;
}

test('energyBalance recovers maintenance from intake and weight trend', () => {
  const eb = energyBalance(history(), '2026-10-13');
  assert.equal(eb.ok, true);
  assert.ok(Math.abs(eb.kgPerWeek - -0.5) < 1e-9);
  const expected = 1800 + (0.5 / 7) * KCAL_PER_KG; // 2350 kcal
  assert.ok(Math.abs(eb.maintenance - expected) < 1e-6, `maintenance ${eb.maintenance}`);
  assert.ok(Math.abs(eb.balance - -550) < 1e-6);
});

test('energyBalance needs enough logged days and weigh-ins', () => {
  assert.equal(energyBalance(history({ loggedDays: 10 }), '2026-10-13').ok, false);
  assert.equal(energyBalance(history({ weighEvery: 7 }), '2026-10-13').ok, false);
});

test('goalForecast', () => {
  const fc = goalForecast(77, -0.5, 70, '2026-10-13');
  assert.equal(fc.status, 'on-track');
  assert.equal(fc.weeks, 14);
  assert.equal(fc.date, '2027-01-19');
  assert.equal(goalForecast(77, 0.3, 70, '2026-10-13').status, 'off-track');
  assert.equal(goalForecast(70.1, -0.5, 70, '2026-10-13').status, 'reached');
  assert.equal(goalForecast(90, -0.05, 70, '2026-10-13').status, 'too-far');
  assert.equal(goalForecast(null, -0.5, 70, '2026-10-13'), null);
});

test('nightsAfter pairs each day with the following night', () => {
  const rows = [
    { date: '2026-10-01', energy: 3000 },
    { date: '2026-10-02', energy: 1500 },
    { date: '2026-10-03', energy: 2900, hrv: 30, sleep_hr: 60, sleep: { asleep: 400 } },
    { date: '2026-10-04', hrv: 50, sleep_hr: 52, sleep: { asleep: 460 } },
  ];
  const byDate = new Map(rows.map((r) => [r.date, r]));
  const high = (r) => (!r.energy ? null : r.energy > 2000 ? 0 : 1);
  const { groups, ok } = nightsAfter(rows, byDate, high, { minNights: 1 });
  // Oct 1 (high) has no night data on Oct 2; Oct 2 (low) → Oct 3; Oct 3 (high) → Oct 4.
  assert.deepEqual(groups, [{ n: 1, hrv: 50, sleepHR: 52, asleep: 460 }, { n: 1, hrv: 30, sleepHR: 60, asleep: 400 }]);
  assert.equal(ok, true);
  assert.equal(nightsAfter(rows, byDate, high).ok, false);
  assert.equal(median([3, 1, 2]), 2);
  assert.equal(median([4, 1, 2, 3]), 2.5);
});

test('weekdayAverages starts on Monday and skips unlogged days', () => {
  const rows = [{ date: '2026-10-19', energy: 2000 }, { date: '2026-10-12', energy: 2400 }, { date: '2026-10-18', energy: 3000 }, { date: '2026-10-17', energy: 0 }];
  const w = weekdayAverages(rows);
  assert.deepEqual(w[0], { avg: 2200, n: 2 }); // Mondays
  assert.deepEqual(w[6], { avg: 3000, n: 1 }); // Sunday
  assert.deepEqual(w[5], { avg: null, n: 0 }); // Saturday, not logged
});

test('proteinPerKg splits workout and rest days and uses the last known weight', () => {
  const rows = [
    { date: '2026-10-01', energy: 2000, protein: 160, weight: 80 },
    { date: '2026-10-20', energy: 2000, protein: 120, workouts: [{}] }, // 19 days after the weigh-in
  ];
  const byDate = new Map(rows.map((r) => [r.date, r]));
  const p = proteinPerKg(rows, byDate);
  assert.equal(p.all.avg, 1.75);
  assert.equal(p.rest.avg, 2);
  assert.equal(p.workout.avg, 1.5);
});

test('goal changes between two dates, grouped per day', () => {
  const changes = [
    { date: '2026-04-10', goal: 'energy', from: 2350, to: 2200 },
    { date: '2026-04-10', goal: 'weight', from: 80, to: 78 },
    { date: '2026-09-12', goal: 'energy', from: 2200, to: 2100 },
  ];
  const got = goalChangesIn(changes, '2026-04-01', '2026-05-01');
  assert.equal(got.length, 1);
  assert.equal(got[0][0], '2026-04-10');
  assert.deepEqual(Object.keys(got[0][1]), ['energy', 'weight']);
  assert.deepEqual(goalChangesIn(undefined, '2026-01-01', '2026-12-31'), []);

  // The weight goal in effect: before the first change, after it, or the current one.
  assert.equal(weightGoalAt(changes, '2026-03-01', 78), 80);
  assert.equal(weightGoalAt(changes, '2026-04-10', 78), 78);
  assert.equal(weightGoalAt([], '2026-04-10', 75), 75);
  assert.equal(weightGoalAt([{ date: '2026-08-16', goal: 'weight', from: 0, to: 75 }], '2026-08-01', 75), null);
});

test('range summary for the printable report', () => {
  const rows = [
    { date: '2026-10-05', energy: 2000, energy_goal: 2200, protein: 120, carb: 200, fat: 70, meals: { breakfast: 500, dinner: 1500 }, weight: 80, steps: 8000, sleep: { asleep: 420, deep: 80, rem: 90 }, foods: [{ n: 'Skyr', e: 150 }, { n: 'Pizza', e: 850 }], activities: [{ name: 'Running', energy: 400, dur: 40 }] },
    { date: '2026-10-06', energy: 1800, energy_goal: 2000, protein: 100, weight: 79.5, steps: 12000, foods: [{ n: 'Skyr', e: 150 }] },
    { date: '2026-10-07' },
  ];
  const byDate = new Map(rows.map((r) => [r.date, r]));
  const s = rangeSummary(rows, byDate);
  assert.equal(s.days, 3);
  assert.equal(s.logged, 2);
  assert.equal(s.energy, 1900);
  assert.equal(s.goal, 2100);
  assert.equal(s.meals.breakfast, 250);
  assert.equal(s.steps, 10000);
  assert.equal(s.nights, 1);
  assert.equal(s.asleep, 420);
  assert.deepEqual(s.workouts, { n: 1, dur: 40, e: 400 });
  assert.deepEqual(s.weeks.map((w) => [w.start, w.days]), [['2026-10-05', 3]]);
  assert.equal(s.foods[0].name, 'Skyr');
  assert.equal(s.foods[0].n, 2);
  assert.equal(Math.round(s.foods[1].share * 100), 74);
  assert.equal(s.weightFrom, 80);
});

test('the weight goal line is drawn for the whole range or not at all', () => {
  // Goal 75 kg, then 70 kg, over weigh-ins around 79 kg: both, axis widened.
  assert.deepEqual(weightGoalLine([null, 75, 70], 78, 80), { goals: [null, 75, 70], lo: 70, hi: 80 });
  // A goal far below the weigh-ins hides the whole line.
  assert.deepEqual(weightGoalLine([75, 60], 78, 80), { goals: [null, null], lo: 78, hi: 80 });
  assert.deepEqual(weightGoalLine([null, null], 78, 80).goals, [null, null]);
});

test('weight forecast looks ahead a quarter of the range or just past the goal', () => {
  // 90-day chart, 0,7 kg/week down, goal 1 kg away: reached on day 10.
  const near = weightForecast(80, -0.7, 79, 90);
  assert.equal(near.hit, 10);
  assert.equal(near.days, 12);
  assert.equal(near.values[9], 79);
  assert.equal(near.values[10], null);
  // Goal far away: a quarter of the range, no hit.
  const far = weightForecast(80, -0.7, 70, 90);
  assert.equal(far.days, 23);
  assert.equal(far.hit, null);
  assert.equal(Math.round(far.values[6] * 10) / 10, 79.3);
  // Heading away from the goal: the line still shows, never hitting it.
  assert.equal(weightForecast(80, 0.5, 70, 30).hit, null);
});

test('highlights: a beaten workout record, lowest weight, most steps, protein streak', () => {
  const days = [];
  for (let i = 0; i < 90; i++) {
    const date = iso(addDays(parse('2026-07-28'), i));
    days.push({ date, energy: 2000, protein: i >= 82 ? 160 : 100, protein_goal: 150, steps: i === 88 ? 20000 : 8000, weight: 85 - i * 0.05 });
  }
  days[10].workouts = [{ id: 'a', type: 'RUNNING', dist: 5000, dur: 1800 }];
  days[87].workouts = [{ id: 'b', type: 'RUNNING', dist: 8000, dur: 2400 }];
  const byDate = new Map(days.map((d) => [d.date, d]));
  const got = highlights(days, byDate, { weight: 75 }, null);
  assert.deepEqual(got.map((x) => x.kind), ['best', 'best', 'weight-low']);
  const all = highlights(days.map((d) => ({ ...d, workouts: undefined })), byDate, { weight: 75 }, null);
  assert.deepEqual(all.map((x) => x.kind), ['weight-low', 'steps', 'protein']);
  assert.equal(all[0].since, null); // never lower since the first weigh-in
  assert.equal(all[1].value, 20000);
  assert.equal(all[2].value, 8);
  // Too short a history says nothing.
  assert.deepEqual(highlights(days.slice(-10), byDate, {}, null).filter((x) => x.kind !== 'protein'), []);
});
