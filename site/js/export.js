// The export of a date range: one table per kind of data (days, foods,
// recipes, activities, workouts, sleep), written as CSV or JSON files with
// the same columns and rows and bundled as a ZIP. GPS routes and photos are
// left out. No DOM access, so Node can test this file.

import { MEALS, addDays, iso, parse } from './format.js';
import { RESTLESS, STAGES } from './health.js';
import { macroGoal } from './insights.js';

// Sleep stage names by code; nights without stage tracking say "asleep" (4).
const stageName = (code) => [...STAGES, RESTLESS].find((st) => st.code === code)?.key ?? 'asleep';
const km = (m) => (m ? m / 1000 : null);
const sum = (xs) => xs.reduce((a, b) => a + b, 0);
const upper = (s) => s?.toUpperCase();
const additives = (p) => p?.add?.join('; ');

// "HH:MM" plus minutes, wrapping past midnight.
function clockPlus(hhmm, mins) {
  if (!hhmm) return null;
  const [h, m] = hhmm.split(':').map(Number);
  const t = (((h * 60 + m + Math.round(mins)) % 1440) + 1440) % 1440;
  return `${String(Math.floor(t / 60)).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}`;
}

// Each table is [name, items(days, data), row(item, data)]: the items are
// the table's rows before formatting, a row is a list of [column, value].
// row must also work on an empty item ({d: {}, f: {}, ...}), which gives the
// columns of a table without rows.
const TABLES = [
  ['days', (days) => days.map((d) => ({ d })), ({ d }) => [
    ['date', d.date],
    ['kcal_eaten', d.energy], ['kcal_goal', d.energy_goal], ['kcal_goal_base', d.energy_base],
    ['protein_g', d.protein], ['protein_goal_g', macroGoal(d, 'protein')],
    ['carbs_g', d.carb], ['carbs_goal_g', macroGoal(d, 'carb')],
    ['fat_g', d.fat], ['fat_goal_g', macroGoal(d, 'fat')],
    ...MEALS.map(([m]) => [`kcal_${m}`, d.meals?.[m]]),
    ['water_ml', d.water], ['steps', d.steps], ['weight_kg', d.weight],
    ['kcal_activities', d.activity_energy], ['kcal_burned_fitbit', d.kcal_burned], ['resting_hr', d.rhr],
    ['zone_min_fat_burn', d.azm?.[0]], ['zone_min_cardio', d.azm?.[1]], ['zone_min_peak', d.azm?.[2]],
    ['zone_minutes', d.azm && sum(d.azm)],
    ['foods', d.foods?.length], ['activities', d.activities?.length], ['workouts', d.workouts?.length],
  ]],
  ['foods', (days) => days.flatMap((d) => (d.foods || []).map((f) => ({ d, f }))), ({ d, f }, data) => {
    const p = data.products?.[f.x];
    return [
      ['date', d.date], ['meal', f.m], ['name', f.n],
      ['amount', f.a], ['unit', f.u], ['serving', f.s], ['servings', f.sq],
      ['kcal', f.e], ['protein_g', f.pr], ['carbs_g', f.c], ['fat_g', f.f], ['fiber_g', f.fi], ['sugar_g', f.su], ['saturated_fat_g', f.sf],
      ['ai_estimate', f.ai || null],
      // A recipe changed since that day whose version eaten is unknown.
      ['recipe_id', f.r], ['recipe_changed_since', f.rs || null],
      ['barcode', f.x], ['product', p?.n], ['brand', p?.b],
      ['nutri_score', upper(p?.ns)], ['nova', p?.nova], ['additives', additives(p)],
    ];
  }],
  // The recipes eaten in the range, one row per ingredient of the whole
  // recipe (all portions); foods.recipe_id points here.
  ['recipes', (days, data) => {
    const ids = new Set(days.flatMap((d) => (d.foods || []).map((f) => f.r).filter(Boolean)));
    return [...ids].flatMap((id) => (data.recipes?.[id]?.items || []).map((it) => ({ id, r: data.recipes[id], it })));
  }, ({ id, r, it }, data) => {
    const p = data.products?.[it.x];
    return [
      ['recipe_id', id], ['recipe', r.name], ['portions', r.portions],
      ['ingredient', it.n], ['producer', it.p], ['amount', it.a], ['unit', it.u], ['serving', it.s], ['servings', it.sq],
      ['kcal', it.e], ['protein_g', it.pr], ['carbs_g', it.c], ['fat_g', it.f],
      ['barcode', it.x], ['nutri_score', upper(p?.ns)], ['nova', p?.nova], ['additives', additives(p)],
    ];
  }],
  // Activities as logged in Yazio, some imported from Fitbit workouts.
  ['activities', (days) => days.flatMap((d) => (d.activities || []).map((a) => ({ d, a }))), ({ d, a }) => [
    ['date', d.date], ['name', a.name], ['kcal', a.energy], ['duration_min', a.dur], ['distance_km', km(a.dist)],
    ['fitbit_workout_id', a.w],
  ]],
  ['workouts', (days) => days.flatMap((d) => (d.workouts || []).map((w) => ({ d, w }))), ({ d, w }) => [
    ['date', d.date], ['workout_id', w.id], ['type', w.type], ['name', w.name], ['start', w.start],
    ['active_s', w.dur], ['elapsed_s', w.elapsed], ['kcal', w.energy],
    ['avg_hr', w.hr_avg], ['max_hr', w.hr_max], ['zone_minutes', w.azm],
    ['distance_km', km(w.dist)], ['steps', w.steps], ['elevation_m', w.elev],
    ['light_zone_s', w.zones?.[0]], ['moderate_zone_s', w.zones?.[1]], ['vigorous_zone_s', w.zones?.[2]], ['peak_zone_s', w.zones?.[3]],
    ['moderate_from_bpm', w.limits?.[0]], ['vigorous_from_bpm', w.limits?.[1]], ['peak_from_bpm', w.limits?.[2]],
    ['pauses', w.pauses?.length], ['paused_s', w.pauses && sum(w.pauses.map(([a, b]) => b - a))],
  ]],
  // The workouts' heart rate, averaged per minute.
  ['workout_heart_rate', (days) => days.flatMap((d) => (d.workouts || []).flatMap((w) => (w.hr || []).map(([s, bpm]) => ({ d, w, s, bpm })))), ({ d, w, s, bpm }) => [
    ['date', d.date], ['workout_id', w.id], ['second', s], ['time', clockPlus(w.start, s / 60)], ['bpm', bpm],
  ]],
  ['workout_splits', (days) => days.flatMap((d) => (d.workouts || []).flatMap((w) => (w.splits || []).map(([m, s], i) => ({ d, w, i, m, s })))), ({ d, w, i, m, s }) => [
    ['date', d.date], ['workout_id', w.id], ['km', i + 1], ['distance_m', m], ['seconds', s],
  ]],
  // The night that ended on the date, with what Fitbit measured during it.
  ['sleep', (days) => days.filter((d) => d.sleep || d.hrv || d.sleep_hr || d.resp || d.spo2 || d.temp != null).map((d) => ({ d, s: d.sleep || {} })), ({ d, s }) => [
    ['date', d.date], ['start', s.start], ['end', s.end],
    ['asleep_min', s.asleep], ['awake_min', s.awake], ['deep_min', s.deep], ['light_min', s.light], ['rem_min', s.rem],
    ['restless_moments', s.restless?.length], ['restless_min', s.restless_min],
    ['to_fall_asleep_min', s.to_sleep], ['awake_after_wakeup_min', s.after_wake],
    ['sleeping_hr', d.sleep_hr], ['hrv_ms', d.hrv], ['hrv_deep_ms', d.hrv_deep], ['hrv_entropy', d.hrv_ent],
    ['breathing_rate', d.resp], ['spo2_pct', d.spo2], ['skin_temp_delta_c', d.temp],
  ]],
  // Each stage of the night in order; restless moments overlap the stages.
  ['sleep_stages', (days) => days.flatMap((d) => {
    const s = d.sleep;
    if (!s) return [];
    const parts = [...(s.stages || []), ...(s.restless || []).map(([a, b]) => [a, b, RESTLESS.code])].sort((a, b) => a[0] - b[0]);
    return parts.map(([min, dur, code]) => ({ d, s, min, dur, code }));
  }), ({ d, s, min, dur, code }) => [
    ['date', d.date], ['start', clockPlus(s.start, min)], ['minute', min], ['duration_min', dur], ['stage', stageName(code)],
  ]],
];

