import { test } from 'node:test';
import assert from 'node:assert/strict';
import { MACRO_TABS, activityHistory, dayActivities, macroSources, rankSources, referencedFiles, sessionsPerWeek, filterDays, foodHistory, matchRank, matchesQuery, rangeDays, searchAll, sortBy, sortFoods } from '../site/js/query.js';

const days = [
  { date: '2026-10-11', energy: 2300, energy_goal: 2100, foods: [{ n: 'Pizza Margherita', m: 'dinner', e: 800 }] },
  { date: '2026-10-12', energy: 1900, energy_goal: 2100, weight: 78.4, activities: [{ name: 'krafttraining', energy: 200 }] },
  { date: '2026-10-13', steps: 9000, sleep: { asleep: 420 }, workouts: [{ id: 'w', type: 'BIKING', route_src: 'routes/a.json', route: [[1, 2, 0, 0, 0]] }] },
];

test('search matches dates, foods, activities and workouts, every word', () => {
  assert.ok(matchesQuery(days[0], 'pizza'));
  assert.ok(matchesQuery(days[0], 'PIZZA oct'));
  assert.ok(!matchesQuery(days[0], 'pizza nov'));
  assert.ok(matchesQuery(days[1], 'strength')); // the English name of a German activity
  assert.ok(matchesQuery(days[2], 'cycling'));
  assert.ok(matchesQuery(days[2], '10-13'));
  assert.ok(matchesQuery(days[2], '  '));
});

test('day filters', () => {
  const dates = (f, q = '') => filterDays(days, f, q).map((d) => d.date.slice(-2));
  assert.deepEqual(dates('all'), ['11', '12', '13']);
  assert.deepEqual(dates('over'), ['11']);
  assert.deepEqual(dates('under'), ['12']);
  assert.deepEqual(dates('weight'), ['12']);
  assert.deepEqual(dates('training'), ['12', '13']);
  assert.deepEqual(dates('sleep'), ['13']);
  assert.deepEqual(dates('logged', 'pizza'), ['11']);
  assert.deepEqual(dates('unknown'), ['11', '12', '13']);
});

test('sorting keeps missing values last in both directions', () => {
  const by = (desc) => sortBy(days, (r) => r.energy, desc).map((d) => d.date.slice(-2));
  assert.deepEqual(by(true), ['11', '12', '13']);
  assert.deepEqual(by(false), ['12', '11', '13']);
  assert.deepEqual(sortBy(days, (r) => r.date, true).map((d) => d.date.slice(-2)), ['13', '12', '11']);
});

test('foods sort by count, calories, average and name, and filter by search', () => {
  const foods = [['Apple', { n: 5, e: 400 }], ['banana', { n: 2, e: 300 }], ['Cake', { n: 1, e: 450 }]];
  const names = (k, q) => sortFoods([...foods], k, q).map(([n]) => n);
  assert.deepEqual(names('count'), ['Apple', 'banana', 'Cake']);
  assert.deepEqual(names('kcal'), ['Cake', 'Apple', 'banana']);
  assert.deepEqual(names('avg'), ['Cake', 'banana', 'Apple']);
  assert.deepEqual(names('name'), ['Apple', 'banana', 'Cake']);
  assert.deepEqual(names('count', 'AN'), ['banana']);
});

test('food history over every day, newest first', () => {
  const hist = [
    { date: '2026-08-30', foods: [{ n: 'Skyr', m: 'breakfast', e: 150, a: 250, u: 'g', x: '111' }] },
    { date: '2026-09-02' },
    { date: '2026-10-01', foods: [{ n: 'Skyr', m: 'snack', e: 90, a: 150, u: 'g', x: '111' }, { n: 'Apple', m: 'snack', e: 50 }] },
    { date: '2026-10-02', foods: [{ n: 'Skyr', m: 'breakfast', e: 120, a: 200, u: 'g', x: '222', ai: true }, { n: 'Skyr', m: 'snack', e: 60, a: 100, u: 'g', x: '111' }] },
  ];
  const f = foodHistory(hist, 'Skyr');
  assert.equal(f.n, 4);
  assert.equal(f.days, 3);
  assert.deepEqual(f.entries.map((e) => e.date), ['2026-10-02', '2026-10-02', '2026-10-01', '2026-08-30']);
  assert.equal(f.energy, 420);
  assert.equal(f.avgEnergy, 105);
  assert.equal(f.avgAmount, 175);
  assert.equal(f.unit, 'g');
  assert.deepEqual(f.months, [['2026-08', 1], ['2026-09', 0], ['2026-10', 3]]);
  assert.deepEqual(Object.fromEntries(f.meals), { breakfast: 2, snack: 2 });
  assert.equal(f.barcode, '111');
  assert.equal(f.ai, true);
  assert.equal(f.first, '2026-08-30');
  assert.equal(f.last, '2026-10-02');

  // Mixed units give no average amount; an unknown food has no entries.
  hist[0].foods[0].u = 'portion';
  assert.equal(foodHistory(hist, 'Skyr').avgAmount, null);
  const none = foodHistory(hist, 'Pizza');
  assert.equal(none.n, 0);
  assert.equal(none.last, null);
  assert.equal(foodHistory([], 'Skyr').months.length, 0);
});

