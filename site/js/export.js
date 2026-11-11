// The export of a date range: one table per kind of data (days, foods,
// recipes, activities, workouts, sleep, goal changes, weekly reviews, and
// what people wrote: workout notes, lifts and benchmark workouts), written as CSV or JSON files with
// the same columns and rows and bundled as a ZIP. GPS routes and photos are
// left out. No DOM access, so Node can test this file.

import { MEALS, addDays, iso, parse } from './format.js';
import { RESTLESS, STAGES, bestEntries, wodBestResults } from './health.js';
import { goalChangesIn, macroGoal, weightTrendAt } from './insights.js';

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
  // weight_trend_kg: the average of the weigh-ins of the 7 days up to the
  // date, as the dashboard and the report use it.
  ['days', (days) => days.map((d) => ({ d })), ({ d }, data) => [
    ['date', d.date],
    ['kcal_eaten', d.energy], ['kcal_goal', d.energy_goal], ['kcal_goal_base', d.energy_base],
    ['protein_g', d.protein], ['protein_goal_g', macroGoal(d, 'protein')],
    ['carbs_g', d.carb], ['carbs_goal_g', macroGoal(d, 'carb')],
    ['fat_g', d.fat], ['fat_goal_g', macroGoal(d, 'fat')],
    ...MEALS.map(([m]) => [`kcal_${m}`, d.meals?.[m]]),
    ['water_ml', d.water], ['steps', d.steps], ['weight_kg', d.weight], ['weight_trend_kg', d.date && data.byDate ? weightTrendAt(data.byDate, d.date) : null],
    ['kcal_activities', d.activity_energy], ['kcal_burned_fitbit', d.kcal_burned], ['resting_hr', d.rhr],
    ['zone_min_fat_burn', d.azm?.[0]], ['zone_min_cardio', d.azm?.[1]], ['zone_min_peak', d.azm?.[2]],
    ['zone_minutes', d.azm && sum(d.azm)], ['vo2max', d.vo2max], ['vo2max_rating', d.vo2_level],
    ['eumetria_readiness', d.scores?.readiness?.value], ['eumetria_sleep_score', d.scores?.sleep?.value],
    ['eumetria_recovery', d.scores?.recovery?.value], ['training_load_ratio', d.scores?.load?.ratio],
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
// extras are what the page loads besides data, each table only with it:
// { lifts, wods } (the lifts and benchmark tables) and notes (a Map of the
// workout notes, as state.notes).
export function exportTables(data, from, to, extras = null) {
  const byDate = new Map(data.days.map((d) => [d.date, d]));
  const days = [];
  for (let d = parse(from); d <= parse(to); d = addDays(d, 1)) days.push(byDate.get(iso(d)) || { date: iso(d) });
  const withDates = { ...data, byDate };
  const tables = TABLES.map(([name, items, row]) => [
    name,
    row(EMPTY, {}).map(([k]) => k),
    items(days, withDates).map((it) => Object.fromEntries(row(it, withDates).map(([k, v]) => [k, value(v)]))),
  ]);
  tables.push(...textTables(data, from, to, byDate, extras?.notes));
  return extras?.lifts || extras?.wods ? [...tables, ...bestTables(extras, from, to)] : tables;
}

// The goal changes of the range, the weekly reviews of the weeks that touch
// it, and the notes of its workouts (only when given).
function textTables(data, from, to, byDate, notes) {
  const table = (name, columns, rows) => [name, columns, rows.map((r) => Object.fromEntries(columns.map((k) => [k, value(r[k] ?? null)])))];
  const weekEnd = (monday) => iso(addDays(parse(monday), 6));
  const out = [
    table('goal_changes', ['date', 'goal', 'before', 'after'],
      goalChangesIn(data.goal_changes, from, to).flatMap(([date, ch]) => Object.values(ch).map((c) => ({ date, goal: c.goal, before: c.from, after: c.to })))),
    table('weekly_reviews', ['week_start', 'week_end', 'model', 'written_at', 'text', 'text_de'],
      (data.reviews || []).filter((r) => r.week <= to && weekEnd(r.week) >= from).sort((a, b) => a.week.localeCompare(b.week))
        .map((r) => ({ week_start: r.week, week_end: weekEnd(r.week), model: r.model, written_at: r.created_at, text: r.text, text_de: r.text_de }))),
  ];
  if (notes) {
    // A note's date is its workout's.
    const workouts = new Map();
    for (let d = parse(from); d <= parse(to); d = addDays(d, 1)) {
      for (const w of byDate.get(iso(d))?.workouts || []) workouts.set(w.id, { date: iso(d), w });
    }
    out.push(table('workout_notes', ['date', 'workout_id', 'workout_type', 'markdown', 'written_by', 'updated_at'],
      [...notes.values()].filter((n) => workouts.has(n.workout_id)).map((n) => ({
        date: workouts.get(n.workout_id).date, workout_id: n.workout_id, workout_type: workouts.get(n.workout_id).w.type,
        markdown: n.markdown, written_by: n.updated_by, updated_at: n.updated_at,
      })).sort((a, b) => a.date.localeCompare(b.date))));
  }
  return out;
}

// The lifts and benchmark results of the range, and every benchmark
// workout. personal_best: better than every earlier result of its kind.
// A result's value is seconds (time; reps done when capped), rounds (with
// reps_after_rounds), reps or kg, by the workout's scoring.
function bestTables({ lifts = [], wods = { workouts: [], results: [] } }, from, to) {
  const inRange = (x) => x.date >= from && x.date <= to;
  const liftPb = new Set(bestEntries(lifts).map((e) => e.id));
  const wodPb = new Set(wodBestResults(wods.workouts, wods.results).map((r) => r.id));
  const byId = new Map(wods.workouts.map((w) => [w.id, w]));
  const table = (name, columns, rows) => [name, columns, rows.map((r) => Object.fromEntries(columns.map((k) => [k, value(r[k] ?? null)])))];
  const byDate = (a, b) => a.date.localeCompare(b.date) || a.id - b.id;
  return [
    table('lifts', ['date', 'lift', 'kg', 'reps', 'personal_best', 'note'],
      lifts.filter(inRange).sort(byDate).map((e) => ({ ...e, personal_best: liftPb.has(e.id), note: e.note || null }))),
    table('benchmark_results', ['date', 'workout', 'scoring', 'value', 'reps_after_rounds', 'scaled', 'capped', 'personal_best', 'fitbit_workout', 'note'],
      wods.results.filter((r) => inRange(r) && byId.has(r.workout)).sort(byDate).map((r) => ({
        date: r.date, workout: byId.get(r.workout).name, scoring: byId.get(r.workout).scoring, value: r.value,
        reps_after_rounds: byId.get(r.workout).scoring === 'rounds' ? r.reps || 0 : null, scaled: !!r.scaled, capped: !!r.capped,
        personal_best: wodPb.has(r.id), fitbit_workout: r.fitbit_workout || null, note: r.note || null,
      }))),
    table('benchmark_workouts', ['name', 'scoring', 'time_cap_s', 'description'],
      wods.workouts.map((w) => ({ name: w.name, scoring: w.scoring, time_cap_s: w.time_cap || null, description: w.description || null }))),
  ];
}

// toCSV writes rows as CSV. Text that a spreadsheet would take for a
// formula (=, +, -, @, a tab or a carriage return first) gets a ' in front
// and quotes around it, so opening the file runs nothing (pentest F10):
// notes, lift names and food names come from other users and from Yazio.
// Numbers stay numbers, negative ones too.
export function toCSV(columns, rows) {
  const cell = (v) => {
    if (v == null) return '';
    let s = String(v);
    const formula = typeof v === 'string' && /^[=+\-@\t\r]/.test(s) && !/^[-+]?\d+([.,]\d+)?$/.test(s);
    if (formula) s = "'" + s;
    return formula || /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  return [columns.join(','), ...rows.map((r) => columns.map((k) => cell(r[k])).join(','))].join('\n') + '\n';
}

// exportFiles returns the export as [path, text] pairs in a folder of its
// own: an info file with the range and goals, then one file per table.
export function exportFiles(data, from, to, format, now = new Date(), extras = null) {
  const dir = `eumetria-${from}-to-${to}/`;
  const info = {
    title: data.title, timezone: data.timezone, from, to, exported_at: now.toISOString(),
    ...Object.fromEntries(Object.entries(data.goals || {}).map(([k, v]) => [`goal_${k}`, value(v)])),
  };
  const tables = exportTables(data, from, to, extras);
  if (format === 'json') {
    const json = (v) => JSON.stringify(v, null, 2) + '\n';
    return [[dir + 'info.json', json(info)], ...tables.map(([name, , rows]) => [`${dir}${name}.json`, json(rows)])];
  }
  return [
    [dir + 'info.csv', toCSV(['key', 'value'], Object.entries(info).map(([key, value]) => ({ key, value })))],
    ...tables.map(([name, columns, rows]) => [`${dir}${name}.csv`, toCSV(columns, rows)]),
  ];
}
