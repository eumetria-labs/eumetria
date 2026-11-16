import { test } from 'node:test';
import assert from 'node:assert/strict';
import { phaseOn, phaseSlots, phaseSummary, trainedOn, trainingVsRest } from '../site/js/phase.js';
import { parse } from '../site/js/format.js';

const cut = { id: 1, kind: 'cut', label: 'Winter cut', start: '2026-10-03', end: '2026-10-05' };
const open = { id: 2, kind: 'maintain', label: '', start: '2026-10-06', end: '' };
const comp = { id: 3, kind: 'event', label: 'Open', start: '2026-10-04', end: '2026-10-04' };
const ahead = { id: 4, kind: 'event', label: 'Later', start: '2026-12-01', end: '2026-12-01' };

test('phases fall on the slots they touch, events on their day', () => {
  const dates = ['2026-10-01', '2026-10-02', '2026-10-03', '2026-10-04', '2026-10-05', '2026-10-06', '2026-10-07'].map(parse);
  const { bands, events } = phaseSlots(dates, [cut, open, comp, ahead], '2026-10-07');
  assert.deepEqual(bands.map((b) => [b.phase.id, b.from, b.to]), [[1, 2, 4], [2, 5, 6]]);
  assert.deepEqual(events.map((e) => [e.phase.id, e.i]), [[3, 3]]);
});

test('on weekly slots a phase covers each week it touches', () => {
  const weeks = ['2026-09-21', '2026-09-28', '2026-10-05', '2026-10-12'].map(parse);
  const { bands, events } = phaseSlots(weeks, [cut, comp], '2026-10-18');
  assert.deepEqual(bands.map((b) => [b.from, b.to]), [[1, 2]]);
  assert.deepEqual(events.map((e) => e.i), [1]);
});

test('the phase on a day: a running one lasts until today', () => {
  assert.equal(phaseOn([cut, open], '2026-10-04', '2026-10-10').id, 1);
  assert.equal(phaseOn([cut, open], '2026-10-09', '2026-10-10').id, 2);
  assert.equal(phaseOn([cut, open], '2026-10-11', '2026-10-10'), null);
  assert.equal(phaseOn([comp], '2026-10-04', '2026-10-10'), null);
});

test('a training day has a workout, an activity or a noted result', () => {
  assert.ok(trainedOn({ date: 'a', workouts: [{}] }));
  assert.ok(trainedOn({ date: 'a', activities: [{}] }));
  assert.ok(trainedOn({ date: 'a' }, new Set(['a'])));
  assert.ok(!trainedOn({ date: 'a', workouts: [] }));
});

test('training vs rest days average the logged days of each', () => {
  const rows = [
    { date: '1', energy: 2600, energy_goal: 2500, protein: 180, carb: 300, fat: 80, workouts: [{}] },
    { date: '2', energy: 2400, energy_goal: 2500, protein: 160, carb: 260, fat: 70 },
    { date: '3', energy: 2000, energy_goal: 2200, protein: 150, carb: 200, fat: 70 },
    { date: '4' }, // not logged
    { date: '5', energy: 2800, protein: 200, carb: 320, fat: 90 },
  ];
  const r = trainingVsRest(rows, new Set(['5']));
  assert.deepEqual(r.training, { n: 2, energy: 2700, vsGoal: 100, protein: 190, carb: 310, fat: 85 });
  assert.deepEqual(r.rest, { n: 2, energy: 2200, vsGoal: -150, protein: 155, carb: 230, fat: 70 });
});

test('a phase in numbers: weight change, averages and training per week', () => {
  const byDate = new Map();
  const phase = { kind: 'cut', start: '2026-09-01', end: '2026-09-28' };
  for (let i = 0; i < 28; i++) {
    const date = `2026-09-${String(i + 1).padStart(2, '0')}`;
    byDate.set(date, { date, weight: 90 - i * 0.1, energy: 2000, protein: 160, workouts: i % 7 < 3 ? [{}] : [] });
  }
  const s = phaseSummary(phase, byDate, '2026-10-10');
  assert.equal(s.days, 28);
  assert.equal(s.logged, 28);
  assert.equal(s.energy, 2000);
  assert.equal(s.protein, 160);
  assert.equal(s.perWeek, 3);
  // The first week's average (90 … 89.4) to the last one's (87.9 … 87.3).
  assert.equal(Math.round(s.weight.change * 10) / 10, -2.1);
  // Too short for a weight change.
  assert.equal(phaseSummary({ kind: 'cut', start: '2026-09-01', end: '2026-09-10' }, byDate, '2026-10-10').weight, null);
});