test('food history months cross the year', () => {
  const f = foodHistory([{ date: '2025-11-30' }, { date: '2026-01-05', foods: [{ n: 'Tea', e: 2 }] }], 'Tea');
  assert.deepEqual(f.months, [['2025-11', 0], ['2025-12', 0], ['2026-01', 1]]);
});

test('day activities: Yazio entries named after their Fitbit workout, unlinked workouts added', () => {
  const r = {
    date: '2026-10-14',
    activities: [
      { name: 'crossfit', energy: 400, dur: 60 },
      { name: 'training', energy: 300, w: 'w1' },
      { name: 'walking', energy: 100, dur: 30, w: 'w2' },
    ],
    workouts: [
      { id: 'w1', type: 'RUNNING', dur: 1800, dist: 5000, energy: 320 },
      { id: 'w2', type: 'OTHER', dur: 1500 },
      { id: 'w3', type: 'BIKING', dur: 3600, dist: 20000, energy: 500 },
    ],
  };
  assert.deepEqual(dayActivities(r), [
    { key: 'CrossFit', energy: 400, dur: 60, dist: 0, w: null },
    { key: 'Running', energy: 300, dur: 30, dist: 5000, w: 'w1' }, // minutes from the workout's seconds
    { key: 'Walking', energy: 100, dur: 30, dist: 0, w: 'w2' }, // an OTHER workout keeps the Yazio name
    { key: 'Cycling', energy: 500, dur: 60, dist: 20000, w: 'w3' },
  ]);
});

test('activity history over every day', () => {
  const hist = [
    { date: '2026-09-28', activities: [{ name: 'crossfit', energy: 400, dur: 60 }] }, // a Monday
    { date: '2026-10-05', activities: [{ name: 'CrossFit', energy: 500 }] }, // Monday
    { date: '2026-10-08', activities: [{ name: 'crossfit', energy: 450, dur: 50 }, { name: 'walking', energy: 80 }] }, // Thursday
  ];
  const a = activityHistory(hist, 'CrossFit');
  assert.equal(a.n, 3);
  assert.equal(a.days, 3);
  assert.deepEqual(a.entries.map((e) => e.date), ['2026-10-08', '2026-10-05', '2026-09-28']);
  assert.equal(a.energy, 1350);
  assert.equal(a.avgEnergy, 450);
  assert.equal(a.avgDur, 55); // only entries with a duration
  assert.equal(a.dur, 110);
  assert.deepEqual(a.weekdays, [2, 0, 0, 1, 0, 0, 0]);
  assert.deepEqual(a.months, [['2026-09', 1], ['2026-10', 2]]);
  assert.equal(a.last, '2026-10-08');
  assert.equal(activityHistory(hist, 'Walking').avgDur, null);
  assert.equal(activityHistory(hist, 'Yoga').n, 0);
});

test('referencedFiles finds photos, routes and archive files anywhere in the data', () => {
  const data = {
    archive: ['days/2025.json'],
    recipes: { r: { name: 'Bowl', img: 'photos/r.jpg' } },
    days: [
      { date: '2026-10-12', photos: [{ m: 'lunch', src: 'photos/a.jpg' }, { src: 'data:image/svg+xml,x' }] },
      { date: '2026-10-13', workouts: [{ id: 'w', route_src: 'routes/a.json' }, { id: 'v', route_src: 'routes/a.json' }] },
    ],
  };
  assert.deepEqual(referencedFiles(data).sort(), ['days/2025.json', 'photos/a.jpg', 'photos/r.jpg', 'routes/a.json']);
});

test('sessionsPerWeek counts activities per Monday-first week', () => {
  const byDate = new Map([
    ['2026-10-05', { date: '2026-10-05', activities: [{ name: 'running', energy: 300 }] }], // Monday
    ['2026-10-11', { date: '2026-10-11', activities: [{ name: 'running', energy: 300 }, { name: 'yoga', energy: 90 }] }], // Sunday
    ['2026-10-14', { date: '2026-10-14', workouts: [{ id: 'w', type: 'BIKING', dur: 1800 }] }],
  ]);
  const d = (s) => new Date(s + 'T00:00:00Z');
  const weeks = sessionsPerWeek(byDate, d('2026-09-20'), d('2026-10-15'), d('2026-10-07'));
  // Not before the first synced day (Oct 7), so from its week, Oct 5.
  assert.deepEqual(weeks.map((w) => [w.start.toISOString().slice(0, 10), w.n]), [['2026-10-05', 3], ['2026-10-12', 1]]);
  assert.equal(weeks[0].keys.size, 2);
});

