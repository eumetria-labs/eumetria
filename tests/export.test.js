import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { exportFiles, exportTables, toCSV } from '../site/js/export.js';
import { crc32, zip } from '../site/js/zip.js';

const data = {
  title: 'T', timezone: 'Europe/Berlin', goals: { energy: 2000, steps: 10000 },
  products: { 111: { n: 'Skyr', b: 'Arla', ns: 'a', nova: 1, add: ['e330', 'e440'] } },
  recipes: { r1: { name: 'Porridge', portions: 2, items: [{ n: 'Oats', a: 80, u: 'g', e: 300, pr: 10, c: 50, f: 6, x: '111' }] } },
  days: [
    {
      date: '2026-10-12', energy: 2306.444, energy_goal: 2400, energy_base: 2000, protein_goal: 150, meals: { breakfast: 500 },
      foods: [{ n: 'Skyr, natur', m: 'breakfast', e: 150, pr: 27, c: 10, f: 0.5, fi: 0, su: 6, sf: 0.3, a: 250, u: 'g', x: '111' }, { n: 'Porridge', m: 'breakfast', e: 350, r: 'r1', ai: true }],
      photos: [{ src: 'photos/x.jpg' }],
      activities: [{ name: 'Cycling', energy: 241, dur: 41, dist: 15857, w: 'w1' }],
      workouts: [{ id: 'w1', type: 'BIKING', start: '17:30', dur: 2460, zones: [60, 120, 600, 0], limits: [120, 140, 160], hr: [[0, 110], [60, 130]], splits: [[1000, 150]], pauses: [[100, 160]], route_src: 'routes/a.json', route: [[1, 2, 0, 0, 0]] }],
      sleep: { start: '23:50', end: '07:00', asleep: 400, stages: [[0, 20, 1], [20, 30, 2]], restless: [[25, 2]], restless_min: 2 },
      hrv: 33.3, azm: [5, 10, 0],
    },
    { date: '2026-10-14', energy: 1800 },
  ],
};

const table = (name) => {
  const [, columns, rows] = exportTables(data, '2026-10-11', '2026-10-14').find(([n]) => n === name);
  return { columns, rows };
};

test('the days table has every date of the range, with scaled macro goals', () => {
  const { rows } = table('days');
  assert.deepEqual(rows.map((r) => r.date), ['2026-10-11', '2026-10-12', '2026-10-13', '2026-10-14']);
  assert.equal(rows[0].kcal_eaten, null);
  assert.equal(rows[1].kcal_eaten, 2306.44);
  assert.equal(rows[1].protein_goal_g, 180);
  assert.equal(rows[1].kcal_breakfast, 500);
  assert.equal(rows[1].zone_minutes, 15);
  assert.equal(rows[1].foods, 2);
});

test('foods carry their product data and recipes their ingredients', () => {
  const foods = table('foods').rows;
  assert.equal(foods[0].nutri_score, 'A');
  assert.equal(foods[0].protein_g, 27);
  assert.equal(foods[0].fiber_g, 0); // known zero stays 0
  assert.equal(foods[0].sugar_g, 6);
  assert.equal(foods[1].fiber_g, null); // unknown stays empty
  assert.equal(foods[0].additives, 'e330; e440');
  assert.equal(foods[1].recipe_id, 'r1');
  assert.equal(foods[1].ai_estimate, true);
  assert.equal(foods[0].ai_estimate, null);
  const rec = table('recipes').rows;
  assert.equal(rec.length, 1);
  assert.equal(rec[0].recipe, 'Porridge');
  assert.equal(rec[0].brand, undefined);
  assert.equal(rec[0].nova, 1);
});

test('workouts, heart rate, splits and sleep stages', () => {
  const [w] = table('workouts').rows;
  assert.equal(w.vigorous_zone_s, 600);
  assert.equal(w.peak_from_bpm, 160);
  assert.equal(w.paused_s, 60);
  assert.equal(w.distance_km, null);
  assert.deepEqual(table('workout_heart_rate').rows.map((r) => [r.time, r.bpm]), [['17:30', 110], ['17:31', 130]]);
  assert.equal(table('workout_splits').rows[0].seconds, 150);
  assert.equal(table('activities').rows[0].distance_km, 15.86);
  const [s] = table('sleep').rows;
  assert.equal(s.asleep_min, 400);
  assert.equal(s.hrv_ms, 33.3);
  assert.equal(s.restless_moments, 1);
  assert.deepEqual(table('sleep_stages').rows.map((r) => [r.start, r.stage]), [['23:50', 'light'], ['00:10', 'deep'], ['00:15', 'restless']]);
});

test('no routes or photos, and empty tables still have their columns', () => {
  const text = exportFiles(data, '2026-10-11', '2026-10-14', 'json').map(([, t]) => t).join('');
  assert.ok(!text.includes('routes/') && !text.includes('photos/'));
  const none = exportTables(data, '2026-10-14', '2026-10-14');
  const [, columns, rows] = none.find(([n]) => n === 'workouts');
  assert.equal(rows.length, 0);
  assert.ok(columns.includes('workout_id') && columns.includes('paused_s'));
});

test('CSV and JSON hold the same tables and values', () => {
  const now = new Date('2026-10-20T08:00:00Z');
  const csv = exportFiles(data, '2026-10-11', '2026-10-14', 'csv', now);
  const json = exportFiles(data, '2026-10-11', '2026-10-14', 'json', now);
  assert.deepEqual(csv.map(([p]) => p.replace('.csv', '')), json.map(([p]) => p.replace('.json', '')));
  assert.equal(csv[0][0], 'eumetria-2026-10-11-to-2026-10-14/info.csv');
  assert.equal(JSON.parse(json[0][1]).goal_steps, 10000);
  const foodsCSV = csv.find(([p]) => p.endsWith('/foods.csv'))[1];
  const foodsJSON = JSON.parse(json.find(([p]) => p.endsWith('/foods.json'))[1]);
  assert.equal(foodsCSV.split('\n')[1].split(',').slice(0, 3).join(','), '2026-10-12,breakfast,"Skyr');
  assert.equal(foodsJSON[0].name, 'Skyr, natur');
});

test('toCSV quotes cells with commas, quotes and line breaks', () => {
  assert.equal(toCSV(['a', 'b'], [{ a: 'x, "y"', b: null }, { a: 1.5, b: true }]), 'a,b\n"x, ""y""",\n1.5,true\n');
});

test('crc32 and a ZIP that unzip can read', (t) => {
  assert.equal(crc32(new TextEncoder().encode('123456789')), 0xcbf43926);
  const dir = mkdtempSync(join(tmpdir(), 'zip-'));
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  writeFileSync(join(dir, 'a.zip'), zip([['x/a.csv', 'a,b\n'], ['x/b.json', '{"k":"ü"}\n']]));
  try {
    execFileSync('unzip', ['-q', join(dir, 'a.zip'), '-d', dir]);
  } catch (err) {
    if (err.code === 'ENOENT') return t.skip('unzip not installed');
    throw err;
  }
  assert.equal(readFileSync(join(dir, 'x/a.csv'), 'utf8'), 'a,b\n');
  assert.equal(readFileSync(join(dir, 'x/b.json'), 'utf8'), '{"k":"ü"}\n');
});