// Numbers keep at most two decimals; missing values become null.
const value = (v) => (v == null || v === '' || v === false || Number.isNaN(v) ? null : typeof v === 'number' ? Math.round(v * 100) / 100 : v);
const EMPTY = { d: {}, f: {}, r: {}, it: {}, a: {}, w: {}, s: {} };

// exportTables returns the tables of the days from..to (ISO dates,
// inclusive) as [name, columns, rows], each row an object. The days table
// has a row for every date in the range, logged or not.
export function exportTables(data, from, to) {
  const byDate = new Map(data.days.map((d) => [d.date, d]));
  const days = [];
  for (let d = parse(from); d <= parse(to); d = addDays(d, 1)) days.push(byDate.get(iso(d)) || { date: iso(d) });
  return TABLES.map(([name, items, row]) => [
    name,
    row(EMPTY, {}).map(([k]) => k),
    items(days, data).map((it) => Object.fromEntries(row(it, data).map(([k, v]) => [k, value(v)]))),
  ]);
}

export function toCSV(columns, rows) {
  const cell = (v) => {
    if (v == null) return '';
    const s = String(v);
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  return [columns.join(','), ...rows.map((r) => columns.map((k) => cell(r[k])).join(','))].join('\n') + '\n';
}

// exportFiles returns the export as [path, text] pairs in a folder of its
// own: an info file with the range and goals, then one file per table.
export function exportFiles(data, from, to, format, now = new Date()) {
  const dir = `eumetria-${from}-to-${to}/`;
  const info = {
    title: data.title, timezone: data.timezone, from, to, exported_at: now.toISOString(),
    ...Object.fromEntries(Object.entries(data.goals || {}).map(([k, v]) => [`goal_${k}`, value(v)])),
  };
  const tables = exportTables(data, from, to);
  if (format === 'json') {
    const json = (v) => JSON.stringify(v, null, 2) + '\n';
    return [[dir + 'info.json', json(info)], ...tables.map(([name, , rows]) => [`${dir}${name}.json`, json(rows)])];
  }
  return [
    [dir + 'info.csv', toCSV(['key', 'value'], Object.entries(info).map(([key, value]) => ({ key, value })))],
    ...tables.map(([name, columns, rows]) => [`${dir}${name}.csv`, toCSV(columns, rows)]),
  ];
}