test('matchRank: prefix, then word start, then substring; umlauts any way', () => {
  assert.equal(matchRank('Skyr natur', 'sky'), 0);
  assert.equal(matchRank('Greek yogurt', 'yog'), 1);
  assert.equal(matchRank('Bircher-Müsli', 'müsli'), 1);
  assert.equal(matchRank('Müsli', 'MUESLI'), 0);
  assert.equal(matchRank('Müsli', 'musli'), 0);
  assert.equal(matchRank('Crème fraîche', 'creme fr'), 0);
  assert.equal(matchRank('Weißbrot', 'weissbrot'), 0);
  assert.equal(matchRank('Joghurt mit Müsli', 'mit joghurt'), 2);
  assert.equal(matchRank('Banana', 'apple'), null);
});

test('searchAll groups foods, activities and days', () => {
  const days = [
    { date: '2026-10-11', energy: 2000, foods: [{ n: 'Müsli' }, { n: 'Apple' }], activities: [{ name: 'running', energy: 300 }] },
    { date: '2026-10-12', energy: 1800, foods: [{ n: 'Bircher-Müsli' }, { n: 'Müsli' }] },
    { date: '2026-10-13', foods: [{ n: 'Banana' }], workouts: [{ id: 'w', type: 'RUNNING', dur: 1800 }] },
  ];
  const r = searchAll(days, 'muesli');
  // Prefix first, then by count.
  assert.deepEqual(r.foods.map((f) => [f.name, f.n]), [['Müsli', 2], ['Bircher-Müsli', 1]]);
  assert.deepEqual(r.days.map((d) => d.date), ['2026-10-12', '2026-10-11']); // newest first
  assert.deepEqual(searchAll(days, 'run').activities.map((a) => [a.name, a.n]), [['Running', 2]]);
  assert.deepEqual(searchAll(days, 'oct 13').days.map((d) => d.date), ['2026-10-13']);
  assert.deepEqual(searchAll(days, '  '), { foods: [], activities: [], days: [] });
  assert.equal(searchAll(days, 'a', 1).foods.length, 1);
});

test('rangeDays: last N days, all, custom; clamped to the synced days', () => {
  const d = (s) => new Date(s + 'T00:00:00Z');
  const day = ({ start, end }) => [start.toISOString().slice(0, 10), end.toISOString().slice(0, 10)];
  const first = d('2026-08-16'), last = d('2026-10-23');
  assert.deepEqual(day(rangeDays('7', first, last)), ['2026-10-17', '2026-10-23']);
  assert.deepEqual(day(rangeDays('30', first, last)), ['2026-09-24', '2026-10-23']);
  assert.deepEqual(day(rangeDays('365', first, last)), ['2026-08-16', '2026-10-23']);
  assert.deepEqual(day(rangeDays('all', first, last)), ['2026-08-16', '2026-10-23']);
  assert.deepEqual(day(rangeDays('custom', first, last, '2026-09-01', '2026-09-30')), ['2026-09-01', '2026-09-30']);
  assert.deepEqual(day(rangeDays('custom', first, last, '2026-09-30', '2026-09-01')), ['2026-09-01', '2026-09-30']);
  assert.deepEqual(day(rangeDays('custom', first, last, '2026-01-01', '2026-12-31')), ['2026-08-16', '2026-10-23']);
  assert.deepEqual(day(rangeDays('custom', first, last)), ['2026-09-24', '2026-10-23']);
});

test('macro sources: biggest and best per calorie, partial fibre counted fairly', () => {
  const day = (foods) => ({ date: '2026-10-01', foods });
  const skyr = { n: 'Skyr', e: 126, pr: 22, c: 7, f: 0.4, fi: 0, su: 7, sf: 0.2 };
  const pizza = { n: 'Pizza', e: 800, pr: 32, c: 100, f: 30, fi: 5, su: 8, sf: 12 };
  const oats = { n: 'Oats', e: 220, pr: 8, c: 36, f: 4, fi: 6, sf: 0.8 };
  const bowl = { n: 'Bowl', e: 600, pr: 30, c: 60, f: 25 }; // AI: no fibre or saturated fat
  const rows = [day([skyr, pizza, oats, bowl]), day([skyr, pizza, oats]), day([skyr, oats, bowl])];
  const { foods, totals } = macroSources(rows);
  assert.equal(totals.pr, 22 * 3 + 32 * 2 + 8 * 3 + 30 * 2);
  assert.equal(totals.fiE, 126 * 3 + 800 * 2 + 220 * 3); // only entries with fibre
  const tab = (k) => rankSources(foods, MACRO_TABS.find((x) => x.key === k), { minN: 2 });
  assert.deepEqual(tab('pr').top.map(([n]) => n), ['Skyr', 'Pizza', 'Bowl', 'Oats']);
  assert.equal(tab('pr').best[0][0], 'Skyr'); // most protein per kcal
  assert.deepEqual(tab('fi').best.map(([n]) => n), ['Oats', 'Pizza']); // Bowl has no fibre data, Skyr none
  assert.deepEqual(tab('f').best.map(([n]) => n), ['Oats', 'Pizza']); // lowest saturated share first
  assert.deepEqual(tab('su').best.map(([n]) => n), ['Skyr', 'Pizza']); // 5,6 and 1 g per 100 kcal; Oats has no sugar data
  assert.equal(totals.suE, 126 * 3 + 800 * 2);
});
